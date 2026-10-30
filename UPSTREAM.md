# Upstream

Fork of https://github.com/vbatts/git-validation (`main`).

- Forked from: [`a8d4555`](https://github.com/vbatts/git-validation/commit/a8d455533459b620fa656bad095b943e70cede9b), committed 2025-04-14
- Reviewed through: [`a8d4555`](https://github.com/vbatts/git-validation/commit/a8d455533459b620fa656bad095b943e70cede9b), reviewed 2026-10-15

## Incorporated

| Upstream commit | Here | What |
|---|---|---|

## Not incorporated

| Upstream commit | Why |
|---|---|

## Changes upstream does not have

| Change | Here |
| --- | --- |
| Installed from `github.com/forkcloser/git-validation/cmd/git-validation`, and released as signed binaries | https://github.com/forkcloser/git-validation/pull/7 |
| On Windows, `GIT_CHECK_EXCLUDE` works (git's version was compared by a parser that refused `2.42.0.windows.2`); hashicorp/go-version leaves the dependencies | https://github.com/forkcloser/git-validation/pull/8 |
| Logs through `log/slog` (colored on a terminal, JSON otherwise) instead of logrus; errors name the git command that failed | https://github.com/forkcloser/git-validation/pull/12 |
| Flags parsed by kong: long flags take two dashes (`--run`, `--range`; `-run` is refused), `-v`/`-D`/`-q`/`-d` gain long forms, `--no-travis-pr-only` | https://github.com/forkcloser/git-validation/pull/16 |
| `-D` records each git command run, the skipped check of an initial commit and the commit a rangeless run starts from, as debug records through `log/slog` | https://github.com/forkcloser/git-validation/pull/20 |
| A `--run` name no rule has is refused, naming it and listing the rules there are, where it ran nothing and passed; a `message_regexp` value that does not compile is refused before any commit is read, with the regexp package's words, where it panicked on the first commit; the final count is of commits with a failing result, not of results; "using commit range" is logged at debug | https://github.com/forkcloser/git-validation/pull/24 |
| The short-subject rule counts characters, not bytes, against its limits; a `--range` git cannot resolve is an error naming the range and the failed git command in a repository with its whole history, nothing validated | https://github.com/forkcloser/git-validation/pull/39 |
| A `--range` git cannot resolve is an error in a shallow clone too, where it used to validate what the clone had from its oldest commit, so a depth of one passed the head commit alone; the error names the end the clone does not have and what to fetch (`fetch-depth: 0`, `git fetch --deepen` or `--unshallow`), and a shallow clone deep enough for the range runs as before | https://github.com/forkcloser/git-validation/pull/47 |
