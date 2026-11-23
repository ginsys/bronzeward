# Changelog fragments

A change adds one file here instead of editing `CHANGELOG.md`, so two pull requests never edit the
same lines. A release turns the files into one section of `CHANGELOG.md` and deletes them.

- **Name:** `<issue>-<slug>.md`, for example `587-dsn-error-text.md`. `<issue>` is the issue number,
  or the pull request number if there is no issue. `<slug>` is lowercase letters, digits and dashes.
- **Line 1:** the section: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed` or `Security`.
- **The rest:** exactly one bullet. It starts with `- `, its continuation lines are indented two
  spaces, and it ends with `Fixes owner/repo#N.` or `Refs owner/repo#N.`

```
Fixed
- A malformed database DSN no longer reaches the error text. Fixes owner/repo#587.
```

A change that needs no entry (CI, tests, documentation only) carries the label `no-changelog`
instead; the `Changelog fragment` check then passes without a file. The release workflow and the
full rules are in
[ginsys/.github, docs/release.md](https://github.com/ginsys/.github/blob/main/docs/release.md).
