package shortsubject

import (
	"strings"
	"unicode/utf8"

	"github.com/forkcloser/git-validation/git"
	"github.com/forkcloser/git-validation/validate"
)

// The two lengths of a subject, in characters: it must be strictly shorter
// than limit, and is better kept within preferred. The description and the
// messages name them too.
const (
	limit     = 90
	preferred = 72
)

// ShortSubjectRule is the rule being registered.
var ShortSubjectRule = validate.Rule{
	Name:        "short-subject",
	Description: "commit subjects are strictly less than 90 (github ellipsis length)",
	Run:         ValidateShortSubject,
	Default:     true,
}

func init() {
	validate.RegisterRule(ShortSubjectRule)
}

// ValidateShortSubject checks that the commit's subject is strictly less than
// 90 characters (preferably not more than 72 chars).
func ValidateShortSubject(r validate.Rule, c git.CommitEntry) (vr validate.Result) {
	if len(strings.Split(c["parent"], " ")) > 1 {
		vr.Pass = true
		vr.Msg = "merge commits do not require length check"

		return vr
	}

	// The limit is in characters, as GitHub's ellipsis and a reader count
	// them; in bytes, 85 accented characters failed the rule.
	length := utf8.RuneCountInString(c["subject"])

	if length >= limit {
		vr.Pass = false
		vr.Msg = "commit subject exceeds 90 characters"

		return vr
	}

	vr.Pass = true
	if length > preferred {
		vr.Msg = "commit subject is under 90 characters, but is still more than 72 chars"
	} else {
		vr.Msg = "commit subject is 72 characters or less! *yay*"
	}

	return vr
}
