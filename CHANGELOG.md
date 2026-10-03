# Changelog

All notable changes to this project are recorded here. Versions follow
[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/); the
rules that map commits to versions are in [CONTRIBUTING.md](CONTRIBUTING.md).

<!--
Release sections below this line are inserted by git-cliff from cliff.toml on
every release. Edit the header above by hand; edit nothing below the marker.
-->

<!-- git-cliff: end of header -->
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

