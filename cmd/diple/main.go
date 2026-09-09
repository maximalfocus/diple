//go:build unix

// Command diple runs an AI coding-agent CLI inside a transparent terminal
// layer. `diple <agent> [args…]` is the wrapped form the shims call.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/maximalfocus/diple/internal/adapter"
	_ "github.com/maximalfocus/diple/internal/adapter/claude"
	"github.com/maximalfocus/diple/internal/agent"
	"github.com/maximalfocus/diple/internal/wrap"
)

const usage = `usage: diple [--record <fixture>] [--plain] [--no-marks] <agent> [args…]
       diple blocks <transcript>

Runs <agent> (for example claude) under Diple. Non-interactive invocations
(-p/--print, --version, --help) and sessions without a terminal run the real
agent directly.

  --record <fixture>   write the session as a fixture for replay
  --plain              draw only with reverse and underline
  --no-marks           no gutter mark on annotated blocks
  blocks <transcript>  print the turns and blocks detected in a transcript
`

func main() {
	os.Exit(run(os.Args[1:]))
}

type flags struct {
	record  string
	plain   bool
	noMarks bool
}

func run(args []string) int {
	var f flags
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--plain":
			f.plain, args = true, args[1:]
		case a == "--no-marks":
			f.noMarks, args = true, args[1:]
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
		default:
			return wrapAgent(a, args[1:], f)
		}
	}
	fmt.Fprint(os.Stderr, usage)
	return 64
}

func wrapAgent(name string, args []string, f flags) int {
	self, _ := os.Executable()
	path, err := agent.Locate(name, os.Getenv("PATH"), "", self)
	if err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "diple: %s: command not found\n", name)
			return 127
		}
		fmt.Fprintf(os.Stderr, "diple: %v\n", err)
		return 1
	}
	argv0 := name
	if strings.ContainsRune(name, os.PathSeparator) {
		argv0 = filepath.Base(name)
	}

	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if !interactive || agent.Bypass(args) {
		argv := append([]string{argv0}, args...)
		err := syscall.Exec(path, argv, os.Environ())
		fmt.Fprintf(os.Stderr, "diple: exec %s: %v\n", path, err)
		return 126
	}

	ad, _ := adapter.For(argv0)
	code, err := wrap.Run(wrap.Options{
		Path: path, Name: argv0, Args: args, Record: f.record, Adapter: ad, Plain: f.plain, NoMarks: f.noMarks,
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
		if perr == nil || errors.As(perr, new(*adapter.VersionError)) {
			break
		}
	}
	if perr != nil {
		fmt.Fprintf(os.Stderr, "diple: %v\n", perr)
		return 1
	}
	fmt.Printf("agent %s version %s session %s turns %d\n", chosen, tr.Version, tr.SessionID, len(tr.Turns))
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
