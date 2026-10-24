package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/handlers"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// peerListenerEnabled is the single gate for everything peer-facing: the mTLS
// listener, the https clients and the removal of the peer routes from the
// public router. It is the same definition raft uses for "this node has
// peers", so a single node and dev never open a listener or need new config.
func peerListenerEnabled(cfg *config.Config) bool {
	return cfg.ClusterHasPeers()
}

// registerPeerRoutes mounts the node-to-node endpoints: cluster join behind
// the internal HMAC and blob transfer behind its own derived-key HMAC. Each
// route keeps its own check; they are not interchangeable.
func (a *Agent) registerPeerRoutes(r chi.Router, sessionSecret []byte) {
	if a.Raft != nil {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireInternalAuth(sessionSecret))
			r.Post("/internal/cluster/join", handlers.NewJoinHandler(a.Raft.Raft).Join)
		})
	}
	if a.Blobs != nil {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireBlobSyncAuth(sessionSecret))
			blobH := handlers.NewBlobHandler(a.Blobs)
			r.Get("/internal/blob/{hash}", blobH.Get)
			r.Head("/internal/blob/{hash}", blobH.Head)
		})
	}
}

// registerControlPeerRoutes mounts the peer endpoints on the control router
// only when the node has no peers. With peers they are served exclusively by
// the peer listener, so neither the web nor the control port serves them.
func (a *Agent) registerControlPeerRoutes(r chi.Router, sessionSecret []byte) {
	if peerListenerEnabled(a.Config) {
		return
	}
	a.registerPeerRoutes(r, sessionSecret)
}

// buildPeerRouter is the handler of the peer listener.
func (a *Agent) buildPeerRouter(sessionSecret []byte) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders())
	a.registerPeerRoutes(r, sessionSecret)
	return r
}

// newPeerServer builds the peer listener's http.Server with the same explicit
// timeouts and slog error log as the web server. tlsCfg is cloned because
// http.Server mutates NextProtos.
func newPeerServer(addr string, h http.Handler, tlsCfg *tls.Config, logger *slog.Logger) *http.Server {
	srv := newHTTPServer(addr, h, logger)
	srv.TLSConfig = tlsCfg.Clone()
	return srv
}

// newPeerHTTPClient builds the client used for outgoing peer calls: the
// cluster CA is the only trust root and the node certificate is the client
// certificate. cfg is cloned.
func newPeerHTTPClient(cfg *tls.Config) *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: cfg.Clone()},
	}
}

// startPeerListener binds the cluster bind host and peer port, then serves
// the peer router over the strict cluster mTLS config. It returns once the
// port is bound, so the address gossip advertises is already live.
func (a *Agent) startPeerListener(sessionSecret []byte) error {
	return a.servePeer(a.buildPeerRouter(sessionSecret))
}

// servePeer is startPeerListener with the handler supplied by the caller.
func (a *Agent) servePeer(h http.Handler) error {
	if a.peerTLS == nil {
		return errors.New("peer listener: no cluster TLS configuration loaded")
	}
	host, err := a.Config.ClusterBindHost()
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(host, strconv.Itoa(a.Config.Cluster.PeerPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("peer listen on %s: %w", addr, err)
	}
	srv := newPeerServer(addr, h, a.peerTLS, a.logger)
	a.peerServer = srv
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("peer: listener stopped", "addr", addr, "error", err)
		}
	}()
	a.logger.Info("peer: listening (mutual TLS)", "addr", addr)
	return nil
}

// startControlServer serves the control router on an already bound listener,
// with the same explicit timeouts and error log as the web server.
func (a *Agent) startControlServer(ln net.Listener, h http.Handler) {
	addr := ln.Addr().String()
	srv := newHTTPServer(addr, h, a.logger)
	a.controlServer = srv
	a.controlAddr = addr
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("control: listener stopped", "addr", addr, "error", err)
		}
	}()
	a.logger.Info("control: listening", "addr", addr)
}

// ControlAddr returns the address the control listener bound, or "" before
// Run has bound it.
func (a *Agent) ControlAddr() string {
	return a.controlAddr
}

// shutdownControlServer drains the control listener.
func (a *Agent) shutdownControlServer() error {
	if a.controlServer == nil {
		return nil
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.controlServer.Shutdown(drainCtx)
}

// shutdownPeerServer drains the peer listener.
func (a *Agent) shutdownPeerServer() error {
	if a.peerServer == nil {
		return nil
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.peerServer.Shutdown(drainCtx)
}
