package git

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Commits returns a set of commits.
// If commitrange is a git still range 12345...54321, then it will be isolated set of commits.
// If commitrange is a single commit, all ancestor commits up through the hash provided.
// If commitrange is an empty commit range, then nil is returned.
func Commits(commitrange string) ([]CommitEntry, error) {
	// TEST if it has _enough_ of a range from rev-list
	commitrange, err := checkRevList(commitrange)
	if err != nil {
		return nil, fmt.Errorf("validating the commit range: %w", err)
	}

	cmdArgs := []string{"git", "rev-list", commitrange}

	output, err := command(cmdArgs[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}

	if len(output) == 0 {
		return nil, nil
	}

	commitHashes := strings.Split(strings.TrimSpace(string(output)), "\n")

	commits := make([]CommitEntry, len(commitHashes))
	for i, commitHash := range commitHashes {
		c, err := LogCommit(commitHash)
		if err != nil {
			return commits, err
		}

		commits[i] = *c
	}

	return commits, nil
}

// Since the commitrange requested may be longer than the depth being cloned in CI,
// check for an error, if so do a git log to get the oldest available commit for a reduced range.
func checkRevList(commitrange string) (string, error) {
	cmdArgs := []string{"git", "rev-list", commitrange}

	_, err := command(cmdArgs[1:]...).Output()
	if err == nil {
		// no issues, return now
		return commitrange, nil
	}

	cmdArgs = []string{"git", "log", "--pretty=oneline", "--no-show-signature"}

	output, err := command(cmdArgs[1:]...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}
	// This "output" is now the list of available commits and short description.
	// We want the last commit hash only.. (i.e. `| tail -n1 | awk '{ print $1 }'`)
	chunks := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(chunks) == 1 {
		commit, _, _ := strings.Cut(chunks[0], " ")
		return commit, nil
	}

	lastCommit, _, _ := strings.Cut(chunks[len(chunks)-1], " ")

	return lastCommit + "..HEAD", nil
}

// FieldNames are for the formating and rendering of the CommitEntry structs.
// Keys here are from git log pretty format "format:...".
var FieldNames = map[string]string{
	"%h":  "abbreviated_commit",
	"%p":  "abbreviated_parent",
	"%t":  "abbreviated_tree",
	"%aD": "author_date",
	"%aE": "author_email",
	"%aN": "author_name",
	"%b":  "body",
	"%H":  "commit",
	"%N":  "commit_notes",
	"%cD": "committer_date",
	"%cE": "committer_email",
	"%cN": "committer_name",
	"%e":  "encoding",
	"%P":  "parent",
	"%D":  "refs",
	"%f":  "sanitized_subject_line",
	"%GS": "signer",
	"%GK": "signer_key",
	"%s":  "subject",
	"%G?": "verification_flag",
}

func gitVersion() (string, error) {
	cmd := command("version")
	cmd.Stderr = os.Stderr

	buf, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.Fields(string(buf))[2], nil
}

// https://github.com/vbatts/git-validation/issues/37
var versionWithExcludes = "1.9.5"

func gitVersionNewerThan(otherV string) (bool, error) {
	gv, err := gitVersion()
	if err != nil {
		return false, err
	}

	return versionAtLeast(gv, otherV)
}

var errNoVersionNumber = errors.New("no version number")

// versionAtLeast reports whether git version have is want or later. Only the
// leading dot-separated numbers count, so a Windows build's 2.42.0.windows.2
// is 2.42.0, and a missing segment is lower than any present one: 1.9 is
// before 1.9.5.
func versionAtLeast(have, want string) (bool, error) {
	haveNumbers, err := versionNumbers(have)
	if err != nil {
		return false, err
	}

	wantNumbers, err := versionNumbers(want)
	if err != nil {
		return false, err
	}

	return slices.Compare(haveNumbers, wantNumbers) >= 0, nil
}

func versionNumbers(gitVersion string) ([]int, error) {
	var numbers []int

	for field := range strings.SplitSeq(gitVersion, ".") {
		number, err := strconv.Atoi(field)
		if err != nil {
			break
		}

		numbers = append(numbers, number)
	}

	if len(numbers) == 0 {
		return nil, fmt.Errorf("git version %q: %w", gitVersion, errNoVersionNumber)
	}

	return numbers, nil
}

// Check warns if changes introduce whitespace errors.
// Returns non-zero if any issues are found.
func Check(commit string) ([]byte, error) {
	logs, err := LogUntil(commit)
	if err != nil {
		return nil, err
	}

	if len(logs) == 1 {
		slog.Debug("skipping the whitespace check of the initial commit, which has no parent", "commit", commit)

		return []byte{}, nil
	}

	args := []string{
		"--no-pager", "log", "--check",
		fmt.Sprintf("%s^..%s", commit, commit),
	}

	if excludeEnvList := os.Getenv("GIT_CHECK_EXCLUDE"); excludeEnvList != "" {
		gitNewEnough, err := gitVersionNewerThan(versionWithExcludes)
		if err != nil {
			return nil, err
		}

		if gitNewEnough {
			for exclude := range strings.SplitSeq(excludeEnvList, ":") {
				if exclude == "" {
					continue
				}

				args = append(args, "--", ".", ":(exclude)"+exclude)
			}
		}
	}

	cmd := command(args...)

	cmd.Stderr = os.Stderr

	return cmd.Output()
}

// Show returns the diff of a commit.
//
// NOTE: This could be expensive for very large commits.
func Show(commit string) ([]byte, error) {
	cmd := command("--no-pager", "show", commit)

	cmd.Stderr = os.Stderr

	return cmd.Output()
}

// CommitEntry represents a single commit's information from `git`.
// See also FieldNames.
type CommitEntry map[string]string

// LogUntil returns the array of oneline commits from initial until the provided commit.
func LogUntil(commit string) ([]string, error) {
	cmd := command("--no-pager", "log", "--oneline", "--no-show-signature", commit)

	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}

	return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
}

// LogCommit assembles the full information on a commit from its commit hash.
func LogCommit(commit string) (*CommitEntry, error) {
	c := CommitEntry{}

	for k, v := range FieldNames {
		cmd := command("--no-pager", "log", "-1", `--pretty=format:`+k+``, commit)

		cmd.Stderr = os.Stderr

		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
		}

		commitMessage := strings.ReplaceAll(string(out), "\r\n", "\n")
		c[v] = strings.TrimSpace(commitMessage)
	}

	return &c, nil
}

// FetchHeadCommit returns the hash of FETCH_HEAD.
func FetchHeadCommit() (string, error) {
	cmdArgs := []string{"git", "--no-pager", "rev-parse", "--verify", "FETCH_HEAD"}

	output, err := command(cmdArgs[1:]...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}

	return strings.TrimSpace(string(output)), nil
}

// HeadCommit returns the hash of HEAD.
func HeadCommit() (string, error) {
	cmdArgs := []string{"git", "--no-pager", "rev-parse", "--verify", "HEAD"}

	output, err := command(cmdArgs[1:]...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}

	return strings.TrimSpace(string(output)), nil
}

// command returns the git command for args, recorded at debug level.
func command(args ...string) *exec.Cmd {
	slog.Debug("running git", "cmd", "git "+strings.Join(args, " "))

	return exec.Command("git", args...)
}
