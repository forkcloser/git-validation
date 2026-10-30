package main_test

// Two decisions on the command's contract, each a case that fails on the
// behaviour before them: the short-subject rule counts characters, not
// bytes, so a subject of 89 non-ASCII characters passes and one of 90
// fails; and a --range git cannot resolve is an error naming the range,
// with nothing validated, in a shallow clone whose history does not reach
// the range's start as well, where the error says what to fetch.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractShortSubjectCountsRunes(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "Root", true)

	under := strings.Repeat("é", 89)
	r.commit("b", "b\n", under, true)

	if code, out := r.run(
		nil,
		"--run",
		"short-subject",
	); code != 0 || !strings.Contains(out, "PASS") ||
		strings.Contains(out, "FAIL") {
		t.Errorf("89 characters of two bytes each: exit %d, want the rule to pass\n%s", code, out)
	}

	over := strings.Repeat("é", 90)
	r.commit("c", "c\n", over, true)

	if code, out := r.run(nil, "--run", "short-subject"); code == 0 || !strings.Contains(out, "FAIL") {
		t.Errorf("90 characters: exit %d, want the rule to fail\n%s", code, out)
	}
}

//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractUnresolvableRangeIsAnError(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "Root", true)
	r.commit("b", "b\n", "Change", true)

	cmd := exec.CommandContext(t.Context(), binary, "--range", "nosuchref..HEAD")
	cmd.Dir = r.dir
	cmd.Env = os.Environ()

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("an unresolvable range validated the history:\n%s", out)
	}

	if strings.Contains(string(out), " * ") {
		t.Errorf("commits were validated under an unresolvable range:\n%s", out)
	}

	if !strings.Contains(stderr.String(), "nosuchref") {
		t.Errorf("the error does not name the range:\n%s", stderr.String())
	}
}

// A shallow clone whose history does not reach the range's start a CI
// configuration names is refused like any unresolvable range, nothing
// validated, and the error says what to fetch; it used to run over what the
// clone had, so a depth of one validated the head commit alone and passed.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractShallowCloneRefusesARangeOutOfReach(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "Root", true)
	base := r.git("rev-parse", "HEAD")
	r.commit("b", "b\n", "Second", true)
	r.commit("c", "c\n", "Third", true)

	shallow := shallowClone(t, r.dir, "1")

	cmd := exec.CommandContext(t.Context(), binary, "--dir", shallow, "--range", base+"..HEAD")
	cmd.Dir = shallow
	cmd.Env = os.Environ()

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("a range past the clone's depth validated what the clone has:\n%s", out)
	}

	if strings.Contains(string(out), " * ") {
		t.Errorf("commits were validated under a range past the clone's depth:\n%s", out)
	}

	for _, want := range []string{base + " is not in it", "shallow clone", "fetch-depth: 0", "--unshallow"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the error does not say %q:\n%s", want, stderr.String())
		}
	}
}

// A shallow clone deep enough for the range runs as a whole clone does.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractShallowCloneWithTheRangeInReachRuns(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "Root", true)
	base := r.git("rev-parse", "HEAD")
	r.commit("b", "b\n", "Second", true)
	r.commit("c", "c\n", "Third", true)

	shallow := shallowClone(t, r.dir, "3")

	cmd := exec.CommandContext(t.Context(), binary, "--dir", shallow, "--range", base+"..HEAD")
	cmd.Dir = shallow
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil || strings.Count(string(out), "PASS") != 2 {
		t.Errorf("shallow clone deep enough for the range: %v\n%s", err, out)
	}
}

// shallowClone clones dir with the given depth and returns the clone's path.
func shallowClone(t *testing.T, dir, depth string) string {
	t.Helper()

	shallow := filepath.Join(t.TempDir(), "shallow")

	clone := exec.CommandContext(t.Context(), "git", "clone", "-q", "--depth", depth, "file://"+dir, shallow)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("shallow clone: %v\n%s", err, out)
	}

	return shallow
}
