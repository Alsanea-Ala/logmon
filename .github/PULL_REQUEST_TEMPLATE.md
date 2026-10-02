## What

<!-- What this changes and why. Link any related issue. -->

## Why

<!--
What breaks without it. If this closes a documented gap, say which one.
-->

## Scope check

- [ ] No new collector interface, repository, or service layer (see [CONTRIBUTING.md](CONTRIBUTING.md))
- [ ] No speculative optimization of storage locking or per-record file handles
- [ ] Existing storage layout and wire protocol remain backward compatible, or the change is called out explicitly

## Verification

- [ ] `make fmt && make vet && make lint && go test ./...`
- [ ] `go test -race ./...`
- [ ] Verified against a clean clone, if this touches config, install, or deployment docs

## If this touches `internal/server/tui.go`

- [ ] Terminal-size assertions included (`TestViewFitsTerminal`, `TestResizeKeepsFrameValid`)
- [ ] Checked at narrow width, where the status bar drops hints rather than wrapping

## Checklist

- [ ] No tokens, token hashes, or private hostnames in the diff
- [ ] Limitations section updated if behaviour changed