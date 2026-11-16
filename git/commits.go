package git

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
func Commits(ctx context.Context, commitrange string) ([]CommitEntry, error) {
	// TEST if it has _enough_ of a range from rev-list
	commitrange, err := checkRevList(ctx, commitrange)
	if err != nil {
		return nil, fmt.Errorf("validating the commit range: %w", err)
	}

	return entries(ctx, commitrange)
}

// errRange is a --range git cannot resolve: a typo, a ref that is not
// there, or a start past the depth of a shallow clone. It names the range.
var errRange = errors.New("the commit range does not resolve")

// checkRevList returns the range to validate: the range itself, once git
// resolves it. One git does not resolve is an error. Before, it validated
// what the repository had, so a typo, or a shallow clone whose depth did not
// reach the range's start, validated the whole history and passed. In a
// shallow clone the error says what to fetch.
func checkRevList(ctx context.Context, commitrange string) (string, error) {
	cmdArgs := []string{"git", "rev-list", commitrange}

	_, err := command(ctx, cmdArgs[1:]...).Output()

	err = ended(ctx, err)
	if err == nil {
		return commitrange, nil
	}

	// A git that was stopped says nothing of the range.
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}

	// The git command that failed is named, and its exit unwraps, as every
	// git failure here does; git's own words follow.
	detail := ""

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && len(exitErr.Stderr) > 0 {
		detail = " (" + strings.TrimSpace(string(exitErr.Stderr)) + ")"
	}

	if shallowRepository(ctx) {
		detail += "; the repository is a shallow clone"

		// A typo and an end past the depth look the same from here: both
		// are not in the clone.
		switch missing := missingEnds(ctx, commitrange); len(missing) {
		case 0:
		case 1:
			detail += " and " + missing[0] + " is not in it"
		default:
			detail += " and " + strings.Join(missing, " and ") + " are not in it"
		}

		detail += ": fetch more history (actions/checkout fetch-depth: 0, git fetch --deepen=<n> or --unshallow)"
	}

	return "", fmt.Errorf("%w: %s: %w%s", errRange, strings.Join(cmdArgs, " "), err, detail)
}

// missingEnds returns the ends of a range (a..b, a...b, or a single revision)
// that do not name a commit the repository has.
func missingEnds(ctx context.Context, commitrange string) []string {
	var missing []string

	for end := range strings.SplitSeq(strings.ReplaceAll(commitrange, "...", ".."), "..") {
		if end == "" {
			continue
		}

		err := command(ctx, "rev-parse", "--verify", "--quiet", end+"^{commit}").Run()
		if err != nil {
			missing = append(missing, end)
		}
	}

	return missing
}

// shallowRepository reports whether the repository was cloned with a depth,
// so that its history may not reach a range's start.
func shallowRepository(ctx context.Context) bool {
	output, err := command(ctx, "rev-parse", "--is-shallow-repository").Output()
	err = ended(ctx, err)

	return err == nil && strings.TrimSpace(string(output)) == "true"
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

func gitVersion(ctx context.Context) (string, error) {
	cmd := command(ctx, "version")
	cmd.Stderr = os.Stderr

	buf, err := cmd.Output()

	err = ended(ctx, err)
	if err != nil {
		return "", fmt.Errorf("git version: %w", err)
	}

	return strings.Fields(string(buf))[2], nil
}

// https://github.com/vbatts/git-validation/issues/37
var versionWithExcludes = "1.9.5"

func gitVersionNewerThan(ctx context.Context, otherV string) (bool, error) {
	gv, err := gitVersion(ctx)
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
func Check(ctx context.Context, commit string) ([]byte, error) {
	logs, err := LogUntil(ctx, commit)
	if err != nil {
		return nil, err
	}

	if len(logs) == 1 {
		slog.DebugContext(
			ctx,
			"skipping the whitespace check of the initial commit, which has no parent",
			"commit",
			commit,
		)

		return []byte{}, nil
	}

	args := []string{
		"--no-pager", "log", "--check",
		fmt.Sprintf("%s^..%s", commit, commit),
	}

	if excludeEnvList := os.Getenv("GIT_CHECK_EXCLUDE"); excludeEnvList != "" {
		var gitNewEnough bool

		gitNewEnough, err = gitVersionNewerThan(ctx, versionWithExcludes)
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

	cmd := command(ctx, args...)

	cmd.Stderr = os.Stderr

	// The output holds what git found when it exits non-zero, which is how
	// it says there are whitespace errors: both go back to the caller.
	out, err := cmd.Output()

	err = ended(ctx, err)
	if err != nil {
		return out, fmt.Errorf("git log --check %s: %w", commit, err)
	}

	return out, nil
}

// Show returns the diff of a commit.
//
// NOTE: This could be expensive for very large commits.
func Show(ctx context.Context, commit string) ([]byte, error) {
	cmd := command(ctx, "--no-pager", "show", commit)

	cmd.Stderr = os.Stderr

	out, err := cmd.Output()

	err = ended(ctx, err)
	if err != nil {
		return out, fmt.Errorf("git show %s: %w", commit, err)
	}

	return out, nil
}

// CommitEntry represents a single commit's information from `git`.
// See also FieldNames.
type CommitEntry map[string]string

// LogUntil returns the array of oneline commits from initial until the provided commit.
func LogUntil(ctx context.Context, commit string) ([]string, error) {
	cmd := command(ctx, "--no-pager", "log", "--oneline", "--no-show-signature", commit)

	cmd.Stderr = os.Stderr

	out, err := cmd.Output()

	err = ended(ctx, err)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}

	return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
}

// LogCommit assembles the full information on a commit from its commit hash.
func LogCommit(ctx context.Context, commit string) (*CommitEntry, error) {
	found, err := entries(ctx, "-1", commit)
	if err != nil {
		return nil, err
	}

	if len(found) != 1 {
		return nil, fmt.Errorf("git log -1 %s: %w: %d", commit, errEntries, len(found))
	}

	return &found[0], nil
}

// errEntries is a git log that did not print the commits asked of it, field
// for field.
var errEntries = errors.New("git log did not print whole commits")

// entries is the commits git log lists for the revisions, newest first, every
// field of FieldNames read: one git for all of them. The fields are printed
// with a NUL between them and between commits, which no field holds, so that
// a body with any line in it is read whole.
func entries(ctx context.Context, revisions ...string) ([]CommitEntry, error) {
	formats := slices.Sorted(maps.Keys(FieldNames))

	// Under log.showSignature git prints what it found of a signature ahead
	// of each commit, inside the first field.
	args := append(
		[]string{"--no-pager", "log", "-z", "--no-show-signature", "--pretty=format:" + strings.Join(formats, "%x00")},
		revisions...,
	)

	cmd := command(ctx, args...)

	cmd.Stderr = os.Stderr

	out, err := cmd.Output()

	err = ended(ctx, err)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}

	if len(out) == 0 {
		return nil, nil
	}

	fields := strings.Split(string(out), "\x00")
	if len(fields)%len(formats) != 0 {
		return nil, fmt.Errorf("%s: %w: %d fields", strings.Join(cmd.Args, " "), errEntries, len(fields))
	}

	found := make([]CommitEntry, 0, len(fields)/len(formats))

	for commit := range slices.Chunk(fields, len(formats)) {
		entry := CommitEntry{}

		for idx, format := range formats {
			entry[FieldNames[format]] = strings.TrimSpace(strings.ReplaceAll(commit[idx], "\r\n", "\n"))
		}

		found = append(found, entry)
	}

	return found, nil
}

// FetchHeadCommit returns the hash of FETCH_HEAD.
func FetchHeadCommit(ctx context.Context) (string, error) {
	cmdArgs := []string{"git", "--no-pager", "rev-parse", "--verify", "FETCH_HEAD"}

	output, err := command(ctx, cmdArgs[1:]...).Output()

	err = ended(ctx, err)
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}

	return strings.TrimSpace(string(output)), nil
}

// HeadCommit returns the hash of HEAD.
func HeadCommit(ctx context.Context) (string, error) {
	cmdArgs := []string{"git", "--no-pager", "rev-parse", "--verify", "HEAD"}

	output, err := command(ctx, cmdArgs[1:]...).Output()

	err = ended(ctx, err)
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(cmdArgs, " "), err)
	}

	return strings.TrimSpace(string(output)), nil
}

// ended is err, or the context's error with it when the context is done: a
// git that was killed because its context ended says "signal: killed", and
// the caller asked whether its context ended.
func ended(ctx context.Context, err error) error {
	if err == nil || ctx.Err() == nil {
		return err
	}

	return fmt.Errorf("%w: %w", ctx.Err(), err)
}

// command returns the git command for args, recorded at debug level. git is
// killed when ctx ends.
func command(ctx context.Context, args ...string) *exec.Cmd {
	slog.DebugContext(ctx, "running git", "cmd", "git "+strings.Join(args, " "))

	// #nosec G204 -- the program is git; its arguments are this file's own subcommands, commits git named, and the
	// range the operator gave, by --range or by their CI's variables (TRAVIS_COMMIT_RANGE, TRAVIS_COMMIT, GITHUB_SHA),
	// which is theirs to give as it would be to git
	return exec.CommandContext(ctx, "git", args...)
}
