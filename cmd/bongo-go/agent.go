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
	agentMode       string
	agentConfig     string
	agentBind       string
	agentJoin       string
	agentLogLevel   string
	bootstrapExpect int
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start a bongo-go agent",
	Long:  "Start a bongo-go agent in server, client, or dev mode.",
	RunE:  runAgent,
}

func init() {
	agentCmd.Flags().StringVarP(&agentMode, "mode", "m", "dev", "Agent mode: server, client, or dev")
	agentCmd.Flags().StringVarP(&agentConfig, "config", "c", "", "Path to HCL config file")
	agentCmd.Flags().StringVarP(&agentBind, "bind", "b", "0.0.0.0", "Address to bind listeners to")
	agentCmd.Flags().StringVarP(&agentJoin, "join", "j", "", "Address of an existing agent to join")
	agentCmd.Flags().StringVar(&agentLogLevel, "log-level", "", "Log level: debug, info, warn, error")
	agentCmd.Flags().IntVar(&bootstrapExpect, "bootstrap-expect", 0, "Number of expected servers to bootstrap the cluster")

	rootCmd.AddCommand(agentCmd)
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Validate mode.
	switch agentMode {
	case "server", "client", "dev":
	default:
		return fmt.Errorf("invalid mode %q: must be server, client, or dev", agentMode)
	}

	// Load config with precedence: CLI flags > env > file > defaults.
	v := viper.New()
	if agentConfig != "" {
		v.Set("config", agentConfig)
	}
	if cmd.Flags().Changed("mode") {
		v.Set("mode", agentMode)
	}
	if cmd.Flags().Changed("bind") {
		v.Set("bind", agentBind)
	}
	if cmd.Flags().Changed("join") {
		v.Set("join", agentJoin)
	}
	if cmd.Flags().Changed("log-level") {
		v.Set("log-level", agentLogLevel)
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

	// Create agent.
	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		return fmt.Errorf("creating agent: %w", err)
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
