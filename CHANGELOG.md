# Changelog

All notable changes to this project are recorded here. Versions follow
[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/); the
rules that map commits to versions are in [CONTRIBUTING.md](CONTRIBUTING.md).

<!--
Release sections below this line are inserted by git-cliff from cliff.toml on
every release. Edit the header above by hand; edit nothing below the marker.
-->

<!-- git-cliff: end of header -->
## [0.2.0] - 2026-10-15

### Features

- *(server)* Titled panels, a palette, and a working help overlay
- *(server)* Split the sidebar into separate agents and files panels
- *(server)* Colour log lines by severity
- *(tui)* Ingest graph
- *(tui)* A text filter that filters, and line wrapping
- *(tui)* Last-seen and record count on the agents panel
- *(tui)* One key per meaning, and numbered panels
- *(tui)* A log time window on `t`
- *(tui)* A GUI config file
- *(tui)* Size the panels by content and give the focused one two thirds
- *(server)* Carry the source's stated severity on the wire
- *(tui)* Click and wheel, off by default

### Bug Fixes

- *(tui)* The selection row was illegible on a themed terminal
- *(ci)* Install the pre-commit hook and pin the lint toolchain
- *(release)* Nfpm filename template is file_name_template in v2
- *(release)* Extra_files never matched a raw binary
- *(ci)* Anchor the release range on the last tag, not the last bump commit

## [0.1.1] - 2026-10-15

### Bug Fixes

- *(ci)* Make release tag step idempotent on reruns
- *(ci)* Push releases with a personal token

## [0.1.0] - 2026-10-03

### Features

- *(agent)* Add yaml conf
- *(agent)* Cobra CLI, config rewrite and animated banner
- *(protocol)* Bounded NDJSON wire and stored record types
- *(server)* Ingest, headless mode and lazygit-style TUI
- *(tui)* Responsive breakpoint layout and header hardening
- *(agent)* Require an explicit hostname

### Bug Fixes

- *(tui)* Render header on first frame and tighten layout
- *(tui)* Size frames to the terminal and drop chrome backgrounds
- *(ci)* Trigger on main, the default branch
- *(ci)* Install golangci-lint in the pipeline
- *(tui)* Remove a dead width default and empty focus branches
- Correct the git-cliff filter name and the action inputs
- Use git-cliff's shipped template and prepend instead of output

