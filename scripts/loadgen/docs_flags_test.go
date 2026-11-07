package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

const loadTestingDoc = "../../docs/load-testing.md"

// loadgenCommand finds a loadgen invocation in the runbook: a built binary or
// go run.
var loadgenCommand = regexp.MustCompile(`(?:go run \./scripts/loadgen|\./loadgen)\s+([a-z][a-z-]*)`)

// docCommand is one loadgen invocation found in the runbook.
type docCommand struct {
	sub   string
	flags []string
	line  int
	// stray is set when a backslash sits inside the command rather than at
	// the end of a line: the shell turns it into a positional argument.
	stray bool
}

// parseDocCommands returns every loadgen command in doc. A trailing backslash
// continues a command onto the next line. Flags are collected up to the end of
// the command: a pipe, a shell operator, a redirect or a comment ends it, so
// the flags of the next command are not credited to loadgen.
func parseDocCommands(doc string) []docCommand {
	var cmds []docCommand
	lines := strings.Split(doc, "\n")
	for i := 0; i < len(lines); i++ {
		start := i
		joined := lines[i]
		for strings.HasSuffix(strings.TrimRight(joined, " \t"), `\`) && i+1 < len(lines) {
			joined = strings.TrimSuffix(strings.TrimRight(joined, " \t"), `\`) + " " + lines[i+1]
			i++
		}
		m := loadgenCommand.FindStringSubmatchIndex(joined)
		if m == nil {
			continue
		}
		c := docCommand{sub: joined[m[2]:m[3]], line: start + 1}
	tokens:
		for _, tok := range strings.Fields(joined[m[3]:]) {
			switch {
			case tok == "|", tok == "||", tok == "&&", tok == ";", tok == "&",
				strings.HasPrefix(tok, ">"), strings.HasPrefix(tok, "2>"),
				strings.HasPrefix(tok, "#"), strings.HasPrefix(tok, "`"),
				strings.HasPrefix(tok, "<"):
				break tokens
			case strings.Contains(tok, `\`):
				c.stray = true
			case strings.HasPrefix(tok, "--") && len(tok) > 2:
				name := strings.TrimPrefix(tok, "--")
				if eq := strings.IndexByte(name, '='); eq >= 0 {
					name = name[:eq]
				}
				c.flags = append(c.flags, name)
			}
		}
		cmds = append(cmds, c)
	}
	return cmds
}

// flagNames lists the names a flag set registers.
func flagNames(fs *flag.FlagSet) map[string]bool {
	names := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { names[f.Name] = true })
	return names
}

// checkDocFlags returns one message for each command in doc that names an
// unknown subcommand or a flag the subcommand does not register.
//
// newFlagSet adds the smtp flags (--rate, --ramp and the --smtp-* family) to
// every subcommand, so a lookup alone would accept --rate on clients. Those
// flags are accepted here only on smtp, or where the subcommand registers
// them itself.
func checkDocFlags(doc string) (problems []string, seen map[string]int) {
	seen = map[string]int{}
	smtpOnly := flagNames(func() *flag.FlagSet {
		fs := flag.NewFlagSet("smtp", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		registerSMTPFlags(fs, &smtpConfig{})
		return fs
	}())
	for _, c := range parseDocCommands(doc) {
		if _, ok := commands[c.sub]; !ok {
			problems = append(problems, fmt.Sprintf("line %d: unknown subcommand %q", c.line, c.sub))
			continue
		}
		seen[c.sub]++
		if c.stray {
			problems = append(problems, fmt.Sprintf("line %d: loadgen %s has a backslash inside the line", c.line, c.sub))
		}
		fs := newFlagSet(c.sub, &Config{}, io.Discard)
		own := map[string]bool{}
		if add := subcommandFlags[c.sub]; add != nil {
			sub := flag.NewFlagSet(c.sub, flag.ContinueOnError)
			sub.SetOutput(io.Discard)
			add(sub, &Config{})
			own = flagNames(sub)
		}
		for _, name := range c.flags {
			switch {
			case fs.Lookup(name) == nil:
				problems = append(problems, fmt.Sprintf("line %d: loadgen %s has no flag --%s", c.line, c.sub, name))
			case smtpOnly[name] && c.sub != "smtp" && !own[name]:
				problems = append(problems, fmt.Sprintf("line %d: --%s is an smtp flag and loadgen %s does not use it", c.line, name, c.sub))
			}
		}
	}
	return problems, seen
}

// TestLoadTestingDocFlags proves every flag the runbook gives to loadgen is a
// flag the subcommand registers, so the runbook cannot drift from the tool.
func TestLoadTestingDocFlags(t *testing.T) {
	b, err := os.ReadFile(loadTestingDoc)
	if err != nil {
		t.Fatal(err)
	}
	problems, seen := checkDocFlags(string(b))
	for _, p := range problems {
		t.Error(p)
	}
	total := 0
	for _, n := range seen {
		total += n
	}
	// A parser that matches nothing must not pass.
	if total < 10 {
		t.Fatalf("found %d loadgen commands in the runbook, want at least 10", total)
	}
	for sub := range commands {
		if seen[sub] == 0 {
			t.Errorf("the runbook has no command for subcommand %q", sub)
		}
	}
}

// TestCheckDocFlagsRejects feeds the checker input it must refuse. Each case
// is otherwise a valid command, so a pass would mean the check is blind.
func TestCheckDocFlagsRejects(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "misspelt flag",
			doc:  "```sh\n./loadgen clients --test-domain bongo.test --clients-insecure-tsl\n```\n",
			want: "no flag --clients-insecure-tsl",
		},
		{
			name: "misspelt flag after a continuation",
			doc:  "go run ./scripts/loadgen sample --test-domain bongo.test \\\n  --sample-insecure-tsl\n",
			want: "no flag --sample-insecure-tsl",
		},
		{
			name: "misspelt flag with a value",
			doc:  "./loadgen seed --userz=10\n",
			want: "no flag --userz",
		},
		{
			name: "smtp flag on another subcommand",
			doc:  "./loadgen clients --test-domain bongo.test --rate 10\n",
			want: "is an smtp flag",
		},
		{
			name: "backslash inside a line",
			doc:  "./loadgen smtp --rate 150 \\  --smtp-duration 72h\n",
			want: "backslash inside the line",
		},
		{
			name: "unknown subcommand",
			doc:  "./loadgen spray --test-domain bongo.test\n",
			want: "unknown subcommand",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems, _ := checkDocFlags(tt.doc)
			if len(problems) != 1 || !strings.Contains(problems[0], tt.want) {
				t.Fatalf("problems = %q, want one containing %q", problems, tt.want)
			}
		})
	}
}

// TestCheckDocFlagsAccepts proves the parser's boundaries: valid flags,
// a flag in a pipe's next command, and the smtp flag on smtp.
func TestCheckDocFlagsAccepts(t *testing.T) {
	doc := "./loadgen smtp --rate=10 --smtp-size 100 --from a@bongo.test > out.txt\n" +
		"./loadgen check --test-domain bongo.test | grep --nonsense\n" +
		"go build -o loadgen ./scripts/loadgen\n"
	problems, seen := checkDocFlags(doc)
	if len(problems) != 0 {
		t.Fatalf("problems = %q", problems)
	}
	if seen["smtp"] != 1 || seen["check"] != 1 {
		t.Fatalf("seen = %v", seen)
	}
}
