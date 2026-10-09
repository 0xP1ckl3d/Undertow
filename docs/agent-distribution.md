# Payload deployment

Use **Payloads** in the [GUI](gui.md) to build and deliver configured agents, or use the console `payload` commands below. `undertow` is the full server, client, GUI, console, and diagnostic framework. A configured `undertow-agent` is a smaller, non-interactive deployment binary. Both use the same runtime for authentication, transports, inventory, capabilities, pivots, relays, execution, transfers, scripts, WASM, native modules, .NET assemblies, BOFs, and reconnects. The full `undertow agent ...` CLI remains available for manual diagnostics.

## Prepare release templates

Run `sh tools/build-release.sh bin` on Linux or `./tools/build-release.ps1` on Windows before deploying the server. The release produces Windows amd64 and Linux amd64/arm64 thin templates plus `undertow-agent-templates.json`. Keep these next to `undertow`, or point the server at them with `--agent-templates DIRECTORY`. The server verifies the template hash and build identity, then stamps an artifact without invoking a compiler.

The profile and artifact store defaults to `agent-distribution` beneath the server working directory. `--agent-store DIRECTORY` selects another location. Its `state.json` holds profile credentials and per-artifact enrollment secrets; restrict access to the server operator and back it up with the server identity. This is server state, not endpoint state.

## Build and deliver from the GUI

A **profile** is reusable connection and capability policy. An **artifact** is one immutable build or custom upload stored on the server. A deployed **agent** is a running instance with its own identity. Creating a profile, building, hosting, or generating a helper does not start an agent on an endpoint.

1. Open **Payloads → Profiles → New**. Choose a name, carrier, and endpoint-reachable address. Direct DNS, QUIC, and WebSocket take the **host only** in the GUI; it adds the active listener's port. DNS requires numeric IPv4. TCP relay takes a child-reachable parent `HOST:PORT`; SMB takes `\\PARENT_HOST\pipe\NAME`.
2. Review carrier-specific options, optional advertised CIDRs, denied capabilities, and sleep interval/jitter. The server supplies its fingerprint and safe defaults. New QUIC/WebSocket profiles follow the listener's TLS mode; the self-signed option still keeps the separate Undertow identity pin. Click **Create profile**. Select an existing profile and **Save changes** to edit it.
3. Open **Build payload** (or **Build from profile**), choose profile and **Windows · x64**, **Linux · x64**, or **Linux · ARM64**. Optionally choose a filename, then **Build payload**. The server stamps a verified template; no compiler runs on the endpoint. If the GUI shows a saved port/TLS mismatch, reopen and save the profile before building. The console accepts explicit external port mappings when needed.
4. Open **Artifacts** and select the result. Check ID, platform, SHA-256, and **Server file**. The server file is storage, not an endpoint install path. **Download verified binary** retrieves the build through the authenticated client even before hosting. Deliver it yourself and run it without connection arguments.
5. For server HTTPS delivery, click **Host on HTTPS listener**. The WebSocket HTTPS listener must be active. Review the target-reachable **Download URL**; keep it private.
6. Under **Deploy helper**, choose PowerShell or POSIX shell, click **Generate preview**, review, then **Download script**. Run it on the intended endpoint as described in [Getting started](getting-started.md#3-build-host-and-deploy-a-headless-windows-agent). It verifies SHA-256 before installation and launch.
7. Return to **Agents** to identify the callback, then read Overview and start normal work. A payload ID identifies the artifact; an agent ID identifies a running copy. Multiple copies of one payload are independent agents.

Profile edits affect future builds only. Rebuild for embedded setting changes, or use the selected agent's live sleep override for its connection rhythm. See [Profiles and credentials](#profiles-credentials-and-endpoint-state).

**Payloads → Retrieval settings** separates delivery from callback addressing. Use **Public retrieval host → Save host** for a server HTTPS hostname/IP endpoints can reach, without scheme or port. Changing host does not rotate tokens. For **Public retrieval path**, click **Review change**, then **Confirm change**: this rotates all hosted tokens and permanently invalidates previous URLs. Reprint or copy the current URLs before using helpers. These are shared server settings and do not change existing agents' embedded callback destinations or parent-hosted delivery.

Use **Unhost** to stop retrieval, **Revoke enrollment** to reject future callbacks from that build, and **Delete artifact** to remove its server file. These are different actions; none shuts down an already running agent session. Read [Lifecycle effects](#inspect-and-manage-lifecycle) before cleanup. A disconnected or sleeping record may still represent a process that will reconnect.

## Profile → build → host → run

From the server or an authenticated client console, including operator-only mode:

```text
payload
payload profile create office server=undertow.example.com transport=quic
payload build office windows amd64
payload host PAYLOAD_ID
payload url PAYLOAD_ID
payload deploy-script PAYLOAD_ID powershell
```

Typing `payload` in either console explains each step. The profile name (`office`) identifies reusable connection settings. `payload build` prints a **24-character payload ID** for the newly built binary. Each running copy creates its own **32-character agent ID** when it starts. Use the payload ID with `payload show`, `host`, `url`, `unhost`, `revoke`, and `delete`; use the agent ID, hostname, or current agent number with connected-agent commands. `payload profiles`, `payload list`, and `payload hosted` list profile names, full payload IDs, and active download URLs respectively. A unique payload ID prefix or exact server filename also works for payload commands, but the full ID is safest to copy.

The build output's **Server file** is the path on the Undertow server where the binary is stored. It is not an installation path on the endpoint. Hosting prints an opaque **Download URL** for the endpoint and its **Download path** on the server's HTTPS listener. Choose the endpoint's own install path when downloading. `payload show PAYLOAD_ID` displays all of these again, while `payload url PAYLOAD_ID` reprints the URL, path, and hash. A stopped WebSocket HTTPS listener makes the URL unavailable until it starts again.

The server fills in its known fingerprint, listener settings, and other safe defaults. For DNS, QUIC, and WebSocket, give the target-reachable `server=HOST`; Undertow uses the selected carrier listener's port. DNS requires a numeric IPv4 resolver address. The console also accepts an explicit `HOST:PORT` for unusual external port mappings. Relay profiles still need a child-reachable parent endpoint. An authenticated client console can read, build, host, download, and manage profiles and payloads directly.

New payloads have neutral filenames based on their full payload ID. `payload build office windows amd64 filename=worker.exe` selects another safe filename. A build is an immutable profile snapshot with its own enrollment credential. Each running copy creates a separate Ed25519 identity. Editing `office` leaves old binaries unchanged; building again from the same profile creates a new payload and credential. One payload can run on multiple endpoints at once; each process gets a different agent ID. Reconnects within one process keep that ID.

### Idle sleep

Profiles can set `sleep-seconds=SECONDS` and `sleep-jitter=PERCENT` (0–50). The default interval is zero, which keeps a continuous connection. The GUI exposes the same fields in **Payloads → Profiles**. These settings are embedded in each new build. For a connected agent, **Agent Overview → Connection rhythm** or `agent sleep AGENT_ID [SECONDS JITTER]` shows and updates its effective policy; the per-agent override is saved on the server and reapplied when that agent reconnects. `payload profile show NAME` displays the profile policy. Older agent binaries without sleep support continue their persistent sessions and must be rebuilt before their sleep policy can be updated live.

The interval is the base time **between check-ins**, not the time the agent stays awake first. After connecting, the current agent build allows a short idle grace of one tenth of the interval, clamped to 1–15 seconds. If no work arrives during that grace, it asks the server to sleep and then closes its carrier for the interval, varied by up to the configured jitter percentage. For example, a one-hour interval yields at most 15 seconds of idle awake time before a roughly hourly callback. An agent sleeps only after the server confirms there are no live mux streams, active jobs, forwards, relay listeners or children, active server routes, or client-accepted routes that depend on it. Interactive sessions, foreground execution, transfers, and streaming jobs keep their streams open. Sleep never suspends an active relay listener, reverse forward, or accepted TUN route.

The server records a confirmed sleep as **Sleeping**, with the requested delay, expected check-in, and a deadline for marking the agent lost. The full GUI workspace remains visible while it sleeps. An operator can change the per-agent sleep policy, including switching to continuous mode, at any time; the server saves it and sends it on the next callback. Operators can submit supported commands while the agent sleeps: the server retains them and dispatches them when it calls back. A request for a live stream waits for the callback and then keeps the connection open for that stream. Background jobs also remain connected while they run; receiving their output in chunks across multiple check-ins is deferred. Retained history and the virtual address stay with the same agent identity. The server marks it **Disconnected** only after three expected callback opportunities have passed, allowing for the configured jitter and a 30-second transport grace. Unexpected session loss remains **Disconnected** immediately, including for an agent configured for check-ins. Sleep state and deadlines survive a server restart. Agents built with the earlier sleep protocol still wait the full interval before sleeping; the GUI identifies that legacy timing so operators can rebuild them for the shorter idle grace.

DNS closes the authenticated session before its next poll; QUIC and WebSocket close their connections; TCP and SMB relay children close and redial through the existing parent listener. A parent with an active relay listener or child remains connected. Carrier failures continue to use the existing reconnect backoff rather than the idle sleep interval. Stopping the parent relay or losing its session still interrupts child connectivity in the usual way.

`payload host` requires an active WebSocket HTTPS listener and returns a random retrieval URL and SHA-256. Hosting is explicit. By default the public path is `/RANDOM_TOKEN`, with no product, profile, payload, or filesystem identifier. Set an initial neutral prefix with the server's `--payload-retrieval-path /downloads/` flag. In the console, `payload retrieval-path` shows the current prefix and `payload retrieval-path set /downloads/` changes it immediately and saves it. The new prefix applies to every hosted artifact and rotates their download tokens, so previous URLs remain invalid even if the prefix is restored. Run `payload hosted` or `payload url PAYLOAD_ID` to reprint current URLs. On restart, an explicit startup flag overrides the saved prefix. Only artifacts explicitly hosted in Undertow can be retrieved. Save each URL as a secret capability, download with GET, check the printed SHA-256, then run the file according to its intended use. HEAD returns the same metadata without a body. `payload unhost PAYLOAD_ID` immediately invalidates the URL; hosting again issues a new one. The former `/.undertow/artifacts/` route is unavailable.

On Windows the release template uses the GUI subsystem, so normal packaged operation opens no console window. On Linux an interactive launch starts the long-running process in a separate session; launches under a service manager or other nonterminal supervisor remain under that supervisor. The agent uses bounded progressive reconnect delays, approximately 2, 5, 10, 30, 60, 120, then 300 seconds. A healthy session resets the schedule. Runtime cancellation interrupts the wait. The deployment scripts contain no watchdog or reconnect loop.

Optional `payload deploy-script PAYLOAD_ID powershell|shell` prints a helper that downloads, verifies SHA-256, places, and launches a hosted binary. The PowerShell helper works with Windows PowerShell 5.1 and PowerShell 7+, scopes any self-signed certificate exception to its download HTTP client, and computes SHA-256 before installation. Manual retrieval and launch always work.

## Upload a custom artifact

Open **Payloads → Upload artifact** in the browser GUI to add an existing file to the server artifact catalog. Select the file, give it an operator-facing label, and declare its target platform and architecture. Windows custom artifacts must use an `.exe` filename. The client calculates SHA-256 before upload, the server verifies the complete stream before publishing the record, and uploads are limited to 512 MiB.

An uploaded artifact appears with built artifacts under **Payloads → Artifacts**. It can be downloaded, hosted on the server HTTPS listener, hosted through an agent's TCP or SMB relay, and used to generate the same pinned deploy helper. Hosting remains explicit and uses a new opaque retrieval token. Deleting it removes the server copy and catalog entry.

A custom artifact contains no Undertow connection profile or enrollment credential, so it cannot be revoked or correlated as an Undertow payload. Mark a Windows upload as **Service compatible** only when the executable actually implements the Windows service control interface. This assertion makes it selectable for Service Control Jump; it does not modify the executable.

## Host a payload through a connected agent

When a child can reach a parent agent but cannot reach the server's HTTPS payload listener, enable delivery on the parent's **existing relay listener**. TCP relay listeners accept pinned HTTPS downloads; Windows named-pipe relays accept a pinned PowerShell helper over the pipe. The server keeps the artifact. For each download the parent requests its bytes through its current authenticated Undertow session and streams them to the child. The parent does not store the binary or run a shell. The relay listener continues to carry child agent sessions. Downloads are disabled until an operator explicitly enables them for an artifact.

```text
agents
use 1
show
relay start 192.168.10.20:8443
relay list
back
payload profile create branch server=192.168.10.20:8443 transport=relay
payload build branch windows amd64
payload host-agent PAYLOAD_ID PARENT_AGENT_ID 192.168.10.20:8443 192.168.10.20
payload agent-hosts PAYLOAD_ID
payload verify-script-agent HOST_ID powershell
payload deploy-script-agent HOST_ID powershell
payload unhost-agent HOST_ID
```

Run these commands in the **server or connected client console**. Replace `1` with the parent agent's current number and use its full ID from `show` for `PARENT_AGENT_ID`. `RELAY_BIND` must exactly match an active listener on that parent. For TCP, `PUBLIC_HOST` is the IP address or DNS hostname the child uses for the HTTPS download URL, without a scheme or port; Undertow adds the relay listener's port. Omitting the TCP bind uses `0.0.0.0:8443` on the parent. Use the parent's reachable IP or hostname, never `0.0.0.0`, as the child's `server` destination and `PUBLIC_HOST`. A loopback bind is appropriate only for a child running on the parent host. `payload retrieval-host` and `payload retrieval-path` configure the separate server HTTPS download listener; they do not change agent-hosted delivery.

The returned host ID identifies one active delivery token. It works only while that relay listener and parent session are active. `payload agent-hosts` lists active endpoints. `payload unhost-agent` invalidates one token while leaving the relay listener and child sessions intact. `relay stop RELAY_BIND` closes that listener, blocking new child connections and downloads; established child sessions follow the normal lifecycle. Artifact revocation or deletion invalidates its tokens. Parent disconnect or server restart invalidates delivery tokens. The saved relay listener is restored when the same parent reconnects and reports its relay capability, but **artifact delivery must be enabled again** to obtain a new token and helper. The audit log records actions; the active host list reflects current delivery state. PowerShell helpers pin the temporary agent TLS certificate and verify artifact SHA-256; TCP POSIX helpers pin its public key and verify SHA-256. No extra server HTTPS handler is started. `payload verify-script-agent` is an optional HEAD-only diagnostic for an accessible workstation; it neither gates deployment nor downloads artifact bytes.

For a Windows SMB pipe, build a `relay-smb` artifact and use `payload host-agent PAYLOAD_ID PARENT_AGENT_ID \\.\pipe\NAME PARENT_HOST`. The fourth argument is the parent's SMB host or IP reachable by the child; `.` works for a helper run on the parent itself. Then run `payload deploy-script-agent HOST_ID powershell` to print a pinned helper. An SMB pipe has no browser URL or POSIX shell helper. `payload deploy-script-agent HOST_ID shell` remains available for a Linux child on a TCP relay. A helper is printed for review; generating it does not start the child. To save the build to the console host for another delivery channel, use `payload download PAYLOAD_ID [OUTPUT]`. See [Windows SMB named-pipe relays](smb-named-pipe-relays.md).

In the GUI, open **Payloads → Artifacts**, select the build, then **Host through an agent**. Choose the connected parent, active relay, and child-reachable host. Click **Enable HTTPS downloads** for TCP or **Enable pipe delivery** for SMB, then preview/download the helper. TCP supplies a URL and PowerShell/POSIX helpers; SMB supplies a pipe endpoint and PowerShell helper. Use the explicit disable control to invalidate delivery without stopping the child relay. **Preview diagnostic script** provides the optional pinned HEAD check. **Networking → Relays → Create child payload** preselects the relay carrier for profile creation but does not enable delivery or run the child.

**Prepare the parent host's inbound path before deployment.** `relay start` proves only that the agent bound its local TCP socket or pipe; it cannot prove that a remote child can reach it. For TCP on Windows, the parent host's effective firewall policy must allow inbound connections to the chosen port and executable path. Windows may create `TCP Query User` block rules when a new executable first listens without an approved exception, especially under a medium integrity account. An earlier allow rule for the *same full executable path* can make a later medium integrity run reachable; copying the binary to Desktop or another new path does not grant access. Undertow does not add, remove, disable, or bypass host firewall rules. If a child workstation is available, run `Test-NetConnection PARENT_IP_OR_HOST -Port 8443` there, then use the optional `verify-script-agent` helper to check the pinned endpoint without downloading or starting a payload. A failed remote test needs a network or approved policy correction before deployment. The generated PowerShell HTTPS helper bypasses system proxies for the direct relay connection and reports the endpoint when connection setup fails.

For SMB pipe delivery, the parent's Windows SMB service must be reachable and the child must authenticate to Windows before the Undertow pipe handshake begins. A successful `Test-NetConnection PARENT_HOST -Port 445` checks the network path, while `net use \\PARENT_HOST\IPC$` checks Windows authentication. Domain membership alone does not prove that two hosts can authenticate to each other. Cloned Windows hosts with duplicate machine SIDs can fail peer authentication and log LsaSrv event 6167. See [Windows SMB named-pipe relays](smb-named-pipe-relays.md#diagnose-a-failed-connection).

## Download for your own delivery

If you will wrap the binary or distribute it through another channel, save it to the **console host** before or after hosting:

```text
payload download PAYLOAD_ID ./staging/worker.exe
```

This uses the authenticated control connection, so an unhosted build can be downloaded too. The command verifies SHA-256 before publishing the file and refuses to replace an existing file. With no output argument, it saves to `outputs/downloads/payloads/FILENAME` under the console's working directory; the `outputs/` tree is ignored by Git. You can also pass a filename or an existing directory, and the command creates missing parent directories. Server and connected VPN client consoles can both download and manage payloads directly. See [Getting started](getting-started.md#3-build-host-and-deploy-a-headless-windows-agent) for the hosted PowerShell flow.

## Public HTTPS download host

By default, Undertow builds a hosted URL using the payload profile's `server` host and the server's active HTTPS listener port. A relay agent may instead connect to a parent at `127.0.0.1:8443` or an internal address that cannot serve public downloads. In the server or connected client console, set the external HTTPS host separately before printing its deploy script:

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
| `payload delete` | Removes the server file and retrieval URL and hides the artifact. For a built Undertow payload it retains the enrollment record so deployed copies can reconnect; use `payload revoke PAYLOAD_ID` to block future enrollment. A custom artifact has no enrollment record, so deletion removes its catalog record completely. |

Older releases deleted the enrollment secret with the artifact. That cannot be undone by this update. If the original store backup is unavailable, build and deploy a new payload for those agents.

## Profiles, credentials, and endpoint state

Create/edit fields include `server`, `transport`, `domain`, `fingerprint`, `auth`, `payload-profile`, `sleep-seconds`, `sleep-jitter`, `websocket-path`, `tls-server-name`, `tls-insecure-skip-verify`, `deny`, and comma-separated IPv4 `routes`. `token-file=PATH` and `password-file=PATH` remain supported for profile creation. New configured payloads receive a 32-byte enrollment secret at build time regardless of the profile's source credential. Each running process creates its own Ed25519 identity. Revocation remains scoped to the payload credential and blocks future enrollment by all its copies. Existing manual agents and VPN clients retain the server's normal enrollment method. Anyone with a binary can extract its enrollment credential, so treat the binary as sensitive. `payload profile show`, `payload list`, `payload show`, and lifecycle events do not display secrets.

The older `agent profile`, `agent build`, `agent artifacts`, and related deployment commands remain as compatibility aliases. New console help and examples use `payload` to keep deployment binaries distinct from connected agents.

**Endpoint state:** Normal configured-agent execution reads the executable and writes no identity key, config, log, telemetry, PID, status, or other state file. It needs no writable home or working directory and prints no local connection diagnostics. The executable contains the server fingerprint, connection settings, enrollment secret, opaque profile and artifact IDs, and capability/route policy. Friendly names, target platform, build version, creation time, hosted state, and hashes stay in the server's distribution store. Server-side lifecycle events provide operational visibility. Explicit file features, such as operator-requested uploads and module loading, may create the files those features require. The full `undertow agent` command retains its explicit `--agent-key` file workflow.

## Format and compatibility

New artifacts use a bounded JSON payload with compact field names, followed by SHA-256, a four-byte length, and an eight-byte version 4 binary footer. The reader strictly checks length, integrity, fields, and version before connecting. Earlier format versions, including version 3 with a fixed agent identity, must be rebuilt to share one payload across hosts. Their server-side records can remain until removed, but the new thin-agent reader does not include the old profile-format parser. A changed public prefix rotates hosted retrieval tokens; previously copied URLs must be replaced with the current URLs from `payload hosted`. The old product-labelled path no longer works. The whole artifact has a SHA-256 for download verification. A format version and Undertow build identity are recorded server-side.

Back to [documentation home](README.md).
