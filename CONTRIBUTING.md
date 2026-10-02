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

Lefthook runs fmt, vet, lint, and tests serially on commit. Bare `make` prints
the available targets and installs nothing.

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
and no GitHub Release.

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