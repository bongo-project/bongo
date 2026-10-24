package config

import "testing"

func TestClusterBindHost(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		bind, adv  string
		clusterBnd string
		join       []string
		want       string
		wantErr    bool
	}{
		{name: "loopback advertise narrows wildcard bind", role: "server", bind: "0.0.0.0", adv: "127.0.0.1", want: "127.0.0.1"},
		{name: "ipv6 loopback advertise", role: "server", bind: "0.0.0.0", adv: "::1", want: "::1"},
		{name: "localhost advertise", role: "server", bind: "0.0.0.0", adv: "localhost", want: "127.0.0.1"},
		{name: "loopback bind with no advertise", role: "server", bind: "127.0.0.1", want: "127.0.0.1"},
		{name: "routable advertise, server.bind_addr ignored", role: "server", bind: "0.0.0.0", adv: "10.0.0.5", want: "10.0.0.5"},
		{name: "routable bind, no advertise", role: "server", bind: "10.0.0.5", want: "10.0.0.5"},
		{name: "join with routable advertise", role: "server", bind: "0.0.0.0", adv: "10.0.0.5", join: []string{"10.0.0.2:8301"}, want: "10.0.0.5"},
		{name: "explicit cluster.bind_addr wins", role: "server", bind: "0.0.0.0", adv: "10.0.0.5", clusterBnd: "10.0.0.6", want: "10.0.0.6"},
		{name: "explicit cluster.bind_addr with loopback advertise", role: "server", bind: "0.0.0.0", adv: "127.0.0.1", clusterBnd: "10.0.0.6", want: "10.0.0.6"},
		{name: "cluster.bind_addr hostname rejected", role: "server", bind: "0.0.0.0", adv: "10.0.0.5", clusterBnd: "bongo-1", wantErr: true},
		{name: "cluster.bind_addr wildcard rejected", role: "server", bind: "0.0.0.0", adv: "10.0.0.5", clusterBnd: "0.0.0.0", wantErr: true},
		{name: "hostname advertise rejected", role: "server", bind: "0.0.0.0", adv: "bongo-1", wantErr: true},
		{name: "hostname advertise with cluster.bind_addr", role: "server", bind: "0.0.0.0", adv: "bongo-1", clusterBnd: "10.0.0.6", want: "10.0.0.6"},
		{name: "server wildcard bind, no advertise rejected", role: "server", bind: "0.0.0.0", wantErr: true},
		{name: "dev wildcard bind, no advertise, binds loopback", role: "dev", bind: "0.0.0.0", want: "127.0.0.1"},
		{name: "dev empty bind binds loopback", role: "dev", want: "127.0.0.1"},
		{name: "dev ipv6 wildcard binds loopback", role: "dev", bind: "::", want: "127.0.0.1"},
		{name: "dev routable advertise", role: "dev", bind: "0.0.0.0", adv: "10.0.0.5", want: "10.0.0.5"},
		{name: "dev routable bind honoured", role: "dev", bind: "10.0.0.5", want: "10.0.0.5"},
		{name: "dev join keeps wildcard bind", role: "dev", bind: "0.0.0.0", join: []string{"10.0.0.2:8301"}, want: "0.0.0.0"},
		{name: "dev hostname advertise rejected", role: "dev", bind: "0.0.0.0", adv: "bongo-1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Server.Role = tt.role
			cfg.Server.BindAddr = tt.bind
			cfg.Server.AdvertiseAddr = tt.adv
			cfg.Cluster.BindAddr = tt.clusterBnd
			cfg.Cluster.Join = tt.join
			got, err := cfg.ClusterBindHost()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ClusterBindHost = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ClusterBindHost: %v", err)
			}
			if got != tt.want {
				t.Errorf("ClusterBindHost = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClusterBindHostErrorPointsAtKey(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Role = "server"
	cfg.Server.BindAddr = "0.0.0.0"
	cfg.Server.AdvertiseAddr = "bongo-1"
	_, err := cfg.ClusterBindHost()
	if err == nil || !contains(err.Error(), "cluster.bind_addr") {
		t.Fatalf("error %v should point to cluster.bind_addr", err)
	}
}

func TestClusterHasPeers(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		bind, adv string
		join      []string
		want      bool
	}{
		{"single node loopback advertise", "server", "0.0.0.0", "127.0.0.1", nil, false},
		{"localhost advertise", "server", "0.0.0.0", "localhost", nil, false},
		{"loopback bind no advertise", "server", "127.0.0.1", "", nil, false},
		{"routable advertise", "server", "0.0.0.0", "10.0.0.5", nil, true},
		{"join with loopback advertise", "server", "0.0.0.0", "127.0.0.1", []string{"10.0.0.2:8301"}, true},
		{"cluster bind routable with loopback advertise", "server", "0.0.0.0", "127.0.0.1", nil, true},
		{"dev never has peers", "dev", "0.0.0.0", "10.0.0.5", []string{"10.0.0.2:8301"}, false},
		{"edge never has peers", "edge", "0.0.0.0", "10.0.0.5", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Server.Role = tt.role
			cfg.Server.BindAddr = tt.bind
			cfg.Server.AdvertiseAddr = tt.adv
			cfg.Cluster.Join = tt.join
			if tt.name == "cluster bind routable with loopback advertise" {
				cfg.Cluster.BindAddr = "192.168.1.88"
			}
			if got := cfg.ClusterHasPeers(); got != tt.want {
				t.Errorf("ClusterHasPeers = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateRejectsUnusableClusterBind(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Role = "server"
	cfg.Server.BindAddr = "0.0.0.0"
	cfg.Server.AdvertiseAddr = "bongo-1"
	if err := cfg.Validate(); err == nil || !contains(err.Error(), "cluster.bind_addr") {
		t.Fatalf("Validate = %v, want cluster.bind_addr error", err)
	}
	cfg.Cluster.BindAddr = "10.0.0.6"
	// Now only the other gates apply; the bind itself must be accepted.
	if err := cfg.Validate(); err != nil && contains(err.Error(), "cluster.bind_addr") {
		t.Fatalf("Validate rejected a valid cluster.bind_addr: %v", err)
	}
}

func TestClusterBindAddrIsRestartClass(t *testing.T) {
	if c, ok := ClassOf("cluster.bind_addr"); !ok || c != ClassRestart {
		t.Fatalf("ClassOf(cluster.bind_addr) = %v, %v; want restart", c, ok)
	}
}

func TestClusterPeerAddr(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		bind, adv string
		join      []string
		port      int
		want      string
	}{
		{"single node loopback advertise", "server", "0.0.0.0", "127.0.0.1", nil, 8302, ""},
		{"dev", "dev", "0.0.0.0", "10.0.0.5", nil, 8302, ""},
		{"routable advertise", "server", "0.0.0.0", "10.0.0.5", nil, 8302, "10.0.0.5:8302"},
		{"custom port", "server", "0.0.0.0", "10.0.0.5", nil, 9443, "10.0.0.5:9443"},
		{"ipv6 advertise", "server", "0.0.0.0", "2001:db8::5", nil, 8302, "[2001:db8::5]:8302"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Server.Role = tt.role
			cfg.Server.BindAddr = tt.bind
			cfg.Server.AdvertiseAddr = tt.adv
			cfg.Cluster.Join = tt.join
			cfg.Cluster.PeerPort = tt.port
			if got := cfg.ClusterPeerAddr(); got != tt.want {
				t.Errorf("ClusterPeerAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

// A config file written before cluster.peer_port existed must still load and
// get the default port.
func TestPeerPortDefaultsWhenAbsentFromFile(t *testing.T) {
	cfg, err := LoadFromFile("../../test/testdata/config.hcl")
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if cfg.Cluster.PeerPort != 8302 {
		t.Errorf("PeerPort = %d, want 8302", cfg.Cluster.PeerPort)
	}
}
