package main_test

// Two decisions on the command's contract, each a case that fails on the
// behaviour before them: the short-subject rule counts characters, not
// bytes, so a subject of 89 non-ASCII characters passes and one of 90
// fails; and a --range git cannot resolve is an error naming the range,
// with nothing validated, where a shallow clone whose history does not
// reach the range's start still runs over what it has.

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

// A shallow clone's history may not reach the range's start a CI
// configuration names; the run then covers what the clone has, as before.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv
func TestContractShallowCloneRunsOverWhatItHas(t *testing.T) {
	r := newRepo(t)

	r.commit("a", "a\n", "Root", true)
	base := r.git("rev-parse", "HEAD")
	r.commit("b", "b\n", "Second", true)
	r.commit("c", "c\n", "Third", true)

	shallow := filepath.Join(t.TempDir(), "shallow")

	clone := exec.CommandContext(t.Context(), "git", "clone", "-q", "--depth", "1", "file://"+r.dir, shallow)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("shallow clone: %v\n%s", err, out)
	}

	cmd := exec.CommandContext(t.Context(), binary, "--dir", shallow, "--range", base+"..HEAD")
	cmd.Dir = shallow
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "PASS") {
		t.Errorf("shallow clone: %v\n%s", err, out)
	}
}
