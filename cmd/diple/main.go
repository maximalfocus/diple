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

	"github.com/maximalfocus/diple/internal/agent"
	"github.com/maximalfocus/diple/internal/wrap"
)

const usage = `usage: diple [--record <fixture>] <agent> [args…]

Runs <agent> (for example claude) under Diple. Non-interactive invocations
(-p/--print, --version, --help) and sessions without a terminal run the real
agent directly.

  --record <fixture>   write the session as a fixture for replay
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	recordPath := ""
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--record":
			if len(args) < 2 {
				fmt.Fprint(os.Stderr, usage)
				return 64
			}
			recordPath, args = args[1], args[2:]
		case strings.HasPrefix(a, "--record="):
			recordPath, args = strings.TrimPrefix(a, "--record="), args[1:]
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, usage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "diple: unknown option %s\n%s", a, usage)
			return 64
		default:
			return wrapAgent(a, args[1:], recordPath)
		}
	}
	fmt.Fprint(os.Stderr, usage)
	return 64
}

func wrapAgent(name string, args []string, recordPath string) int {
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

	code, err := wrap.Run(wrap.Options{
		Path: path, Name: argv0, Args: args, Record: recordPath,
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
