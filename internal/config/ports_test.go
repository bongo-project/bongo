package config

import (
	"strings"
	"testing"
)

// serverCfg is a single-node server config with loopback advertise, the base
// the port cases start from.
func serverCfg() *Config {
	c := Defaults()
	c.Server.Role = "server"
	c.Server.BindAddr = "0.0.0.0"
	c.Server.AdvertiseAddr = "127.0.0.1"
	return c
}

func TestValidateWildcardBindNeedsAdvertise(t *testing.T) {
	c := Defaults()
	c.Server.Role = "server"
	c.Server.BindAddr = "0.0.0.0"
	c.Server.AdvertiseAddr = ""
	c.Cluster.BindAddr = ""
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted a wildcard bind with no advertise_addr and no cluster.bind_addr")
	}
	for _, key := range []string{"server.bind_addr", "server.advertise_addr", "cluster.bind_addr"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}

	c.Server.AdvertiseAddr = "127.0.0.1"
	if err := c.Validate(); err != nil {
		t.Errorf("with advertise_addr 127.0.0.1: %v", err)
	}

	c.Server.AdvertiseAddr = ""
	c.Cluster.BindAddr = "10.0.0.6"
	if err := c.Validate(); err != nil {
		t.Errorf("with cluster.bind_addr 10.0.0.6: %v", err)
	}
}

func TestValidateListenerPorts(t *testing.T) {
	redirectOn := func(c *Config) {
		c.Web.HTTPSRedirect = true
		c.Web.TLS = TLSConfig{CertFile: "/certs/web.crt", KeyFile: "/certs/web.key"}
	}
	agentSMTP := func(c *Config) {
		c.Server.Role = "agent"
		c.Server.Agents = []string{"smtp"}
		c.AgentServer = &AgentConfig{ServerAddress: "127.0.0.1:8400", TLS: TLSConfig{CertFile: "c.crt", KeyFile: "c.key", CAFile: "ca.pem"}}
	}
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr []string // empty means must validate
	}{
		{"enabled pop3 against web", func(c *Config) {
			c.POP3.Enabled = true
			c.POP3.Port = 8303
			c.Web.Port = 8303
		}, []string{"pop3.port", "web.port"}},
		{"enabled pop3 tls port out of range", func(c *Config) {
			c.POP3.Enabled = true
			c.POP3.PortTLS = 65537
		}, []string{"pop3.port_tls"}},
		{"agent smtp against submission", func(c *Config) {
			agentSMTP(c)
			c.SMTP.Port = 2526
			c.SMTP.SubmissionPort = 2526
		}, []string{"smtp.port", "smtp.submission_port"}},
		{"agent smtp against web", func(c *Config) {
			agentSMTP(c)
			c.SMTP.Port = 8303
			c.Web.Port = 8303
		}, []string{"smtp.port", "web.port"}},
		{"agent smtp submission out of range", func(c *Config) {
			agentSMTP(c)
			c.SMTP.SubmissionPort = 70001
		}, []string{"smtp.submission_port"}},
		{"agent smtp never uses dev substitution", func(c *Config) {
			agentSMTP(c)
			c.Web.Port = 2525
		}, nil},
		{"agent without smtp ignores smtp ports", func(c *Config) {
			agentSMTP(c)
			c.Server.Agents = []string{"imap"}
			c.SMTP.Port = 8303
			c.Web.Port = 8303
		}, nil},
		{"imap tls port too high", func(c *Config) { c.IMAP.PortTLS = 70000 }, []string{"imap.port_tls"}},
		{"submission port negative", func(c *Config) { c.SMTP.SubmissionPort = -1 }, []string{"smtp.submission_port"}},
		{"raft port zero", func(c *Config) { c.Cluster.Raft.Port = 0 }, []string{"cluster.raft.port"}},
		{"redirect port too high", func(c *Config) {
			redirectOn(c)
			c.Web.HTTPRedirectPort = 65536
		}, []string{"web.http_redirect_port"}},
		{"peer against gossip", func(c *Config) {
			c.Server.AdvertiseAddr = "10.0.0.5"
			c.Cluster.PeerPort = 8303
			c.Cluster.Gossip.Port = 8303
		}, []string{"cluster.peer_port", "cluster.gossip.port"}},
		{"raft against web", func(c *Config) {
			c.Cluster.Raft.Port = 8303
			c.Web.Port = 8303
		}, []string{"cluster.raft.port", "web.port"}},
		{"control against imap", func(c *Config) {
			c.Server.ControlAddr = "127.0.0.1:8303"
			c.IMAP.Port = 8303
		}, []string{"server.control_addr", "imap.port"}},
		{"smtp against submission", func(c *Config) {
			c.SMTP.Port = 2526
			c.SMTP.SubmissionPort = 2526
		}, []string{"smtp.port", "smtp.submission_port"}},
		{"redirect against web", func(c *Config) {
			redirectOn(c)
			c.Web.Port = 8443
			c.Web.HTTPRedirectPort = 8443
		}, []string{"web.http_redirect_port", "web.port"}},
		{"redirect default 80 against web 80", func(c *Config) {
			redirectOn(c)
			c.Web.Port = 80
		}, []string{"web.http_redirect_port", "web.port"}},

		{"disabled pop3 may share a port", func(c *Config) {
			c.POP3.Enabled = false
			c.POP3.Port = 8303
			c.Web.Port = 8303
		}, nil},
		{"redirect off may share a port", func(c *Config) {
			redirectOn(c)
			c.Web.HTTPSRedirect = false
			c.Web.HTTPRedirectPort = 8443
			c.Web.Port = 8443
		}, nil},
		{"redirect without cert is not bound", func(c *Config) {
			c.Web.HTTPSRedirect = true
			c.Web.Port = 80
		}, nil},
		{"dev raft is in memory", func(c *Config) {
			c.Server.Role = "dev"
			c.Cluster.Raft.Port = 8303
			c.Web.Port = 8303
		}, nil},
		{"loopback server has no peer listener", func(c *Config) {
			c.Cluster.PeerPort = 8303
			c.IMAP.Port = 8303
		}, nil},
		{"dev explicit imap port 1143 is fine", func(c *Config) {
			c.Server.Role = "dev"
			c.IMAP.Port = 1143
		}, nil},
		{"dev effective imap port collides with explicit web", func(c *Config) {
			c.Server.Role = "dev"
			c.Web.Port = 1143
		}, []string{"imap.port", "web.port"}},
		{"dev gossip collides with effective web", func(c *Config) {
			c.Server.Role = "dev"
			c.Cluster.Gossip.Port = 8080
		}, []string{"cluster.gossip.port", "web.port"}},
		{"agent never binds the redirect listener", func(c *Config) {
			agentSMTP(c)
			c.Web.HTTPSRedirect = true
			c.Web.TLS.CertFile, c.Web.TLS.KeyFile = "/x/cert.pem", "/x/key.pem"
			c.Web.HTTPRedirectPort = c.Web.Port
		}, nil},
		{"dev effective web 8080 collides with control", func(c *Config) {
			c.Server.Role = "dev"
			c.Server.ControlAddr = "127.0.0.1:8080"
		}, []string{"server.control_addr", "web.port"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serverCfg()
			tt.modify(c)
			err := c.Validate()
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate accepted the config, want error naming %v", tt.wantErr)
			}
			for _, key := range tt.wantErr {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error %q does not name %s", err, key)
				}
			}
		})
	}
}

func TestValidateDefaultsAndProductionShape(t *testing.T) {
	dev := Defaults()
	dev.Server.Role = "dev"
	if err := dev.Validate(); err != nil {
		t.Errorf("dev defaults: %v", err)
	}
	if err := serverCfg().Validate(); err != nil {
		t.Errorf("server defaults with loopback advertise: %v", err)
	}

	for _, plain := range []bool{false, true} {
		c := serverCfg()
		c.Web.Port = 8080
		c.Web.PlainHTTP = plain
		if !plain {
			c.Web.TLS = TLSConfig{CertFile: "/certs/web.crt", KeyFile: "/certs/web.key"}
			c.Web.HTTPSRedirect = true
		}
		c.SMTP.Port, c.SMTP.SubmissionPort = 25, 587
		c.IMAP.Port, c.IMAP.PortTLS = 143, 993
		c.POP3.Enabled = false
		if err := c.Validate(); err != nil {
			t.Errorf("production shape (plain_http=%v): %v", plain, err)
		}
	}
}

func TestListenPorts(t *testing.T) {
	d := Defaults()
	pair := func(a, b int) [2]int { return [2]int{a, b} }
	mx, sub := d.SMTP.ListenPorts(true)
	imapP, imapT := d.IMAP.ListenPorts(true)
	popP, popT := d.POP3.ListenPorts(true)
	if pair(mx, sub) != pair(2525, 2587) || pair(imapP, imapT) != pair(1143, 1993) ||
		pair(popP, popT) != pair(1110, 1995) || d.Web.ListenPort(true) != 8080 {
		t.Errorf("dev substitution: smtp %d/%d imap %d/%d pop3 %d/%d web %d",
			mx, sub, imapP, imapT, popP, popT, d.Web.ListenPort(true))
	}
	mx, sub = d.SMTP.ListenPorts(false)
	imapP, imapT = d.IMAP.ListenPorts(false)
	popP, popT = d.POP3.ListenPorts(false)
	if pair(mx, sub) != pair(25, 587) || pair(imapP, imapT) != pair(143, 993) ||
		pair(popP, popT) != pair(110, 995) || d.Web.ListenPort(false) != 443 {
		t.Errorf("production ports: smtp %d/%d imap %d/%d pop3 %d/%d", mx, sub, imapP, imapT, popP, popT)
	}
	d.SMTP.Port, d.SMTP.SubmissionPort = 2526, 2588
	d.IMAP.Port, d.IMAP.PortTLS = 1144, 1994
	d.POP3.Port, d.POP3.PortTLS = 1111, 1996
	d.Web.Port = 8081
	mx, sub = d.SMTP.ListenPorts(true)
	imapP, imapT = d.IMAP.ListenPorts(true)
	popP, popT = d.POP3.ListenPorts(true)
	if pair(mx, sub) != pair(2526, 2588) || pair(imapP, imapT) != pair(1144, 1994) ||
		pair(popP, popT) != pair(1111, 1996) || d.Web.ListenPort(true) != 8081 {
		t.Errorf("explicit ports must win in dev: smtp %d/%d imap %d/%d pop3 %d/%d web %d",
			mx, sub, imapP, imapT, popP, popT, d.Web.ListenPort(true))
	}
}
