package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/web/handlers"
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

	cfg, configFile, err := config.LoadWithDiscovery(v)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if servePlainHTTP {
		cfg.Web.PlainHTTP = true
	}

	// Set up structured logger with LogSink for admin UI streaming.
	logLevel := parseLogLevel(cfg.Telemetry.Log.Level)
	logSink := handlers.NewLogSink()

	// Logs always go to stdout (so `docker logs`/journald still work). When
	// telemetry.log.file is set, also append to that file for off-host shipping.
	var logOut io.Writer = os.Stdout
	if path := cfg.Telemetry.Log.File; path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if err != nil {
			return fmt.Errorf("opening log file %s: %w", path, err)
		}
		defer f.Close()
		logOut = io.MultiWriter(os.Stdout, f)
	}

	var baseHandler slog.Handler
	if cfg.Telemetry.Log.Format == "text" {
		baseHandler = slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: logLevel})
	} else {
		baseHandler = slog.NewJSONHandler(logOut, &slog.HandlerOptions{Level: logLevel})
	}
	// Wrap base handler so log entries are also sent to the admin log viewer.
	handler := logSink.SlogHandler(baseHandler)
	logger := slog.New(handler)

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

	// Set up signal-based cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal", "signal", sig.String())
		cancel()
	}()

	return a.Run(ctx)
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
