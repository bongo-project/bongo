package config

import (
	"fmt"
	"net"
	"slices"
	"strconv"
)

// Production default ports and their dev-mode replacements. Dev substitutes
// only when the configured value is still the production default, so an
// explicitly configured port is always honoured -- which is what lets each
// in-process test agent bind its own ports. The runtime listeners and
// Validate both read these through the ListenPort(s) methods, so the two can
// not disagree about which port a dev node binds.
const (
	defaultSMTPPort       = 25
	defaultSubmissionPort = 587
	devSMTPPort           = 2525
	devSubmissionPort     = 2587

	defaultIMAPPort    = 143
	defaultIMAPTLSPort = 993
	devIMAPPort        = 1143
	devIMAPTLSPort     = 1993

	defaultPOP3Port    = 110
	defaultPOP3TLSPort = 995
	devPOP3Port        = 1110
	devPOP3TLSPort     = 1995

	defaultWebPort = 443
	devWebPort     = 8080

	// defaultRedirectPort is used when web.http_redirect_port is 0.
	defaultRedirectPort = 80
)

// substituteDev returns the dev replacement when dev is set and port is still
// the production default.
func substituteDev(port, prodDefault, devDefault int, dev bool) int {
	if dev && port == prodDefault {
		return devDefault
	}
	return port
}

// ListenPorts returns the MX and submission ports the SMTP server binds.
func (s *SMTPConfig) ListenPorts(dev bool) (mx, submission int) {
	return substituteDev(s.Port, defaultSMTPPort, devSMTPPort, dev),
		substituteDev(s.SubmissionPort, defaultSubmissionPort, devSubmissionPort, dev)
}

// ListenPorts returns the plain and implicit-TLS ports the IMAP server binds.
func (i *IMAPConfig) ListenPorts(dev bool) (plain, implicitTLS int) {
	return substituteDev(i.Port, defaultIMAPPort, devIMAPPort, dev),
		substituteDev(i.PortTLS, defaultIMAPTLSPort, devIMAPTLSPort, dev)
}

// ListenPorts returns the plain and implicit-TLS ports the POP3 server binds.
func (p *POP3Config) ListenPorts(dev bool) (plain, implicitTLS int) {
	return substituteDev(p.Port, defaultPOP3Port, devPOP3Port, dev),
		substituteDev(p.PortTLS, defaultPOP3TLSPort, devPOP3TLSPort, dev)
}

// ListenPort returns the port the web listener binds.
func (w *WebConfig) ListenPort(dev bool) int {
	return substituteDev(w.Port, defaultWebPort, devWebPort, dev)
}

// RedirectPort returns the port of the HTTP to HTTPS redirect listener; an
// unset (0) port means 80.
func (w *WebConfig) RedirectPort() int {
	if w.HTTPRedirectPort == 0 {
		return defaultRedirectPort
	}
	return w.HTTPRedirectPort
}

// redirectListenerEnabled mirrors the condition under which the agent binds
// the redirect listener: redirect on, a web certificate that will actually be
// loaded (plain_http skips TLS entirely), and not the dev or agent role
// (runAgent serves health only and never binds it).
func (c *Config) redirectListenerEnabled() bool {
	return c.Web.HTTPSRedirect && !c.Web.PlainHTTP &&
		c.Web.TLS.CertFile != "" && c.Web.TLS.KeyFile != "" &&
		c.Server.Role != "dev" && c.Server.Role != "agent"
}

type listenerPort struct {
	key  string
	port int
}

// enabledListenerPorts lists the TCP ports this node will bind, using the
// effective values (dev substitutions, redirect default). Ports of listeners
// that do not run are left out so they cannot cause a false collision:
//
//   - web always binds, in every role; web.enabled is not consulted at runtime.
//   - the control listener runs in every role except agent.
//   - smtp, imap and pop3 bind on server and dev nodes when enabled; the smtp
//     agent binds the smtp ports when "smtp" is in server.agents.
//   - raft binds TCP only for the server role; dev raft is in-memory.
//   - gossip runs on server and dev nodes.
//   - the peer listener runs only when ClusterHasPeers.
//   - telemetry.prometheus.port is dead config (nothing binds it) and is
//     excluded; add it here if metrics ever get their own listener.
func (c *Config) enabledListenerPorts() ([]listenerPort, error) {
	dev := c.Server.Role == "dev"
	mailNode := c.Server.Role == "server" || dev

	var ports []listenerPort
	add := func(key string, port int) { ports = append(ports, listenerPort{key, port}) }

	if c.Server.Role != "agent" {
		_, portStr, err := net.SplitHostPort(c.Server.ControlAddr)
		if err != nil {
			return nil, fmt.Errorf("server.control_addr: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("server.control_addr: port %q is not a number", portStr)
		}
		add("server.control_addr", port)
	}
	add("web.port", c.Web.ListenPort(dev))
	if c.redirectListenerEnabled() {
		add("web.http_redirect_port", c.Web.RedirectPort())
	}
	// The SMTP agent binds both SMTP ports whenever "smtp" is in
	// server.agents, regardless of smtp.enabled, and never in dev mode.
	if c.Server.Role == "agent" && slices.Contains(c.Server.Agents, "smtp") {
		mx, sub := c.SMTP.ListenPorts(false)
		add("smtp.port", mx)
		add("smtp.submission_port", sub)
	}
	if mailNode {
		if c.SMTP.Enabled {
			mx, sub := c.SMTP.ListenPorts(dev)
			add("smtp.port", mx)
			add("smtp.submission_port", sub)
		}
		if c.IMAP.Enabled {
			plain, tlsPort := c.IMAP.ListenPorts(dev)
			add("imap.port", plain)
			add("imap.port_tls", tlsPort)
		}
		if c.POP3.Enabled {
			plain, tlsPort := c.POP3.ListenPorts(dev)
			add("pop3.port", plain)
			add("pop3.port_tls", tlsPort)
		}
		add("cluster.gossip.port", c.Cluster.Gossip.Port)
	}
	if c.Server.Role == "server" {
		add("cluster.raft.port", c.Cluster.Raft.Port)
	}
	if c.ClusterHasPeers() {
		add("cluster.peer_port", c.Cluster.PeerPort)
	}
	return ports, nil
}

// validateListenerPorts requires every enabled listener port to be 1-65535
// and no two enabled listeners to share a port. Ports are compared regardless
// of host: the listeners share server.bind_addr, 0.0.0.0 conflicts with any
// specific address on the same port, and the control listener is on loopback.
// That refuses a few setups that would bind (raft on cluster.bind_addr
// 10.0.0.6:8300 beside web on 10.0.0.5:8300), which is deliberate.
func (c *Config) validateListenerPorts() error {
	ports, err := c.enabledListenerPorts()
	if err != nil {
		return err
	}
	seen := make(map[int]string, len(ports))
	for _, lp := range ports {
		if lp.port < 1 || lp.port > 65535 {
			return fmt.Errorf("%s: %d is out of range (1-65535)", lp.key, lp.port)
		}
		if first, dup := seen[lp.port]; dup {
			return fmt.Errorf("%s: %d is also used by %s; every listener needs its own port", lp.key, lp.port, first)
		}
		seen[lp.port] = lp.key
	}
	return nil
}
