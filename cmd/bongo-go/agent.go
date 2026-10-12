package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/web/handlers"
)

var (
	serveRole       string
	serveConfig     string
	serveBind       string
	serveJoin       string
	serveLogLevel   string
	serveDev        bool
	servePlainHTTP  bool
	bootstrapExpect int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the bongo-go server",
	Long:  "Start bongo-go in server, edge, dev, or agent role.",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveRole, "role", "", "Server role: server, edge, dev, or agent")
	serveCmd.Flags().BoolVar(&serveDev, "dev", false, "Shorthand for --role=dev")
	serveCmd.Flags().StringVarP(&serveConfig, "config", "c", "", "Path to HCL config file")
	serveCmd.Flags().StringVarP(&serveBind, "bind", "b", "0.0.0.0", "Address to bind listeners to")
	serveCmd.Flags().StringVarP(&serveJoin, "join", "j", "", "Address of an existing node to join")
	serveCmd.Flags().StringVar(&serveLogLevel, "log-level", "", "Log level: debug, info, warn, error")
	serveCmd.Flags().BoolVar(&servePlainHTTP, "plain-http", false, "Disable TLS for the web server (use behind a reverse proxy)")
	serveCmd.Flags().IntVar(&bootstrapExpect, "bootstrap-expect", 0, "Number of expected servers to bootstrap the cluster")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	// Resolve role only if explicitly provided: --dev takes precedence, then
	// --role. If neither is given, the role comes from the config file (falling
	// back to the "dev" default when there is no config). This prevents a
	// `serve --config bongo.hcl` from silently overriding role="server" with the
	// dev default and running with ephemeral storage.
	role := ""
	if serveRole != "" {
		role = serveRole
	}
	if serveDev {
		role = "dev"
	}

	// Validate the role when one was explicitly set.
	if role != "" {
		switch role {
		case "server", "edge", "dev", "agent":
		default:
			return fmt.Errorf("invalid role %q: must be server, edge, dev, or agent", role)
		}
	}

	// Load config with precedence: CLI flags > env > file > defaults.
	v := viper.New()
	if serveConfig != "" {
		v.Set("config", serveConfig)
	}
	if role != "" {
		v.Set("role", role)
	}
	if cmd.Flags().Changed("bind") {
		v.Set("bind", serveBind)
	}
	if cmd.Flags().Changed("join") {
		v.Set("join", serveJoin)
	}
	if cmd.Flags().Changed("log-level") {
		v.Set("log-level", serveLogLevel)
	}

	// In dev mode, skip auto-discovery of config files (bongo.hcl in cwd,
	// /etc/bongo/bongo.hcl) so that --dev is fully self-contained.
	// An explicit --config flag is still honoured.
	if serveDev && serveConfig == "" {
		v.Set("skip-discovery", "true")
	}

	// Validate before the logger, storage or any listener exists, so a bad
	// config refuses to start. The error reaches stderr via cobra, not the
	// JSON log, because the logger is not built yet.
	cfg, configFile, err := loadServeConfig(v)
	if err != nil {
		return err
	}

	if servePlainHTTP {
		cfg.Web.PlainHTTP = true
	}

	// Pin the config file and snapshot the running config for the reloader.
	// The baseline is taken here, before NewAgent mutates cfg (dev mode sets
	// Web.PlainHTTP), so those startup-only changes never show up as diffs.
	var reloadBaseline *config.Config
	var reloadFile string
	if configFile != "" {
		abs, err := filepath.Abs(configFile)
		if err != nil {
			abs = configFile
		}
		reloadFile = abs
		v.Set("config", abs)
		reloadBaseline = config.Clone(cfg)
	}

	// Set up structured logger with LogSink for admin UI streaming.
	logLevel := cfg.Telemetry.Log.SlogLevel()
	logSink := handlers.NewLogSink()

	// Logs always go to stdout (so `docker logs`/journald still work). When
	// telemetry.log.file is set, also write to that file for off-host shipping.
	// The file is size-rotated in place (lumberjack), which keeps disk usage
	// bounded while letting an external shipper keep tailing the same path.
	var logOut io.Writer = os.Stdout
	if path := cfg.Telemetry.Log.File; path != "" {
		logCfg := cfg.Telemetry.Log
		rotator := &lumberjack.Logger{
			Filename:   path,
			MaxSize:    logCfg.LogMaxSizeMB(),
			MaxBackups: logCfg.LogMaxBackups(),
			MaxAge:     logCfg.LogMaxAgeDays(),
			Compress:   logCfg.Compress,
		}
		defer rotator.Close()
		logOut = io.MultiWriter(os.Stdout, rotator)
	}

	logger, logLevelVar := newServeLogger(cfg.Telemetry.Log.Format, logLevel, logOut, logSink)

	if configFile != "" {
		logger.Info("config loaded",
			"file", configFile,
			"role", cfg.Server.Role,
			"log_level", cfg.Telemetry.Log.Level,
			"log_format", cfg.Telemetry.Log.Format,
		)
	} else {
		logger.Info("no config file found, using defaults",
			"role", cfg.Server.Role,
			"log_level", cfg.Telemetry.Log.Level,
			"log_format", cfg.Telemetry.Log.Format,
		)
	}

	// Create agent.
	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	a.LogSink = logSink
	a.LogLevel = logLevelVar
	if reloadBaseline != nil {
		a.EnableReload(v, reloadFile, servePlainHTTP, reloadBaseline)
	}

	// Set up signal-based cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := watchSignals(ctx, cancel, a.Reload, logger)
	defer stop()

	return a.Run(ctx)
}

// watchSignals routes process signals: SIGINT and SIGTERM call cancel, SIGHUP
// calls reload without cancelling. Both handlers are registered before it
// returns. The returned stop unregisters them and waits for the goroutines to
// exit; it is meant to be deferred around Run, not called on ctx.Done, so a
// second signal during graceful shutdown is still swallowed instead of
// restoring the default terminate action. A burst of SIGHUPs coalesces into
// one pending reload; Reload serialises and logs its own failures.
func watchSignals(ctx context.Context, cancel context.CancelFunc, reload func(context.Context) error, logger *slog.Logger) (stop func()) {
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, syscall.SIGINT, syscall.SIGTERM)
	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)

	wctx, wcancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		select {
		case sig := <-termCh:
			logger.Info("received signal", "signal", sig.String())
			cancel()
		case <-wctx.Done():
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-wctx.Done():
				return
			case sig := <-hupCh:
				logger.Info("received signal", "signal", sig.String())
				_ = reload(ctx)
			}
		}
	}()

	return func() {
		signal.Stop(hupCh)
		signal.Stop(termCh)
		wcancel()
		wg.Wait()
	}
}

// newServeLogger builds the process logger and routes gRPC's global logger
// through it. It must run before agent.NewAgent creates any gRPC server or
// client, because grpclog.SetLoggerV2 is not goroutine-safe.
//
// The returned LevelVar is the live level: Set on it changes the logger, the
// admin log sink and every With child at once.
func newServeLogger(format string, level slog.Level, out io.Writer, sink *handlers.LogSink) (*slog.Logger, *slog.LevelVar) {
	lv := new(slog.LevelVar)
	lv.Set(level)
	var base slog.Handler
	if format == "text" {
		base = slog.NewTextHandler(out, &slog.HandlerOptions{Level: lv})
	} else {
		base = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: lv})
	}
	// Wrap base handler so log entries are also sent to the admin log viewer.
	logger := slog.New(sink.SlogHandler(base))
	observability.InstallGRPCLogger(logger)
	return logger, lv
}

// loadServeConfig loads the merged configuration and validates it. Serve is
// the only entrypoint that validates: LoadWithDiscovery stays a pure merge so
// tools needing only part of the config are not subject to server-only gates.
func loadServeConfig(v *viper.Viper) (*config.Config, string, error) {
	cfg, configFile, err := config.LoadWithDiscovery(v)
	if err != nil {
		return nil, "", fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, "", fmt.Errorf("invalid config: %w", err)
	}
	return cfg, configFile, nil
}
