package messageregexp_test

import (
	"strings"
	"testing"

	"github.com/forkcloser/git-validation/git"
	"github.com/forkcloser/git-validation/rules/messageregexp"
	"github.com/forkcloser/git-validation/validate"
)

// TestCheckValue: a value that is not a regular expression is refused with
// the regexp package's words, before any commit is read; the rule
// registers the check, so -run 'message_regexp=(' is a usage error where
// it was a panic after the first commit's line.
func TestCheckValue(t *testing.T) {
	t.Parallel()

	if messageregexp.RegexpRule.Check == nil {
		t.Fatal("message_regexp registers no Check")
	}

	err := messageregexp.RegexpRule.Check("(")
	if err == nil || !strings.Contains(err.Error(), "value does not compile: error parsing regexp: missing closing )") {
		t.Errorf("Check(\"(\") = %v", err)
	}

	for _, ok := range []string{"", "^JIRA-[0-9]+", "a|b"} {
		if err := messageregexp.CheckValue(ok); err != nil {
			t.Errorf("Check(%q) = %v", ok, err)
		}
	}
}

// TestRunWithoutCheckFailsTheCommit: the rule run on its own with a value
// its check would refuse fails the commit with the reason, and never
// panics.
func TestRunWithoutCheckFailsTheCommit(t *testing.T) {
	t.Parallel()

	rule := validate.Rule{Name: "message_regexp", Value: "("}
	commit := git.CommitEntry{"commit": "1111", "subject": "(", "body": "", "parent": "0000"}

	got := messageregexp.ValidateMessageRegexp(rule, commit)
	if got.Pass || !strings.Contains(got.Msg, "message_regexp value does not compile") {
		t.Errorf("Run with \"(\": pass=%v msg=%q", got.Pass, got.Msg)
	}

	rule.Value = "^\\("

	if got := messageregexp.ValidateMessageRegexp(rule, commit); !got.Pass {
		t.Errorf("Run with a matching value: pass=%v msg=%q", got.Pass, got.Msg)
	}
}
