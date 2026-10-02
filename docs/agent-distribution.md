# Payload deployment

`undertow` is the full server, client, console, and diagnostic framework. A configured `undertow-agent` is a smaller, non-interactive deployment binary. Both call the same internal agent runtime for authentication, transports, inventory, mux, capabilities, pivots, relays, execution, transfers, scripts, WASM, native modules, BOFs, and reconnects. The full `undertow agent ...` CLI remains available for manual and diagnostic use.

## Prepare release templates

Run `sh tools/build-release.sh bin` on Linux or `./tools/build-release.ps1` on Windows before deploying the server. The release produces Windows amd64 and Linux amd64/arm64 thin templates plus `undertow-agent-templates.json`. Keep these next to `undertow`, or point the server at them with `--agent-templates DIRECTORY`. The server verifies the template hash and build identity, then stamps an artifact without invoking a compiler.

The profile and artifact store defaults to `agent-distribution` beneath the server working directory. `--agent-store DIRECTORY` selects another location. Its `state.json` holds profile credentials and per-artifact enrollment secrets; restrict access to the server operator and back it up with the server identity. This is server state, not endpoint state.

## Profile → build → host → run

From the server or an authenticated VPN client console:

```text
payload
payload profile create office server=undertow.example.com:443 transport=quic
payload build office windows amd64
payload host PAYLOAD_ID
payload url PAYLOAD_ID
payload deploy-script PAYLOAD_ID powershell
```

Typing `payload` in either console explains each step. The profile name (`office`) identifies reusable connection settings. `payload build` prints a **24-character payload ID** for the newly built binary and its embedded **32-character agent ID**, which identifies the agent after it connects. Use the payload ID with `payload show`, `host`, `url`, `unhost`, `revoke`, and `delete`; use the agent ID, hostname, or current agent number with connected-agent commands. `payload profiles`, `payload list`, and `payload hosted` list profile names, full payload IDs, and active download URLs respectively. A unique payload ID prefix or exact server filename also works for payload commands, but the full ID is safest to copy.

The build output's **Server file** is the path on the Undertow server where the binary is stored. It is not an installation path on the endpoint. Hosting prints an opaque **Download URL** for the endpoint and its **Download path** on the server's HTTPS listener. Choose the endpoint's own install path when downloading. `payload show PAYLOAD_ID` displays all of these again, while `payload url PAYLOAD_ID` reprints the URL, path, and hash. A stopped WebSocket HTTPS listener makes the URL unavailable until it starts again.

The server fills in its known fingerprint, listener settings, and other safe defaults. If a listener binds `0.0.0.0` or `::`, give the reachable `server=HOST:PORT`. An authenticated client console can read, build, host, download, and manage profiles and payloads directly.

New payloads have neutral filenames based on their full payload ID. `payload build office windows amd64 filename=worker.exe` selects another safe filename. A build is an immutable profile snapshot with its own Ed25519 agent identity and enrollment credential. Editing `office` leaves old binaries unchanged; building again from the same profile creates a new payload, identity, and credential. Copying one binary to multiple endpoints deliberately duplicates the same agent identity, so deploy separate builds when endpoints must be distinguishable.

`payload host` requires an active WebSocket HTTPS listener and returns a random retrieval URL and SHA-256. Hosting is explicit. By default the public path is `/RANDOM_TOKEN`, with no product, profile, payload, or filesystem identifier. Set an initial neutral prefix with the server's `--payload-retrieval-path /downloads/` flag. In the console, `payload retrieval-path` shows the current prefix and `payload retrieval-path set /downloads/` changes it immediately and saves it. The new prefix applies to every hosted payload and rotates their download tokens, so previous URLs remain invalid even if the prefix is restored. Run `payload hosted` or `payload url PAYLOAD_ID` to reprint current URLs. On restart, an explicit startup flag overrides the saved prefix. Only hosted Undertow payloads can be retrieved. Save each URL as a secret capability, download with GET, check the printed SHA-256, then run the binary with **no connection arguments**. HEAD returns the same metadata without a body. `payload unhost PAYLOAD_ID` immediately invalidates the URL; hosting again issues a new one. The former `/.undertow/artifacts/` route is unavailable.

On Windows the release template uses the GUI subsystem, so normal packaged operation opens no console window. On Linux an interactive launch starts the long-running process in a separate session; launches under a service manager or other nonterminal supervisor remain under that supervisor. The agent uses bounded progressive reconnect delays, approximately 2, 5, 10, 30, 60, 120, then 300 seconds. A healthy session resets the schedule. Runtime cancellation interrupts the wait. The deployment scripts contain no watchdog or reconnect loop.

Optional `payload deploy-script PAYLOAD_ID powershell|shell` prints a helper that downloads, verifies SHA-256, places, and launches a hosted binary. The PowerShell helper works with Windows PowerShell 5.1 and PowerShell 7+, scopes any self-signed certificate exception to its download HTTP client, and computes SHA-256 before installation. Manual retrieval and launch always work.

## Download for your own delivery

If you will wrap the binary or distribute it through another channel, save it to the **console host** before or after hosting:

```text
payload download PAYLOAD_ID ./staging/worker.exe
```

This uses the authenticated control connection, so an unhosted build can be downloaded too. The command verifies SHA-256 before publishing the file and refuses to replace an existing file. With no output argument, it saves to `payloads/FILENAME` under the console's working directory; this directory is ignored by Git. You can also pass a filename or an existing directory, and the command creates missing parent directories. Server and connected VPN client consoles can both download and manage payloads directly. See [Getting started](getting-started.md#3-build-host-and-deploy-a-headless-windows-agent) for the hosted PowerShell flow.

## Public HTTPS download host

By default, Undertow builds a hosted URL using the payload profile's `server` host and the server's active HTTPS listener port. A relay agent may instead connect to a parent at `127.0.0.1:8443` or an internal address that cannot serve public downloads. On the server console, set the external HTTPS host separately before printing its deploy script:

```text
payload retrieval-host set SERVER_IP
payload url PAYLOAD_ID
payload deploy-script PAYLOAD_ID powershell
```

Give only a DNS name or IP address, without a scheme or port. `payload retrieval-host` shows the current setting. It is saved in the server's distribution state and applies to all hosted payload URLs; the HTTPS listener supplies the port. Changing the host changes the displayed URL but does not rotate its opaque download token. Existing URLs still reach the same listener if their host remains reachable. The agent's embedded connection address does not change. For an unhosted build or your own delivery channel, use `payload download` instead.

## Inspect and manage lifecycle

```text
agents
use 1
show
agent events
session kill
agent shutdown
payload unhost PAYLOAD_ID
payload revoke PAYLOAD_ID
payload delete PAYLOAD_ID
```

`show` displays the profile, artifact, transport, session uptime, and reconnect policy. `agent events AGENT_NUMBER|ID|HOSTNAME` shows recent connection, reconnect, shutdown, and disconnection events; a full ID still works after the agent disconnects. The server keeps a bounded in-memory history. No extra heartbeat exists for logging. The agent sends a reconnect count with the next normal inventory after an outage. Events do not contain enrollment secrets, identity keys, shell content, module arguments, or file content. Events are not durable across server restarts.

These commands have different effects:

| Command | Effect |
| --- | --- |
| `session kill` | Closes the current session; the agent stays running and may reconnect. |
| `agent shutdown` | Sends an authenticated shutdown request, waits for acknowledgement, and stops that configured agent process. The executable remains. |
| `payload unhost` | Disables future downloads from that payload's current URL; it does not affect enrollment or running agents. |
| `payload revoke` | Rejects future enrollment with that payload's credential; current sessions stay connected. Other payloads and manual agent/client enrollment are unaffected. |
| `payload delete` | Removes the server's payload record, file, retrieval URL, and enrollment credential; it does not remove deployed copies. |

## Profiles, credentials, and endpoint state

Create/edit fields include `server`, `transport`, `domain`, `fingerprint`, `auth`, `payload-profile`, `websocket-path`, `tls-server-name`, `tls-insecure-skip-verify`, `deny`, and comma-separated IPv4 `routes`. `token-file=PATH` and `password-file=PATH` remain supported for profile creation. New configured payloads receive a fresh Ed25519 identity and 32-byte enrollment secret at build time regardless of the profile's source credential; the secret is scoped to that identity and can be revoked per payload. Existing manual agents and VPN clients retain the server's normal enrollment method. Anyone with a binary can extract its embedded private key and credential, so treat the binary as sensitive. `payload profile show`, `payload list`, `payload show`, and lifecycle events do not display secrets.

The older `agent profile`, `agent build`, `agent artifacts`, and related deployment commands remain as compatibility aliases. New console help and examples use `payload` to keep deployment binaries distinct from connected agents.

**Endpoint state:** Normal configured-agent execution reads the executable and writes no identity key, config, log, telemetry, PID, status, or other state file. It needs no writable home or working directory and prints no local connection diagnostics. The executable contains the private identity key, server fingerprint, connection settings, enrollment secret, opaque profile and artifact IDs, and capability/route policy. Friendly names, target platform, build version, creation time, hosted state, and hashes stay in the server's distribution store. Server-side lifecycle events provide operational visibility. Explicit file features, such as operator-requested uploads and module loading, may create the files those features require. The full `undertow agent` command retains its explicit `--agent-key` file workflow.

## Format and compatibility

New artifacts use a bounded JSON payload with compact field names, followed by SHA-256, a four-byte length, and an eight-byte version 3 binary footer. The reader strictly checks length, integrity, fields, and version before connecting. Earlier version 1 and 2 binaries must be rebuilt to get the embedded identity and no-write execution model. Their server-side records can remain until removed, but the new thin-agent reader does not include the old profile-format parser. A changed public prefix rotates hosted retrieval tokens; previously copied URLs must be replaced with the current URLs from `payload hosted`. The old product-labelled path no longer works. The whole artifact has a SHA-256 for download verification. A format version and Undertow build identity are recorded server-side.
