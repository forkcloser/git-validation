package validate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/forkcloser/git-validation/git"
)

// Runner is the for processing a set of rules against a range of commits.
// A run writes nothing to standard output and does not read QUIET: what it
// found is in Results, and a caller that wants to follow the run sets Report.
type Runner struct {
	Root        string
	Rules       []Rule
	Results     Results
	CommitRange string // if this is empty, then it will default to FETCH_HEAD, then HEAD
	// Report, when set, is called by Run with each commit and the results of
	// its rules, as soon as the commit is validated and in the range's order;
	// Results holds them already.
	Report func(commit git.CommitEntry, results Results)
}

// NewRunner returns an initiallized Runner. A rule whose Check refuses its
// Value is refused here, before any commit is read.
func NewRunner(ctx context.Context, root string, rules []Rule, commitrange string) (runner *Runner, err error) {
	for _, rule := range rules {
		if rule.Check == nil {
			continue
		}

		if checkErr := rule.Check(rule.Value); checkErr != nil {
			return nil, fmt.Errorf("rule %s: %w", rule.Name, checkErr)
		}
	}

	newroot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path of %q: %w", root, err)
	}

	if commitrange == "" {
		var cwd string

		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("finding the working directory: %w", err)
		}
		defer func() {
			if chdirErr := os.Chdir(cwd); chdirErr != nil {
				runner, err = nil, errors.Join(err, fmt.Errorf("returning to %q: %w", cwd, chdirErr))
			}
		}()

		if err = os.Chdir(newroot); err != nil {
			return nil, fmt.Errorf("entering %q: %w", newroot, err)
		}

		commitrange, err = git.FetchHeadCommit(ctx)
		if err != nil {
			commitrange, err = git.HeadCommit(ctx)
			if err != nil {
				return nil, fmt.Errorf("finding a commit to validate from in %q: %w", newroot, err)
			}
		}

		slog.DebugContext(ctx, "no commit range given, validating from", "commit", commitrange)
	}

	return &Runner{
		Root:        newroot,
		Rules:       rules,
		CommitRange: commitrange,
	}, nil
}

// Run processes the rules for each commit in the range provided, appending
// every result to Results. When ctx ends it stops and returns the context's
// error: Results holds the commits validated whole before that, and nothing
// of the one it was at.
func (r *Runner) Run(ctx context.Context) (err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding the working directory: %w", err)
	}
	defer func() {
		if chdirErr := os.Chdir(cwd); chdirErr != nil {
			err = errors.Join(err, fmt.Errorf("returning to %q: %w", cwd, chdirErr))
		}
	}()

	if err = os.Chdir(r.Root); err != nil {
		return fmt.Errorf("entering %q: %w", r.Root, err)
	}

	// collect the entries
	c, err := git.Commits(ctx, r.CommitRange)
	if err != nil {
		return fmt.Errorf("listing the commits of %q: %w", r.CommitRange, err)
	}

	for _, commit := range c {
		if err = ctx.Err(); err != nil {
			return fmt.Errorf("stopped before %s: %w", commit["abbreviated_commit"], err)
		}

		results := Commit(ctx, commit, r.Rules)

		// A rule that was stopped has not judged the commit: what it
		// returned is the stop, and is not a result.
		if err = ctx.Err(); err != nil {
			return fmt.Errorf("stopped at %s: %w", commit["abbreviated_commit"], err)
		}

		r.Results = append(r.Results, results...)

		if r.Report != nil {
			r.Report(commit, results)
		}
	}

	return nil
}
