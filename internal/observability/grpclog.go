package observability

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"google.golang.org/grpc/grpclog"
)

var _ grpclog.LoggerV2 = (*GRPCLogger)(nil)

// GRPCLogger adapts slog to grpclog.LoggerV2. Each call becomes one slog
// record with a stable msg key and the library text in "line". gRPC info is
// chatty and logs at DEBUG; warnings at WARN; errors and fatals at ERROR.
// The Depth variants are deliberately not implemented: grpclog then falls back
// to the *ln methods, which receive every component line as "[component] text".
type GRPCLogger struct {
	logger    *slog.Logger
	verbosity int
	exit      func(int)
}

// NewGRPCLogger returns a logger that reports V(l) true for l <= verbosity.
func NewGRPCLogger(logger *slog.Logger, verbosity int) *GRPCLogger {
	return &GRPCLogger{logger: logger, verbosity: verbosity, exit: os.Exit}
}

// InstallGRPCLogger routes gRPC's global logger through logger. Call it once
// at process startup, before any gRPC server or client is created:
// grpclog.SetLoggerV2 is not goroutine-safe. It replaces the
// GRPC_GO_LOG_SEVERITY_LEVEL, GRPC_GO_LOG_VERBOSITY_LEVEL and
// GRPC_GO_LOG_FORMATTER environment settings.
func InstallGRPCLogger(logger *slog.Logger) {
	grpclog.SetLoggerV2(NewGRPCLogger(logger, 0))
}

func (g *GRPCLogger) log(level slog.Level, msg, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	g.logger.Log(context.Background(), level, msg, "line", line)
}

func (g *GRPCLogger) Info(args ...any) {
	g.log(slog.LevelDebug, "grpc.info", fmt.Sprint(args...))
}

func (g *GRPCLogger) Infoln(args ...any) {
	g.log(slog.LevelDebug, "grpc.info", fmt.Sprintln(args...))
}

func (g *GRPCLogger) Infof(format string, args ...any) {
	g.log(slog.LevelDebug, "grpc.info", fmt.Sprintf(format, args...))
}

func (g *GRPCLogger) Warning(args ...any) {
	g.log(slog.LevelWarn, "grpc.warning", fmt.Sprint(args...))
}

func (g *GRPCLogger) Warningln(args ...any) {
	g.log(slog.LevelWarn, "grpc.warning", fmt.Sprintln(args...))
}

func (g *GRPCLogger) Warningf(format string, args ...any) {
	g.log(slog.LevelWarn, "grpc.warning", fmt.Sprintf(format, args...))
}

func (g *GRPCLogger) Error(args ...any) {
	g.log(slog.LevelError, "grpc.error", fmt.Sprint(args...))
}

func (g *GRPCLogger) Errorln(args ...any) {
	g.log(slog.LevelError, "grpc.error", fmt.Sprintln(args...))
}

func (g *GRPCLogger) Errorf(format string, args ...any) {
	g.log(slog.LevelError, "grpc.error", fmt.Sprintf(format, args...))
}

// Fatal logs at ERROR and then terminates the process, as grpclog requires.
func (g *GRPCLogger) Fatal(args ...any) {
	g.log(slog.LevelError, "grpc.fatal", fmt.Sprint(args...))
	g.exit(1)
}

func (g *GRPCLogger) Fatalln(args ...any) {
	g.log(slog.LevelError, "grpc.fatal", fmt.Sprintln(args...))
	g.exit(1)
}

func (g *GRPCLogger) Fatalf(format string, args ...any) {
	g.log(slog.LevelError, "grpc.fatal", fmt.Sprintf(format, args...))
	g.exit(1)
}

// V reports whether verbosity level l is enabled.
func (g *GRPCLogger) V(l int) bool { return l <= g.verbosity }
