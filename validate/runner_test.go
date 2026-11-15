package validate_test

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gitpkg "github.com/forkcloser/git-validation/git"
	"github.com/forkcloser/git-validation/validate"
)

// newRepo is an empty directory and a git that runs in it, under an identity
// and a configuration of the test's own. It sets the environment, so its
// caller cannot run in parallel.
func newRepo(t *testing.T) (dir string, git func(args ...string) string) {
	t.Helper()

	dir = t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")

	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	git = func(args ...string) string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir

		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}

		return strings.TrimSpace(string(out))
	}

	return dir, git
}

// TestNewRunnerRecordsTheCommitItStartsFrom gives NewRunner no range in a
// one-commit repository made for it: the runner starts from HEAD, and says so
// at debug level.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestNewRunnerRecordsTheCommitItStartsFrom(t *testing.T) {
	dir, git := newRepo(t)

	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "only commit")
	head := git("rev-parse", "HEAD")

	var records bytes.Buffer

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(&records, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	runner, err := validate.NewRunner(dir, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if runner.CommitRange != head {
		t.Errorf("CommitRange = %q, want HEAD, %s", runner.CommitRange, head)
	}

	want := `msg="no commit range given, validating from" commit=` + head
	if !strings.Contains(records.String(), want) {
		t.Errorf("no record %s in:\n%s", want, records.String())
	}
}

// errValueDoesNotCompile stands in for a rule's refusal of its value.
var errValueDoesNotCompile = errors.New("value does not compile")

// TestNewRunnerRefusesAValueItsRuleCannotRun: a rule's Check judges its
// Value before any commit is read, so the run is refused with the rule's
// name and the check's words, outside any repository.
func TestNewRunnerRefusesAValueItsRuleCannotRun(t *testing.T) {
	t.Parallel()

	rule := validate.Rule{
		Name:  "checked",
		Value: "(",
		Check: func(value string) error {
			if value == "(" {
				return errValueDoesNotCompile
			}

			return nil
		},
	}

	runner, err := validate.NewRunner(t.TempDir(), []validate.Rule{rule}, "HEAD")
	if !errors.Is(err, errValueDoesNotCompile) || runner != nil {
		t.Fatalf("NewRunner with a refused value: %v, %v", runner, err)
	}

	if !strings.Contains(err.Error(), "rule checked: value does not compile") {
		t.Fatalf("NewRunner with a refused value: %v; want the rule's name and the check's words", err)
	}

	rule.Value = "ok"

	rules := []validate.Rule{rule, {Name: "unchecked"}}

	if _, err := validate.NewRunner(t.TempDir(), rules, "HEAD"); err != nil {
		t.Fatalf("NewRunner with an accepted value and an unchecked rule: %v", err)
	}
}

// TestRunIsSilentAndReportsEachCommit: Run writes nothing to standard output,
// with QUIET set or not, since the report is the caller's to print; every
// result is in Results; and Report, when set, receives each commit once with
// the results of that commit alone, in the order Results holds them and once
// they are in it.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestRunIsSilentAndReportsEachCommit(t *testing.T) {
	dir, git := newRepo(t)

	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "base")
	base := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "first")
	git("commit", "-q", "--allow-empty", "-m", "second")

	rules := []validate.Rule{
		{Name: "is-first", Run: func(_ validate.Rule, c gitpkg.CommitEntry) validate.Result {
			return validate.Result{Pass: c["subject"] == "first", Msg: "is-first on " + c["subject"]}
		}},
		{Name: "always", Run: func(_ validate.Rule, c gitpkg.CommitEntry) validate.Result {
			return validate.Result{Pass: true, Msg: "always on " + c["subject"]}
		}},
	}

	for _, quiet := range []string{"", "1"} {
		t.Setenv("QUIET", quiet)

		runner, err := validate.NewRunner(dir, rules, base+"..HEAD")
		if err != nil {
			t.Fatal(err)
		}

		var reported []string

		runner.Report = func(commit gitpkg.CommitEntry, results validate.Results) {
			if have, want := len(runner.Results), len(reported)+len(results); have != want {
				t.Errorf(
					"Report for %q: Results holds %d, want %d, this commit's included",
					commit["subject"],
					have,
					want,
				)
			}

			for _, res := range results {
				if res.CommitEntry["commit"] != commit["commit"] {
					t.Errorf("Report for %q carries a result of %q", commit["subject"], res.CommitEntry["subject"])
				}

				reported = append(reported, res.Msg)
			}
		}

		printed := stdoutOf(t, func() {
			if err := runner.Run(); err != nil {
				t.Fatal(err)
			}
		})
		if printed != "" {
			t.Errorf("QUIET=%q: Run printed %q", quiet, printed)
		}

		var recorded []string
		for _, res := range runner.Results {
			recorded = append(recorded, res.Msg)
		}

		if len(recorded) != 4 || !slices.Equal(recorded, reported) {
			t.Errorf("QUIET=%q: Results %q, reported %q; want the same four", quiet, recorded, reported)
		}

		if fail := runner.Results.CommitsToFix(); fail != 1 {
			t.Errorf("QUIET=%q: CommitsToFix = %d, want 1", quiet, fail)
		}
	}

	runner, err := validate.NewRunner(dir, rules, base+"..HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err := runner.Run(); err != nil || len(runner.Results) != 4 {
		t.Errorf("Run with no Report: %v, %d results; want 4", err, len(runner.Results))
	}
}

// stdoutOf is what run writes to the process's standard output.
func stdoutOf(t *testing.T, run func()) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}

	// The library's silence is a claim about the process's standard output,
	// which only swapping it shows from inside the process.
	previous := os.Stdout
	os.Stdout = file //nolint:reassign // see above

	defer func() { os.Stdout = previous }() //nolint:reassign // restores it

	run()

	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	out, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}
