# Security & Operations Guide

This document describes the threat model assumed by DroidMCP, the
difference between **dev mode** and **production mode**, and the
configuration knobs each operator should set before exposing a server
beyond a single shell session.

If you only read one section: never expose a DroidMCP port outside
`localhost` without **both** a strong `DROIDMCP_API_KEY` *and* TLS
(`DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY`). The `termux` and
`filesystem` servers in particular give whoever can reach the port the
ability to run arbitrary commands and read/write files.

## Threat model

DroidMCP assumes the following adversary capabilities and limits:

- **In scope.** Any process able to reach the listening TCP port (a
  malicious app on the same device, a peer on the same Wi-Fi if the
  port is bound to a non-loopback address, a captured client token).
- **In scope.** A misconfigured `DROIDMCP_ROOT` that points to a
  sensitive subtree (private keys, `~/.ssh`, the whole rootfs).
- **In scope.** A leaked API key in logs or error messages.
- **Out of scope.** A local attacker who already has root or the
  Termux UID, or who can attach a debugger. DroidMCP cannot defend
  against an attacker with the same privileges as the server.
- **Out of scope.** Arbitrary command execution as a *feature* of the
  `termux` MCP. That server is intentionally powerful and should be
  treated as a remote shell.

Mitigations the codebase currently implements:

| Layer | Mitigation |
|-------|------------|
| Auth | Per-server / global API key checked with `crypto/subtle.ConstantTimeCompare`. |
| Transport | Optional TLS via `DROIDMCP_TLS_CERT` / `_KEY`; HSTS sent only when TLS is active. |
| Headers | `Cache-Control: no-store` and `X-Content-Type-Options: nosniff` on every response. |
| Logging | `slog`-based, with credential redaction in attribute keys (`api_key`, `token`, `password`, …). |
| `mcp-filesystem` | `securePath` rejects absolute paths and traversal; everything resolves under `DROIDMCP_ROOT`. |
| `mcp-scraper` | Anti-SSRF: rejects RFC1918 / loopback / link-local by default (override with `DROIDMCP_SCRAPER_ALLOW_PRIVATE=1`). |
| `mcp-network` | Refuses public targets by default (override with `DROIDMCP_NETWORK_ALLOW_PUBLIC=1`). |
| `mcp-termux` | Optional allowlist via `DROIDMCP_TERMUX_ALLOWLIST=cmd1,cmd2,…`; `install_pkg` quotes the package name (`pkg install -- <name>`). |
| `mcp-clipboard` | All inputs piped via stdin, never embedded in shell arguments. |

Known gaps that operators should keep in mind (tracked in `AUDIT_REPORT.txt`):

- `securePath` does not yet resolve symlinks (audit item 2.2). A
  symlink already present under `DROIDMCP_ROOT` can point outside it.
- The default `DROIDMCP_ROOT` is still `/` (audit item 2.3). Override
  it explicitly in production.
- No rate limit yet (audit 2.7). Pair the server with a reverse proxy
  if you need one.

## Authentication

Every server enforces the same scheme:

1. On startup the server resolves an API key via
   `config.ResolveAPIKey("<server-name>")` which checks, in order:
   - `DROIDMCP_<SERVER>_KEY` (e.g. `DROIDMCP_TERMUX_KEY`)
   - `DROIDMCP_API_KEY` (global fallback)
2. If both are unset, the server starts in **dev mode** and logs
   `auth=disabled`. Every request is accepted. Use this only on
   loopback for local development.
3. If a key is set, every inbound request must carry it in the
   `X-DroidMCP-Key` HTTP header. The comparison is constant-time.
4. `GET /healthz` is always served unauthenticated so external
   supervisors (systemd, k8s, docker healthchecks) can probe the
   server without holding the key.

Per-server keys override the global one, so you can give a different
client a different key per MCP:

```bash
# Global key used by everything except termux.
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"
# Stricter key only for the high-privilege shell server.
export DROIDMCP_TERMUX_KEY="$(openssl rand -base64 32)"
```

Clients pass the key as a header. Example with `curl`:

```bash
curl -H "X-DroidMCP-Key: $DROIDMCP_API_KEY" http://localhost:3000/sse
```

For Claude Code / Gemini CLI, set the header in the MCP server entry:

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "https://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<paste-the-same-value>" }
    }
  }
}
```

## TLS

Plain HTTP is fine on `localhost`. Anywhere else, terminate TLS at the
server:

```bash
export DROIDMCP_TLS_CERT=/path/to/cert.pem
export DROIDMCP_TLS_KEY=/path/to/key.pem
droidmcp-filesystem
```

When both env vars are present:

- `baseURL` advertised in the MCP handshake becomes `https://…`.
- `ListenAndServeTLS` is used instead of `ListenAndServe`.
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` is
  added to every response. (Sent only over TLS — advertising HSTS over
  plain HTTP would lock browsers out.)

You can self-sign with `openssl req -x509 …` for an internal device,
but for any public exposure use a real certificate.

## Logging

DroidMCP writes structured logs to **stderr** (stdout is reserved for
potential protocol traffic). Two env vars control behaviour:

| Variable | Values | Default |
|----------|--------|---------|
| `DROIDMCP_LOG_LEVEL` | `debug`, `info`, `warn`/`warning`, `error`/`err` | `info` |
| `DROIDMCP_LOG_FORMAT` | `json`, anything else falls back to `text` | `text` |

In production, prefer JSON so the logs are machine-parseable:

```bash
export DROIDMCP_LOG_LEVEL=info
export DROIDMCP_LOG_FORMAT=json
```

**Credential redaction.** Attribute keys whose names match
`token`, `secret`, `password`, `passwd`, `authorization`, `apikey`,
`api_key`, `api-key`, or the standalone word `key` are replaced with
`[REDACTED]` before they reach the sink. The redactor is intentionally
narrow to avoid mangling normal attributes like `auth=enabled` in the
startup banner — see `internal/logger/logger.go` for the full list.

The request logger never reads or logs the `X-DroidMCP-Key` header.

## Filesystem root

`mcp-filesystem` confines all paths to `DROIDMCP_ROOT`. The shipped
default is `/`, which is **insecure** — any client with the key (or
any caller at all in dev mode) can read or overwrite system files.

Always set an explicit root in production:

```bash
# On Android / Termux:
export DROIDMCP_ROOT=/storage/emulated/0/DroidMCP

# On a Linux box:
export DROIDMCP_ROOT=/srv/droidmcp/workspace
```

The directory must exist and be a directory; `LoadConfig` fail-fasts
with a descriptive error otherwise (`DROIDMCP_ROOT "<path>": not a
directory`). Symlinks that already live under `DROIDMCP_ROOT` and
point elsewhere are not yet rewritten (audit item 2.2), so avoid
mounting a root that contains untrusted symlinks.

## `mcp-clipboard` requirements

The clipboard server shells out to `termux-clipboard-get` and
`termux-clipboard-set`, both of which come from the
[`termux-api`](https://wiki.termux.com/wiki/Termux:API) package.
Without it, every clipboard request returns:

```
termux-api package not installed; run `pkg install termux-api` and
ensure the Termux:API app is installed on the device
```

To make the server usable:

1. Install the `Termux:API` Android app (F-Droid or Play Store) — the
   same source as Termux itself.
2. In the Termux shell:

   ```bash
   pkg install termux-api
   ```

3. Grant the Termux:API app any permissions it requests on first run
   (clipboard access on newer Android versions).

The clipboard server also stores a local history in memory (no disk
persistence). Two env vars cap it:

| Variable | Meaning | Default |
|----------|---------|---------|
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` | Max entries kept | server-defined |
| `DROIDMCP_CLIPBOARD_HISTORY_BYTES`   | Max total bytes  | server-defined |

Older entries are evicted FIFO when either cap is reached.

## `mcp-termux` allowlist

`mcp-termux` exposes `run_command`, which is effectively a remote
shell. Two safeguards exist:

- `install_pkg` quotes the package name (`pkg install -- <name>`) so
  flags injected via the package field cannot reach `pkg`.
- `DROIDMCP_TERMUX_ALLOWLIST` restricts which top-level commands are
  callable through `run_command`:

  ```bash
  export DROIDMCP_TERMUX_ALLOWLIST="ls,cat,grep,git,go"
  ```

  When unset, every command is allowed. The wrappers (`termux-battery-status`,
  `termux-location`, `termux-notification`, `termux-toast`,
  `termux-sms-send`, `termux-tts-speak`) bypass the allowlist on
  purpose — those are explicit tools and the operator opted in by
  starting the server.

If you do not need shell access, do not start `droidmcp-termux`.

## Scraper and network defaults

`mcp-scraper` rejects RFC1918 / link-local / loopback URLs by default
to prevent SSRF (audit 2.1). Override on a hardened, isolated host
only:

```bash
export DROIDMCP_SCRAPER_ALLOW_PRIVATE=1
```

`mcp-network` refuses non-RFC1918 targets by default to prevent
turning the device into a port scanner against the public internet
(audit 2.10). Same override pattern:

```bash
export DROIDMCP_NETWORK_ALLOW_PUBLIC=1
```

Both knobs accept `1`, `true`, `yes`, `on` (case-insensitive).

## Production checklist

Before exposing any DroidMCP server beyond `localhost`:

- [ ] `DROIDMCP_API_KEY` set to a random ≥32-byte secret (or a
      per-server key for every server you start).
- [ ] `DROIDMCP_TLS_CERT` / `_KEY` configured if the listener is
      reachable from anything but loopback.
- [ ] `DROIDMCP_ROOT` set to a dedicated directory — never `/`.
- [ ] `DROIDMCP_LOG_FORMAT=json` and the logs shipped somewhere
      durable.
- [ ] `DROIDMCP_TERMUX_ALLOWLIST` set if you actually need
      `mcp-termux`. Otherwise don't run it.
- [ ] `DROIDMCP_SCRAPER_ALLOW_PRIVATE` / `_NETWORK_ALLOW_PUBLIC`
      left unset unless you understand the implications.
- [ ] `GET /healthz` returns 200 from outside (e.g. a smoke check
      from your supervisor).
- [ ] Binary verified against the published `SHA256SUMS` (and ideally
      the cosign `.sig`/`.pem` for the release tag).

In dev mode (loopback only, no key, plain HTTP) the same servers are
fine for experimentation — just understand the moment you bind to a
non-loopback interface, you owe yourself the items above.
