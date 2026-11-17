# Upstream

Fork of https://github.com/vbatts/git-validation (`main`).

- Forked from: [`a8d4555`](https://github.com/vbatts/git-validation/commit/a8d455533459b620fa656bad095b943e70cede9b), committed 2025-04-14
- Reviewed through: [`a8d4555`](https://github.com/vbatts/git-validation/commit/a8d455533459b620fa656bad095b943e70cede9b), reviewed 2026-11-17

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
| `-D` records each git command run and the commit a rangeless run starts from, as debug records through `log/slog` | https://github.com/forkcloser/git-validation/pull/20 |
| A `--run` name no rule has is refused, naming it and listing the rules there are, where it ran nothing and passed; a `message_regexp` value that does not compile is refused before any commit is read, with the regexp package's words, where it panicked on the first commit; the final count is of commits with a failing result, not of results; "using commit range" is logged at debug | https://github.com/forkcloser/git-validation/pull/24 |
| The short-subject rule counts characters, not bytes, against its limits; a `--range` git cannot resolve is an error naming the range and the failed git command in a repository with its whole history, nothing validated | https://github.com/forkcloser/git-validation/pull/39 |
| A `--range` git cannot resolve is an error in a shallow clone too, where it used to validate what the clone had from its oldest commit, so a depth of one passed the head commit alone; the error names the end the clone does not have and what to fetch (`fetch-depth: 0`, `git fetch --deepen` or `--unshallow`), and a shallow clone deep enough for the range runs as before | https://github.com/forkcloser/git-validation/pull/47 |
| `QUIET` in the environment no longer quiets the command; `-q` does | https://github.com/forkcloser/git-validation/pull/81 |
| Errors from git and from the file system say what was being done when they happened | https://github.com/forkcloser/git-validation/pull/83 |
| A run can be stopped: on an interrupt the git that is running is killed, no further commit is read, and the error says the run was stopped, where nothing could stop a git that hangs | https://github.com/forkcloser/git-validation/pull/86 |
| A range is read with one `git log`: two git processes for the range, in place of twenty more a commit | https://github.com/forkcloser/git-validation/pull/91 |
| The dangling-whitespace rule checks each commit by itself (`git log --check X^!`, with `log.showRoot` on), where upstream checks the range `X^..X`: a merge is no longer failed for the whitespace errors of the commits it brings in, which `git show --check` of the merge does not show, and a first commit, which that range cannot name and upstream skipped, is checked | https://github.com/forkcloser/git-validation/pull/92 |
| In a shallow clone, the commit the clone was cut at has no parent to compare with and cannot have its whitespace checked. Upstream takes it for a first commit, skips its check and passes it; here its whitespace result is neither a pass nor a failure: `SKIP` under `-v`, a warning on standard error at every verbosity, and the commit's line and the exit status are those of the rules that ran | https://github.com/forkcloser/git-validation/pull/94 |
