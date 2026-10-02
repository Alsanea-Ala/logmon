# Security Policy

## Reporting a vulnerability

Please do not open a public issue for a security problem.

Report privately via GitHub's **Security** tab on this repository
(**Security → Report a vulnerability**). Include:

- what the issue is and where in the code
- how to reproduce it
- the impact you assessed

Expect an acknowledgement within a few days. Fixes for confirmed issues are
released as a new version; there is no separate security-fix branch.

## Threat model

Logmon is designed for a **trusted private LAN or VPN**. Read this section
before deploying it anywhere else.

### Transport is plaintext

Agents connect over raw TCP with no TLS. The agent token is sent in cleartext
inside the handshake frame, and record contents are unencrypted in transit.
Anyone able to observe traffic between an agent and the server can read log
data and capture a token that is immediately reusable.

Mitigations, all external to Logmon:

- Run over a VPN (WireGuard, Tailscale, IPsec) or on a physically isolated
  segment.
- Firewall the server port to known agent addresses only.
- Treat the token as a credential that must travel over encrypted links.

There is no TLS support and no certificate handling. This is the single most
important limitation of the project.

### Authentication

- Agents authenticate with a bearer token. The server stores only the SHA-256
  digest, in `server.yaml`; the plaintext token lives on the agent.
- The comparison uses `crypto/subtle.ConstantTimeCompare` and is not
  timing-leakable.
- Use a distinct token per agent so one compromised host does not yield every
  agent's identity. Token rotation means editing the allowlist and restarting
  the server; there is no reload.
- Tokens must be at least 32 characters. Generate 32 random bytes with
  `openssl rand -hex 32`.

### Integrity

Records carry no sequence number, MAC, or signature. An attacker on the network
path can alter log text in flight, and the server will timestamp and store the
altered line as authentic. This means Logmon records what an agent *sent*, not
what the application *wrote*. Do not treat it as a tamper-evident audit log.

### Local access

- Anyone who can read the agent config or `LOGMON_AGENT_TOKEN` can impersonate
  that agent. Keep both mode `0600`, owned by the agent user.
- Anyone who can write to `data_dir` can forge stored records offline. The
  server creates files `0640` and directories `0750`.
- Server agent presence is in-memory only and resets on restart, so it is not
  an access-control record.

### Privileges

- The journald collector requires read access to the system journal. Group
  membership varies by distribution; see the table in the README.
- The Docker collector requires Docker socket access, which is effectively
  root. Prefer Docker's journald logging driver so the agent can drop that
  access entirely.

## Out of scope

- Plaintext transport (documented above; a design constraint, not a bug)
- Lack of TLS
- Agent-side privilege escalation via Docker socket access
- Anything requiring local root on the agent or server host