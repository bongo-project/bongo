// Command loadgen generates load against a Bongo test deployment.
//
//	go run ./scripts/loadgen check --target-allow HOST[:PORT] --test-domain DOMAIN [--target HOST:PORT] [--from ADDR] [--rcpt ADDR]
//
// The tool is fail-closed. run parses the flags of every subcommand and calls
// Validate before it dispatches, so no subcommand can reach the network
// without passing the safety layer. A subcommand that dials or addresses
// mail later must also call checkTarget and checkAddress for each connection
// and each envelope address it derives itself (seed data, files), because
// those are not known when the flags are parsed.
//
// Exit codes: 0 ok, 2 usage error or refusal.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// command runs after Validate has accepted cfg.
type command func(cfg Config, stdout, stderr io.Writer) int

const (
	exitOK     = 0
	exitRefuse = 2
)

// commands holds every subcommand; a subcommand in its own file may register
// itself here from init(). All of them inherit the guard in runWith.
var commands = map[string]command{
	"check": runCheck,
	"smtp":  runSMTP,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWith(commands, args, stdout, stderr)
}

func runWith(table map[string]command, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(table, stderr)
		return exitRefuse
	}
	name := args[0]
	cmd, ok := table[name]
	if !ok {
		fmt.Fprintf(stderr, "loadgen: unknown subcommand %q\n", name)
		usage(table, stderr)
		return exitRefuse
	}
	var cfg Config
	fs := newFlagSet(name, &cfg, stderr)
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flagHelp) {
			return exitOK
		}
		return exitRefuse
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "loadgen: unexpected argument %q\n", fs.Arg(0))
		return exitRefuse
	}
	// The guard sits here, not in each command, so a new subcommand cannot
	// forget it.
	if err := Validate(cfg); err != nil {
		fmt.Fprintf(stderr, "loadgen: refused: %v\n", err)
		return exitRefuse
	}
	return cmd(cfg, stdout, stderr)
}

func usage(table map[string]command, w io.Writer) {
	names := make([]string, 0, len(table))
	for n := range table {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "usage: loadgen <%s> [flags]\n", strings.Join(names, "|"))
}

// runCheck reports the validated configuration. It sends nothing.
func runCheck(cfg Config, stdout, _ io.Writer) int {
	allow := make([]string, len(cfg.Allow))
	for i, t := range cfg.Allow {
		allow[i] = t.String()
	}
	override := "no"
	if cfg.IOwnThisTarget {
		override = "yes"
	}
	fmt.Fprintf(stdout, "allowed targets: %s\n", strings.Join(allow, ", "))
	fmt.Fprintf(stdout, "targets: %s\n", strings.Join(cfg.Targets, ", "))
	fmt.Fprintf(stdout, "test domain: %s\n", cfg.TestDomain)
	fmt.Fprintf(stdout, "senders: %d, recipients: %d\n", len(cfg.Senders), len(cfg.Recipients))
	fmt.Fprintf(stdout, "production override: %s\nconfiguration ok\n", override)
	return exitOK
}
