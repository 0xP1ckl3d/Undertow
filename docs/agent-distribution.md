# Configured thin agents

`undertow` is the full server, client, console, and diagnostic framework. A configured `undertow-agent` is a smaller, non-interactive deployment binary. Both call the same internal agent runtime for authentication, transports, inventory, mux, capabilities, pivots, relays, execution, transfers, scripts, WASM, native modules, BOFs, and reconnects. The full `undertow agent ...` CLI remains available for manual and diagnostic use.

## Prepare release templates

Run `sh tools/build-release.sh bin` on Linux or `./tools/build-release.ps1` on Windows before deploying the server. The release produces Windows amd64 and Linux amd64/arm64 thin templates plus `undertow-agent-templates.json`. Keep these next to `undertow`, or point the server at them with `--agent-templates DIRECTORY`. The server verifies the template hash and build identity, then stamps an artifact without invoking a compiler.

The profile and artifact store defaults to `agent-distribution` beneath the server working directory. `--agent-store DIRECTORY` selects another location. Its `state.json` holds profile credentials and per-artifact enrollment secrets; restrict access to the server operator and back it up with the server identity. This is server state, not endpoint state.

## Build, host, and run

From the server console:

```text
agent profile create office server=undertow.example.com:443 transport=quic
agent profile show office
agent build office windows amd64
agent artifacts
agent host ARTIFACT_ID
```

The server fills in its known fingerprint, listener settings, and other safe defaults. If a listener binds `0.0.0.0` or `::`, give the reachable `server=HOST:PORT`. A client console can read profiles and artifacts. To let one connected client create or change deployment state, the server operator runs `client distribution-admin SESSION_ID on`; the grant ends when that client disconnects. `off` removes it immediately. Agent execution and shutdown are operational permissions separate from distribution administration.

New artifacts have neutral filenames based on their full artifact ID, such as `84c13e21...exe`. `agent build office windows amd64 filename=agent.exe` selects another safe filename. A build is an immutable profile snapshot with its own Ed25519 agent identity and enrollment credential. Editing `office` leaves old binaries unchanged; building again from the same profile creates a new artifact, identity, and credential. Copying one artifact to multiple endpoints deliberately duplicates the same agent identity, so deploy separate builds when endpoints must be distinguishable.

`agent host` requires an active WebSocket HTTPS listener and returns a random retrieval URL, size, and SHA-256. Hosting is explicit. By default the public path is `/RANDOM_TOKEN`, with no product, profile, artifact, or filesystem identifier. The server's optional `--agent-retrieval-path /downloads/` setting places the token below that prefix; configure the same flag on each server restart. Only hosted Undertow artifacts can be retrieved. Save the URL as a secret capability, download with GET, check the printed SHA-256, then run the binary with **no connection arguments**. HEAD returns the same metadata without a body. `agent unhost ARTIFACT_ID` immediately invalidates the URL; hosting again issues a new one. The former `/.undertow/artifacts/` route is unavailable.

On Windows the release template uses the GUI subsystem, so normal packaged operation opens no console window. On Linux an interactive launch starts the long-running process in a separate session; launches under a service manager or other nonterminal supervisor remain under that supervisor. The agent uses bounded progressive reconnect delays, approximately 2, 5, 10, 30, 60, 120, then 300 seconds. A healthy session resets the schedule. Runtime cancellation interrupts the wait. The deployment scripts contain no watchdog or reconnect loop.

Optional `agent deploy-script ARTIFACT_ID powershell|shell` prints a helper that downloads, verifies SHA-256, places, and launches a hosted binary. The PowerShell helper scopes any self-signed certificate exception to its artifact HTTP client. Manual retrieval and launch always work.

## Inspect and manage lifecycle

```text
agents
use 1
show
agent events
session kill
agent shutdown
agent unhost ARTIFACT_ID
agent revoke ARTIFACT_ID
agent delete ARTIFACT_ID
```

`show` displays the profile, artifact, transport, session uptime, and reconnect policy. `agent events AGENT_NUMBER|ID|HOSTNAME` shows recent connection, reconnect, shutdown, and disconnection events; a full ID still works after the agent disconnects. The server keeps a bounded in-memory history. No extra heartbeat exists for logging. The agent sends a reconnect count with the next normal inventory after an outage. Events do not contain enrollment secrets, identity keys, shell content, module arguments, or file content. Events are not durable across server restarts.

These commands have different effects:

| Command | Effect |
| --- | --- |
| `session kill` | Closes the current session; the agent stays running and may reconnect. |
| `agent shutdown` | Sends an authenticated shutdown request, waits for acknowledgement, and stops that configured agent process. The executable remains. |
| `agent unhost` | Disables future downloads from that artifact's current URL; it does not affect enrollment or running agents. |
| `agent revoke` | Rejects future enrollment with that artifact's credential; current sessions stay connected. Other artifacts and manual agent/client enrollment are unaffected. |
| `agent delete` | Removes the server's artifact record, file, retrieval URL, and enrollment credential; it does not remove deployed copies. |

## Profiles, credentials, and endpoint state

Create/edit fields include `server`, `transport`, `domain`, `fingerprint`, `auth`, `payload-profile`, `websocket-path`, `tls-server-name`, `tls-insecure-skip-verify`, `deny`, and comma-separated IPv4 `routes`. `token-file=PATH` and `password-file=PATH` remain supported for profile creation. New configured artifacts receive a fresh Ed25519 identity and 32-byte enrollment secret at build time regardless of the profile's source credential; the secret is scoped to that identity and can be revoked per artifact. Existing manual agents and VPN clients retain the server's normal enrollment method. Anyone with a binary can extract its embedded private key and credential, so treat the binary as sensitive. `profile show`, `artifacts`, `show`, and lifecycle events do not display secrets.

**Endpoint state:** Normal configured-agent execution reads the executable and writes no identity key, config, log, telemetry, PID, status, or other state file. It needs no writable home or working directory and prints no local connection diagnostics. The executable contains the private identity key, server fingerprint, connection settings, enrollment secret, opaque profile and artifact IDs, and capability/route policy. Friendly names, target platform, build version, creation time, hosted state, and hashes stay in the server's distribution store. Server-side lifecycle events provide operational visibility. Explicit file features, such as operator-requested uploads and module loading, may create the files those features require. The full `undertow agent` command retains its explicit `--agent-key` file workflow.

## Format and compatibility

New artifacts use a bounded JSON payload with compact field names, followed by SHA-256, a four-byte length, and an eight-byte version 3 binary footer. The reader strictly checks length, integrity, fields, and version before connecting. Earlier version 1 and 2 binaries must be rebuilt to get the embedded identity and no-write execution model. Their server-side records can remain until removed, but the new thin-agent reader does not include the old profile-format parser. Previously hosted random URLs use the newly configured retrieval path after a server restart; the old product-labelled path no longer works. The whole artifact has a SHA-256 for download verification. A format version and Undertow build identity are recorded server-side.
