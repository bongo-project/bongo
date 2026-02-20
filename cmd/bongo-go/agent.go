package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/config"
)

var (
	serveRole       string
	serveConfig     string
	serveBind       string
	serveJoin       string
	serveLogLevel   string
	serveDev        bool
	bootstrapExpect int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the bongo-go server",
	Long:  "Start bongo-go in server, edge, or dev role.",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveRole, "role", "", "Server role: server, edge, or dev")
	serveCmd.Flags().BoolVar(&serveDev, "dev", false, "Shorthand for --role=dev")
	serveCmd.Flags().StringVarP(&serveConfig, "config", "c", "", "Path to HCL config file")
	serveCmd.Flags().StringVarP(&serveBind, "bind", "b", "0.0.0.0", "Address to bind listeners to")
	serveCmd.Flags().StringVarP(&serveJoin, "join", "j", "", "Address of an existing node to join")
	serveCmd.Flags().StringVar(&serveLogLevel, "log-level", "", "Log level: debug, info, warn, error")
	serveCmd.Flags().IntVar(&bootstrapExpect, "bootstrap-expect", 0, "Number of expected servers to bootstrap the cluster")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	// Resolve role: --dev flag takes precedence, then --role, then default.
	role := "dev"
	if serveRole != "" {
		role = serveRole
	}
	if serveDev {
		role = "dev"
	}

	// Validate role.
	switch role {
	case "server", "edge", "dev":
	default:
		return fmt.Errorf("invalid role %q: must be server, edge, or dev", role)
	}

	// Load config with precedence: CLI flags > env > file > defaults.
	v := viper.New()
	if serveConfig != "" {
		v.Set("config", serveConfig)
	}
	v.Set("role", role)
	if cmd.Flags().Changed("bind") {
		v.Set("bind", serveBind)
	}
	if cmd.Flags().Changed("join") {
		v.Set("join", serveJoin)
	}
	if cmd.Flags().Changed("log-level") {
		v.Set("log-level", serveLogLevel)
	}

	cfg, err := config.Load(v)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Set up structured logger.
	logLevel := parseLogLevel(cfg.Telemetry.Log.Level)
	var handler slog.Handler
	if cfg.Telemetry.Log.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	}
	logger := slog.New(handler)

	logger.Info("config loaded",
		"role", cfg.Server.Role,
		"log_level", cfg.Telemetry.Log.Level,
		"log_format", cfg.Telemetry.Log.Format,
	)

	// Create agent.
	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		return fmt.Errorf("starting server: %w", err)
	}

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
