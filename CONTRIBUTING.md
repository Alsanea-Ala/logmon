# Contributing to Logmon

## Scope

Logmon is a deliberately small log collector. Before adding anything, read
[Limitations](README.md#limitations) — a large share of "missing features" are
known, bounded trade-offs rather than oversights, and several are intentional.

The current target is fewer than 50 agents on a trusted LAN or VPN.

## Prerequisites

- Go `1.25.7`, matching `go.mod`
- `golangci-lint` on `PATH`
- Lefthook for the git hooks
- `journalctl` and the Docker CLI only when manually exercising those collectors

## Build and verify

```bash
make fmt      # gofmt; may modify and stage files
make vet
make lint
go test ./...
go test -race ./...
make build    # bin/agent and bin/server
```

Run a single package or test:

```bash
go test ./internal/server
go test ./internal/server -run '^TestAuthenticatedRecordIsStoredBeforeAck$'
```

Install the hook once after cloning:

```bash
lefthook install
```

Without it nothing gates a commit. `.git/hooks` is not in the repository, so a
fresh clone has no hook and `lefthook.yml` is only a description of one that
does not exist. Lefthook then runs fmt, vet, lint, and tests serially on
commit, and refuses the commit if any of them fails.

Bare `make` prints the available targets and installs nothing.

`make lint` runs under the Go version in `go.mod`, pinned by the hook. Running
it by hand on a newer toolchain reports a panic from inside the linter rather
than a finding, because `GOTOOLCHAIN=auto` treats `go.mod` as a minimum and
prefers your newer local Go. Use `GOTOOLCHAIN=go1.25.7 make lint`, or read the
pin in `lefthook.yml`.

## Design constraints

These are deliberate. Please read before proposing architecture changes.

- **No collector interface layer.** Sources are a concrete `switch` on
  `source.Type` in `internal/agent/agent.go`. Add an interface only when a
  second concrete implementation exists that needs it.
- **No database, repository, or service layer.** Storage is append-only JSONL
  written through one function.
- **Storage is opened per record under a single lock.** This is sized for the
  sub-50-agent target. The code says so at the lock and at the `OpenFile` call.
  If you profile and find contention, replacing both is a legitimate change;
  doing it speculatively is not.
- **Bubbles widgets are not used.** `internal/server/tui.go` is plain Bubble Tea
  on purpose. Adding a widget dependency is a design change, not a convenience.
- **Config is strict.** YAML decoding uses `KnownFields(true)`, so unknown keys
  fail startup. Keep it that way; silently ignored typos are worse than errors.

### Runtime shape

- One goroutine per configured source produces into a single buffered channel.
- One sender goroutine consumes in order.
- Each agent holds exactly one persistent TCP connection.
- One record is in flight per agent, awaiting its ACK before the next is sent.

That last point bounds throughput at one round trip per line. It is the main
known scaling limit, not an oversight.

## Commit messages

This project follows [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/).

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

Allowed types: `feat`, `fix`, `perf`, `refactor`, `docs`, `style`, `test`,
`build`, `ci`, `chore`, `revert`.

| Form | Use | Release |
|---|---|---|
| `feat(server): ingest and TUI` | New behaviour | minor |
| `fix(tui): render header on first frame` | Bug fix | patch |
| `perf(agent): batch records per ACK` | Performance change, no behaviour change | patch |
| `refactor: extract token hashing` | Internal restructuring | none |
| `docs: rewrite README` | Documentation | none |
| `test: cover rotation detection` | Tests only | none |
| `build: pin toolchain in go.mod` | Build system, deps, toolchain | none |
| `ci: add race job` | Pipelines and automation | none |
| `chore: project tooling` | Maintenance with no source effect | none |

Scope is optional and goes in parentheses: `feat(agent): ...`.

A breaking change is marked either with `!` before the colon, or with a footer:

```
feat(agent)!: require an explicit hostname

BREAKING CHANGE: agent configs without hostname no longer start.
```

The Release column reflects what GoReleaser puts in the changelog. Per the
specification, changes that do not affect the public API — `docs`, `style`,
`test` — may be excluded, so they do not by themselves produce a release. That
is why a run of `docs:` and `chore:` commits yields no version: nobody tags,
nothing publishes.

Tag-driven, so the version is a human decision:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

The subject format is a convention, not something the toolchain checks. No
commit-msg hook runs, locally or in CI, so a malformed subject will be accepted.
Read it back before pushing.

Earlier commits in this history use `ref:`, which is not a specification type.
New refactors must use `refactor:`. Nothing rejects either form — the subject
format is a convention, not something the toolchain checks.

## Releases

Versioning is automated on push to `main`. A workflow reads the commits since
the previous `chore: vX.Y.Z` commit and applies these rules, in order:

| Commits in the range | Result |
|---|---|
| only `docs`, `ref`, `ci`, `chore`, `build` | no release, nothing published |
| `fix` or `perf` | patch |
| `feat` | minor |
| `!` in the subject, or a `BREAKING CHANGE:` footer | major |

Major wins over minor and patch. A range with no release-worthy commit exits
quietly — that is why a run of `docs:` and `chore:` merges produces no version
and no GitHub Release. "Quietly" is load-bearing: the version step publishes no
`version` output, and every step after it is gated on
`steps.bump.outputs.version != ''`, because exiting that one step does not stop
the ones after it.

### The `ci` scope is the release switch

A change to the pipeline has two possible subjects, and which one you pick
decides whether it ships:

| Subject | Bump | Use for |
|---|---|---|
| `ci: ...` | none | pipeline changes that make an *existing* pending release work |
| `fix(ci): ...` | patch | a pipeline change you want published as a build |

`fix(ci):` matches the `fix` rule on its type, not on its scope, so it cuts a
patch release like any other fix. `ci:` matches nothing and the job skips.

The distinction is worth being deliberate about, because it is the only
mechanism that turns a pipeline change into a release. Typing `fix(ci):` on a
commit that is merely unblocking something already pending ships a build whose
only content is the fix that unblocked it.

Note that the scope does not affect the changelog filters on its own: both
`.goreleaser.yaml` and `cliff.toml` match on the type, so `fix(ci):` reaches
release notes unless a `^fix\(ci\):` filter is added to both. Keep those two
lists identical when you do.

The rules live in a shell script inside `release-tag.yml` and are covered by
`internal/ci`, which extracts the script from the workflow and runs it against
crafted git histories. Three bugs shipped there in a row, all silent — the job
went green, or failed somewhere other than the cause. Run `go test ./internal/ci`
after editing that script.

The first release is `v0.1.0` regardless of history. The rules apply from the
second release onward, because applying them to an untagged project would
derive `v0.2.0` from a `v0.1.0` base that never existed.

Breaking changes are detected by looking for `BREAKING CHANGE:` as the last
line of a commit message's final paragraph, not as a substring anywhere in it.
That distinction is load-bearing: commit `f2e9360` quotes the phrase inside a
fenced code block as a documentation example, and a substring match reads it
as a breaking change and cuts a major release on the first run.

Nobody needs to tag manually, but it still works:

```bash
git tag v1.2.3 && git push origin v1.2.3
```

That publishes through the same pipeline, which is the escape hatch for
cutting a release that the commit history would not have produced.

The release commit changes only `CHANGELOG.md`. `CHANGELOG.md` is generated by
[git-cliff](https://git-cliff.org) from `cliff.toml`; do not edit it by hand,
because the next release overwrites it.

Releasing pushes to `main` directly, so the job needs a repository secret that
this repository cannot create for itself: **`ACTIONS_PUSH_TOKEN`**, a
fine-grained personal access token with *Contents: read and write* on this
repo. Store it under **Settings → Secrets and variables → Actions**.

`main` requires a pull request, and `GITHUB_TOKEN` cannot push past that. The
bypass route was tried and does not work for a workflow: `github-actions[bot]`
is not selectable in a ruleset bypass list, and classic branch protection has no
actor picker for an app. An admin account *does* bypass, so the push uses that
account's token instead. Renew it when it expires or releases stop.

Because a personal token is not `GITHUB_TOKEN`, GitHub does not suppress
workflow runs for the refs it creates. Both `ci.yml` and `publish.yml` now
trigger normally off a release, so `release-tag.yml` no longer dispatches
`publish` by hand — doing both would start two goreleaser runs against the same
tag.

The release commit changes only `CHANGELOG.md`, which is why letting it through
without review is reasonable.

### If a release fails partway

The tag is pushed only after the commit lands, in a separate `git push`. If the
job still stops between the two, a tag can exist on a commit that never reached
`main`, and `set -e` will have stopped the tag push — so the commit is missing
from `main` and no GitHub Release exists.

Do not re-run the failed job to fix this. It checks out the pre-release commit,
finds no `chore: vX.Y.Z` boundary in history, sees the existing tag, and
computes the next version from the whole history instead — which cuts a higher
version than the one that failed. Recover by hand:

```bash
git fetch origin 'refs/tags/vX.Y.Z:refs/tags/vX.Y.Z'
git push origin 'vX.Y.Z^{}:main'    # fast-forward, never --force
git push origin 'refs/tags/vX.Y.Z'  # only if the tag is missing remotely
```

The second push starts `publish.yml` off the tag. The fast-forward matters
beyond tidiness: it puts the `chore: vX.Y.Z` boundary commit on `main`, so the
next run classifies that range as chore-only and cuts nothing. Without it the
next run finds no boundary and derives a spurious
version.

`go install github.com/Alsanea-Ala/logmon/cmd/server@latest` resolves once a
`v*` tag exists; before the first release it has nothing to resolve against.

## Branches and pull requests

- Work on a topic branch, not `main`.
- **Squash merge.** The pull request title becomes the commit subject, so write
  it in the Conventional Commits form above — the release rules read it
  directly. Set the repository's default squash message to "Pull request title
  only"; otherwise the body inherits every commit subject from the branch.
- Do not use merge commits. `Merge pull request #N from ...` matches no
  conventional type, so it lands in the changelog as noise.
- Keep pull requests scoped. The initial import was large; a review covering
  agent, protocol, and server at once is much harder than three smaller ones.
- If a change touches `internal/server/tui.go`, include the terminal-size
  assertions in the same PR. Frame sizing is covered by
  `TestViewFitsTerminal` and `TestResizeKeepsFrameValid`, and layout
  regressions are the most common defect in that file.

## Reporting bugs

Open an issue with: what you expected, what happened, agent and server versions,
and the relevant config with tokens redacted. See
[Troubleshooting](README.md#troubleshooting) first — silent-failure symptoms
are listed there.

## Security issues

Do not open a public issue. See [SECURITY.md](SECURITY.md).

## License

Contributions are accepted under the GNU Affero General Public License v3. See
[LICENSE](LICENSE).