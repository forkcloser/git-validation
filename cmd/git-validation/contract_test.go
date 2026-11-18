// The command's contract from the outside: the binary built once, run
// against repositories made for each case, judged by its exit status and
// what it prints, as the README shows them.
package main_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/forkcloser/git-validation/internal/testchild"
)

// binary is the command built once for every case.
//
//nolint:gochecknoglobals // built in TestMain, read by every test
var binary string

// buildDeadline is what building the command may take.
const buildDeadline = 5 * time.Minute

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "git-validation-contract")
	if err != nil {
		panic(err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	binary = filepath.Join(dir, "git-validation"+exeSuffix())

	ctx, cancel := context.WithTimeout(context.Background(), buildDeadline)
	defer cancel()

	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.WaitDelay = time.Second

	if out, err := build.CombinedOutput(); err != nil {
		panic(string(out))
	}

	m.Run()
}

// repo is a fresh repository with a deterministic identity, every git call
// made in it.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) repo {
	t.Helper()

	dir := t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")

	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Setenv("GIT_AUTHOR_NAME", "Test Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "author@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "committer@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-02T03:04:05+00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-02T03:04:06+00:00")

	for _, v := range []string{"TRAVIS", "TRAVIS_COMMIT_RANGE", "TRAVIS_COMMIT", "TRAVIS_PULL_REQUEST", "GITHUB_ACTIONS", "GITHUB_SHA", "GIT_CHECK_EXCLUDE", "QUIET"} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}

	r := repo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")

	return r
}

// git runs git in the repository and returns its standard output trimmed;
// what it says on standard error is the failure's message alone, never a
// value a test parses (a host's git shim may warn there).
func (r repo) git(args ...string) string {
	r.t.Helper()

	cmd := testchild.Command(r.t, "git", args...)
	cmd.Dir = r.dir

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}

	return strings.TrimSpace(string(out))
}

// commit adds a file with content and commits with the message; signed
// adds the sign-off git writes.
func (r repo) commit(file, content, message string, signed bool) string {
	r.t.Helper()

	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}

	args := []string{"add", "-A"}
	r.git(args...)

	args = []string{"commit", "-q", "-m", message}
	if signed {
		args = append(args, "-s")
	}

	r.git(args...)

	return r.git("rev-parse", "--short", "HEAD")
}

// run runs the binary in the repository with args and env, returning its
// exit status and standard output; what it logs goes to standard error and
// is not the report.
func (r repo) run(env []string, args ...string) (int, string) {
	r.t.Helper()

	code, out, _ := r.runAll(env, args...)

	return code, out
}

// runAll is run, with what the binary logged to standard error.
func (r repo) runAll(env []string, args ...string) (code int, stdout, logged string) {
	r.t.Helper()

	cmd := testchild.Command(r.t, binary, args...)
	cmd.Dir = r.dir

	cmd.Env = append(os.Environ(), env...)

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()

	var exit *exec.ExitError

	switch {
	case err == nil:
		return 0, string(out), stderr.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out), stderr.String()
	default:
		r.t.Fatalf("running %s: %v\n%s", binary, err, stderr.String())

		return -1, "", ""
	}
}

// TestContractListRules: --list-rules prints every rule, quoted name and
// description, in the registry's order, and exits 0.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractListRules(t *testing.T) {
	r := newRepo(t)

	code, out := r.run(nil, "--list-rules")

	want := `"dangling-whitespace" -- checking the presence of dangling whitespaces on line endings
"DCO" -- makes sure the commits are signed
"message_regexp" -- checks the commit message for a user provided regular expression
"short-subject" -- commit subjects are strictly less than 90 (github ellipsis length)
`
	if code != 0 || out != want {
		t.Errorf("exit %d, output:\n%s\nwant:\n%s", code, out, want)
	}
}

// TestContractDefaultRunOverAHistory: with no range the whole history is
// validated, newest first, one line per commit with its abbreviated hash
// and quoted subject and PASS or FAIL; a failing rule adds its reason on
// an indented line; the run exits 1 when any commit fails, with the count
// at the end, and 0 when none does.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractDefaultRunOverAHistory(t *testing.T) {
	r := newRepo(t)

	first := r.commit("a", "a\n", "First change", true)
	second := r.commit("b", "b\n", "Second change", true)

	code, out := r.run(nil)
	want := " * " + second + " \"Second change\" ... PASS\n * " + first + " \"First change\" ... PASS\n"

	if code != 0 || out != want {
		t.Errorf("a clean history: exit %d, output:\n%s\nwant:\n%s", code, out, want)
	}

	third := r.commit("c", "c\n", "Unsigned change", false)

	code, out = r.run(nil)
	if code != 1 {
		t.Errorf("an unsigned commit: exit %d, want 1", code)
	}

	for _, line := range []string{
		" * " + third + " \"Unsigned change\" ... FAIL\n",
		"  - FAIL - does not have a valid Developer Certificate of Origin",
		" * " + second + " \"Second change\" ... PASS\n",
		"commits to fix\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("output lacks %q:\n%s", line, out)
		}
	}

	if strings.Contains(out, "- PASS -") {
		t.Errorf("rule passes printed without -v:\n%s", out)
	}
}

// TestContractWhitespaceErrorsAreNamed: a commit that adds trailing
// whitespace fails the dangling-whitespace rule with the command that shows
// them, and a commit that does not passes it.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractWhitespaceErrorsAreNamed(t *testing.T) {
	r := newRepo(t)

	clean := r.commit("a", "a\n", "Clean change", true)
	dirty := r.commit("b", "b \n", "Dirty change", true)

	code, out := r.run(nil, "--run", "dangling-whitespace", "-v")
	if code != 1 {
		t.Errorf("a commit with trailing whitespace: exit %d, want 1", code)
	}

	for _, line := range []string{
		" * " + dirty + " \"Dirty change\" ... FAIL\n",
		// The rule names the commit by its whole hash, which the short one starts.
		"  - FAIL - has whitespace errors. See `git show --check " + dirty,
		" * " + clean + " \"Clean change\" ... PASS\n",
		"  - PASS - commit does not have any whitespace errors\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("output lacks %q:\n%s", line, out)
		}
	}
}

// TestContractWhitespaceIsEachCommitsOwn: a merge is not failed for the
// whitespace errors of the commits it brings in, which are not in the
// range; a first commit is checked as any other.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractWhitespaceIsEachCommitsOwn(t *testing.T) {
	r := newRepo(t)

	root := r.commit("a", "a \n", "Root with a trailing blank", true)

	r.git("checkout", "-q", "-b", "feature")

	work := r.commit("f", "f\n", "Feature work", true)

	r.git("checkout", "-q", "main")
	r.commit("c", "c \n", "On main, with a trailing blank", true)
	r.git("checkout", "-q", "feature")
	r.git("merge", "-q", "--no-ff", "-m", "Merge main into feature", "main")

	merge := r.git("rev-parse", "--short", "HEAD")

	// What the branch adds to main: its own commit, and the merge.
	code, out := r.run(nil, "--run", "dangling-whitespace", "-v", "--range", "main..feature")
	if code != 0 {
		t.Errorf("a branch that merged main, whose own commit is clean: exit %d, want 0:\n%s", code, out)
	}

	for _, line := range []string{
		" * " + merge + " \"Merge main into feature\" ... PASS\n",
		" * " + work + " \"Feature work\" ... PASS\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("output lacks %q:\n%s", line, out)
		}
	}

	// The whole of main reaches its first commit.
	code, out = r.run(nil, "--run", "dangling-whitespace", "-v", "--range", "main")
	if line := " * " + root + " \"Root with a trailing blank\" ... FAIL\n"; code != 1 || !strings.Contains(out, line) {
		t.Errorf("a first commit with a trailing blank: exit %d, want 1 and %q:\n%s", code, line, out)
	}
}

// TestContractWhitespaceNotCheckedInAShallowClone: the commit a shallow
// clone was cut at cannot have its whitespace checked, and is not said to
// pass it: the rule's line is SKIP under -v, a warning on standard error
// names the commit and what to do, at every verbosity, and the exit
// status is that of the rules that ran; a clone one commit deeper checks
// its head, and a range it holds with its parents skips nothing.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractWhitespaceNotCheckedInAShallowClone(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a \n", "Root with a trailing blank", true)
	middle := r.commit("b", "b\n", "A clean change", true)
	last := r.commit("d", "d\n", "Another clean change", true)
	full := r.git("rev-parse", "HEAD")

	const (
		skipped = "  - SKIP - whitespace not checked: the commit's parents are not in this shallow clone\n"
		warning = "whitespace not checked: the commit's parents are not in this shallow clone; " +
			"fetch the history (actions/checkout fetch-depth: 0, git fetch --unshallow) or give a range it holds"
	)

	// A clone of depth 1, as a CI checks out.
	cut := repo{t: t, dir: filepath.Join(t.TempDir(), "clone")}
	r.git("clone", "-q", "--no-local", "--depth", "1", r.dir, cut.dir)

	commit := " * " + last + " \"Another clean change\" ... PASS\n"

	code, out, logged := cut.runAll(nil, "--run", "dangling-whitespace", "-v")
	if code != 0 || out != commit+skipped {
		t.Errorf("depth 1, -v: exit %d and %q, want 0 and %q", code, out, commit+skipped)
	}

	if !strings.Contains(logged, warning) || !strings.Contains(logged, full) {
		t.Errorf("depth 1, -v: logged %q, want the warning and the commit %s", logged, full)
	}

	code, out, logged = cut.runAll(nil, "--run", "dangling-whitespace")
	if code != 0 || out != commit || !strings.Contains(logged, warning) {
		t.Errorf("depth 1: exit %d, %q, logged %q; want 0, the commit's line alone and the warning", code, out, logged)
	}

	code, out, logged = cut.runAll(nil, "--run", "dangling-whitespace", "-q")
	if code != 0 || out != "" || !strings.Contains(logged, warning) || !strings.Contains(logged, full) {
		t.Errorf("depth 1, -q: exit %d, %q, logged %q; want 0, nothing and the warning", code, out, logged)
	}

	// With every rule, the commit's verdict is that of the rules that ran.
	code, out, _ = cut.runAll(nil, "-v")
	if code != 0 || !strings.Contains(out, commit) || !strings.Contains(out, skipped) ||
		strings.Count(out, "  - PASS - ") == 0 {
		t.Errorf(
			"depth 1, every rule: exit %d and %q, want 0, the commit passing, one SKIP and the others' PASS",
			code,
			out,
		)
	}

	// One commit deeper the head has its parent, and is checked. A run with
	// no range still reaches the commit the clone was cut at, one further
	// back; a range the clone holds with its parents does not.
	deeper := repo{t: t, dir: filepath.Join(t.TempDir(), "clone")}
	r.git("clone", "-q", "--no-local", "--depth", "2", r.dir, deeper.dir)

	checked := commit + "  - PASS - commit does not have any whitespace errors\n"
	before := " * " + middle + " \"A clean change\" ... PASS\n"

	code, out, logged = deeper.runAll(nil, "--run", "dangling-whitespace", "-v")
	if code != 0 || out != checked+before+skipped || strings.Count(logged, warning) != 1 {
		t.Errorf(
			"depth 2: exit %d, %q, logged %q; want 0, %q and one warning",
			code,
			out,
			logged,
			checked+before+skipped,
		)
	}

	code, out, logged = deeper.runAll(nil, "--run", "dangling-whitespace", "-v", "--range", "HEAD~1..HEAD")
	if code != 0 || out != checked || logged != "" {
		t.Errorf(
			"depth 2, a range it holds: exit %d, %q, logged %q; want 0, %q and nothing logged",
			code,
			out,
			logged,
			checked,
		)
	}
}

// TestContractVerboseAndQuiet: -v prints every rule's result under its
// commit; -q prints nothing for a passing commit and one line per failing
// rule, the hash and the reason, with -v or without; QUIET in the
// environment is not -q.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractVerboseAndQuiet(t *testing.T) {
	r := newRepo(t)

	good := r.commit("a", "a\n", "Good change", true)
	bad := r.commit("b", "b\n", "Bad change", false)

	_, out := r.run(nil, "-v")

	for _, line := range []string{
		" * " + good + " \"Good change\" ... PASS\n",
		"  - PASS - has a valid DCO\n",
		"  - PASS - commit subject is 72 characters or less! *yay*\n",
		"  - PASS - commit does not have any whitespace errors\n",
		" * " + bad + " \"Bad change\" ... FAIL\n",
		"  - FAIL - does not have a valid Developer Certificate of Origin",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("-v output lacks %q:\n%s", line, out)
		}
	}

	code, out := r.run(nil, "-q")
	if code != 1 ||
		!strings.HasPrefix(out, " "+bad+" - FAIL - does not have a valid Developer Certificate of Origin") ||
		strings.Contains(out, good) {
		t.Errorf("-q: exit %d, output:\n%s", code, out)
	}

	// -q wins over -v: the two together give the quiet report.
	_, quiet := r.run(nil, "-q")
	if _, both := r.run(nil, "-q", "-v"); both != quiet {
		t.Errorf("-q -v:\n%s\nwant the -q report:\n%s", both, quiet)
	}

	// Quiet is the flag's alone: QUIET in the environment changes nothing.
	_, plain := r.run(nil)
	if _, withEnv := r.run([]string{"QUIET=1"}); withEnv != plain || !strings.Contains(plain, good) {
		t.Errorf("QUIET=1 without -q:\n%s\nwant the plain report:\n%s", withEnv, plain)
	}

	// Every failing rule of a commit is printed under -q, not the first.
	twice := r.commit("c", "c\n", strings.Repeat("x", 95), false)

	_, out = r.run(nil, "-q")
	if strings.Count(out, " "+twice+" - FAIL - ") != 2 || !strings.Contains(out, "exceeds 90") {
		t.Errorf("-q with two failing rules on one commit:\n%s", out)
	}
}

// TestContractRunSelectsRules: --run limits the run to the named rules,
// so an unsigned commit passes a run of short-subject alone; message_regexp
// with a value checks every commit against it.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractRunSelectsRules(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "JIRA-1 a change", false)
	r.commit("b", "b\n", "a change without a ticket", false)

	if code, out := r.run(nil, "--run", "short-subject"); code != 0 || strings.Contains(out, "FAIL") {
		t.Errorf("--run short-subject over unsigned commits: exit %d\n%s", code, out)
	}

	code, out := r.run(nil, "--run", "message_regexp=^JIRA-[0-9]+", "-v")
	if code != 1 || !strings.Contains(out, "\"JIRA-1 a change\" ... PASS") ||
		!strings.Contains(out, "\"a change without a ticket\" ... FAIL") {
		t.Errorf("--run message_regexp: exit %d\n%s", code, out)
	}
}

// TestContractRangeAndDir: --range validates only the commits in the
// range; --dir validates a repository elsewhere, the caller's directory
// untouched.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractRangeAndDir(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "Unsigned root", false)
	base := r.git("rev-parse", "HEAD")
	signed := r.commit("b", "b\n", "Signed change", true)

	code, out := r.run(nil, "--range", base+"..HEAD")
	if code != 0 || out != " * "+signed+" \"Signed change\" ... PASS\n" {
		t.Errorf("--range: exit %d\n%s", code, out)
	}

	elsewhere := t.TempDir()
	cmd := testchild.Command(t, binary, "--dir", r.dir, "--range", base+"..HEAD")
	cmd.Dir = elsewhere
	cmd.Env = os.Environ()

	if out, err := cmd.Output(); err != nil || !strings.Contains(string(out), "PASS") {
		t.Errorf("--dir from elsewhere: %v\n%s", err, out)
	}
}

// TestContractCIEnvironments: under GitHub Actions the range is
// GITHUB_SHA..HEAD unless --no-github; under Travis, TRAVIS_COMMIT_RANGE
// with its three dots made two, and a non-pull-request build yields with
// exit 0 unless --no-travis-pr-only.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractCIEnvironments(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "Unsigned root", false)
	base := r.git("rev-parse", "HEAD")
	r.commit("b", "b\n", "Signed change", true)
	r.commit("c", "c\n", "Another signed change", true)

	github := []string{"GITHUB_ACTIONS=true", "GITHUB_SHA=" + base}

	if code, out := r.run(github); code != 0 || strings.Count(out, "PASS") != 2 {
		t.Errorf("GitHub Actions range: exit %d\n%s", code, out)
	}

	if code, _ := r.run(github, "--no-github"); code != 1 {
		t.Errorf("--no-github: exit %d, want 1 (the unsigned root validated)", code)
	}

	travis := []string{"TRAVIS=true", "TRAVIS_COMMIT_RANGE=" + base + "...HEAD", "TRAVIS_PULL_REQUEST=7"}

	if code, out := r.run(travis); code != 0 || strings.Count(out, "PASS") != 2 {
		t.Errorf("Travis range: exit %d\n%s", code, out)
	}

	notPR := []string{"TRAVIS=true", "TRAVIS_PULL_REQUEST=false"}

	if code, out := r.run(notPR); code != 0 || !strings.Contains(out, "not a PR build") {
		t.Errorf("Travis non-PR build: exit %d\n%s", code, out)
	}

	if code, _ := r.run(notPR, "--no-travis-pr-only", "--no-travis"); code != 1 {
		t.Errorf("--no-travis-pr-only --no-travis: exit %d, want 1 (the whole history validated)", code)
	}
}

// TestContractUsageErrorExitsOne: an unknown flag is a usage error, exit 1.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractUsageErrorExitsOne(t *testing.T) {
	r := newRepo(t)

	if code, _ := r.run(nil, "--nonesuch"); code != 1 {
		t.Errorf("an unknown flag: exit %d, want 1", code)
	}
}

// TestContractBadRegexpIsAUsageError: a message_regexp value that does not
// compile is refused before any commit is validated: exit 1, no report
// line on stdout.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractBadRegexpIsAUsageError(t *testing.T) {
	r := newRepo(t)
	r.commit("a", "a\n", "A change", true)

	code, out := r.run(nil, "--run", "message_regexp=(")
	if code != 1 || strings.Contains(out, " * ") {
		t.Errorf("exit %d, stdout:\n%s", code, out)
	}
}

// TestContractUnknownRuleIsRefused: a --run name no rule has is refused,
// exit 1; no commit is reported PASS by a run of no rules.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractUnknownRuleIsRefused(t *testing.T) {
	r := newRepo(t)
	r.commit("a", "a\n", "A change", false)

	code, out := r.run(nil, "--run", "typo")
	if code != 1 || strings.Contains(out, "PASS") {
		t.Errorf("exit %d, stdout:\n%s", code, out)
	}
}

// TestContractCountIsOfCommits: the final line counts commits with a
// failing rule, one commit failing two rules counted once.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractCountIsOfCommits(t *testing.T) {
	r := newRepo(t)
	r.commit("a", "a\n", strings.Repeat("x", 95), false)

	_, out := r.run(nil)
	if !strings.HasSuffix(out, "1 commits to fix\n") {
		t.Errorf("stdout ends:\n%s", out)
	}
}

// exeSuffix is what the OS needs appended to run a binary built to a bare name.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}

	return ""
}
