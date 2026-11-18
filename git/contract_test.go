// The git package's contract from the outside: what a CommitEntry holds for
// a commit git made, what a range lists, what `git log --check` finds.
package git_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/forkcloser/git-validation/git"
	"github.com/forkcloser/git-validation/internal/testchild"
)

// repo is a fresh repository the test's process has entered, with a
// deterministic identity and no configuration beyond it.
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
	t.Chdir(dir)

	r := repo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")

	return r
}

// git runs git in the repository and returns its standard output trimmed;
// standard error is the failure's message alone, never a parsed value.
func (r repo) git(args ...string) string {
	r.t.Helper()

	cmd := testchild.Command(r.t, "git", args...)

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}

	return strings.TrimSpace(string(out))
}

// commit writes content to file (nil leaves the tree alone and allows an
// empty commit), stages everything and commits with the message, returning
// the hash.
func (r repo) commit(file string, content []byte, message string) string {
	r.t.Helper()

	if content != nil {
		if err := os.WriteFile(filepath.Join(r.dir, file), content, 0o600); err != nil {
			r.t.Fatal(err)
		}
	}

	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", message)

	return r.git("rev-parse", "HEAD")
}

// TestContractLogCommitIsGitsView: a CommitEntry carries the commit's
// hashes, subject, body, author and committer, parents and tree as git
// formats them, the body's CRLF folded to LF, every field trimmed; a root
// commit has an empty parent, a merge commit two separated by a space.
//
//nolint:paralleltest // newRepo enters the repository with t.Chdir and t.Setenv
func TestContractLogCommitIsGitsView(t *testing.T) {
	r := newRepo(t)

	first := r.commit(
		"a",
		[]byte("a\n"),
		"First subject\r\n\r\nFirst body line one\r\nline two  \r\n\r\nSigned-off-by: Test Author <author@example.com>\r\n",
	)
	second := r.commit("b", []byte("b\n"), "Second subject")

	e, err := git.LogCommit(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}

	for field, want := range map[string]string{
		"commit":                 first,
		"abbreviated_commit":     first[:7],
		"subject":                "First subject",
		"body":                   "First body line one\nline two\n\nSigned-off-by: Test Author <author@example.com>",
		"author_name":            "Test Author",
		"author_email":           "author@example.com",
		"committer_name":         "Test Committer",
		"committer_email":        "committer@example.com",
		"parent":                 "",
		"abbreviated_parent":     "",
		"sanitized_subject_line": "First-subject",
		"verification_flag":      "N",
		"encoding":               "",
		"commit_notes":           "",
	} {
		if got := (*e)[field]; got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}

	if (*e)["tree"] != "" || len((*e)["abbreviated_tree"]) < 7 || (*e)["author_date"] == "" ||
		(*e)["committer_date"] == "" {
		t.Errorf("tree, dates: %q %q %q", (*e)["abbreviated_tree"], (*e)["author_date"], (*e)["committer_date"])
	}

	e2, err := git.LogCommit(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}

	if (*e2)["parent"] != first || (*e2)["abbreviated_parent"] != first[:7] || (*e2)["body"] != "" {
		t.Errorf(
			"second: parent %q abbreviated %q body %q",
			(*e2)["parent"],
			(*e2)["abbreviated_parent"],
			(*e2)["body"],
		)
	}

	// A message git did not clean keeps its CRLF, and the entry folds it.
	r.git(
		"commit",
		"-q",
		"--allow-empty",
		"--cleanup=verbatim",
		"-m",
		"Verbatim subject\r\n\r\nline one\r\nline two\r\n",
	)
	verbatim := r.git("rev-parse", "HEAD")
	r.git("reset", "-q", "--hard", second)

	v, err := git.LogCommit(t.Context(), verbatim)
	if err != nil {
		t.Fatal(err)
	}

	if (*v)["body"] != "line one\nline two" {
		t.Errorf("a body written with CRLF: %q, want its lines folded to LF", (*v)["body"])
	}

	r.git("checkout", "-q", "-b", "side", first)
	side := r.commit("c", []byte("c\n"), "Side subject")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--no-ff", "--no-edit", "side")
	merge := r.git("rev-parse", "HEAD")

	m, err := git.LogCommit(t.Context(), merge)
	if err != nil {
		t.Fatal(err)
	}

	if parents := strings.Split((*m)["parent"], " "); len(parents) != 2 || parents[0] != second || parents[1] != side {
		t.Errorf("merge parents %q, want %s then %s", (*m)["parent"], second, side)
	}
}

// TestContractASignedCommitIsReadAsWritten: with log.showSignature set, git
// prints what it found of a commit's signature ahead of the commit. No field
// of the entry holds that line; the signature is in the fields that are its
// own.
//
//nolint:paralleltest // newRepo enters the repository with t.Chdir and t.Setenv
func TestContractASignedCommitIsReadAsWritten(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("no ssh-keygen on PATH to make a signing key with")
	}

	r := newRepo(t)
	keys := t.TempDir()
	key := filepath.Join(keys, "key")

	out, err := testchild.Command(t, keygen, "-q", "-t", "ed25519", "-N", "", "-C", "signer", "-f", key).
		CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}

	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}

	signers := filepath.Join(keys, "signers")
	if err = os.WriteFile(signers, []byte("signer@example.com "+string(public)), 0o600); err != nil {
		t.Fatal(err)
	}

	r.git("config", "gpg.format", "ssh")
	r.git("config", "user.signingkey", key+".pub")
	r.git("config", "gpg.ssh.allowedSignersFile", signers)
	r.git("config", "log.showSignature", "true")

	if err = os.WriteFile(filepath.Join(r.dir, "a"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.git("add", "-A")
	r.git("commit", "-q", "-S", "-m", "A signed subject\n\nA signed body")

	signed := r.git("rev-parse", "HEAD")

	// The premise: git does print the line under this configuration.
	if shown := r.git("log", "-1", "--pretty=format:%s"); !strings.Contains(shown, "signature") {
		t.Fatalf("git log prints no signature line ahead of the commit: %q", shown)
	}

	one, err := git.LogCommit(t.Context(), signed)
	if err != nil {
		t.Fatal(err)
	}

	ranged, err := git.Commits(t.Context(), signed)
	if err != nil || len(ranged) != 1 {
		t.Fatalf("Commits: %v, %d commits, want one", err, len(ranged))
	}

	for _, entry := range []git.CommitEntry{*one, ranged[0]} {
		for field, want := range map[string]string{
			"commit":            signed,
			"subject":           "A signed subject",
			"body":              "A signed body",
			"refs":              "HEAD -> main",
			"verification_flag": "G",
			"signer":            "signer@example.com",
		} {
			if entry[field] != want {
				t.Errorf("%s = %q, want %q", field, entry[field], want)
			}
		}

		for field, value := range entry {
			if strings.Contains(value, "signature") {
				t.Errorf("%s holds git's signature line: %q", field, value)
			}
		}
	}
}

// TestContractCommitsListsARange: Commits lists the commits `git rev-list`
// lists for the range, newest first; a single commit names its whole
// history; an empty range lists nothing and errs nothing; a range git
// cannot resolve falls back to the whole history, from the root commit to
// HEAD, or to the root commit alone when it is the only one.
//
//nolint:paralleltest // newRepo enters the repository with t.Chdir and t.Setenv
func TestContractCommitsListsARange(t *testing.T) {
	r := newRepo(t)

	hashes := make([]string, 0, 4)
	for i := range 4 {
		hashes = append(hashes, r.commit("f"+strconv.Itoa(i), []byte(strconv.Itoa(i)+"\n"), "Subject "+strconv.Itoa(i)))
	}

	subjects := func(entries []git.CommitEntry) []string {
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e["subject"])
		}

		return out
	}

	for _, c := range []struct {
		rng     string
		want    []string
		refused bool
	}{
		{hashes[1] + ".." + hashes[3], []string{"Subject 3", "Subject 2"}, false},
		{hashes[3], []string{"Subject 3", "Subject 2", "Subject 1", "Subject 0"}, false},
		{"HEAD", []string{"Subject 3", "Subject 2", "Subject 1", "Subject 0"}, false},
		{hashes[3] + ".." + hashes[3], nil, false},
		// A range git cannot resolve is an error naming it in a repository
		// with its whole history; it used to validate the whole history.
		{"nonesuch..HEAD", nil, true},
	} {
		got, err := git.Commits(t.Context(), c.rng)
		if c.refused {
			// The range, and git's own words on it.
			if err == nil || !strings.Contains(err.Error(), c.rng) || !strings.Contains(err.Error(), "fatal:") {
				t.Errorf("%s: error %v, want one naming the range with git's message", c.rng, err)
			}

			continue
		}

		if err != nil {
			t.Errorf("%s: %v", c.rng, err)

			continue
		}

		if !slices.Equal(subjects(got), c.want) {
			t.Errorf("%s lists %v, want %v", c.rng, subjects(got), c.want)
		}
	}

	if head, err := git.HeadCommit(t.Context()); err != nil || head != hashes[3] {
		t.Errorf("HeadCommit %q %v, want %s", head, err, hashes[3])
	}

	if _, err := git.FetchHeadCommit(t.Context()); err == nil {
		t.Error("FetchHeadCommit with no fetch ever made: no error")
	}
}

// TestContractCheckFindsWhitespaceErrors: Check runs `git log --check` on
// the commit alone: a change adding trailing whitespace fails with git's
// exit status 2, a clean change passes, a first commit is checked as any
// other; a merge answers for none of the commits it brings in, and git
// shows no change of its own to check; GIT_CHECK_EXCLUDE, colon-separated,
// leaves named paths out of the check, empty entries ignored.
func TestContractCheckFindsWhitespaceErrors(t *testing.T) {
	r := newRepo(t)

	root := r.commit("a.txt", []byte("clean line with a trailing space \n"), "Root")
	clean := r.commit("b.txt", []byte("clean\n"), "Clean")
	dirty := r.commit("c.txt", []byte("dirty \n"), "Dirty")

	if err := os.MkdirAll(filepath.Join(r.dir, "vendor"), 0o750); err != nil {
		t.Fatal(err)
	}

	vendored := r.commit(filepath.Join("vendor", "d.txt"), []byte("vendored \n"), "Vendored")

	var exit *exec.ExitError

	if _, err := git.Check(t.Context(), root); !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("a first commit adding trailing whitespace: %v, want git's exit status 2", err)
	}

	if _, err := git.Check(t.Context(), clean); err != nil {
		t.Errorf("a clean commit: %v", err)
	}

	_, err := git.Check(t.Context(), dirty)
	if err == nil {
		t.Fatal("a commit adding trailing whitespace: no error")
	}

	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("a commit adding trailing whitespace: %v, want git's exit status 2", err)
	}

	if _, err := git.Check(t.Context(), vendored); err == nil {
		t.Error("a vendored file with trailing whitespace, no exclusion: no error")
	}

	t.Setenv("GIT_CHECK_EXCLUDE", "./vendor::./nonesuch")

	if _, err := git.Check(t.Context(), vendored); err != nil {
		t.Errorf("a vendored file with trailing whitespace, vendor excluded: %v", err)
	}

	if _, err := git.Check(t.Context(), dirty); err == nil {
		t.Error("the dirty commit outside the excluded paths: no error")
	}

	t.Setenv("GIT_CHECK_EXCLUDE", "")

	// A branch cut before the dirty commits, which merges main into itself:
	// the merge brings them in, and they are not its own.
	r.git("checkout", "-q", "-b", "feature", clean)
	r.commit("e.txt", []byte("feature\n"), "Feature")
	r.git("merge", "-q", "--no-ff", "--no-edit", "main")

	if _, err := git.Check(t.Context(), r.git("rev-parse", "HEAD")); err != nil {
		t.Errorf("a merge of commits with trailing whitespace: %v, want none: they are not the merge's", err)
	}

	// A merge that adds a line of its own: git shows no change for a merge.
	r.git("checkout", "-q", "-b", "other", clean)
	r.commit("f.txt", []byte("other\n"), "Other")
	r.git("merge", "-q", "--no-ff", "--no-commit", "feature")

	evil := r.commit("g.txt", []byte("added by the merge \n"), "Merge with a change of its own")

	if parents := strings.Fields(r.git("log", "-1", "--pretty=format:%P", evil)); len(parents) != 2 {
		t.Fatalf("the premise: a merge, got parents %v", parents)
	}

	if _, err := git.Check(t.Context(), evil); err != nil {
		t.Errorf("a merge that adds trailing whitespace itself: %v; git shows no change for a merge", err)
	}
}

// TestContractCheckOfAFirstCommitAndOfACut: a first commit is checked
// whatever log.showRoot says; the commit a shallow clone was cut at is
// not a first commit, and is not checked, which Check says, as its own
// change cannot be told from the tree before it; a clone deep enough to
// hold the first commit checks it, at the very depth of the history too.
//
//nolint:paralleltest // newRepo sets the environment with t.Setenv, and the test enters each clone
func TestContractCheckOfAFirstCommitAndOfACut(t *testing.T) {
	r := newRepo(t)

	root := r.commit("a.txt", []byte("a trailing space \n"), "Root")
	r.commit("b.txt", []byte("b\n"), "B")
	r.commit("c.txt", []byte("c\n"), "C")
	head := r.commit("d.txt", []byte("d\n"), "D")

	var exit *exec.ExitError

	r.git("config", "log.showRoot", "false")

	if _, err := git.Check(t.Context(), root); !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("a first commit adding trailing whitespace, log.showRoot off: %v, want git's exit status 2", err)
	}

	for _, depth := range []string{"1", "2"} {
		clone := repo{t: t, dir: filepath.Join(t.TempDir(), "clone")}
		r.git("clone", "-q", "--no-local", "--depth", depth, r.dir, clone.dir)
		t.Chdir(clone.dir)

		if shallow := clone.git("rev-parse", "--is-shallow-repository"); shallow != "true" {
			t.Fatalf("the premise: a shallow clone, got %s", shallow)
		}

		cut := clone.git("rev-list", "--max-parents=0", "HEAD")
		if cut == root {
			t.Fatalf("the premise: a clone of depth %s cut after the first commit", depth)
		}

		if _, err := git.Check(t.Context(), cut); !errors.Is(err, git.ErrParentsNotHere) {
			t.Errorf("depth %s, the commit the clone was cut at: %v, want ErrParentsNotHere", depth, err)
		}

		// At depth 1 the head is the commit the clone was cut at.
		if _, err := git.Check(t.Context(), head); err != nil && head != cut {
			t.Errorf("depth %s, a clean commit with its parent: %v", depth, err)
		}
	}

	// At the depth of the history git still calls the clone shallow, and
	// lists the first commit as where it was cut.
	for _, depth := range []string{"4", "10"} {
		whole := repo{t: t, dir: filepath.Join(t.TempDir(), "clone")}
		r.git("clone", "-q", "--no-local", "--depth", depth, r.dir, whole.dir)
		t.Chdir(whole.dir)

		if _, err := git.Check(t.Context(), root); !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Errorf("depth %s, a clone that holds the first commit: %v, want git's exit status 2", depth, err)
		}
	}
}
