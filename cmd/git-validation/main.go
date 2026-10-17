package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/mycophonic/primordium/app/logger"

	_ "github.com/forkcloser/git-validation/rules/danglingwhitespace"
	_ "github.com/forkcloser/git-validation/rules/dco"
	_ "github.com/forkcloser/git-validation/rules/messageregexp"
	_ "github.com/forkcloser/git-validation/rules/shortsubject"
	"github.com/forkcloser/git-validation/validate"
)

var (
	flCommitRange = flag.String("range", "", "use this commit range instead (implies -no-travis)")
	flListRules   = flag.Bool("list-rules", false, "list the rules registered")
	flRun         = flag.String("run", "", "comma delimited list of rules to run. Defaults to all.")
	flVerbose     = flag.Bool("v", false, "verbose")
	flDebug       = flag.Bool("D", false, "debug output")
	flQuiet       = flag.Bool("q", false, "less output")
	flDir         = flag.String("d", ".", "git directory to validate from")
	flNoGithub    = flag.Bool(
		"no-github",
		false,
		"disables Github Actions environment checks (when env GITHUB_ACTIONS=true is set)",
	)
	flNoTravis     = flag.Bool("no-travis", false, "disables travis environment checks (when env TRAVIS=true is set)")
	flTravisPROnly = flag.Bool(
		"travis-pr-only",
		true,
		"when on travis, only run validations if the CI-Build is checking pull-request build",
	)
)

func main() {
	flag.Parse()

	configureLogger(*flDebug, *flQuiet)

	if *flQuiet {
		if err := os.Setenv("QUIET", "1"); err != nil {
			exitOn("quiet output", err)
		}
	}

	if *flListRules {
		for _, r := range validate.RegisteredRules {
			fmt.Printf("%q -- %s\n", r.Name, r.Description)
		}

		return
	}

	if *flTravisPROnly && strings.ToLower(os.Getenv("TRAVIS_PULL_REQUEST")) == "false" {
		fmt.Printf("only to check travis PR builds and this not a PR build. yielding.\n")
		return
	}

	// rules to be used
	var rules []validate.Rule

	for _, r := range validate.RegisteredRules {
		// only those that are Default
		if r.Default {
			rules = append(rules, r)
		}
	}
	// or reduce the set being run to what the user provided
	if *flRun != "" {
		rules = validate.FilterRules(validate.RegisteredRules, validate.SanitizeFilters(*flRun))
	}

	slog.Debug("rules to run", "rules", ruleNames(rules))

	commitRange := *flCommitRange
	if commitRange == "" {
		if strings.ToLower(os.Getenv("TRAVIS")) == "true" && !*flNoTravis {
			if os.Getenv("TRAVIS_COMMIT_RANGE") != "" {
				commitRange = strings.Replace(os.Getenv("TRAVIS_COMMIT_RANGE"), "...", "..", 1)
			} else if os.Getenv("TRAVIS_COMMIT") != "" {
				commitRange = os.Getenv("TRAVIS_COMMIT")
			}
		}
		// https://docs.github.com/en/actions/reference/environment-variables
		if strings.ToLower(os.Getenv("GITHUB_ACTIONS")) == "true" && !*flNoGithub {
			commitRange = fmt.Sprintf("%s..%s", os.Getenv("GITHUB_SHA"), "HEAD")
		}
	}

	slog.Info("using commit range", "range", commitRange)

	runner, err := validate.NewRunner(*flDir, rules, commitRange, *flVerbose)
	if err != nil {
		exitOn("preparing the run", err)
	}

	if err := runner.Run(); err != nil {
		exitOn("validating commits", err)
	}

	_, fail := runner.Results.PassFail()
	if fail > 0 {
		fmt.Printf("%d commits to fix\n", fail)
		os.Exit(1)
	}
}

// configureLogger installs the process-wide slog handler through primordium:
// tint on a terminal, JSON when stderr is redirected. -D shows debug records,
// -q only warnings and errors.
func configureLogger(debug, quiet bool) {
	level := slog.LevelInfo

	switch {
	case debug:
		level = slog.LevelDebug
	case quiet:
		level = slog.LevelWarn
	}

	logger.SetDefaultsForLogger(context.Background(), level)
}

// exitOn logs err with the step that failed and exits 1.
func exitOn(step string, err error) {
	slog.Error("git-validation failed", "step", step, "err", err)
	os.Exit(1)
}

func ruleNames(rules []validate.Rule) []string {
	names := make([]string, 0, len(rules))
	for _, rule := range rules {
		names = append(names, rule.Name)
	}

	return names
}
