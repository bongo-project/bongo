package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// flagHelp is returned by Parse after -h so run can exit 0.
var flagHelp = flag.ErrHelp

// Config is the parsed command line shared by every subcommand. It holds no
// secrets: generated passwords live only in Password values.
type Config struct {
	// Allow is the --target-allow list.
	Allow []Target
	// Targets are the hosts a run intends to contact; each must be in Allow.
	Targets        []string
	TestDomain     string
	IOwnThisTarget bool
	Senders        []string
	Recipients     []string
	// Seed holds the seed subcommand's options.
	Seed SeedOptions
	SMTP smtpConfig
	// Sink and Egress hold the sink and egress-check subcommands' options.
	Sink   sinkConfig
	Egress egressConfig
	// Clients holds the clients subcommand's options.
	Clients clientsOptions
}

// listFlag is a repeatable flag that also splits on commas.
type listFlag struct{ vals *[]string }

func (l listFlag) String() string {
	if l.vals == nil {
		return ""
	}
	return strings.Join(*l.vals, ",")
}

func (l listFlag) Set(s string) error {
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			// An empty item is a typo, and dropping it silently could
			// leave the allow list shorter than the operator meant.
			return errors.New("empty item in list")
		}
		*l.vals = append(*l.vals, item)
	}
	return nil
}

// targetAllowFlag parses --target-allow entries into Targets.
type targetAllowFlag struct{ cfg *Config }

func (f targetAllowFlag) String() string { return "" }

func (f targetAllowFlag) Set(s string) error {
	var items []string
	if err := (listFlag{&items}).Set(s); err != nil {
		return err
	}
	for _, it := range items {
		t, err := parseTarget(it)
		if err != nil {
			return fmt.Errorf("target-allow %q: %w", it, err)
		}
		f.cfg.Allow = append(f.cfg.Allow, t)
	}
	return nil
}

// subcommandFlags lets a subcommand register its own flags without editing
// newFlagSet; the file that defines the subcommand fills it in init.
var subcommandFlags = map[string]func(*flag.FlagSet, *Config){}

func newFlagSet(name string, cfg *Config, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Var(targetAllowFlag{cfg}, "target-allow", "host[:port] the tool may contact (repeatable or comma-separated; an entry without a port allows any port)")
	fs.Var(listFlag{&cfg.Targets}, "target", "host:port to contact; must be in --target-allow (repeatable or comma-separated)")
	fs.StringVar(&cfg.TestDomain, "test-domain", "", "every sender and recipient domain must be this domain or a subdomain of it")
	fs.BoolVar(&cfg.IOwnThisTarget, "i-own-this-target", false, "allow a target on the built-in production list; never lifts the test-domain rules")
	fs.Var(listFlag{&cfg.Senders}, "from", "envelope sender address (repeatable or comma-separated)")
	fs.Var(listFlag{&cfg.Recipients}, "rcpt", "envelope recipient address (repeatable or comma-separated)")
	if add := subcommandFlags[name]; add != nil {
		add(fs, cfg)
	}
	registerSMTPFlags(fs, &cfg.SMTP)
	return fs
}
