// The validate package's contract from the outside: what the README and
// each rule's description promise, held through the exported API with the
// four rule packages registered as the command registers them.
package validate_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/forkcloser/git-validation/git"
	_ "github.com/forkcloser/git-validation/rules/danglingwhitespace"
	"github.com/forkcloser/git-validation/rules/dco"
	"github.com/forkcloser/git-validation/rules/messageregexp"
	"github.com/forkcloser/git-validation/rules/shortsubject"
	"github.com/forkcloser/git-validation/validate"
)

// TestContractRegistryIsTheREADMEsList: the four rules register under the
// names and descriptions `--list-rules` prints, sorted without regard to
// case; DCO, short-subject and dangling-whitespace run by default,
// message_regexp only when asked for.
func TestContractRegistryIsTheREADMEsList(t *testing.T) {
	t.Parallel()

	want := []struct {
		name, description string
		byDefault         bool
	}{
		{"dangling-whitespace", "checking the presence of dangling whitespaces on line endings", true},
		{"DCO", "makes sure the commits are signed", true},
		{"message_regexp", "checks the commit message for a user provided regular expression", false},
		{"short-subject", "commit subjects are strictly less than 90 (github ellipsis length)", true},
	}

	if len(validate.RegisteredRules) != len(want) {
		t.Fatalf("%d rules registered, want %d", len(validate.RegisteredRules), len(want))
	}

	for i, w := range want {
		r := validate.RegisteredRules[i]
		if r.Name != w.name || r.Description != w.description || r.Default != w.byDefault || r.Run == nil {
			t.Errorf("rule %d is %q %q default %v, want %q %q default %v", i, r.Name, r.Description, r.Default, w.name, w.description, w.byDefault)
		}
	}
}

// TestContractRunListSelectsRules: `--run` is a comma-separated list, spaces
// around a name ignored, names matched without regard to case, in the
// registry's order whatever the list's; `name=value` configures the rule;
// a name no rule has selects nothing.
func TestContractRunListSelectsRules(t *testing.T) {
	t.Parallel()

	names := func(rules []validate.Rule) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			out = append(out, r.Name)
		}

		return out
	}

	for _, c := range []struct {
		run  string
		want []string
	}{
		{"DCO", []string{"DCO"}},
		{"dco", []string{"DCO"}},
		{" short-subject , DCO ", []string{"DCO", "short-subject"}},
		{"short-subject,dco,dangling-whitespace", []string{"dangling-whitespace", "DCO", "short-subject"}},
		{"nonesuch", nil},
		{"DCO,nonesuch", []string{"DCO"}},
		{"message_regexp=^fix", []string{"message_regexp"}},
	} {
		got := names(validate.FilterRules(validate.RegisteredRules, validate.SanitizeFilters(c.run)))
		if !slices.Equal(got, c.want) {
			t.Errorf("--run %q selects %v, want %v", c.run, got, c.want)
		}
	}

	configured := validate.FilterRules(validate.RegisteredRules, validate.SanitizeFilters("message_regexp=^a=b$"))
	if len(configured) != 1 || configured[0].Value != "^a=b$" {
		t.Errorf("message_regexp=^a=b$ configures %+v, want the value after the first = whole", configured)
	}
}

func entry(subject, body string, parents ...string) git.CommitEntry {
	return git.CommitEntry{
		"commit":             "0123456789abcdef0123456789abcdef01234567",
		"abbreviated_commit": "0123456",
		"subject":            subject,
		"body":               body,
		"parent":             strings.Join(parents, " "),
	}
}

const (
	parentA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	parentB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// TestContractDCO: "makes sure the commits are signed": a commit passes
// when a line of its body is a Developer Certificate of Origin sign-off,
// `Signed-off-by: Name <email>`, the name non-empty, the email with an @
// and no angle brackets, the line nothing else; anywhere in the body, any
// number of times; a merge commit passes unsigned.
func TestContractDCO(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		body string
		pass bool
	}{
		{"signed", "Signed-off-by: Ada Lovelace <ada@example.com>", true},
		{"signed after text", "Fixes the thing.\n\nSigned-off-by: Ada Lovelace <ada@example.com>", true},
		{"signed before text", "Signed-off-by: Ada Lovelace <ada@example.com>\n\nMore text after.", true},
		{"signed twice", "Signed-off-by: A <a@example.com>\nSigned-off-by: B <b@example.com>", true},
		{"single-word name", "Signed-off-by: ada <ada@example.com>", true},
		{"unsigned", "Fixes the thing.", false},
		{"empty body", "", false},
		{"no email", "Signed-off-by: Ada Lovelace", false},
		{"email without @", "Signed-off-by: Ada Lovelace <ada.example.com>", false},
		{"no name", "Signed-off-by: <ada@example.com>", false},
		{"lower-case key", "signed-off-by: Ada Lovelace <ada@example.com>", false},
		{"indented", "  Signed-off-by: Ada Lovelace <ada@example.com>", false},
		{"trailing text", "Signed-off-by: Ada Lovelace <ada@example.com> (reviewed)", false},
		{"in the subject only", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			subject := "a change"
			if c.name == "in the subject only" {
				subject = "Signed-off-by: Ada Lovelace <ada@example.com>"
			}

			r := dco.ValidateDCO(dco.DcoRule, entry(subject, c.body, parentA))
			if r.Pass != c.pass {
				t.Errorf("pass %v (%q), want %v", r.Pass, r.Msg, c.pass)
			}

			if !c.pass && !strings.Contains(r.Msg, "does not have a valid Developer Certificate of Origin") {
				t.Errorf("failure message %q does not name the DCO", r.Msg)
			}
		})
	}

	if r := dco.ValidateDCO(dco.DcoRule, entry("Merge branch", "", parentA, parentB)); !r.Pass {
		t.Errorf("an unsigned merge commit: %q, want a pass", r.Msg)
	}
}

// TestContractDCOAgreesWithGitsTrailerSyntax: for a sign-off that git
// itself writes (`git commit -s`, a trailer `Signed-off-by: Name <email>`),
// the rule and git's own trailer parsing agree on what a sign-off is: the
// regexp accepts exactly the lines `git interpret-trailers` would emit for
// an identity with a non-empty name and an email holding an @.
func TestContractDCOAgreesWithGitsTrailerSyntax(t *testing.T) {
	t.Parallel()

	identities := []struct {
		name, email string
		valid       bool
	}{
		{"Ada Lovelace", "ada@example.com", true},
		{"ada", "a@b", true},
		{"Ada Lovelace", "ada+tag@sub.example.com", true},
		{"Ada <Lovelace>", "ada@example.com", false},
		{"Ada Lovelace", "ada.example.com", false},
		{"", "ada@example.com", false},
		{"Ada Lovelace", "", false},
	}

	for _, id := range identities {
		line := "Signed-off-by: " + id.name + " <" + id.email + ">"
		if got := dco.ValidDCO.MatchString(line); got != id.valid {
			t.Errorf("%q: accepted %v, want %v", line, got, id.valid)
		}
	}
}

// TestContractShortSubject: "commit subjects are strictly less than 90":
// 89 passes, 90 fails, the pass naming the 72-character courtesy line when
// the subject is over it; a merge commit is not checked.
func TestContractShortSubject(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		length int
		pass   bool
		msg    string
	}{
		{0, true, "72 characters or less"},
		{72, true, "72 characters or less"},
		{73, true, "more than 72"},
		{89, true, "more than 72"},
		{90, false, "exceeds 90"},
		{200, false, "exceeds 90"},
	} {
		r := shortsubject.ValidateShortSubject(shortsubject.ShortSubjectRule, entry(strings.Repeat("s", c.length), "", parentA))
		if r.Pass != c.pass || !strings.Contains(r.Msg, c.msg) {
			t.Errorf("%d characters: pass %v %q, want %v with %q", c.length, r.Pass, r.Msg, c.pass, c.msg)
		}
	}

	long := strings.Repeat("s", 200)
	if r := shortsubject.ValidateShortSubject(shortsubject.ShortSubjectRule, entry(long, "", parentA, parentB)); !r.Pass {
		t.Errorf("a merge commit with a long subject: %q, want a pass", r.Msg)
	}
}

// TestContractMessageRegexp: off by default; given a value, a commit passes
// when any line of its subject or body matches; a blank value is a pass
// that says so; a merge commit is not checked.
func TestContractMessageRegexp(t *testing.T) {
	t.Parallel()

	rule := messageregexp.RegexpRule
	rule.Value = `^JIRA-[0-9]+`

	for _, c := range []struct {
		name          string
		subject, body string
		pass          bool
	}{
		{"subject matches", "JIRA-12 fix", "", true},
		{"body line matches", "fix", "details\nJIRA-7 closes it", true},
		{"nothing matches", "fix", "details", false},
		{"match not at a line start", "see JIRA-12", "", false},
	} {
		r := messageregexp.ValidateMessageRegexp(rule, entry(c.subject, c.body, parentA))
		if r.Pass != c.pass {
			t.Errorf("%s: pass %v (%q), want %v", c.name, r.Pass, r.Msg, c.pass)
		}
	}

	if r := messageregexp.ValidateMessageRegexp(rule, entry("fix", "", parentA, parentB)); !r.Pass {
		t.Errorf("a merge commit: %q, want a pass", r.Msg)
	}

	blank := messageregexp.RegexpRule
	if r := messageregexp.ValidateMessageRegexp(blank, entry("fix", "", parentA)); !r.Pass || !strings.Contains(r.Msg, "noop") {
		t.Errorf("a blank value: pass %v %q, want a noop pass", r.Pass, r.Msg)
	}

	if _, err := regexp.Compile(rule.Value); err != nil {
		t.Fatal(err)
	}
}

// TestContractResultsCount: a commit's results are one per rule in the
// rules' order, and PassFail counts them.
func TestContractResultsCount(t *testing.T) {
	t.Parallel()

	rules := validate.FilterRules(validate.RegisteredRules, validate.SanitizeFilters("DCO,short-subject"))

	results := validate.Commit(entry(strings.Repeat("s", 95), "", parentA), rules)
	if len(results) != 2 {
		t.Fatalf("%d results for two rules", len(results))
	}

	if pass, fail := results.PassFail(); pass != 0 || fail != 2 {
		t.Errorf("pass %d fail %d, want 0 and 2", pass, fail)
	}

	mixed := validate.Commit(entry("short", "Signed-off-by: A <a@example.com>", parentA), rules)
	if pass, fail := mixed.PassFail(); pass != 2 || fail != 0 {
		t.Errorf("pass %d fail %d for two passing rules, want 2 and 0", pass, fail)
	}

	half := validate.Commit(entry("short", "", parentA), rules)
	if pass, fail := half.PassFail(); pass != 1 || fail != 1 {
		t.Errorf("pass %d fail %d for one of two rules failing, want 1 and 1", pass, fail)
	}
}
