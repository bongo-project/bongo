package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// neverRunsAgent names test functions that build config.Defaults() but provably
// never start an agent in the server role. Dev and agent roles bind nothing in
// NewAgent (every listener is bound in Run), but the server role binds the
// Raft transport there, so a server-role test belongs on isolatePorts even if
// it never calls Run. Keep this list short.
var neverRunsAgent = map[string]bool{
	"TestNewAgentDev":  true,
	"newShutdownAgent": true,
}

// isolatingHelpers draw their ports through isolatePorts themselves (checked
// by this test as ordinary functions), so calling one counts as isolation.
var isolatingHelpers = map[string]bool{
	"isolatePorts":         true,
	"isolatePortsExtra":    true,
	"readyTestConfig":      true,
	"readyEphemeralConfig": true,
	"peerRunConfig":        true,
	"redirectRunConfig":    true,
}

// TestRunBasedTestsIsolatePorts fails when a test function constructs an Agent
// (NewAgent) without drawing its ports through isolatePorts or a helper that
// does. Defaults leave gossip on 8301 and the Raft transport on 8300, and dev
// mode swaps IMAP, POP3 and SMTP onto fixed dev ports, so such a test collides
// with any other agent on the machine, including another package's.
//
// The rule is keyed on NewAgent, not on config.Defaults(), so a config built in
// a helper (agentConfig, say) and handed to an agent here cannot escape.
//
// A function that passes one of its own *config.Config parameters to NewAgent, or to another
// such function, is a constructor wrapper (newReadyTestAgent, runAgentUntilReady).
// Wrappers are found by fixpoint over all files and are exempt themselves, but
// every caller of NewAgent or of a wrapper must call an isolating helper (or be
// a wrapper itself, passing its own cfg parameter on).
//
// Config closures (returning *config.Config and calling config.Defaults(),
// inside a function that builds agents) are checked on their own, because the
// caller rule judges a whole test function and one isolated closure in a table
// would excuse its siblings.
//
// Limitation: the guard checks that an isolating helper is called somewhere in
// the function, not that a later assignment does not override a port again. What
// isolatePorts does is pinned by TestIsolatePortsAvoidsFixedPorts.
func TestRunBasedTestsIsolatePorts(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()

	type fnInfo struct {
		file, name string
		calls      []*ast.CallExpr
		params     map[string]bool // parameters of type *config.Config
	}
	type parsedFile struct {
		name string
		file *ast.File
	}
	var parsed []parsedFile
	var fns []*fnInfo
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed = append(parsed, parsedFile{name, f})
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			info := &fnInfo{file: name, name: fn.Name.Name, params: map[string]bool{}}
			for _, fl := range fn.Type.Params.List {
				if !returnsConfig(fl.Type) {
					continue
				}
				for _, n := range fl.Names {
					info.params[n.Name] = true
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					info.calls = append(info.calls, call)
				}
				return true
			})
			fns = append(fns, info)
		}
	}

	// First pass: constructors are NewAgent plus every wrapper, to a fixpoint.
	constructors := map[string]bool{"NewAgent": true, "newReadyTestAgent": true}
	wrappers := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			if wrappers[fn.name] {
				continue
			}
			for _, call := range fn.calls {
				id, ok := call.Fun.(*ast.Ident)
				if !ok || !constructors[id.Name] {
					continue
				}
				passesCfg := false
				for _, a := range call.Args {
					if arg, ok := a.(*ast.Ident); ok && fn.params[arg.Name] {
						passesCfg = true
					}
				}
				if passesCfg {
					wrappers[fn.name] = true
					constructors[fn.name] = true
					changed = true
					break
				}
			}
		}
	}

	// Second pass: every non-wrapper caller of a constructor must isolate.
	var offenders []string
	for _, fn := range fns {
		if wrappers[fn.name] || neverRunsAgent[fn.name] {
			continue
		}
		var builds, isolated bool
		for _, call := range fn.calls {
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				continue
			}
			if constructors[id.Name] {
				builds = true
			}
			if isolatingHelpers[id.Name] {
				isolated = true
			}
		}
		if builds && !isolated {
			offenders = append(offenders, fn.file+": "+fn.name)
		}
	}
	buildsAgents := map[string]bool{}
	for _, fn := range fns {
		for _, call := range fn.calls {
			if id, ok := call.Fun.(*ast.Ident); ok && constructors[id.Name] {
				buildsAgents[fn.name] = true
			}
		}
	}

	// Config closures: inside a function that builds agents, a closure returning
	// *config.Config and calling config.Defaults() must isolate within its own
	// body. Without this a table of config closures passed to one wrapper call is
	// judged as a whole, and one isolated sibling would excuse an un-isolated one.
	var factoryOffenders []string
	for _, f := range parsed {
		check := func(label string, ft *ast.FuncType, body *ast.BlockStmt) {
			if body == nil || ft.Results == nil || len(ft.Results.List) != 1 || !returnsConfig(ft.Results.List[0].Type) {
				return
			}
			var defaults, isolated bool
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					if x, ok := fun.X.(*ast.Ident); ok && x.Name == "config" && fun.Sel.Name == "Defaults" {
						defaults = true
					}
				case *ast.Ident:
					if isolatingHelpers[fun.Name] {
						isolated = true
					}
				}
				return true
			})
			if defaults && !isolated {
				factoryOffenders = append(factoryOffenders, label)
			}
		}
		for _, decl := range f.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || isolatingHelpers[fn.Name.Name] {
				continue
			}
			if !buildsAgents[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.FuncLit); ok {
					check(f.name+": "+fn.Name.Name+" (closure at line "+strconv.Itoa(fset.Position(lit.Pos()).Line)+")", lit.Type, lit.Body)
				}
				return true
			})
		}
	}
	sort.Strings(factoryOffenders)
	for _, o := range factoryOffenders {
		t.Errorf("%s builds config.Defaults() without isolatePorts", o)
	}

	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("%s constructs an Agent without isolatePorts", o)
	}
}

// returnsConfig reports whether a type expression is *config.Config.
func returnsConfig(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "config" && sel.Sel.Name == "Config"
}

// TestIsolatePortsAvoidsFixedPorts pins what isolatePorts does, which the guard
// above cannot see: for every role, every port an agent binds (as the config
// resolves it, dev substitution included) must be off the production defaults
// and the fixed dev ports, distinct from the others, and gossip must be 0.
func TestIsolatePortsAvoidsFixedPorts(t *testing.T) {
	_, defaultControl, err := net.SplitHostPort(config.Defaults().Server.ControlAddr)
	if err != nil {
		t.Fatal(err)
	}
	control, _ := strconv.Atoi(defaultControl)

	for _, role := range []string{"dev", "server", "agent", "edge"} {
		t.Run(role, func(t *testing.T) {
			def := config.Defaults()
			def.Server.Role = role
			dev := role == "dev"
			fixed := map[int]bool{control: true, def.Cluster.Gossip.Port: true, 443: true}
			add := func(ports ...int) {
				for _, p := range ports {
					fixed[p] = true
				}
			}
			add(def.Web.ListenPort(false), def.Web.ListenPort(true), def.Cluster.Raft.Port, def.Cluster.PeerPort)
			add(def.SMTP.ListenPorts(false))
			add(def.SMTP.ListenPorts(true))
			add(def.SMTP.SubmissionsListenPort(false), def.SMTP.SubmissionsListenPort(true))
			add(def.IMAP.ListenPorts(false))
			add(def.IMAP.ListenPorts(true))
			add(def.POP3.ListenPorts(false))
			add(def.POP3.ListenPorts(true))

			cfg := config.Defaults()
			cfg.Server.Role = role
			isolatePorts(t, cfg)

			_, ctl, err := net.SplitHostPort(cfg.Server.ControlAddr)
			if err != nil {
				t.Fatalf("control addr %q: %v", cfg.Server.ControlAddr, err)
			}
			ctlPort, _ := strconv.Atoi(ctl)
			mx, sub := cfg.SMTP.ListenPorts(dev)
			submissions := cfg.SMTP.SubmissionsListenPort(dev)
			imap, imaps := cfg.IMAP.ListenPorts(dev)
			pop, pops := cfg.POP3.ListenPorts(dev)
			named := []struct {
				name string
				port int
			}{
				{"web", cfg.Web.ListenPort(dev)}, {"control", ctlPort},
				{"smtp", mx}, {"submission", sub}, {"submissions", submissions},
				{"imap", imap}, {"imaps", imaps}, {"pop3", pop}, {"pop3s", pops},
				{"raft", cfg.Cluster.Raft.Port}, {"peer", cfg.Cluster.PeerPort},
			}
			seen := map[int]string{}
			for _, n := range named {
				if fixed[n.port] {
					t.Errorf("%s port %d is a default or fixed dev port", n.name, n.port)
				}
				if n.port == 0 {
					t.Errorf("%s port is 0", n.name)
				}
				if prev, dup := seen[n.port]; dup {
					t.Errorf("%s and %s share port %d", prev, n.name, n.port)
				}
				seen[n.port] = n.name
			}
			if cfg.Cluster.Gossip.Port != 0 {
				t.Errorf("gossip port = %d, want 0", cfg.Cluster.Gossip.Port)
			}
		})
	}
}
