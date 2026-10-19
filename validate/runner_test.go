package validate_test

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/forkcloser/git-validation/validate"
)

// TestNewRunnerRecordsTheCommitItStartsFrom gives NewRunner no range in a
// one-commit repository made for it: the runner starts from HEAD, and says so
// at debug level.
func TestNewRunnerRecordsTheCommitItStartsFrom(t *testing.T) {
	dir := t.TempDir()
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

	git := func(args ...string) string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir

		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}

		return strings.TrimSpace(string(out))
	}

	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "only commit")
	head := git("rev-parse", "HEAD")

	var records bytes.Buffer

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(&records, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	runner, err := validate.NewRunner(dir, nil, "", false)
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
