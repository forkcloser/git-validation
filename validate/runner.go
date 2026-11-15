package validate

import (
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
func NewRunner(root string, rules []Rule, commitrange string) (runner *Runner, err error) {
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
			return nil, err
		}
		defer func() {
			if chdirErr := os.Chdir(cwd); chdirErr != nil {
				runner, err = nil, errors.Join(err, fmt.Errorf("returning to %q: %w", cwd, chdirErr))
			}
		}()

		if err = os.Chdir(newroot); err != nil {
			return nil, err
		}

		commitrange, err = git.FetchHeadCommit()
		if err != nil {
			commitrange, err = git.HeadCommit()
			if err != nil {
				return nil, err
			}
		}

		slog.Debug("no commit range given, validating from", "commit", commitrange)
	}

	return &Runner{
		Root:        newroot,
		Rules:       rules,
		CommitRange: commitrange,
	}, nil
}

// Run processes the rules for each commit in the range provided, appending
// every result to Results.
func (r *Runner) Run() (err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	defer func() {
		if chdirErr := os.Chdir(cwd); chdirErr != nil {
			err = errors.Join(err, fmt.Errorf("returning to %q: %w", cwd, chdirErr))
		}
	}()

	if err = os.Chdir(r.Root); err != nil {
		return err
	}

	// collect the entries
	c, err := git.Commits(r.CommitRange)
	if err != nil {
		return err
	}

	for _, commit := range c {
		vr := Commit(commit, r.Rules)
		r.Results = append(r.Results, vr...)

		if r.Report != nil {
			r.Report(commit, vr)
		}
	}

	return nil
}
