//go:build unix

package main

import (
	"fmt"

	"golang.org/x/term"
)

// sprintState renders a terminal state for comparison. term.State keeps the
// platform termios unexported, so its printed form is the portable handle.
func sprintState(s *term.State) string { return fmt.Sprintf("%+v", *s) }
