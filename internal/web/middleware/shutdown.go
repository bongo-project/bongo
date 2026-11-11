package middleware

import "context"

const serverShutdownKey contextKey = "serverShutdown"

// WithServerShutdown attaches a channel that is closed when the HTTP server
// begins a graceful shutdown. The server sets it on every connection's base
// context; long-lived streams select on it so that http.Server.Shutdown does
// not wait out its whole drain timeout for them.
func WithServerShutdown(ctx context.Context, done <-chan struct{}) context.Context {
	return context.WithValue(ctx, serverShutdownKey, done)
}

// ServerShutdown returns the channel set by WithServerShutdown, or nil when
// none was set (a receive from nil blocks forever, so callers need no check).
func ServerShutdown(ctx context.Context) <-chan struct{} {
	done, _ := ctx.Value(serverShutdownKey).(<-chan struct{})
	return done
}
