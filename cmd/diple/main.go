//go:build unix

// Command diple runs an AI coding-agent CLI inside a transparent terminal
// layer. `diple <agent> [args…]` is the wrapped form the shims call.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/maximalfocus/diple/internal/adapter"
	_ "github.com/maximalfocus/diple/internal/adapter/claude"
	_ "github.com/maximalfocus/diple/internal/adapter/codex"
	_ "github.com/maximalfocus/diple/internal/adapter/pi"
	"github.com/maximalfocus/diple/internal/agent"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/keys"
	"github.com/maximalfocus/diple/internal/persona"
	"github.com/maximalfocus/diple/internal/shim"
	"github.com/maximalfocus/diple/internal/wrap"
)

const usage = `usage: diple [--record <fixture>] [--plain] [--marks=off] <agent> [args…]
       diple blocks <transcript>
       diple on [<agent>…] | off | status
       diple stash|unstash [<agent>]
       diple bindings

Runs <agent> (for example claude) under Diple. Non-interactive invocations
(-p/--print, --version, --help) and sessions without a terminal run the real
agent directly.

  --record <fixture>      write the session as a fixture for replay
  --plain                 draw without colour
  --marks=off             no tail mark on annotated blocks, and no raise
  --motion=off            draw the raise's final frame only
  --copy-on-select=off    copy only on an explicit copy, not on every selection
  --no-archive            do not archive sent folds
  --identity              run the agent as itself, owning nothing
                          (what an identity-only shim asks for)
  on [<agent>…]           install shims for the agents found that Diple
                          wraps, or for the agents named; one Diple has no
                          adapter for runs as itself (identity-only)
  off                     remove the shims and the PATH line
  status                  report the agents found, wrapped and identity-only,
                          the shims, PATH, and startup files
  blocks <transcript>     print the turns and blocks detected in a transcript
  stash [<agent>]         set the agent's saved tray aside
  unstash [<agent>]       give the stashed tray back to the next session
  bindings                print the effective key bindings
`

func main() {
	if code, ok := runPersona(); ok {
		os.Exit(code)
	}
	os.Exit(run(os.Args[1:]))
}

// personaEnv carries a wrapped session across Diple's re-execution through
// the agent's persona. It is one of Diple's own variables, and like them it
// never reaches the agent.
const personaEnv = "DIPLE_PERSONA"

// ownEnv is every variable Diple itself reads or sets. None of them reaches
// the agent, so the agent, and any host integration running inside it, sees
// what the bare CLI would.
var ownEnv = []string{"DIPLE", "DIPLE_SHIM_DIR", personaEnv}

func clearOwnEnv() {
	for _, v := range ownEnv {
		_ = os.Unsetenv(v)
	}
}

// personaRun is what the persona needs to go on wrapping: the real agent
// already located, the name the user typed, and Diple's own options.
type personaRun struct {
	Path           string `json:"path"`
	Name           string `json:"name"`
	Record         string `json:"record,omitempty"`
	Plain          bool   `json:"plain,omitempty"`
	NoMarks        bool   `json:"no_marks,omitempty"`
	NoMotion       bool   `json:"no_motion,omitempty"`
	NoCopyOnSelect bool   `json:"no_copy_on_select,omitempty"`
	NoArchive      bool   `json:"no_archive,omitempty"`
}

// runPersona continues a wrapped session in the persona Diple re-executed
// itself through. ok is false when this process is not a persona.
func runPersona() (code int, ok bool) {
	payload, isPersona := os.LookupEnv(personaEnv)
	if !isPersona {
		return 0, false
	}
	clearOwnEnv()
	var pr personaRun
	if err := json.Unmarshal([]byte(payload), &pr); err != nil || pr.Path == "" || pr.Name == "" {
		fmt.Fprintln(os.Stderr, "diple: the persona was started without its session")
		return 1, true
	}
	f := flags{record: pr.Record, plain: pr.Plain, noMarks: pr.NoMarks, noMotion: pr.NoMotion,
		noCopyOnSelect: pr.NoCopyOnSelect, noArchive: pr.NoArchive}
	return runWrapped(pr.Path, pr.Name, os.Args[1:], f), true
}

type flags struct {
	record         string
	plain          bool
	noMarks        bool
	noMotion       bool
	noCopyOnSelect bool
	noArchive      bool
	identity       bool
}

func run(args []string) int {
	var f flags
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--plain":
			f.plain, args = true, args[1:]
		case a == "--marks=off" || a == "--no-marks":
			f.noMarks, args = true, args[1:]
		case a == "--marks=on":
			f.noMarks, args = false, args[1:]
		case a == "--motion=off":
			f.noMotion, args = true, args[1:]
		case a == "--motion=on":
			f.noMotion, args = false, args[1:]
		case a == "--copy-on-select=off":
			f.noCopyOnSelect, args = true, args[1:]
		case a == "--copy-on-select=on":
			f.noCopyOnSelect, args = false, args[1:]
		case a == "--no-archive":
			f.noArchive, args = true, args[1:]
		case a == "--identity":
			f.identity, args = true, args[1:]
		case a == "--record":
			if len(args) < 2 {
				fmt.Fprint(os.Stderr, usage)
				return 64
			}
			f.record, args = args[1], args[2:]
		case strings.HasPrefix(a, "--record="):
			f.record, args = strings.TrimPrefix(a, "--record="), args[1:]
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, usage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "diple: unknown option %s\n%s", a, usage)
			return 64
		case a == "blocks":
			return printBlocks(args[1:])
		case a == "stash" || a == "unstash":
			return stash(a, args[1:])
		case a == "bindings":
			return bindings(args[1:])
		case a == "on" || a == "off" || a == "status":
			return shims(a, args[1:])
		default:
			return wrapAgent(a, args[1:], f)
		}
	}
	fmt.Fprint(os.Stderr, usage)
	return 64
}

func wrapAgent(name string, args []string, f flags) int {
	self, _ := os.Executable()
	// The shim that launched us says which directory to skip; without one,
	// the installed shim directory is skipped anyway, so a shim on PATH can
	// never send Diple back to itself.
	skip := os.Getenv("DIPLE_SHIM_DIR")
	if skip == "" {
		skip, _ = shim.Dir()
	}
	path, err := agent.Locate(name, os.Getenv("PATH"), skip, self)
	if err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "diple: %s: command not found\n", name)
			return 127
		}
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	// Never execute one of Diple's own shims as the agent: that is an
	// endless loop, and it means the shim directory was not skipped.
	if shim.IsShim(path) {
		fmt.Fprintf(os.Stderr, "diple: %s on PATH is a diple shim, not the real %s; run `diple status`\n", path, name)
		return 127
	}
	argv0 := name
	if strings.ContainsRune(name, os.PathSeparator) {
		argv0 = filepath.Base(name)
	}

	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	bypass := !interactive || os.Getenv("DIPLE") == "0" || agent.Bypass(args)
	clearOwnEnv()
	if bypass || f.identity {
		// A non-interactive form runs the real agent directly, and so does an
		// identity-only one: the process in the pane is the real CLI, and
		// Diple owns nothing there.
		argv := append([]string{argv0}, args...)
		err := syscall.Exec(path, argv, os.Environ())
		fmt.Fprintf(os.Stderr, "diple: exec %s: %v\n", path, err)
		return 126
	}

	// Before wrapping, Diple re-executes itself, same process, through the
	// agent's persona, so a host that inspects the process in its pane sees
	// the agent the user launched. A persona that cannot be made costs the
	// host its name for the agent, never the session.
	if self != "" {
		if dir, err := persona.Dir(); err == nil {
			if p, err := persona.Ensure(dir, self, argv0); err == nil {
				payload, _ := json.Marshal(personaRun{
					Path: path, Name: argv0, Record: f.record, Plain: f.plain,
					NoMarks: f.noMarks, NoMotion: f.noMotion,
					NoCopyOnSelect: f.noCopyOnSelect, NoArchive: f.noArchive,
				})
				env := append(os.Environ(), personaEnv+"="+string(payload))
				_ = syscall.Exec(p, append([]string{argv0}, args...), env)
			}
		}
	}
	return runWrapped(path, argv0, args, f)
}

// runWrapped runs the located agent inside Diple, under the name the user
// typed.
func runWrapped(path, argv0 string, args []string, f flags) int {
	ad, _ := adapter.For(argv0)
	table, complaints := loadBindings()
	for _, c := range complaints {
		fmt.Fprintf(os.Stderr, "diple: %s\n", c)
	}
	code, err := wrap.Run(wrap.Options{
		Path: path, Name: argv0, Args: args, Record: f.record, Adapter: ad, Keys: table,
		Plain: f.plain, NoMarks: f.noMarks, NoMotion: f.noMotion, NoCopyOnSelect: f.noCopyOnSelect, NoArchive: f.noArchive,
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// shims implements `diple on|off|status`: the shims that make Diple
// default-on, and what is in place right now. `diple on` shims the catalogued
// agents found on PATH that Diple wraps; `diple on <agent>…` shims the agents
// named, and one without an adapter is identity-only.
func shims(op string, args []string) int {
	if op != "on" && len(args) != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 64
	}
	dir, err := shim.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	startup, err := shim.StartupFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	path := os.Getenv("PATH")
	switch op {
	case "status":
		fmt.Print(shim.Report(dir, agent.Found(path, dir, self), path, startup))
		return 0
	case "on":
		var wrapped, identity []string
		if len(args) == 0 {
			for _, a := range agent.Found(path, dir, self) {
				if _, ok := adapter.For(a); ok {
					wrapped = append(wrapped, a)
				}
			}
		}
		for _, a := range args {
			if !persona.ValidName(a) {
				fmt.Fprintf(os.Stderr, "diple: %q is not a command name\n", a)
				return 64
			}
			if _, err := agent.Locate(a, path, dir, self); err != nil {
				fmt.Fprintf(os.Stderr, "diple: %s: command not found\n", a)
				return 127
			}
			if _, ok := adapter.For(a); ok {
				wrapped = append(wrapped, a)
			} else {
				identity = append(identity, a)
			}
		}
		if len(wrapped)+len(identity) == 0 {
			fmt.Printf("no agent Diple wraps is on PATH (it knows %s); "+
				"`diple on <agent>` shims any other\n", strings.Join(adapter.Names(), ", "))
			return 0
		}
		changed, err := shim.Install(dir, self, wrapped, identity, startup)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diple: %v\n", err)
			return 1
		}
		report(changed, fmt.Sprintf("shims for %s already in place",
			strings.Join(append(wrapped, identity...), ", ")))
		fmt.Printf("re-read your profile or run: export PATH=%q\n", dir+":"+path)
		return 0
	default:
		changed, err := shim.Remove(dir, startup)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diple: %v\n", err)
			return 1
		}
		report(changed, "nothing to remove")
		return 0
	}
}

func report(changed []string, none string) {
	if len(changed) == 0 {
		fmt.Println(none)
		return
	}
	for _, c := range changed {
		fmt.Println(c)
	}
}

// bindings implements `diple bindings`: the effective table, defaults and
// the user's file together, in the file's own syntax.
func bindings(args []string) int {
	if len(args) != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 64
	}
	table, complaints := loadBindings()
	for _, c := range complaints {
		fmt.Fprintf(os.Stderr, "diple: %s\n", c)
	}
	fmt.Print(table)
	return 0
}

// loadBindings reads the user's binding file over the defaults. Its
// complaints are reported and the session still starts: a config file must
// never cost the user their terminal.
func loadBindings() (keys.Table, []string) {
	path, err := keys.DefaultPath()
	if err != nil {
		return keys.Defaults(), []string{fmt.Sprintf("bindings: %v", err)}
	}
	return keys.Load(path)
}

// stash implements `diple stash|unstash [<agent>]`: the tray a session saved
// is set aside in the agent's one stash slot, and unstashing gives it to the
// agent's next session when it binds its transcript.
func stash(op string, args []string) int {
	agent := ""
	switch len(args) {
	case 0:
	case 1:
		agent = args[0]
	default:
		fmt.Fprint(os.Stderr, usage)
		return 64
	}
	st, err := card.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	if agent == "" {
		// Nothing assumes one agent: the current or most recent session's
		// agent is meant, and Diple asks when that is ambiguous.
		ok := false
		if op == "stash" {
			agent, ok = st.NewestAgent(adapter.Names())
		} else {
			agent, ok = st.StashedAgent(adapter.Names())
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "diple: which agent's tray? run `diple %s <agent>` with one of: %s\n",
				op, strings.Join(adapter.Names(), ", "))
			return 64
		}
	}
	if _, ok := adapter.For(agent); !ok {
		fmt.Fprintf(os.Stderr, "diple: no adapter for %s; known: %s\n",
			agent, strings.Join(adapter.Names(), ", "))
		return 64
	}
	var n int
	if op == "stash" {
		n, err = st.Stash(agent)
	} else {
		n, err = st.Unstash(agent)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	noun := "cards"
	if n == 1 {
		noun = "card"
	}
	if n == 0 {
		fmt.Printf("nothing to %s for %s\n", op, agent)
		return 0
	}
	fmt.Printf("%sed %d %s for %s\n", op, n, noun, agent)
	return 0
}

// printBlocks implements `diple blocks <transcript>`: the turns and blocks
// an adapter detects in a transcript file, one block per line, as fixtures
// and adapter authors need to see them.
func printBlocks(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 64
	}
	f, err := os.Open(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	defer f.Close()
	var (
		tr     *adapter.Transcript
		perr   error
		chosen string
	)
	for _, name := range adapter.Names() {
		ad, _ := adapter.For(name)
		if _, err := f.Seek(0, 0); err != nil {
			fmt.Fprintf(os.Stderr, "diple: %v\n", err)
			return 1
		}
		tr, perr = ad.Parse(f)
		chosen = name
		if perr == nil {
			break
		}
	}
	if perr != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", perr)
		return 1
	}
	version := tr.Version
	if tr.Unverified {
		version += " (unverified)"
	}
	fmt.Printf("agent %s version %s session %s turns %d\n",
		chosen, version, tr.SessionID, len(tr.Turns))
	for _, turn := range tr.Turns {
		fmt.Printf("turn %d\n", turn.Ordinal)
		for i, b := range turn.Blocks {
			text := b.Text
			if b.Kind == "code-block" {
				text = fmt.Sprintf("(%d lines, %s)", strings.Count(b.Text, "\n")+1, b.Lang)
			}
			if b.Kind == "list-item" {
				text = fmt.Sprintf("[%d] %s", b.Ordinal, text)
			}
			if b.Parent >= 0 {
				text = "  " + text
			}
			fmt.Printf("  %3d %-10s %s\n", i, b.Kind, text)
		}
	}
	return 0
}
