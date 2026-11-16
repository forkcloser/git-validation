package danglingwhitespace

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"

	"github.com/forkcloser/git-validation/git"
	"github.com/forkcloser/git-validation/validate"
)

// whitespaceErrors is the status git log --check exits with when it finds
// whitespace errors.
const whitespaceErrors = 2

// notCheckedWarning is logged for a commit whose whitespace could not be
// checked. One commit deeper would only move the cut, so it asks for the
// history or a range.
const notCheckedWarning = "whitespace not checked: the commit's parents are not in this shallow clone; " +
	"fetch the history (actions/checkout fetch-depth: 0, git fetch --unshallow) or give a range it holds"

// DanglingWhitespace is the rule for checking the presence of dangling
// whitespaces on line endings.
var DanglingWhitespace = validate.Rule{
	Name:        "dangling-whitespace",
	Description: "checking the presence of dangling whitespaces on line endings",
	Run:         ValidateDanglingWhitespace,
	Default:     true,
}

func init() {
	validate.RegisterRule(DanglingWhitespace)
}

// ValidateDanglingWhitespace runs Git's check to look for whitespace errors.
func ValidateDanglingWhitespace(ctx context.Context, r validate.Rule, c git.CommitEntry) (vr validate.Result) {
	vr.CommitEntry = c
	vr.Msg = "commit does not have any whitespace errors"
	vr.Pass = true

	_, err := git.Check(ctx, c["commit"])
	if errors.Is(err, git.ErrParentsNotHere) {
		vr.Pass = false
		vr.NotChecked = true
		vr.Msg = "whitespace not checked: " + err.Error()

		slog.WarnContext(ctx, notCheckedWarning, "commit", c["commit"])

		return vr
	}

	if err != nil {
		vr.Pass = false

		// git log --check exits 2 when it finds whitespace errors; anything
		// else is git failing.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == whitespaceErrors {
			vr.Msg = "has whitespace errors. See `git show --check " + c["commit"] + "`."
		} else {
			vr.Msg = "errored with: " + err.Error()
		}
	}

	return vr
}
