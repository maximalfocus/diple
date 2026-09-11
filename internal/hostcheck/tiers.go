package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// subcommands are the verification tiers' uses of the host check, beside the
// single-capture form scripts/verify-hosts.sh runs.
var subcommands = map[string]func(args []string) int{
	"replay":   replayMain,
	"report":   reportMain,
	"evidence": evidenceMain,
	"classify": classifyMain,
}

// portability is R-014's host list, by the names scripts/verify-hosts.sh uses.
// Tier 3 replays a committed capture of each in both drawing modes, and tier-5
// evidence must cover each in both.
var portability = []string{"terminal.app", "iterm2", "wezterm", "kitty", "ghostty", "tmux", "herdr"}

// drawingModes are the modes a capture is recorded in: the default palette,
// and --plain.
var drawingModes = []string{"default", "plain"}

// reportFormat is the version of the evidence report.
const reportFormat = 1

// defaultMaxAge is how long a report stays evidence. An older one has expired
// and is uploaded again, so a head that sat for weeks is verified against the
// hosts as they are now.
const defaultMaxAge = 14 * 24 * time.Hour

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// report is what a developer's live run attests to: one host at one version,
// in one drawing mode, running a build of one pull-request head, and the
// digest of the capture the host check passed.
type report struct {
	Format  int       `json:"format"`
	SHA     string    `json:"sha"`
	Host    string    `json:"host"`
	Version string    `json:"version"`
	Mode    string    `json:"mode"`
	Machine string    `json:"machine"`
	Capture string    `json:"capture"`
	Digest  string    `json:"digest"`
	Result  string    `json:"result"`
	Created time.Time `json:"created"`
}

func modeKnown(mode string) bool {
	for _, m := range drawingModes {
		if m == mode {
			return true
		}
	}
	return false
}

// replayMain is tier 3: every committed capture replays against this build,
// and every portability host has one in both drawing modes.
func replayMain(args []string) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 64
	}
	dir := filepath.Join("testdata", "hosts")
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	failures := replayCommitted(dir, os.Stdout)
	for _, f := range failures {
		fmt.Fprintf(os.Stderr, "FAIL %s\n", f)
	}
	if len(failures) > 0 {
		return 1
	}
	return 0
}

// replayCommitted checks each capture filed as <host>/<version>/<mode>.capture.jsonl
// under dir. Replay feeds the recorded input to the current implementation, so
// it catches a change that breaks what a host once delivered; it does not
// prove the host still delivers it.
func replayCommitted(dir string, out io.Writer) []string {
	hosts, err := os.ReadDir(dir)
	if err != nil {
		return []string{fmt.Sprintf("no committed host captures: %v", err)}
	}
	var failures []string
	seen := map[string]bool{}
	for _, h := range hosts {
		if !h.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(dir, h.Name()))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", h.Name(), err))
			continue
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			for _, mode := range drawingModes {
				p := filepath.Join(dir, h.Name(), v.Name(), mode+".capture.jsonl")
				if _, err := os.Stat(p); err != nil {
					continue
				}
				seen[h.Name()+" "+mode] = true
				named := fmt.Sprintf("%s %s %s", h.Name(), v.Name(), mode)
				problems := check(h.Name(), p, mode == "plain")
				for _, f := range problems {
					failures = append(failures, named+": "+f)
				}
				if len(problems) == 0 {
					fmt.Fprintf(out, "PASS replay %s\n", named)
				}
			}
		}
	}
	for _, h := range portability {
		for _, mode := range drawingModes {
			if !seen[h+" "+mode] {
				failures = append(failures, fmt.Sprintf("%s %s: no committed capture", h, mode))
			}
		}
	}
	return failures
}

// reportMain files one passing capture and its report in an evidence
// directory, for scripts/verify-hosts.sh --evidence.
func reportMain(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	sha := fs.String("sha", "", "the pull-request head the session was built from")
	host := fs.String("host", "", "the host the capture came from")
	version := fs.String("host-version", "", "the version of the host")
	mode := fs.String("mode", "default", "the drawing mode: default or plain")
	machine := fs.String("machine", "", "the machine the host ran on")
	out := fs.String("out", "", "the evidence directory to write into")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if fs.NArg() != 1 || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: hostcheck report --sha <sha> --host <name> --host-version <v> --mode default|plain --machine <m> --out <dir> <capture>")
		return 64
	}
	r := report{Format: reportFormat, SHA: *sha, Host: *host, Version: *version, Mode: *mode, Machine: *machine, Created: time.Now().UTC()}
	if err := writeReport(fs.Arg(0), *out, r); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL %s %s: %v\n", *host, *mode, err)
		return 1
	}
	fmt.Printf("report %s %s %s at %s\n", *host, *version, *mode, short(*sha))
	return 0
}

// writeReport checks the capture and, when it passes, files it and its report
// in dir. A capture that fails gets no report: evidence attests to a pass.
func writeReport(capture, dir string, r report) error {
	switch {
	case !fullSHA.MatchString(r.SHA):
		return fmt.Errorf("%q is not a full commit SHA", r.SHA)
	case r.Host == "":
		return errors.New("no host named")
	case r.Version == "":
		return errors.New("no host version")
	case !modeKnown(r.Mode):
		return fmt.Errorf("unknown drawing mode %q", r.Mode)
	}
	if failures := check(r.Host, capture, r.Mode == "plain"); len(failures) > 0 {
		return fmt.Errorf("the capture does not pass: %v", failures)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r.Capture = r.Host + "-" + r.Mode + ".capture.jsonl"
	r.Digest = digestOf(data)
	r.Result = "PASS"
	if err := os.WriteFile(filepath.Join(dir, r.Capture), data, 0o644); err != nil {
		return err
	}
	js, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, r.Host+"-"+r.Mode+".report.json"), append(js, '\n'), 0o644)
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// evidenceMain is tier 5's CI half: the evidence directories fetched for a
// pull request must bind every host and drawing mode to its head.
func evidenceMain(args []string) int {
	fs := flag.NewFlagSet("evidence", flag.ContinueOnError)
	head := fs.String("head", "", "the pull-request head the evidence must be bound to")
	maxAge := fs.Duration("max-age", defaultMaxAge, "how old a report may be before it has expired")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if !fullSHA.MatchString(*head) {
		fmt.Fprintln(os.Stderr, "usage: hostcheck evidence --head <sha> [--max-age <d>] <dir>...")
		return 64
	}
	failures := verifyEvidence(*head, fs.Args(), time.Now(), *maxAge, os.Stdout)
	for _, f := range failures {
		fmt.Fprintf(os.Stderr, "FAIL %s\n", f)
	}
	if len(failures) > 0 {
		return 1
	}
	return 0
}

// verifyEvidence judges every report in dirs against head and requires a
// passing one for each portability host in each drawing mode. Machines are
// aggregated: evidence from several directories covers the list together, and
// no host is waived because one machine lacks it.
func verifyEvidence(head string, dirs []string, now time.Time, maxAge time.Duration, out io.Writer) []string {
	if len(dirs) == 0 {
		return []string{"no host evidence: a change outside the documentation allowlist needs a head-bound report for every host and drawing mode"}
	}
	var failures []string
	covered := map[string]bool{}
	for _, dir := range dirs {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.report.json"))
		if len(paths) == 0 {
			failures = append(failures, dir+": the evidence holds no report")
			continue
		}
		for _, p := range paths {
			r, err := readReport(p)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", filepath.Base(p), err))
				continue
			}
			named := fmt.Sprintf("%s %s %s", r.Host, r.Version, r.Mode)
			problems := judge(r, dir, head, now, maxAge)
			for _, pr := range problems {
				failures = append(failures, named+": "+pr)
			}
			if len(problems) == 0 {
				covered[r.Host+" "+r.Mode] = true
				fmt.Fprintf(out, "PASS evidence %s at %s on %s\n", named, short(head), r.Machine)
			}
		}
	}
	for _, h := range portability {
		for _, mode := range drawingModes {
			if !covered[h+" "+mode] {
				failures = append(failures, fmt.Sprintf("incomplete: no passing evidence for %s %s at this head", h, mode))
			}
		}
	}
	return failures
}

// judge returns what is wrong with one report, or nothing.
func judge(r report, dir, head string, now time.Time, maxAge time.Duration) []string {
	switch {
	case r.Format != reportFormat:
		return []string{fmt.Sprintf("report format %d, this build reads %d", r.Format, reportFormat)}
	case r.SHA != head:
		return []string{fmt.Sprintf("stale: the report is for %s, the head is %s; run the hosts again at the head", short(r.SHA), short(head))}
	case r.Result != "PASS":
		return []string{"the report does not record a pass"}
	case !modeKnown(r.Mode):
		return []string{fmt.Sprintf("unknown drawing mode %q", r.Mode)}
	case r.Version == "":
		return []string{"the report names no host version"}
	case now.Sub(r.Created) > maxAge:
		return []string{fmt.Sprintf("expired: recorded %s, more than %s ago; upload it again", r.Created.Format(time.RFC3339), maxAge)}
	}
	capture := filepath.Join(dir, filepath.Base(r.Capture))
	data, err := os.ReadFile(capture)
	if err != nil {
		return []string{"the capture it names cannot be read: " + err.Error()}
	}
	if digestOf(data) != r.Digest {
		return []string{"the capture's digest does not match its report"}
	}
	// The capture replays against this build as a committed one does: a report
	// attests to a developer-run session, and the replay is what CI can check.
	var problems []string
	for _, f := range check(r.Host, capture, r.Mode == "plain") {
		problems = append(problems, "replay: "+f)
	}
	return problems
}

func readReport(path string) (report, error) {
	var r report
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("unreadable report: %w", err)
	}
	return r, nil
}
