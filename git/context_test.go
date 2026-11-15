package git_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/forkcloser/git-validation/git"
)

// fakeGitMarker names, in the environment of a process that stands in for
// git, the file it writes once it runs.
const fakeGitMarker = "GIT_VALIDATION_FAKE_GIT"

// TestMain makes the test binary stand in for git when it is run as one: it
// says it has started and then stays, as a git that never ends would, until
// it is killed.
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

// neverEndingGit puts a git that never ends first on PATH, and returns the
// file it writes once it runs.
func neverEndingGit(t *testing.T) string {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}

	source, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = source.Close() }()

	target, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}

	if err := target.Close(); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(dir, "started")

	t.Setenv("PATH", dir)
	t.Setenv(fakeGitMarker, marker)

	return marker
}

// TestContractAContextStopsARunningGit: when its context ends, a git that is
// running is killed, and the call returns the context's error with git's
// own: it does not wait for git to end.
//
//nolint:paralleltest // it sets PATH for the process
func TestContractAContextStopsARunningGit(t *testing.T) {
	marker := neverEndingGit(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		// Cancel once git is running, not before it starts.
		for {
			if _, err := os.Stat(marker); err == nil {
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

	start := time.Now()
	head, err := git.HeadCommit(ctx)

	if !errors.Is(err, context.Canceled) || head != "" {
		t.Fatalf("HeadCommit under a context cancelled while git runs: %q, %v; want context.Canceled", head, err)
	}

	// The git that stood in ends by itself only after ten minutes, with
	// success: an exit that is no success is the kill.
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Success() {
		t.Errorf("the error does not carry git's end by a kill: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Errorf("git was not running when the context ended: %v", statErr)
	}

	if waited := time.Since(start); waited > time.Minute {
		t.Errorf("the call returned after %v", waited)
	}
}

// TestContractADoneContextRunsNoGit: under a context that is already done,
// no git is started, and every call returns the context's error.
//
//nolint:paralleltest // it sets PATH for the process
func TestContractADoneContextRunsNoGit(t *testing.T) {
	marker := neverEndingGit(t)

	done, cancel := context.WithCancel(t.Context())
	cancel()

	calls := map[string]func() error{
		"Commits":         func() error { _, err := git.Commits(done, "HEAD"); return err },
		"Check":           func() error { _, err := git.Check(done, "HEAD"); return err },
		"Show":            func() error { _, err := git.Show(done, "HEAD"); return err },
		"LogUntil":        func() error { _, err := git.LogUntil(done, "HEAD"); return err },
		"LogCommit":       func() error { _, err := git.LogCommit(done, "HEAD"); return err },
		"FetchHeadCommit": func() error { _, err := git.FetchHeadCommit(done); return err },
		"HeadCommit":      func() error { _, err := git.HeadCommit(done); return err },
	}

	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s under a done context: %v; want context.Canceled", name, err)
		}
	}

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a git was started under a done context: %v", err)
	}
}
