package validate

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/forkcloser/git-validation/git"
)

var (
	// RegisteredRules are the available validations to perform on git commits.
	RegisteredRules  = []Rule{}
	registerRuleLock = sync.Mutex{}
)

// RegisterRule includes the Rule in the available set to use. The set stays
// ordered by name, ignoring case: rules register from their packages' init,
// whose order is the Go toolchain's to choose and moved when main gained an
// import, and the set's order is the order of every report.
func RegisterRule(vr Rule) {
	registerRuleLock.Lock()
	defer registerRuleLock.Unlock()

	RegisteredRules = append(RegisteredRules, vr)
	slices.SortStableFunc(RegisteredRules, func(a, b Rule) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// Rule will operate over a provided git.CommitEntry, and return a result.
type Rule struct {
	Name        string // short name for reference in the `-run=...` flag
	Value       string // value to configure for the rule (i.e. a regexp to check for in the commit message)
	Description string // longer Description for readability
	Run         func(Rule, git.CommitEntry) Result
	Default     bool // whether the registered rule is run by default
	// Check refuses a Value the rule cannot run with, before any commit is
	// read (NewRunner calls it); nil for a rule that takes no value.
	Check func(value string) error
}

// Commit processes the given rules on the provided commit, and returns the
// result set, every result carrying the commit (not every rule sets it).
func Commit(c git.CommitEntry, rules []Rule) Results {
	results := Results{}

	for _, r := range rules {
		res := r.Run(r, c)
		res.CommitEntry = c
		results = append(results, res)
	}

	return results
}

// Result is the result for a single validation of a commit.
type Result struct {
	CommitEntry git.CommitEntry
	Pass        bool
	Msg         string
}

// Results is a set of results. This is type makes it easy for the following function.
type Results []Result

// PassFail gives a quick over/under of passes and failures of the results in this set.
func (vr Results) PassFail() (pass, fail int) {
	for _, res := range vr {
		if res.Pass {
			pass++
		} else {
			fail++
		}
	}

	return pass, fail
}

// CommitsToFix is the number of commits with at least one failing result.
func (vr Results) CommitsToFix() int {
	failing := map[string]struct{}{}

	for _, res := range vr {
		if !res.Pass {
			failing[res.CommitEntry["commit"]] = struct{}{}
		}
	}

	return len(failing)
}

// SanitizeFilters takes a comma delimited list and returns the trimmend and
// split (on ",") items in the list.
func SanitizeFilters(filtStr string) (filters []string) {
	for item := range strings.SplitSeq(filtStr, ",") {
		filters = append(filters, strings.TrimSpace(item))
	}

	return filters
}

// FilterRules takes a set of rules and a list of short names to include, and
// returns the reduced set.  The comparison is case insensitive.
//
// Some `includes` rules have values assigned to them.
// i.e. -run "dco,message_regexp='^JIRA-[0-9]+ [A-Z].*$'".
func FilterRules(rules []Rule, includes []string) []Rule {
	ret := []Rule{}

	for _, r := range rules {
		for i := range includes {
			if strings.Contains(includes[i], "=") {
				chunks := strings.SplitN(includes[i], "=", 2)
				if strings.EqualFold(r.Name, chunks[0]) {
					// for these rules, the Name won't be unique per se. There may be
					// multiple "regexp=" with different values. We'll need to set the
					// .Value = chunk[1] and ensure r is dup'ed so they don't clobber
					// each other.
					newR := r
					newR.Value = chunks[1]
					ret = append(ret, newR)
				}
			} else if strings.EqualFold(r.Name, includes[i]) {
				ret = append(ret, r)
			}
		}
	}

	return ret
}

// SelectRules is FilterRules refusing a name that selects no rule: a
// misspelled name in -run would otherwise run nothing and pass every commit.
// The error names the rule and lists the rules there are.
func SelectRules(rules []Rule, includes []string) ([]Rule, error) {
	for _, include := range includes {
		name, _, _ := strings.Cut(include, "=")

		if !slices.ContainsFunc(rules, func(r Rule) bool { return strings.EqualFold(r.Name, name) }) {
			names := make([]string, 0, len(rules))
			for _, r := range rules {
				names = append(names, r.Name)
			}

			return nil, fmt.Errorf("%w %q; the rules are %s", ErrUnknownRule, name, strings.Join(names, ", "))
		}
	}

	return FilterRules(rules, includes), nil
}

// ErrUnknownRule is a -run name no registered rule has.
var ErrUnknownRule = errors.New("unknown rule")

// StringsSliceEqual compares two string arrays for equality.
func StringsSliceEqual(a, b []string) bool {
	if !sort.StringsAreSorted(a) {
		sort.Strings(a)
	}

	if !sort.StringsAreSorted(b) {
		sort.Strings(b)
	}

	for i := range b {
		if !StringsSliceContains(a, b[i]) {
			return false
		}
	}

	for i := range a {
		if !StringsSliceContains(b, a[i]) {
			return false
		}
	}

	return true
}

// StringsSliceContains checks for the presence of a word in string array.
func StringsSliceContains(a []string, b string) bool {
	if !sort.StringsAreSorted(a) {
		sort.Strings(a)
	}

	i := sort.SearchStrings(a, b)

	return i < len(a) && a[i] == b
}
