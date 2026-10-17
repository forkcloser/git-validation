package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/mycophonic/primordium/app/logger"
	"github.com/mycophonic/primordium/app/shutdown"

	_ "github.com/forkcloser/git-validation/rules/danglingwhitespace"
	_ "github.com/forkcloser/git-validation/rules/dco"
	_ "github.com/forkcloser/git-validation/rules/messageregexp"
	_ "github.com/forkcloser/git-validation/rules/shortsubject"
	"github.com/forkcloser/git-validation/validate"
)

// cli is the command line. The flag names are upstream's; kong spells them
// with two dashes, and keeps the one-letter ones as short flags.
type cli struct {
	Range        string `help:"use this commit range instead (implies --no-travis)"`
	ListRules    bool   `help:"list the rules registered"`
	Run          string `help:"comma delimited list of rules to run. Defaults to all."`
	Verbose      bool   `help:"verbose"                                                                          short:"v"`
	Debug        bool   `help:"debug output"                                                                     short:"D"`
	Quiet        bool   `help:"less output"                                                                      short:"q"`
	Dir          string `default:"."                                                                             help:"git directory to validate from"                                                      short:"d"`
	NoGithub     bool   `help:"disables Github Actions environment checks (when env GITHUB_ACTIONS=true is set)" name:"no-github"`
	NoTravis     bool   `help:"disables travis environment checks (when env TRAVIS=true is set)"`
	TravisPROnly bool   `default:"true"                                                                          help:"when on travis, only run validations if the CI-Build is checking pull-request build" name:"travis-pr-only" negatable:""`
}

// errCommitsToFix is a run that found failing commits; they are printed
// already, so the exit carries no message.
var errCommitsToFix = shutdown.ExitError(1)

func main() {
	var flags cli

	// Parse and usage errors are kong's, printed with the usage; every failure
	// exits 1, as the flag package did, and --help exits 0.
	kong.Parse(&flags,
		kong.Name("git-validation"),
		kong.Description("A way to do validation on git commits."),
		kong.UsageOnError(),
		kong.Exit(func(code int) {
			if code != 0 {
				code = 1
			}

			os.Exit(code)
		}),
	)

	configureLogger(flags.Debug, flags.Quiet)

	// shutdown runs the command and exits with the status an ExitError asks
	// for, silently, and 1 for any other error, which it logs.
	shutdown.Run(shutdown.SetDefaults(context.Background()), func(context.Context) error {
		return run(&flags)
	})
}

func run(flags *cli) error {
	if flags.Quiet {
		if err := os.Setenv("QUIET", "1"); err != nil {
			return fmt.Errorf("quiet output: %w", err)
		}
	}

	if flags.ListRules {
		for _, r := range validate.RegisteredRules {
			fmt.Printf("%q -- %s\n", r.Name, r.Description)
		}

		return nil
	}

	if flags.TravisPROnly && strings.ToLower(os.Getenv("TRAVIS_PULL_REQUEST")) == "false" {
		fmt.Println("only to check travis PR builds and this not a PR build. yielding.")
		return nil
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
	if flags.Run != "" {
		rules = validate.FilterRules(validate.RegisteredRules, validate.SanitizeFilters(flags.Run))
	}

	slog.Debug("rules to run", "rules", ruleNames(rules))

	commitRange := flags.Range
	if commitRange == "" {
		if strings.ToLower(os.Getenv("TRAVIS")) == "true" && !flags.NoTravis {
			if os.Getenv("TRAVIS_COMMIT_RANGE") != "" {
				commitRange = strings.Replace(os.Getenv("TRAVIS_COMMIT_RANGE"), "...", "..", 1)
			} else if os.Getenv("TRAVIS_COMMIT") != "" {
				commitRange = os.Getenv("TRAVIS_COMMIT")
			}
		}
		// https://docs.github.com/en/actions/reference/environment-variables
		if strings.ToLower(os.Getenv("GITHUB_ACTIONS")) == "true" && !flags.NoGithub {
			commitRange = fmt.Sprintf("%s..%s", os.Getenv("GITHUB_SHA"), "HEAD")
		}
	}

	slog.Info("using commit range", "range", commitRange)

	runner, err := validate.NewRunner(flags.Dir, rules, commitRange, flags.Verbose)
	if err != nil {
		return fmt.Errorf("preparing the run: %w", err)
	}

	if err := runner.Run(); err != nil {
		return fmt.Errorf("validating commits: %w", err)
	}

	_, fail := runner.Results.PassFail()
	if fail > 0 {
		fmt.Printf("%d commits to fix\n", fail)

		return errCommitsToFix
	}

	return nil
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

func ruleNames(rules []validate.Rule) []string {
	names := make([]string, 0, len(rules))
	for _, rule := range rules {
		names = append(names, rule.Name)
	}

	return names
}
