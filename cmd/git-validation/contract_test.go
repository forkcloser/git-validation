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
)

// binary is the command built once for every case.
//
//nolint:gochecknoglobals // built in TestMain, read by every test
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "git-validation-contract")
	if err != nil {
		panic(err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	binary = filepath.Join(dir, "git-validation"+exeSuffix())

	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, ".")
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

	cmd := exec.CommandContext(r.t.Context(), "git", args...)
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

	cmd := exec.CommandContext(r.t.Context(), binary, args...)
	cmd.Dir = r.dir

	cmd.Env = append(os.Environ(), env...)

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()

	var exit *exec.ExitError

	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	default:
		r.t.Fatalf("running %s: %v\n%s", binary, err, stderr.String())

		return -1, ""
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

// TestContractVerboseAndQuiet: -v prints every rule's result under its
// commit; -q prints nothing for a passing commit and one line per failing
// rule, the hash and the reason.
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
	cmd := exec.CommandContext(t.Context(), binary, "--dir", r.dir, "--range", base+"..HEAD")
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
