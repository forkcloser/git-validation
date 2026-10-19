package validate

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/forkcloser/git-validation/git"
)

func TestSanitizeRules(t *testing.T) {
	set := []struct {
		input  string
		output []string
	}{
		{
			input:  "apples, oranges , bananas",
			output: []string{"apples", "oranges", "bananas"},
		},
		{
			input:  "apples, oranges , bananas, peaches='with cream'",
			output: []string{"apples", "oranges", "bananas", "peaches='with cream'"},
		},
	}

	for i := range set {
		filt := SanitizeFilters(set[i].input)
		if !StringsSliceEqual(filt, set[i].output) {
			t.Errorf("expected output like %v, but got %v", set[i].output, filt)
		}
	}
}

func TestSliceHelpers(t *testing.T) {
	set := []struct {
		A, B  []string
		Equal bool
	}{
		{
			A:     []string{"apples", "bananas", "oranges", "mango"},
			B:     []string{"oranges", "bananas", "apples", "mango"},
			Equal: true,
		},
		{
			A:     []string{"apples", "bananas", "oranges", "mango"},
			B:     []string{"waffles"},
			Equal: false,
		},
	}
	for i := range set {
		got := StringsSliceEqual(set[i].A, set[i].B)
		if got != set[i].Equal {
			t.Errorf("expected %d A and B comparison to be %t, but got %t", i, set[i].Equal, got)
		}
	}
}

func TestRegisterRuleOrder(t *testing.T) {
	saved := RegisteredRules

	t.Cleanup(func() { RegisteredRules = saved })

	RegisteredRules = nil

	for _, name := range []string{"short-subject", "DCO", "message_regexp", "dangling-whitespace"} {
		RegisterRule(Rule{Name: name})
	}

	var got []string
	for _, rule := range RegisteredRules {
		got = append(got, rule.Name)
	}

	want := []string{"dangling-whitespace", "DCO", "message_regexp", "short-subject"}
	if !slices.Equal(got, want) {
		t.Errorf("RegisteredRules = %q, want %q", got, want)
	}
}

// TestSelectRules: a -run name selects the rule of that name whatever its
// case, with a value after '=' on its own copy; a name no rule has is
// refused, naming it and the rules there are, where FilterRules ran
// nothing and so passed every commit.
func TestSelectRules(t *testing.T) {
	rules := []Rule{{Name: "dangling-whitespace"}, {Name: "DCO"}, {Name: "message_regexp"}, {Name: "short-subject"}}

	got, err := SelectRules(rules, []string{"dco", "message_regexp=^x", "message_regexp=^y"})
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, r := range got {
		names = append(names, r.Name+"="+r.Value)
	}

	// The registered order, as FilterRules keeps it, not -run's.
	if want := []string{"DCO=", "message_regexp=^x", "message_regexp=^y"}; !slices.Equal(names, want) {
		t.Errorf("SelectRules = %q, want %q", names, want)
	}

	for _, run := range [][]string{{"typo"}, {"dco", "typo"}, {"typo=^x"}, {""}} {
		got, err := SelectRules(rules, run)
		if !errors.Is(err, ErrUnknownRule) || got != nil {
			t.Errorf("SelectRules(%q) = %v, %v; want ErrUnknownRule", run, got, err)

			continue
		}

		want := `unknown rule "typo"; the rules are dangling-whitespace, DCO, message_regexp, short-subject`
		if run[0] == "" {
			want = `unknown rule ""; the rules are`
		}

		if !strings.Contains(err.Error(), want) {
			t.Errorf("SelectRules(%q): %v; want %s", run, err, want)
		}
	}
}

// TestCommitsToFix counts commits, not results: two failing rules on one
// commit are one commit to fix.
func TestCommitsToFix(t *testing.T) {
	one := git.CommitEntry{"commit": "1111"}
	two := git.CommitEntry{"commit": "2222"}

	results := Results{
		{CommitEntry: one, Pass: false},
		{CommitEntry: one, Pass: false},
		{CommitEntry: one, Pass: true},
		{CommitEntry: two, Pass: true},
	}

	if got := results.CommitsToFix(); got != 1 {
		t.Errorf("CommitsToFix = %d, want 1", got)
	}

	if _, fail := results.PassFail(); fail != 2 {
		t.Errorf("PassFail fail = %d, want 2 results", fail)
	}

	results = append(results, Result{CommitEntry: two, Pass: false})
	if got := results.CommitsToFix(); got != 2 {
		t.Errorf("CommitsToFix = %d, want 2", got)
	}

	if got := (Results{{CommitEntry: one, Pass: true}}).CommitsToFix(); got != 0 {
		t.Errorf("CommitsToFix of passes = %d, want 0", got)
	}
}

// TestCommitStampsTheEntry: every result of Commit carries its commit, the
// rule setting it or not, so CommitsToFix counts commits whatever the rules
// return.
func TestCommitStampsTheEntry(t *testing.T) {
	entry := git.CommitEntry{"commit": "1111"}
	fail := func(Rule, git.CommitEntry) Result { return Result{Pass: false, Msg: "no"} }
	rules := []Rule{{Name: "a", Run: fail}, {Name: "b", Run: fail}}

	results := Commit(entry, rules)
	if len(results) != 2 {
		t.Fatalf("Commit returned %d results, want 2", len(results))
	}

	for i, res := range results {
		if res.CommitEntry["commit"] != "1111" {
			t.Errorf("result %d carries commit %q, want 1111", i, res.CommitEntry["commit"])
		}
	}

	if got := results.CommitsToFix(); got != 1 {
		t.Errorf("CommitsToFix = %d, want 1", got)
	}
}
