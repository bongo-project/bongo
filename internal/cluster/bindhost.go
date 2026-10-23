package cluster

import (
	"net"
	"strings"

	"github.com/bongo-project/bongo/internal/config"
)

// clusterBindHost returns the host the raft and gossip transports listen on.
//
// server.bind_addr is shared with every protocol listener, so it is normally
// 0.0.0.0. The cluster transports must not inherit that when the node
// advertises a loopback address: peers would be handed 127.0.0.1 and could
// never reach it, so the only thing a wildcard bind achieves is exposing the
// transports to other containers on a shared network. A routable (or hostname)
// advertise address is the operator's signal that peers exist, so bind_addr is
// used unchanged; that keeps the bootstrap seed of a multi-node cluster, which
// has an empty cluster.join, reachable.
//
// In the dev role a wildcard (or empty) bind_addr with no advertise address and
// no cluster.join binds loopback: a single ephemeral node has no peers, so
// gossip has no reason to listen on every interface. An explicit routable
// bind_addr or advertise_addr, or a join list, is honoured.
//
// Any 127/8 advertise address makes raft and gossip bind that address, which
// must exist on the host (127.0.0.2 is not configured by default on macOS).
func clusterBindHost(cfg *config.Config) string {
	if cfg.Server.Role == "dev" && cfg.Server.AdvertiseAddr == "" && len(cfg.Cluster.Join) == 0 && isUnspecified(cfg.Server.BindAddr) {
		return "127.0.0.1"
	}
	host := cfg.Server.AdvertiseAddr
	if host == "" {
		host = cfg.Server.BindAddr
	}
	if !config.IsLoopbackHost(host) {
		return cfg.Server.BindAddr
	}
	if strings.EqualFold(host, "localhost") {
		return "127.0.0.1"
	}
	return host
}

// isUnspecified reports whether addr is empty or a wildcard address.
func isUnspecified(addr string) bool {
	if addr == "" {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsUnspecified()
}
