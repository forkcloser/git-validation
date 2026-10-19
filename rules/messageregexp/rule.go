package messageregexp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/forkcloser/git-validation/git"
	"github.com/forkcloser/git-validation/validate"
)

func init() {
	validate.RegisterRule(RegexpRule)
}

// RegexpRule for validating a user provided regex on the commit messages.
var RegexpRule = validate.Rule{
	Name:        "message_regexp",
	Description: "checks the commit message for a user provided regular expression",
	Run:         ValidateMessageRegexp,
	Default:     false, // only for users specifically calling it through -run ...
	Check:       CheckValue,
}

// CheckValue refuses a value that is not a regular expression, with the
// regexp package's own words; the run is refused before any commit is read.
func CheckValue(value string) error {
	if _, err := regexp.Compile(value); err != nil {
		return fmt.Errorf("value does not compile: %w", err)
	}

	return nil
}

// ValidateMessageRegexp is the message regex func to run.
func ValidateMessageRegexp(r validate.Rule, c git.CommitEntry) (vr validate.Result) {
	if r.Value == "" {
		vr.Pass = true
		vr.Msg = "noop: message_regexp value is blank"

		return vr
	}

	vr.CommitEntry = c

	// NewRunner refused the value already; a rule run on its own fails the
	// commit with the reason rather than panicking.
	re, err := regexp.Compile(r.Value)
	if err != nil {
		vr.Pass = false
		vr.Msg = fmt.Sprintf("message_regexp value does not compile: %v", err)

		return vr
	}

	if len(strings.Split(c["parent"], " ")) > 1 {
		vr.Pass = true
		vr.Msg = "merge commits are not checked for message_regexp"

		return vr
	}

	hasValid := false

	for line := range strings.SplitSeq(c["subject"], "\n") {
		if re.MatchString(line) {
			hasValid = true
		}
	}

	for line := range strings.SplitSeq(c["body"], "\n") {
		if re.MatchString(line) {
			hasValid = true
		}
	}

	if !hasValid {
		vr.Pass = false
		vr.Msg = fmt.Sprintf("commit message does not match %q", r.Value)
	} else {
		vr.Pass = true
		vr.Msg = fmt.Sprintf("commit message matches %q", r.Value)
	}

	return vr
}
