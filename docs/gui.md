# Browser operator GUI

The client serves a local browser workspace when it starts. The terminal console remains available: both surfaces call Undertow's Go operations, and neither opening a page nor opening an agent workspace starts an agent command. The browser only contacts the local client. The client speaks to the Undertow server, which owns agents, routes, jobs, payloads, screenshots, and audit records.

## Open the local workspace

Start a client as described in [Getting started](getting-started.md), or connect without a TUN device for observation and agent operations:

```sh
undertow client --operator-only --transport quic --server SERVER_IP:443 \
  --fingerprint FINGERPRINT --tls-insecure-skip-verify --token-file token.key
```

The client prints a `GUI available:` URL at startup and when you attach to its console. Open that URL **in a browser on the client machine**. The service binds to numeric loopback and chooses an available port. Its URL contains a per-launch secret; keep it private. The client exchanges that secret for a local browser cookie and checks Host, Origin, and CSRF tokens on changes. Undertow enrollment credentials stay in Go and are never sent to the browser. A process running as the same OS user is outside this local browser protection boundary.

The GUI starts by default with the client. Use `--no-gui` for terminal-only operation. `undertow client gui --pid-file PATH` reprints the current local launch URL; `undertow client attach --pid-file PATH` also displays it. Closing the page leaves the client and its sessions running. The browser service itself is not a remote listener: use a browser on the client VM or host.

An operator-only client does not install VPN or accepted routes. To use client routing, start an elevated VPN client with `--internal`, `--vpn`, or both. Settings shows which mode is active; the Routes view explains when installation is unavailable.

### First review from the browser

1. Start or attach to the client and copy its printed `GUI available:` URL into a browser **on that client host**. If the page says **Disconnected**, check the client console or **Settings → Status** for the current server address, carrier, and session. The browser service can remain open while its Undertow connection reconnects.
2. Open **Topology**. The central server, operator clients, and connected or retained agents show their actual carrier and relay paths. Double-click a connected agent to open its workspace.
3. Open the agent's **Overview** first for identity, last contact, parent path, capabilities, and advertised routes. In **Console**, type `help` to see the commands available for that selected agent. Merely opening the workspace or Console sends no agent command.
4. Use **Files**, **Jobs**, **Screenshots**, **Modules**, or **Host** for the corresponding operation. Each tab gives you an explicit control and shows progress or retained results. A disconnected agent remains readable but cannot receive new commands.
5. Return to **History** for server audit actions. The client and session IDs are server bound; the operator name in **Settings → Identity** is an unverified claim until server authentication is added.

For terminal equivalents, see [Console guide](console.md). For the server and client startup flags, see [Getting started](getting-started.md) and [CLI reference](cli-reference.md).


## Read the topology

The graph comes from a server-generated snapshot. Drag individual nodes to arrange your local layout; the client stores positions in its local GUI database. **Fit all** brings every node back into view after arranging the graph. Hover a node or line for its session, carrier, route, and health details. Double-click an agent to open its workspace. The carrier list names DNS, QUIC, and WebSocket and marks inactive listeners. An agent's observed public IP is shown only when the server actually observed a globally routable source address. The server public host is operator-configured under **Settings → Carriers**; Undertow does not use an external IP discovery service.

Only routes accepted by a client appear as network paths on the graph. Server configured and merely advertised routes remain visible in Routes without implying that a client can use them. A disconnected agent stays in the graph as an inactive record with its last known parent path and details; its workspace is read-only.

The line between a client and the server shows that client's actual carrier. Hover to see whether it reports Internet VPN or internal routing, and which CIDRs it has accepted and from which agents. The local client also reports its installed route state in Routes. A peer's remote endpoint is the address the server observed; for a NATed agent this is not necessarily an address reachable from an operator workstation. Agents are blue when standard privilege was observed and red when elevated privilege was observed. An unknown privilege remains unclassified until an explicit privileges operation supplies a result.

## Work in an agent workspace

Select an agent from **Agents**. Overview shows the latest platform, carrier, relay parent, capabilities, and routes. On a routing client, accept an advertised CIDR with one click, switch a saved route off or on, or open **Routes** to add a custom CIDR. Route choices remain in the client's route file; the server records active acceptance, and the client owns its OS route installation.

The **Console** tab is the attached Undertow **agent command menu**. It is bound to the selected agent and shows only commands the GUI console can run. `help` uses the terminal console's section and row renderer, and loaded BOF, native, and WASM commands appear with their packaged descriptions. The command area does not start an OS shell. The separate **Live shell** tab requires an explicit **Start live shell** click.


The client stores console commands and streamed output, including foreground runs started in **Modules**, in its restricted local GUI database. Reopen Console after a client restart to see that history. Background module output belongs to the server's retained **Jobs** view. Console history can include sensitive command arguments and output; protect the client host and its GUI database accordingly. The history is bounded to the latest 1,000 entries per agent, and a single entry is capped at 64 KiB.

**Files** provides explicit browse, upload, download, and mkdir controls. Downloads use the existing transfer stream and verify the resulting hash. **Screenshots** lists server-retained captures; listing screens and capturing a screen are separate actions. **Host** preserves each operation's last result on the server until the operator reruns it. When an agent disconnects, retained Console, Jobs, Screenshots, Host results, and last known Overview remain visible; controls that contact the agent are unavailable.

### Console and module commands

The bound console accepts the agent-side command set from Undertow's Go parser: `show`, `agent events`, `agent shutdown`, `session kill`, host operations (`pwd`, `ls`, `stat`, `mkdir`, `rm`, `whoami`, `ps`, `privileges`, `env`, `interfaces`, `dns`, `route-table`), `screens`, `screenshot`, file transfer, direct `exec`, jobs, relay listeners, client routes and forwards, one-shot script/WASM/native/BOF runs, and loaded module commands. Its `help` groups those commands using the terminal menu's section and row renderer. It intentionally excludes navigation commands such as `back`, `use`, `quit`, and top-level server commands because the workspace already identifies the target agent.

The **Modules** page lists the local module bank and packaged BOF commands. Load or unload explicitly, select a connected agent, supply arguments and optional input, then run in foreground or background. Foreground output appears in Console history even when the run started from Modules. Background output is retained as a server job. Loading a module into the client bank does not execute it on any agent.

### Files and transfers

Open **Files**, click **Browse**, then choose a directory or file. A single click inspects metadata; a directory opens on double-click, Enter, or **Open folder**. **Choose local file** and **Upload** send a file to the selected remote path. **Download verified file** retrieves a selected regular file and checks its SHA-256 before offering it to the browser. **Create folder** uses the existing host `mkdir` operation. The transfer has a 512 MiB GUI upload/download limit; the terminal transfer path remains available for other workflows.

**Transfers** shows server-retained transfer IDs, agent, path, operator claim, progress, state and checksum for file transfers started from the GUI Files tab. A running transfer changes to interrupted when its client session goes away or the server restarts. Transfer bytes remain on the normal Undertow stream; the server's transfer table stores metadata only. Browser downloads are staged briefly on the local client, then removed when the client exits. Terminal `upload` and `download` continue to use the existing transfer stream but are not yet indexed in this GUI transfer history.

### Jobs, screenshots and host results

**Jobs** supports explicit background starts, state and output inspection, full output download, cancellation and deletion. The output belongs to the Undertow server and remains available after a browser or client restart. Use a job for long-running work rather than keeping a browser command open. A lost agent's completed output remains readable; controls requiring the agent are disabled.

**Screenshots** first shows retained server history. Click **List screens** to ask the agent which displays are available, then **Capture screen** beside the chosen display. Clicking a historical item views the stored image without contacting the agent. **Download PNG** saves that capture. Opening Screenshots never captures automatically. A Windows capture needs an available interactive desktop; a disconnected or locked desktop can still allow enumeration while capture reports an error.

**Host** shows the last server-retained result for each built-in host operation, including privileges and network information. Clicking **Run** replaces that operation's retained result with the new one. Merely switching tabs or reopening the GUI reads the prior result.

## Manage shared and local settings


**Settings → Client** controls this client's VPN and internal routing modes when it has a TUN device. **Carriers** lists server listeners and starts or stops DNS, QUIC, and WebSocket listeners. The local client refuses to stop the carrier carrying its own control session, including a force-stop request. Use another connected carrier first. **Status** shows session and server details; **Logs** shows recent server worker logs and a live follow view.

**Routes** distinguishes an agent's advertised networks, server routes, this client's saved accepted/custom routes, and routes installed in the local OS. Use an advertised CIDR choice for normal acceptance; enter a custom CIDR only when an agent can reach a network it did not advertise. The agent Overview gives one-click acceptance and toggles for saved routes. Disabling a saved route removes its active server acceptance and local installation while keeping the saved choice for later re-enablement. An operator-only client can inspect routes but cannot install them without a TUN device.

**Forwards** manages this client's agent TCP forwards: choose a connected agent, an agent bind address, and a client target address. The view lists active forwards and requires confirmation to stop one. **Relays** manages child-agent relay listeners separately. Neither page starts an agent-side shell.

**Identity** stores an operator ID and display name as **unverified claims** attached to actions from this client. The server binds the real client and session identifiers itself and keeps a durable audit record. Server user accounts, passwords, profile images, roles, and permissions are not yet implemented. The GUI does not claim those identity fields are authenticated.

## Payloads and relays

**Payloads** manages reusable profiles and server-built artifacts, including downloads, server HTTPS hosting, and deploy helper generation. A new profile suggests the configured server public host and the connected carrier's port; check that the target can actually reach the address before building. The artifact's profile snapshot does not change when a later profile edit is saved.

For a child agent, start a relay on the parent, then click **Create child payload** on that listener. The profile form selects `relay` and pre-fills its bind address as the child's connection destination. Check the address from the child's host: the default loopback bind is reachable only from the parent host. Save the profile, build for the chosen platform, and download the artifact. You can host it on the server's HTTPS payload listener and generate a deploy helper there. Generating or downloading a helper never executes it on an agent.

The relay listener carries Undertow child sessions. To distribute a built artifact through that same listener and port, select it under **Payloads → Artifacts → Host through an agent**. Choose the connected parent, one of its active relay listeners, and the parent's child-reachable IP or DNS name. **Enable payload downloads** adds an opaque download URL to that listener. The parent requests the artifact from the server over its existing authenticated Undertow session for each download; it does not hold the binary. Active URLs show pinned deploy helpers and explicit disable controls. Disabling a URL leaves the relay and its child sessions running. Generating a helper does not execute it. Artifact revocation, deletion, parent disconnect, or server restart invalidates its download URL. See [Topology and relays](topology-and-relays.md) for the relay connection model and [Payload deployment](agent-distribution.md) for the full console workflow.

For a Windows parent and child, **Relays** also offers an **SMB named pipe** carrier. Choose a Windows parent and enter its local bind as `\\.\pipe\NAME`. **Create child payload** then selects an SMB relay profile; enter the child's reachable address as `\\PARENT_HOST\pipe\NAME` and build a Windows artifact. The child appears as a separate `relay-smb` agent in Topology. A named pipe does not have a browser download URL, so retrieve that artifact from the server or use your chosen delivery channel. Follow the [SMB named-pipe relay guide](smb-named-pipe-relays.md) for the full workflow and Windows access checks.

Payload **Retrieval settings** controls the server's public HTTPS host and retrieval path separately from a profile's embedded connection address. Changing the path rotates hosted download tokens and invalidates earlier URLs. The artifact page shows each build's profile snapshot, platform, size, hash, download, hosted URL, helper preview and lifecycle controls. A built artifact is not an agent until an operator deploys and starts it.

## Data ownership and retention

The server owns operational records and shared audit history. The local client stores the graph layout, operator claim fields, module bank references, and bounded console history. Closing a browser tab does not disconnect agents or stop jobs. A client or server restart may interrupt active streams; completed server jobs and screenshots remain available through their retained records. Do not use the browser's local storage as an operational record.

## Operator workflow and safety boundaries

1. Start or attach to the local client and open its printed GUI URL. Check **Settings → Status** for the client session and current carrier.
2. Use **Topology** to choose a connected agent. Double-click its square icon or open it from **Agents**. Read Overview before starting any operation.
3. Run an Undertow built-in command in **Console**, or use the dedicated Files, Jobs, Screenshots, Modules, and Host tabs. Live OS shell requires opening **Live shell** and clicking **Start live shell**; no agent-side command starts on workspace open.
4. Accept an advertised route in Overview or Routes when working from a routing client. Check the graph edge and local installed state. A saved route can be disabled without deleting it.
5. Inspect **History** for server audit actions and **Transfers** or **Jobs** for retained operational results. Operator ID and display name are claims until account authentication is implemented; the server binds the actual client session itself.

The local service has no generic server API proxy. Go handlers expose selected operations and enforce local browser checks. The server validates agent targets and records server-side audit events. The GUI uses SSE to refresh changed operational data; after a replay gap or reconnect it reloads a snapshot. Terminal commands remain available with `undertow client attach`, and `--no-gui` starts the same client without the browser service.

## Visual asset licenses

The graph uses open-source icon packages already bundled with the frontend: Lucide (ISC), Simple Icons through React Icons (CC0 1.0 for brand marks), and Font Awesome brand icons through React Icons (CC BY 4.0). Agent icons reflect the OS reported by Undertow; an unknown OS uses a neutral symbol. No remote image service is queried by the browser.

Back to [documentation home](README.md).
