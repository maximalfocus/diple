// Command hostcheck replays a recorded session and reports whether Diple
// behaved in the host that produced it: that it asked for the mouse and gave
// it back, forwarded the agent's bytes untouched while it owned nothing, drew
// without 24-bit colour, drew the tray when it had cards, and left the
// terminal as it found it.
//
// It is the check behind `scripts/verify-hosts.sh`, and it reads a capture
// written by `diple --record`. Its subcommands are the verification tiers'
// uses of it: `replay` replays the committed captures (tier 3), `report` files
// a passing capture as head-bound evidence and `evidence` verifies that
// evidence (tier 5), and `classify` decides whether a change needs it.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	_ "github.com/maximalfocus/diple/internal/adapter/claude"
	_ "github.com/maximalfocus/diple/internal/adapter/codex"
	_ "github.com/maximalfocus/diple/internal/adapter/pi"
	"github.com/maximalfocus/diple/internal/record"
	"github.com/maximalfocus/diple/internal/wrap"
)

// truecolour is an SGR that selects a 24-bit colour, which Diple never emits.
var truecolour = regexp.MustCompile(`\x1b\[[0-9;]*\b(38|48);2;`)

// sgr is any SGR Diple emits while drawing. Under --plain it may still use
// the bold, dim, reverse, and underline attributes, but no colour at all.
var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// driven names the hosts R-014 puts in the driven class: those offering a
// documented way to type into a running window from outside. Their captures
// must carry a gesture and the card it made. Every other host, named or not,
// is pass-through only, and its capture covers forwarding, the mouse
// envelope, and restore. A driven host with no gesture in its capture was not
// driven at all, and saying so is the point: falling back to the pass-through
// check is how an unverified host came to report a pass.
var drivenHosts = map[string]bool{
	"wezterm": true,
	"kitty":   true,
	"tmux":    true,
	"herdr":   true,
}

func main() {
	if len(os.Args) > 1 {
		if run, ok := subcommands[os.Args[1]]; ok {
			os.Exit(run(os.Args[2:]))
		}
	}
	host := flag.String("host", "", "the host the capture came from")
	version := flag.String("host-version", "", "the version of the host it came from")
	plain := flag.Bool("plain", false, "the capture was recorded with --plain")
	flag.Parse()
	if flag.NArg() != 1 || *host == "" {
		fmt.Fprintln(os.Stderr, "usage: hostcheck --host <name> [--host-version <v>] [--plain] <capture>")
		os.Exit(64)
	}
	// R-014 records the version a host was verified at, as R-012 does for an
	// adapter's CLI: a host's control interface is a versioned dependency,
	// and a run that does not name it cannot be read later.
	named := *host
	if *version != "" {
		named = fmt.Sprintf("%s %s", *host, *version)
	}
	failures := check(*host, flag.Arg(0), *plain)
	for _, f := range failures {
		fmt.Fprintf(os.Stderr, "FAIL %s: %s\n", named, f)
	}
	if len(failures) > 0 {
		os.Exit(1)
	}
	fmt.Printf("PASS %s: %s\n", named, flag.Arg(0))
}

func check(host, path string, plain bool) []string {
	rec, err := record.Open(path)
	if err != nil {
		return []string{fmt.Sprintf("cannot read the capture: %v", err)}
	}
	var failures []string

	// First pass: the agent's bytes alone, with nothing typed. This is what
	// R-002 promises in every host — the terminal sees exactly what the bare
	// CLI would have written.
	bare, _, _, err := replay(rec, false, plain)
	if err != nil {
		return []string{err.Error()}
	}
	agent := agentBytes(rec)
	body := strings.TrimSuffix(strings.TrimPrefix(bare, wrap.EnvelopeStart), wrap.EnvelopeEnd)
	if !strings.HasPrefix(bare, wrap.EnvelopeStart) {
		failures = append(failures, "the mouse envelope was never asked for")
	}
	if !strings.HasSuffix(bare, wrap.EnvelopeEnd) {
		failures = append(failures, "the mouse envelope was never given back")
	}
	if body != agent {
		failures = append(failures, "the agent's bytes were not forwarded unchanged with an empty tray")
	}

	// Second pass: the session as it was actually driven in the host.
	driven, cards, copies, err := replay(rec, true, plain)
	if err != nil {
		return append(failures, err.Error())
	}
	drawn := drawnByDiple(driven, bare)
	if exercised(rec) {
		if cards == 0 {
			failures = append(failures, "a gesture was typed in this host but no card was made")
		}
		if drawn == "" {
			failures = append(failures, "a gesture was typed in this host but Diple drew nothing")
		}
		// R-015: a copy that no host delivers is no copy, so a capture that
		// carries a gesture must also carry the copy that gesture made.
		if copies == 0 {
			failures = append(failures, "a gesture was typed in this host but nothing reached the clipboard")
		}
	}
	if truecolour.MatchString(drawn) {
		failures = append(failures, "Diple drew with 24-bit colour")
	}
	if plain {
		if bad := disallowedUnderPlain(drawn); bad != "" {
			failures = append(failures, "--plain drawing used "+bad)
		}
	}
	if !exercised(rec) {
		if drivenHosts[host] {
			failures = append(failures, "this host is driven, but no gesture reached its capture: it was not verified")
		} else {
			fmt.Printf("note %s: pass-through only; the capture covers forwarding, the envelope, and restore\n", host)
		}
	}
	return failures
}

// replay runs a capture through a fresh session, in the drawing mode it was
// recorded in, optionally feeding the input the host recorded. It returns
// what the terminal received and how many cards the session ended with.
//
// The capture stamps every event with the time since the session started, so
// the replay runs on that clock: the dwell that raises a block is real time in
// the host, and a replay that ignored it could never reproduce the gesture.
func replay(rec *record.Recording, withInput, plain bool) (string, int, int, error) {
	var terminal bytes.Buffer
	sess := wrap.NewSession(&terminal, &bytes.Buffer{}, rec.Header.Cols, rec.Header.Rows, nil)
	sess.Plain = plain
	// The replay must be the session that ran, adapter and all: without one
	// there are no blocks to raise, and the annotate gesture would appear not
	// to have arrived for a reason that has nothing to do with the host. The
	// replay finds no transcript, so alignment falls back to paragraphs, which
	// is the same path the recorded session took.
	if ad, ok := adapter.For(rec.Header.Agent); ok {
		sess.UseAdapter(ad, rec.Header.Agent)
	}
	base := time.Unix(0, 0)
	at := base
	sess.SetClock(func() time.Time { return at })
	if err := sess.Start(); err != nil {
		return "", 0, 0, fmt.Errorf("session did not start: %v", err)
	}
	for _, ev := range rec.Events {
		at = base.Add(ev.At)
		if err := sess.Tick(); err != nil {
			return "", 0, 0, fmt.Errorf("the raise failed: %v", err)
		}
		switch ev.Kind {
		case record.KindOutput:
			if err := sess.HandleOutput(ev.Data); err != nil {
				return "", 0, 0, fmt.Errorf("forwarding failed: %v", err)
			}
		case record.KindInput:
			if !withInput {
				continue
			}
			if err := sess.HandleInput(ev.Data); err != nil {
				return "", 0, 0, fmt.Errorf("input handling failed: %v", err)
			}
		case record.KindResize:
			_ = sess.Resize(ev.Cols, ev.Rows)
		}
	}
	cards, copies := sess.Tray.Len(), sess.Copies()
	if err := sess.Stop(); err != nil {
		return "", 0, 0, fmt.Errorf("session did not stop cleanly: %v", err)
	}
	return terminal.String(), cards, copies, nil
}

// agentBytes is everything the wrapped agent wrote.
func agentBytes(rec *record.Recording) string {
	var b strings.Builder
	for _, ev := range rec.Events {
		if ev.Kind == record.KindOutput {
			b.Write(ev.Data)
		}
	}
	return b.String()
}

// exercised reports whether a Diple gesture reached the host, which is when
// Diple must draw, a card must appear, and a copy must reach the clipboard.
// The gesture claims no modifier: it is a press — on a raised block, or the
// start of a drag — or the free-card key. The pointer merely crossing a
// pass-through host's window, or its wheel, reports motion that makes no card,
// and counting it turned a person's hand on the mouse into a failure.
func exercised(rec *record.Recording) bool {
	for _, ev := range rec.Events {
		if ev.Kind != record.KindInput {
			continue
		}
		for _, sig := range [][]byte{[]byte("\x1bn"), []byte("\x1b[<0;")} {
			if bytes.Contains(ev.Data, sig) {
				return true
			}
		}
	}
	return false
}

// drawnByDiple is what the driven session wrote beyond what the undriven one
// wrote: Diple's own drawing, and nothing of the agent's.
func drawnByDiple(driven, bare string) string {
	i := 0
	for i < len(driven) && i < len(bare) && driven[i] == bare[i] {
		i++
	}
	return driven[i:]
}

// disallowedUnderPlain names the first thing Diple drew that --plain forbids.
// --plain drops colour entirely and leaves the bold, dim, reverse, and
// underline attributes, so any colour parameter is a failure and those
// attributes are not.
func disallowedUnderPlain(drawn string) string {
	allowed := map[string]bool{
		"": true, "0": true, // reset
		"1": true, "2": true, "22": true, // bold, dim, and their reset
		"4": true, "24": true, // underline and its reset
		"7": true, "27": true, // reverse and its reset
	}
	for _, m := range sgr.FindAllStringSubmatch(drawn, -1) {
		for _, p := range strings.Split(m[1], ";") {
			if !allowed[p] {
				return "SGR " + p
			}
		}
	}
	return ""
}
