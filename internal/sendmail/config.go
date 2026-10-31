package sendmail

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/mail"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// maxConfigBytes bounds the file read.
const maxConfigBytes = 64 << 10

// Config is the account used for submission. The file is HCL, like bongo.hcl.
type Config struct {
	Host               string `hcl:"host"`
	Port               int    `hcl:"port,optional"`
	TLS                string `hcl:"tls,optional"` // "implicit" or "starttls"
	Username           string `hcl:"username"`
	Secret             string `hcl:"password"`
	InsecureSkipVerify bool   `hcl:"insecure_skip_verify,optional"`
}

// LoadConfig reads and validates the config file. The file is opened first and
// inspected through the open handle, so the permission check describes the
// bytes that are parsed.
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-supplied config path
	if err != nil {
		return nil, configErr("cannot open config %s: %v", path, reason(err))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, configErr("cannot stat config %s: %v", path, reason(err))
	}
	if !info.Mode().IsRegular() {
		return nil, configErr("config %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, configErr("config %s is accessible by group or others (mode %04o); run chmod 600", path, info.Mode().Perm())
	}
	src, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, configErr("cannot read config %s: %v", path, reason(err))
	}
	if len(src) > maxConfigBytes {
		return nil, configErr("config %s is too large", path)
	}
	return parseConfig(src, path)
}

func reason(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// parseConfig decodes HCL source. hclsimple is not used because it picks the
// format from the extension and "sendmail.conf" is not ".hcl".
func parseConfig(src []byte, name string) (*Config, error) {
	file, diags := hclparse.NewParser().ParseHCL(src, "sendmail.hcl")
	var cfg Config
	if !diags.HasErrors() {
		diags = gohcl.DecodeBody(file.Body, nil, &cfg)
	}
	if diags.HasErrors() {
		return nil, configErr("config %s is invalid: %s", name, diagSummary(diags))
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// diagSummary reports each error's summary and line only. The detail text can
// quote the offending source line, which may be the password.
func diagSummary(diags hcl.Diagnostics) string {
	var parts []string
	for _, d := range diags {
		if d.Severity != hcl.DiagError {
			continue
		}
		s := d.Summary
		if d.Subject != nil {
			s += fmt.Sprintf(" (line %d)", d.Subject.Start.Line)
		}
		parts = append(parts, s)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

func (c *Config) validate() error {
	if c.Host == "" || strings.ContainsAny(c.Host, " \t\r\n/") {
		return configErr("config: host is missing or invalid")
	}
	if c.Port == 0 {
		c.Port = 465
	}
	if c.Port < 1 || c.Port > 65535 {
		return configErr("config: port %d is out of range", c.Port)
	}
	switch c.TLS {
	case "":
		if c.Port == 587 {
			c.TLS = "starttls"
		} else {
			c.TLS = "implicit"
		}
	case "implicit", "starttls":
	default:
		return configErr("config: tls must be \"implicit\" or \"starttls\"")
	}
	if a, err := mail.ParseAddress(c.Username); err != nil || a.Address != c.Username || !strings.Contains(c.Username, "@") {
		return configErr("config: username must be the full account address (user@domain)")
	}
	if c.Secret == "" {
		return configErr("config: password is missing")
	}
	if c.InsecureSkipVerify && !isLoopbackHost(c.Host) {
		return configErr("config: insecure_skip_verify is only allowed for a loopback host (127.0.0.0/8, ::1 or localhost)")
	}
	return nil
}

// isLoopbackHost accepts only literal loopback IPs and the name "localhost".
// Other names are not resolved: a DNS answer can change between this check and
// the connection, which would turn the check into a hole.
func isLoopbackHost(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// domain returns the account's mail domain.
func (c *Config) domain() string {
	return c.Username[strings.LastIndex(c.Username, "@")+1:]
}
