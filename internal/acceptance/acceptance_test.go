package acceptance

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// root is the repository, two levels above this package.
var root = filepath.Join("..", "..")

var (
	// A case list is named for its slice.
	listName = regexp.MustCompile(`^(S-\d{3})\.md$`)
	// A case row starts with the case and its level.
	caseRow = regexp.MustCompile(`^\|\s*([A-Z]-\d{2})\s*\|\s*([^|]*?)\s*\|`)
	// A test names the cases it covers on a comment line of its own.
	coverLine = regexp.MustCompile(`^\s*//\s*Covers\s+(S-\d{3})\s+(.*?)\.?\s*$`)
	caseID    = regexp.MustCompile(`^[A-Z]-\d{2}$`)
	sleeps    = regexp.MustCompile(`\btime\.Sleep\(`)
)

// lists reads every case list in dir, keyed by "S-NNN X-NN", with its level.
func lists(dir string) (map[string]string, error) {
	cases := map[string]string{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return cases, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		m := listName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if row := caseRow.FindStringSubmatch(sc.Text()); row != nil {
				cases[m[1]+" "+row[1]] = row[2]
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return cases, nil
}

// testFiles walks dir for Go test files, skipping fixtures and hidden
// directories.
func testFiles(dir string, visit func(path string) error) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			return visit(p)
		}
		return nil
	})
}

// lines calls fn with every line of the file at p and its number.
func lines(p string, fn func(n int, line string)) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		fn(n, sc.Text())
	}
	return sc.Err()
}

// references finds every case a test names, with where it names it, and every
// coverage line that does not parse.
func references(dir string) (map[string][]string, []string, error) {
	refs := map[string][]string{}
	var malformed []string
	err := testFiles(dir, func(p string) error {
		return lines(p, func(n int, line string) {
			m := coverLine.FindStringSubmatch(line)
			if m == nil {
				return
			}
			at := fmt.Sprintf("%s:%d", p, n)
			ids := strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' || r == ' ' })
			if len(ids) == 0 {
				malformed = append(malformed, at+": names no case")
			}
			for _, id := range ids {
				if !caseID.MatchString(id) {
					malformed = append(malformed, fmt.Sprintf("%s: %q is not a case", at, id))
					continue
				}
				refs[m[1]+" "+id] = append(refs[m[1]+" "+id], at)
			}
		})
	})
	return refs, malformed, err
}

// audit returns every case a test names that no list has, and every unit case
// no test names. Host and regression cases are proved by host evidence instead.
func audit(cases map[string]string, refs map[string][]string, malformed []string) []string {
	failures := append([]string(nil), malformed...)
	for key, at := range refs {
		if _, ok := cases[key]; !ok {
			failures = append(failures, fmt.Sprintf("unknown acceptance case %s, named at %s", key, strings.Join(at, ", ")))
		}
	}
	for key, level := range cases {
		if level == "unit" && len(refs[key]) == 0 {
			failures = append(failures, fmt.Sprintf("acceptance case %s is a unit case no test names", key))
		}
	}
	sort.Strings(failures)
	return failures
}

// TestEveryAcceptanceCaseIsNamedByATest is the case-coverage check for the
// repository itself.
func TestEveryAcceptanceCaseIsNamedByATest(t *testing.T) {
	cases, err := lists(filepath.Join(root, "docs", "acceptance"))
	if err != nil {
		t.Fatal(err)
	}
	refs, malformed, err := references(root)
	if err != nil {
		t.Fatal(err)
	}
	if f := audit(cases, refs, malformed); len(f) != 0 {
		t.Fatalf("acceptance cases and tests disagree:\n%s", strings.Join(f, "\n"))
	}
}

func write(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheAuditRejectsMissingAndUnknownCases(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "docs", "S-900.md"), strings.Join([]string{
		"| Case | Level | Exercise |",
		"|---|---|---|",
		"| T-01 | unit | covered |",
		"| T-02 | unit | nobody names it |",
		"| H-01 | host, driven | proved by host evidence |",
	}, "\n"))
	write(t, filepath.Join(dir, "docs", "README.md"), "| T-09 | unit | not a list |\n")
	comment := "/" + "/ Covers "
	write(t, filepath.Join(dir, "src", "a_test.go"), strings.Join([]string{
		"package a",
		comment + "S-900 T-01.",
		comment + "S-900 T-07, T-01.",
		comment + "S-900 soon.",
		"func TestA() {}",
	}, "\n"))
	cases, err := lists(filepath.Join(dir, "docs"))
	if err != nil {
		t.Fatal(err)
	}
	refs, malformed, err := references(filepath.Join(dir, "src"))
	if err != nil {
		t.Fatal(err)
	}
	f := audit(cases, refs, malformed)
	want := []string{
		"acceptance case S-900 T-02 is a unit case no test names",
		`a_test.go:4: "soon" is not a case`,
		"unknown acceptance case S-900 T-07, named at ",
	}
	if len(f) != len(want) {
		t.Fatalf("failures = %q", f)
	}
	for _, w := range want {
		found := false
		for _, got := range f {
			found = found || strings.Contains(got, w)
		}
		if !found {
			t.Fatalf("no failure contains %q in %q", w, f)
		}
	}
}

// TestNoTestUnderInternalSleeps keeps tier 2 deterministic: timed behaviour is
// driven by injected scheduling, and a wall-clock sleep is how order and load
// came to decide a result. A bounded deadline that only detects a hang is not
// a sleep and stays allowed.
func TestNoTestUnderInternalSleeps(t *testing.T) {
	var found []string
	err := testFiles(filepath.Join(root, "internal"), func(p string) error {
		return lines(p, func(n int, line string) {
			if code := strings.TrimSpace(line); !strings.HasPrefix(code, "//") && sleeps.MatchString(code) {
				found = append(found, fmt.Sprintf("%s:%d", p, n))
			}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("tests under internal/ sleep on the wall clock:\n%s", strings.Join(found, "\n"))
	}
}
