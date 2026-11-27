// Command bongo-update answers the version check of Bongo servers and counts
// how many distinct installations run each version. See README.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bongo-project/bongo/internal/updatecheck"
	"github.com/bongo-project/bongo/internal/version"
)

// buildVersion is the release version of this binary; tests replace it.
var buildVersion = version.Version

// Startup database wait: how often and for how long the schema is retried
// before the service gives up. Tests shorten them.
var (
	schemaRetryEvery = 2 * time.Second
	schemaRetryTotal = 30 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	err := run(ctx, os.Args[1:], os.Getenv, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "bongo-update:", err)
		os.Exit(1)
	}
}

// newServer builds the HTTP server. Its ErrorLog discards everything:
// net/http would otherwise write the client's address to it on a panic or a
// connection error.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ErrorLog:          log.New(io.Discard, "", 0),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
}

// settings holds the resolved flags and environment.
type settings struct {
	listen string
	cfg    config
}

// resolve reads flags with environment fallbacks (a flag wins). The DSN is
// deliberately not here: it comes from the environment only.
func resolve(args []string, getenv func(string) string, stderr io.Writer) (settings, error) {
	fs := flag.NewFlagSet("bongo-update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", ":8080", "address to listen on (env BONGO_UPDATE_LISTEN)")
	latest := fs.String("latest", "", "latest release version (env BONGO_UPDATE_LATEST; default: this binary's version)")
	notes := fs.String("release-notes-url", "", "release notes URL (env BONGO_UPDATE_RELEASE_NOTES_URL; default https://update.bongo-project.org/releases/{latest})")
	security := fs.String("security", "false", "true when the latest release fixes a security issue (env BONGO_UPDATE_SECURITY)")
	if err := fs.Parse(args); err != nil {
		return settings{}, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	pick := func(name, env string, val *string) {
		if !set[name] {
			if v := getenv(env); v != "" {
				*val = v
			}
		}
	}
	pick("listen", "BONGO_UPDATE_LISTEN", listen)
	pick("latest", "BONGO_UPDATE_LATEST", latest)
	pick("release-notes-url", "BONGO_UPDATE_RELEASE_NOTES_URL", notes)
	pick("security", "BONGO_UPDATE_SECURITY", security)

	var sec bool
	switch *security {
	case "true":
		sec = true
	case "false":
	default:
		return settings{}, errors.New("security must be true or false")
	}

	if *latest == "" {
		// A pre-release image must not advertise itself to stable servers.
		v, ok := updatecheck.RunningVersion(buildVersion)
		sv, parsed := updatecheck.ParseSemver(v)
		if !ok || !parsed || len(sv.Pre) > 0 {
			return settings{}, errors.New("latest is not set and this build is not a final release: pass --latest or BONGO_UPDATE_LATEST")
		}
		*latest = v
	}
	l := strings.TrimPrefix(*latest, "v")
	if _, ok := normaliseVersion(l); !ok || strings.HasPrefix(l, "v") {
		return settings{}, errors.New("latest is not a semantic version")
	}
	if *notes == "" {
		*notes = "https://update.bongo-project.org/releases/" + l
	}
	cfg := config{Latest: l, ReleaseNotesURL: *notes, Security: sec}
	if !cfg.valid() {
		return settings{}, errors.New("release notes URL must be an https URL")
	}
	return settings{listen: *listen, cfg: cfg}, nil
}

func run(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) error {
	s, err := resolve(args, getenv, stderr)
	if err != nil {
		return err
	}
	dsn := getenv("BONGO_UPDATE_DSN")
	if dsn == "" {
		return errors.New("BONGO_UPDATE_DSN is not set")
	}
	logger := log.New(stderr, "bongo-update: ", log.LstdFlags|log.LUTC)
	logf := logger.Printf

	st, err := openStore(dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := waitForSchema(ctx, st, schemaRetryEvery, schemaRetryTotal); err != nil {
		logf("database unavailable at startup")
		return err
	}

	srv := newServer(s.listen, newHandler(s.cfg, st, time.Now, logf))
	rollCtx, cancelRoll := context.WithCancel(ctx)
	defer cancelRoll()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rollupLoop(rollCtx, st, time.Now, ticker.C, logf)
	}()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logf("listening on %s, latest %s, security %t", s.listen, s.cfg.Latest, s.cfg.Security)

	var serveErr error
	select {
	case <-ctx.Done():
		logf("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		serveErr = srv.Shutdown(shutCtx)
		cancel()
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = errors.New("server stopped")
		}
	}
	cancelRoll()
	wg.Wait()
	return serveErr
}
