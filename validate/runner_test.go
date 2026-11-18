package validate_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	gitpkg "github.com/forkcloser/git-validation/git"
	"github.com/forkcloser/git-validation/internal/testchild"
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

		cmd := testchild.Command(t, "git", args...)
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

	runner, err := validate.NewRunner(t.Context(), dir, nil, "")
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

	runner, err := validate.NewRunner(t.Context(), t.TempDir(), []validate.Rule{rule}, "HEAD")
	if !errors.Is(err, errValueDoesNotCompile) || runner != nil {
		t.Fatalf("NewRunner with a refused value: %v, %v", runner, err)
	}

	if !strings.Contains(err.Error(), "rule checked: value does not compile") {
		t.Fatalf("NewRunner with a refused value: %v; want the rule's name and the check's words", err)
	}

	rule.Value = "ok"

	rules := []validate.Rule{rule, {Name: "unchecked"}}

	if _, err := validate.NewRunner(t.Context(), t.TempDir(), rules, "HEAD"); err != nil {
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
		{Name: "is-first", Run: func(_ context.Context, _ validate.Rule, c gitpkg.CommitEntry) validate.Result {
			return validate.Result{Pass: c["subject"] == "first", Msg: "is-first on " + c["subject"]}
		}},
		{Name: "always", Run: func(_ context.Context, _ validate.Rule, c gitpkg.CommitEntry) validate.Result {
			return validate.Result{Pass: true, Msg: "always on " + c["subject"]}
		}},
	}

	for _, quiet := range []string{"", "1"} {
		t.Setenv("QUIET", quiet)

		runner, err := validate.NewRunner(t.Context(), dir, rules, base+"..HEAD")
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
			if err := runner.Run(t.Context()); err != nil {
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

	runner, err := validate.NewRunner(t.Context(), dir, rules, base+"..HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err := runner.Run(t.Context()); err != nil || len(runner.Results) != 4 {
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

// ctxKey is the key of a value the context test puts in its context.
type ctxKey struct{}

// TestRunPassesItsContextAndStopsWhenItEnds: Run hands its context to every
// rule; under a context that is done it lists no commit, runs no rule, and
// returns the context's error.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestRunPassesItsContextAndStopsWhenItEnds(t *testing.T) {
	dir, git := newRepo(t)

	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "base")
	git("commit", "-q", "--allow-empty", "-m", "first")

	ran := 0
	rules := []validate.Rule{
		{Name: "sees", Run: func(ctx context.Context, _ validate.Rule, _ gitpkg.CommitEntry) validate.Result {
			ran++

			return validate.Result{Pass: ctx.Value(ctxKey{}) == "the caller's"}
		}},
	}

	runner, err := validate.NewRunner(t.Context(), dir, rules, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err = runner.Run(context.WithValue(t.Context(), ctxKey{}, "the caller's")); err != nil {
		t.Fatal(err)
	}

	if _, fail := runner.Results.PassFail(); ran != 2 || fail != 0 {
		t.Errorf("the rule ran %d times and %d did not see the caller's context; want 2 and 0", ran, fail)
	}

	done, cancel := context.WithCancel(t.Context())
	cancel()

	stopped, err := validate.NewRunner(t.Context(), dir, rules, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err = stopped.Run(done); !errors.Is(err, context.Canceled) {
		t.Errorf("Run under a done context: %v; want context.Canceled", err)
	}

	if ran != 2 || len(stopped.Results) != 0 {
		t.Errorf("under a done context the rule ran %d more times and Results holds %d", ran-2, len(stopped.Results))
	}

	if _, err = validate.NewRunner(done, dir, rules, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("NewRunner with no range, under a done context: %v; want context.Canceled", err)
	}
}

// TestRunStopsAmongTheCommits: a context that ends while Run is among the
// commits stops it there. Run returns the context's error, Results holds
// the commits validated whole and nothing of the one a rule was stopped at,
// Report is called for those alone, and no rule runs for a later commit.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestRunStopsAmongTheCommits(t *testing.T) {
	dir, git := newRepo(t)

	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "base")

	for _, subject := range []string{"one", "two", "three", "four", "five"} {
		git("commit", "-q", "--allow-empty", "-m", subject)
	}

	for _, stopAt := range []struct {
		name string
		// The run is stopped by the rule, at its nth call, or by Report,
		// after its nth commit.
		inRule, inReport int
		results          int
	}{
		{"in a rule, at the first commit", 1, 0, 0},
		{"in a rule, at the third commit", 3, 0, 4},
		{"between the second commit and the third", 0, 2, 4},
	} {
		ctx, cancel := context.WithCancel(t.Context())

		calls := 0
		rules := []validate.Rule{
			{Name: "stops", Run: func(_ context.Context, _ validate.Rule, _ gitpkg.CommitEntry) validate.Result {
				calls++
				if calls == stopAt.inRule {
					cancel()
				}

				return validate.Result{Pass: true}
			}},
			{Name: "asks git", Run: func(ctx context.Context, _ validate.Rule, c gitpkg.CommitEntry) validate.Result {
				_, err := gitpkg.Show(ctx, c["commit"])

				return validate.Result{Pass: err == nil}
			}},
		}

		runner, err := validate.NewRunner(t.Context(), dir, rules, "HEAD~5..HEAD")
		if err != nil {
			t.Fatal(err)
		}

		reported := 0
		runner.Report = func(gitpkg.CommitEntry, validate.Results) {
			reported++
			if reported == stopAt.inReport {
				cancel()
			}
		}

		if err = runner.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: Run = %v; want context.Canceled", stopAt.name, err)
		}

		if _, fail := runner.Results.PassFail(); len(runner.Results) != stopAt.results || fail != 0 {
			t.Errorf("%s: Results holds %d results, %d of them failures; want %d and none",
				stopAt.name, len(runner.Results), fail, stopAt.results)
		}

		if want := stopAt.results / len(rules); reported != want {
			t.Errorf("%s: Report was called %d times, want %d", stopAt.name, reported, want)
		}

		if want := max(stopAt.inRule, stopAt.inReport); calls != want {
			t.Errorf("%s: the first rule ran %d times, want %d", stopAt.name, calls, want)
		}

		cancel()
	}
}

// fakeGitMarker names, in the environment of a process that stands in for
// git, the file it writes once it runs.
const fakeGitMarker = "GIT_VALIDATION_FAKE_GIT"

// TestMain makes the test binary stand in for a git that never ends when it
// is run as one.
func TestMain(m *testing.M) {
	if marker := os.Getenv(fakeGitMarker); marker != "" {
		if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
			os.Exit(1)
		}

		time.Sleep(10 * time.Minute)

		return
	}

	m.Run()
}

// TestNewRunnerStopsLookingWhenItsContextEnds: with no range given,
// NewRunner asks git where to validate from; when its context ends while
// git runs, it returns the context's error and does not wait for git.
func TestNewRunnerStopsLookingWhenItsContextEnds(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}

	if err = os.WriteFile(filepath.Join(dir, name), content, 0o700); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(dir, "started")

	t.Setenv("PATH", dir)
	t.Setenv(fakeGitMarker, marker)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		for {
			if _, statErr := os.Stat(marker); statErr == nil {
				cancel()

				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()

	returned := make(chan error, 1)

	go func() {
		_, runnerErr := validate.NewRunner(ctx, t.TempDir(), nil, "")
		returned <- runnerErr
	}()

	select {
	case err = <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("NewRunner under a context cancelled while git runs: %v; want context.Canceled", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("NewRunner is still waiting for git a minute after its context ended")
	}
}
