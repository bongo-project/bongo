package sendmail

import (
	"net/mail"
	"strings"
)

// DefaultConfigPath is read when --config is not given.
const DefaultConfigPath = "/etc/bongo/sendmail.conf"

// Options is the parsed command line.
type Options struct {
	Recipients     []string // command-line recipients, all qualified
	ReadRecipients bool     // -t
	IgnoreDots     bool     // -i, -oi
	Sender         string   // -f
	FullName       string   // -F
	ConfigPath     string
}

// ParseArgs parses sendmail-style arguments (without argv[0]). The flags are
// not POSIX-style (-oi, -FCronDaemon, -B8BITMIME), so this is hand-written.
func ParseArgs(args []string) (*Options, error) {
	o := &Options{ConfigPath: DefaultConfigPath}
	var rcpts []string
	flagsDone := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if flagsDone || a == "" || a[0] != '-' {
			rcpts = append(rcpts, a)
			continue
		}
		// value returns the text attached to the flag letter, or the next
		// argument when nothing is attached.
		value := func(prefix string) (string, error) {
			if v := strings.TrimPrefix(a, prefix); v != "" {
				return v, nil
			}
			if i+1 >= len(args) {
				return "", usageErr("option %s requires a value", prefix)
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "--":
			flagsDone = true
		case a == "-t":
			o.ReadRecipients = true
		case a == "-i" || a == "-oi":
			o.IgnoreDots = true
		case a == "-odi", a == "-odb", a == "-oem", a == "-v", a == "-bm":
			// Accepted and ignored. -v stays a no-op so no trace can ever
			// echo the AUTH exchange.
		case strings.HasPrefix(a, "-B"):
			// -B8BITMIME and -B7BIT (Debian cron): body type hint, ignored.
			if a == "-B" {
				if i+1 >= len(args) {
					return nil, usageErr("option -B requires a value")
				}
				i++
			}
		case strings.HasPrefix(a, "-f"):
			v, err := value("-f")
			if err != nil {
				return nil, err
			}
			v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "<"), ">")
			if err := rejectControl("-f", v); err != nil {
				return nil, err
			}
			o.Sender = v
		case strings.HasPrefix(a, "-F"):
			v, err := value("-F")
			if err != nil {
				return nil, err
			}
			if err := rejectControl("-F", v); err != nil {
				return nil, err
			}
			o.FullName = v
		case a == "--config":
			if i+1 >= len(args) {
				return nil, usageErr("--config requires a path")
			}
			i++
			o.ConfigPath = args[i]
		case strings.HasPrefix(a, "--config="):
			o.ConfigPath = strings.TrimPrefix(a, "--config=")
		default:
			return nil, usageErr("unsupported option %s", a)
		}
	}
	if o.ConfigPath == "" {
		return nil, usageErr("--config requires a path")
	}
	for _, r := range rcpts {
		addr, err := qualifiedAddress(r)
		if err != nil {
			return nil, err
		}
		o.Recipients = append(o.Recipients, addr)
	}
	return o, nil
}

func rejectControl(flag, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return usageErr("%s value contains a control character", flag)
	}
	return nil
}

// qualifiedAddress returns the bare address of s, refusing one without a
// domain: the server would not route it, and a bare "root" is the usual sign of
// a MAILTO that is not a full address.
func qualifiedAddress(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", usageErr("recipient contains a control character")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || !strings.Contains(a.Address, "@") {
		return "", usageErr("recipient %q is not a full address (user@domain); set MAILTO to a full address", s)
	}
	return a.Address, nil
}
