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

`lefthook.yml` enforces the subject format on commit-msg.

Note: earlier commits in this history use `ref:`, which is not a specification
type. Those commits are already published and cannot be rewritten without
rewriting `master`, so `ref:` is accepted for existing history only. New
refactors must use `refactor:`.

## Releases

GoReleaser builds and publishes on a tag push; see `.goreleaser.yaml` and
`.github/workflows/release.yml`. No CI job creates or pushes a tag, which
keeps a reviewed human between choosing a version and publishing binaries.
`go install .../cmd/server@latest` resolves once a `v*` tag exists.

## Branches and pull requests

- Work on a topic branch, not `master`.
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