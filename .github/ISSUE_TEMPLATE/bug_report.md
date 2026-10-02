---
name: Bug report
about: Something behaves differently from the documented behaviour
title: ''
labels: bug
assignees: ''
---

## What happened

<!-- What you observed. -->

## What you expected

<!-- What the README or your config implies should happen. -->

## Reproduction

<!--
Smallest config and command that shows it.
Redact tokens and token_sha256 values before pasting.
-->

Agent YAML (redacted):

```yaml
```

Server YAML (redacted):

```yaml
```

Commands run:

```bash
```

## Versions

- Logmon version or commit:
- `go version`:
- Distribution and release:
- Kernel (`uname -a`):

## Relevant source type

<!-- file, journald, or docker. If journald, which unit and which groups the agent user is in. If docker, whether the agent has socket access. -->

## Logs or output

<!--
The agent prints a startup banner and config summary, then produces no
further output. Paste that startup block plus whatever you observed: TUI
output, server-side behaviour, or journal entries from the agent process
if it runs under systemd.
-->

## Checklist

- [ ] I searched existing issues
- [ ] I removed tokens and hashes from anything pasted above
- [ ] I read the [Limitations](https://github.com/Alsanea-Ala/logmon#limitations) section and this is not a known limit