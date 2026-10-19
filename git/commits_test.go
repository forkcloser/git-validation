package git

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCommitEntry reads the commits of a two-commit repository made for it,
// not this repository's history: walking that ran one git process per field
// per commit, slower with every commit (651 s on a loaded machine, at 141).
func TestCommitEntry(t *testing.T) {
	newRepository(t, "first commit", "second commit")

	head, err := HeadCommit()
	if err != nil {
		t.Fatal(err)
	}

	commits, err := Commits(head)
	if err != nil {
		t.Fatal(err)
	}

	if len(commits) != 2 {
		t.Fatalf("Commits(HEAD) = %d commits, want 2", len(commits))
	}

	for _, commit := range commits {
		for _, field := range FieldNames {
			if _, ok := commit[field]; !ok {
				t.Errorf("commit %q has no %q field", commit["subject"], field)
			}
		}
	}

	for i, want := range []string{"second commit", "first commit"} {
		if got := commits[i]["subject"]; got != want {
			t.Errorf("commit %d's subject = %q, want %q", i, got, want)
		}

		if got := commits[i]["author_email"]; got != "test@example.com" {
			t.Errorf("commit %d's author_email = %q, want test@example.com", i, got)
		}
	}
}

// TestCommandsAreRecordedAtDebug checks the initial commit with a debug
// handler installed: each git command run and the skipped check are recorded.
func TestCommandsAreRecordedAtDebug(t *testing.T) {
	newRepository(t, "only commit")

	var records bytes.Buffer

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(&records, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	head, err := HeadCommit()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Check(head); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`msg="running git" cmd="git --no-pager rev-parse --verify HEAD"`,
		`msg="running git" cmd="git --no-pager log --oneline --no-show-signature ` + head + `"`,
		`msg="skipping the whitespace check of the initial commit, which has no parent" commit=` + head,
	} {
		if !strings.Contains(records.String(), want) {
			t.Errorf("no record %s in:\n%s", want, records.String())
		}
	}
}

// newRepository makes a repository in a temporary directory with one signed-off
// commit per subject, oldest first, and makes it the working directory. The
// user's and the system's git configuration stay out of it (no signing, no
// signature display), and so does any repository above it.
func newRepository(t *testing.T, subjects ...string) {
	t.Helper()

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
	t.Chdir(dir)

	git := func(args ...string) {
		t.Helper()

		if out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	git("init", "-q")

	for i, subject := range subjects {
		name := filepath.Join(dir, "file"+strconv.Itoa(i))
		if err := os.WriteFile(name, []byte(subject+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		git("add", ".")
		git("commit", "-q", "-s", "-m", subject)
	}
}

func TestMarshal(t *testing.T) {
	buf, err := os.ReadFile("testdata/commits.json")
	if err != nil {
		t.Fatal(err)
	}

	cr := []CommitEntry{}
	if err := json.Unmarshal(buf, &cr); err != nil {
		t.Error(err)
	}

	for _, c := range cr {
		for _, cV := range FieldNames {
			found := false

			for k := range c {
				if k == cV {
					found = true
				}
			}

			if !found {
				t.Errorf("failed to find field names: %q", c)
			}
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have, want string
		ok         bool
	}{
		{"2.54.0", "1.9.5", true},
		{"1.9.5", "1.9.5", true},
		{"1.9.4", "1.9.5", false},
		{"1.9", "1.9.5", false},
		{"1.10.0", "1.9.5", true},
		{"2.42.0.windows.2", "1.9.5", true},
	} {
		got, err := versionAtLeast(tc.have, tc.want)
		if err != nil || got != tc.ok {
			t.Errorf("versionAtLeast(%q, %q) = %v, %v, want %v", tc.have, tc.want, got, err, tc.ok)
		}
	}

	if _, err := versionAtLeast("windows", "1.9.5"); !errors.Is(err, errNoVersionNumber) {
		t.Errorf("versionAtLeast(%q, _) error = %v, want errNoVersionNumber", "windows", err)
	}
}

// TestCommitsOutsideARepository pins the failure path: the git command that
// failed is named in the error, which still unwraps to git's exit.
func TestCommitsOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// No repository above it either.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	_, err := Commits("HEAD~1..HEAD")
	if err == nil {
		t.Fatal("Commits outside a repository succeeded")
	}

	if !strings.Contains(err.Error(), "git log --pretty=oneline --no-show-signature") {
		t.Errorf("Commits error = %q, want it to name the git command", err)
	}

	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Errorf("Commits error = %v, want it to unwrap to *exec.ExitError", err)
	}
}
