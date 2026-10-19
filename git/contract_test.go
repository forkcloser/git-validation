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

	cmd := exec.CommandContext(r.t.Context(), "git", args...)

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

	e, err := git.LogCommit(first)
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

	e2, err := git.LogCommit(second)
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

	r.git("checkout", "-q", "-b", "side", first)
	side := r.commit("c", []byte("c\n"), "Side subject")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--no-ff", "--no-edit", "side")
	merge := r.git("rev-parse", "HEAD")

	m, err := git.LogCommit(merge)
	if err != nil {
		t.Fatal(err)
	}

	if parents := strings.Split((*m)["parent"], " "); len(parents) != 2 || parents[0] != second || parents[1] != side {
		t.Errorf("merge parents %q, want %s then %s", (*m)["parent"], second, side)
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
		rng  string
		want []string
	}{
		{hashes[1] + ".." + hashes[3], []string{"Subject 3", "Subject 2"}},
		{hashes[3], []string{"Subject 3", "Subject 2", "Subject 1", "Subject 0"}},
		{"HEAD", []string{"Subject 3", "Subject 2", "Subject 1", "Subject 0"}},
		{hashes[3] + ".." + hashes[3], nil},
		{"nonesuch..HEAD", []string{"Subject 3", "Subject 2", "Subject 1"}},
	} {
		got, err := git.Commits(c.rng)
		if err != nil {
			t.Errorf("%s: %v", c.rng, err)

			continue
		}

		if !slices.Equal(subjects(got), c.want) {
			t.Errorf("%s lists %v, want %v", c.rng, subjects(got), c.want)
		}
	}

	if head, err := git.HeadCommit(); err != nil || head != hashes[3] {
		t.Errorf("HeadCommit %q %v, want %s", head, err, hashes[3])
	}

	if _, err := git.FetchHeadCommit(); err == nil {
		t.Error("FetchHeadCommit with no fetch ever made: no error")
	}
}

// TestContractCheckFindsWhitespaceErrors: Check runs `git log --check` on
// the commit against its parent: a change adding trailing whitespace
// fails with git's exit status 2, a clean change passes, the root commit
// is not checked; GIT_CHECK_EXCLUDE, colon-separated, leaves named paths
// out of the check, empty entries ignored.
func TestContractCheckFindsWhitespaceErrors(t *testing.T) {
	r := newRepo(t)

	root := r.commit("a.txt", []byte("clean line with a trailing space \n"), "Root")
	clean := r.commit("b.txt", []byte("clean\n"), "Clean")
	dirty := r.commit("c.txt", []byte("dirty \n"), "Dirty")

	if err := os.MkdirAll(filepath.Join(r.dir, "vendor"), 0o750); err != nil {
		t.Fatal(err)
	}

	vendored := r.commit(filepath.Join("vendor", "d.txt"), []byte("vendored \n"), "Vendored")

	if _, err := git.Check(root); err != nil {
		t.Errorf("the root commit: %v, want it skipped", err)
	}

	if _, err := git.Check(clean); err != nil {
		t.Errorf("a clean commit: %v", err)
	}

	_, err := git.Check(dirty)
	if err == nil {
		t.Fatal("a commit adding trailing whitespace: no error")
	}

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("a commit adding trailing whitespace: %v, want git's exit status 2", err)
	}

	if _, err := git.Check(vendored); err == nil {
		t.Error("a vendored file with trailing whitespace, no exclusion: no error")
	}

	t.Setenv("GIT_CHECK_EXCLUDE", "./vendor::./nonesuch")

	if _, err := git.Check(vendored); err != nil {
		t.Errorf("a vendored file with trailing whitespace, vendor excluded: %v", err)
	}

	if _, err := git.Check(dirty); err == nil {
		t.Error("the dirty commit outside the excluded paths: no error")
	}
}
