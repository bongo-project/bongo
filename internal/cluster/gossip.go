package cluster

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/hashicorp/memberlist"

	"github.com/bongo-project/bongo/internal/config"
)

// NodeMeta is the metadata each node broadcasts via memberlist gossip.
type NodeMeta struct {
	Role     string `json:"role"`
	RaftAddr string `json:"raft_addr"`
	HTTPAddr string `json:"http_addr"`
}

// GossipNode wraps a memberlist instance with its configuration.
type GossipNode struct {
	List   *memberlist.Memberlist
	logger *slog.Logger
}

// NewGossipNode creates and starts a memberlist gossip node.
func NewGossipNode(cfg *config.Config, httpAddr string, raftAddr string, logger *slog.Logger) (*GossipNode, error) {
	mlCfg := memberlist.DefaultLANConfig()
	mlCfg.Name = cfg.Server.Name
	bindHost, err := cfg.ClusterBindHost()
	if err != nil {
		return nil, err
	}
	mlCfg.BindAddr = bindHost
	mlCfg.BindPort = cfg.Cluster.Gossip.Port
	mlCfg.LogOutput = io.Discard // We use slog.

	// Gossip encryption/authentication. The encryption_key was previously a
	// declared-but-ignored config field, so setting it gave a false sense of a
	// protected gossip pool while traffic stayed plaintext and unauthenticated.
	// Wire it into memberlist's SecretKey and fail hard on a malformed key
	// rather than silently continuing without encryption.
	if rawKey := cfg.Cluster.Gossip.EncryptionKey; rawKey != "" {
		secret, err := base64.StdEncoding.DecodeString(rawKey)
		if err != nil {
			return nil, fmt.Errorf("cluster.gossip.encryption_key must be base64-encoded: %w", err)
		}
		switch len(secret) {
		case 16, 24, 32:
			mlCfg.SecretKey = secret
		default:
			return nil, fmt.Errorf("cluster.gossip.encryption_key must decode to 16, 24, or 32 bytes (got %d)", len(secret))
		}
	}

	// Use advertise address if set.
	if cfg.Server.AdvertiseAddr != "" {
		mlCfg.AdvertiseAddr = cfg.Server.AdvertiseAddr
	}

	meta := NodeMeta{
		Role:     cfg.Server.Role,
		RaftAddr: raftAddr,
		HTTPAddr: httpAddr,
	}

	delegate := &gossipDelegate{meta: meta}
	events := &gossipEvents{logger: logger}

	mlCfg.Delegate = delegate
	mlCfg.Events = events

	list, err := memberlist.Create(mlCfg)
	if err != nil {
		return nil, fmt.Errorf("creating memberlist: %w", err)
	}

	logger.Info("memberlist started",
		"name", mlCfg.Name,
		"bind", net.JoinHostPort(mlCfg.BindAddr, strconv.Itoa(mlCfg.BindPort)),
		"role", cfg.Server.Role,
	)

	return &GossipNode{
		List:   list,
		logger: logger,
	}, nil
}

// Join joins an existing cluster by contacting the given addresses.
func (g *GossipNode) Join(addrs []string) (int, error) {
	n, err := g.List.Join(addrs)
	if err != nil {
		return n, fmt.Errorf("joining cluster: %w", err)
	}
	g.logger.Info("joined cluster", "contacted", n, "addrs", addrs)
	return n, nil
}

// Members returns the current list of cluster members with their metadata.
func (g *GossipNode) Members() []MemberInfo {
	nodes := g.List.Members()
	members := make([]MemberInfo, 0, len(nodes))
	for _, n := range nodes {
		info := MemberInfo{
			Name:   n.Name,
			Addr:   n.Addr.String(),
			Port:   int(n.Port),
			Status: nodeStateString(n.State),
		}
		// Parse metadata if available.
		if len(n.Meta) > 0 {
			var meta NodeMeta
			if err := json.Unmarshal(n.Meta, &meta); err == nil {
				info.Role = meta.Role
				info.RaftAddr = meta.RaftAddr
				info.HTTPAddr = meta.HTTPAddr
			}
		}
		members = append(members, info)
	}
	return members
}

// Shutdown gracefully leaves the cluster and shuts down memberlist.
func (g *GossipNode) Shutdown() error {
	if err := g.List.Leave(5 * time.Second); err != nil {
		g.logger.Warn("memberlist leave failed", "error", err)
	}
	return g.List.Shutdown()
}

// MemberInfo represents a cluster member with parsed metadata.
type MemberInfo struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Port     int    `json:"port"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	RaftAddr string `json:"raft_addr,omitempty"`
	HTTPAddr string `json:"http_addr,omitempty"`
}

// gossipDelegate implements memberlist.Delegate.
type gossipDelegate struct {
	meta NodeMeta
}

func (d *gossipDelegate) NodeMeta(limit int) []byte {
	data, _ := json.Marshal(d.meta)
	if len(data) > limit {
		return nil
	}
	return data
}

func (d *gossipDelegate) NotifyMsg([]byte)                           {}
func (d *gossipDelegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *gossipDelegate) LocalState(join bool) []byte                { return nil }
func (d *gossipDelegate) MergeRemoteState(buf []byte, join bool)     {}

// gossipEvents implements memberlist.EventDelegate.
type gossipEvents struct {
	logger *slog.Logger
}

func (e *gossipEvents) NotifyJoin(n *memberlist.Node) {
	e.logger.Info("memberlist: node joined", "name", n.Name, "addr", n.Addr)
}

func (e *gossipEvents) NotifyLeave(n *memberlist.Node) {
	e.logger.Info("memberlist: node left", "name", n.Name, "addr", n.Addr)
}

func (e *gossipEvents) NotifyUpdate(n *memberlist.Node) {
	e.logger.Debug("memberlist: node updated", "name", n.Name, "addr", n.Addr)
}

func nodeStateString(s memberlist.NodeStateType) string {
	switch s {
	case memberlist.StateAlive:
		return "alive"
	case memberlist.StateSuspect:
		return "suspect"
	case memberlist.StateDead:
		return "dead"
	case memberlist.StateLeft:
		return "left"
	default:
		return "unknown"
	}
}
