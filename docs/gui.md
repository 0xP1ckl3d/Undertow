# Browser operator GUI

The client serves a local browser workspace when it starts. The terminal console remains available: both surfaces call Undertow's Go operations, and neither opening a page nor opening an agent workspace starts an agent command. The browser only contacts the local client. The client speaks to the Undertow server, which owns agents, routes, jobs, payloads, screenshots, and audit records.

## Open the local workspace

Start a client as described in [Getting started](getting-started.md), or connect without a TUN device for observation and agent operations:

```sh
undertow client --operator-only --transport quic --server SERVER_IP:443 \
  --fingerprint FINGERPRINT --tls-insecure-skip-verify --token-file token.key \
  --operator alice --operator-password-file alice.password
```

The client prints a `GUI available:` URL at startup and when you attach to its console. Open that URL **in a browser on the client machine**. The service binds to numeric loopback and chooses an available port. Its URL contains a per-launch secret; keep it private. The client exchanges that secret for a local browser cookie and checks Host, Origin, and CSRF tokens on changes. Undertow enrollment credentials stay in Go and are never sent to the browser. A process running as the same OS user is outside this local browser protection boundary.

The GUI starts by default with the client. Use `--no-gui` for terminal-only operation. `undertow client gui --pid-file PATH` reprints the current local launch URL; `undertow client attach --pid-file PATH` also displays it. Closing the page leaves the client and its sessions running. The browser service itself is not a remote listener: use a browser on the client VM or host.

An operator-only client does not install VPN or accepted routes. To use client routing, start an elevated VPN client with `--internal`, `--vpn`, or both. Settings shows which mode is active; the Routes view explains when installation is unavailable.

### First review from the browser

1. Start or attach to the client and copy its printed `GUI available:` URL into a browser **on that client host**. If the page says **Disconnected**, check the client console or **Settings → Status** for the current server address, carrier, and session. The browser service can remain open while its Undertow connection reconnects.
2. Open **Topology**. The central server, operator clients, and connected or retained agents show their actual carrier and relay paths. Double-click a connected agent to open its workspace.
3. Open the agent's **Overview** first for identity, last contact, parent path, capabilities, and advertised routes. In **Console**, type `help` to see the commands available for that selected agent. Merely opening the workspace or Console sends no agent command.
4. Use **Files**, **Jobs**, **Screenshots**, **Modules**, or **Host** for the corresponding operation. Each tab gives you an explicit control and shows progress or retained results. A disconnected agent remains readable but cannot receive new commands.
5. Return to **History** for server audit actions. The client, session, and operator identity are bound by the server. **Settings → Identity** shows the authenticated account. A Team Leader can manage other accounts there.

For terminal equivalents, see [Console guide](console.md). For the server and client startup flags, see [Getting started](getting-started.md) and [CLI reference](cli-reference.md).


## Read the topology

The graph comes from a server-generated snapshot. Drag individual nodes to arrange your local layout; the client stores positions in its local GUI database. **Fit all** brings every node back into view after arranging the graph. Hover a node or line for its session, carrier, route, and health details. Move the pointer into the details panel to scroll long records, or click a node or line to pin its details until you click the background. Double-click an agent to open its workspace. Right-click an object for relevant actions: an agent can be opened or renamed, an active relay can be stopped, and a route accepted by this client can be removed. Disconnecting or shutting down an agent and stopping a relay require confirmation. The carrier list names DNS, QUIC, and WebSocket and marks inactive listeners. An agent's observed public IP is shown only when the server actually observed a globally routable source address. The server public host is operator-configured under **Settings → Carriers**; Undertow does not use an external IP discovery service.

Disconnected agents remain available with their last known details and retained history. To clear a lost agent from the working view, open its **Overview** and select **Archive agent**, or right-click its node in **Topology**. Archiving is shared server state; it does not delete the record, stop an agent, or change its reconnect behavior. Archived agents are hidden from **Agents** and **Topology** by default. Turn on **Settings → Agents → Show archived agents** to inspect them and restore one manually. This visibility choice is saved only on the local client. If the same agent identity calls back, the server automatically removes its archive flag and it reappears for all operators. A newly started process with a new agent identity is a separate record.

Only routes accepted by a client appear as network paths on the graph. Server configured and merely advertised routes remain visible in Routes without implying that a client can use them. A relayed child is connected to the specific TCP or SMB listener that accepted it. New sessions record that listener's bind on the server; older agents can be mapped when their parent has only one listener of the child's carrier type. The graph does not guess when several older listeners could match. A disconnected agent stays in the graph as an inactive record with its last known parent path and details; its workspace is read-only.

The line between a client and the server shows that client's actual carrier. When that client accepts a route, the graph highlights its actual carrier and relay path through the agent to the network in green. The client node shows its accepted-route count; hover a highlighted line to see which clients accepted it. The path follows the server and relay listeners rather than drawing a fictitious direct client-to-agent connection. Hover the client carrier to see whether it reports Internet VPN or internal routing, and which CIDRs it has accepted and from which agents. The local client also reports its installed route state in Routes. A peer's remote endpoint is the address the server observed; for a NATed agent this is not necessarily an address reachable from an operator workstation. Agents are blue for a standard process token and red for an elevated one. The agent reports this coarse status with its connection inventory using an OS token/UID check; no host command runs automatically. An explicit **Privileges** operation still provides the detailed, retained result. For older agents that do not report a classification, the server reuses a successful retained **Privileges** result across restarts and reconnects. An agent with no such result remains unclassified.

## Work in an agent workspace

Select an agent from **Agents**. Use **Rename** in its workspace header to give it a shared nickname; the original hostname and stable agent ID remain visible. Clear the nickname to restore the hostname as its primary label. Nicknames persist on the Undertow server and appear for all operators, including in retained disconnected records. The terminal console also accepts `agent rename AGENT_ID "NAME"` after `agents`, or `agent rename "NAME"` from a selected agent. Use `""` to clear it. Overview shows the latest platform, carrier, relay parent, capabilities, and routes. On a routing client, accept an advertised CIDR with one click, switch a saved route off or on, or open **Routes** to add a custom CIDR. Route choices remain in the client's route file; the server records active acceptance, and the client owns its OS route installation.

**Overview → Idle sleep** shows a connected agent's effective interval and jitter. Set the interval to `0` for an always-connected session, or choose `1–86400` seconds and `0–50` percent jitter, then select **Save sleep**. The server saves this per-agent override and restores it on that agent's next callback. Older binaries that do not report sleep support show disabled controls; build a new payload to enable them. A sleeping agent appears disconnected, so agent actions wait for its next callback. Live shell, foreground work, transfers, active jobs, relay listeners, reverse forwards, and accepted routes keep the connection open. The same control is available in the terminal console with `agent sleep AGENT_ID [SECONDS JITTER]`, and in the bound agent Console with `agent sleep [SECONDS JITTER]`. See [Idle sleep](agent-distribution.md#idle-sleep) for carrier behavior.

The **Console** tab is the attached Undertow **agent command menu**. It is bound to the selected agent and shows only commands the GUI console can run. `help` uses the terminal console's section and row renderer, and loaded BOF, native, and WASM commands appear with their packaged descriptions. The command area does not start an OS shell. The separate **Live shell** tab requires an explicit **Start live shell** click.


The client stores console commands and streamed output, including foreground runs started in **Modules**, in its restricted local GUI database. Reopen Console after a client restart to see that history. Background module output belongs to the server's retained **Jobs** view. Console history can include sensitive command arguments and output; protect the client host and its GUI database accordingly. The history is bounded to the latest 1,000 entries per agent, and a single entry is capped at 64 KiB.

**Files** provides explicit browse, upload, download, and mkdir controls. Downloads use the existing transfer stream and verify the resulting hash. **Screenshots** lists server-retained captures; listing screens and capturing a screen are separate actions. **Host** preserves each operation's last result on the server until the operator reruns it. When an agent disconnects, retained Console, Jobs, Screenshots, Host results, and last known Overview remain visible; controls that contact the agent are unavailable.

### Console and module commands

The bound console accepts the agent-side command set from Undertow's Go parser: `show`, `agent events`, `agent shutdown`, `session kill`, host operations (`pwd`, `ls`, `stat`, `mkdir`, `rm`, `whoami`, `ps`, `privileges`, `env`, `interfaces`, `dns`, `route-table`), `screens`, `screenshot`, file transfer, direct `exec`, jobs, relay listeners, client routes and forwards, one-shot script/WASM/native/BOF runs, and loaded module commands. Its `help` groups those commands using the terminal menu's section and row renderer. It intentionally excludes navigation commands such as `back`, `use`, `quit`, and top-level server commands because the workspace already identifies the target agent.

The **Modules** page lists the local module bank and packaged BOF commands. Load or unload explicitly, select a connected agent, supply arguments and optional input, then run in foreground or background. Foreground output appears in Console history even when the run started from Modules. Background output is retained as a server job. Loading a module into the client bank does not execute it on any agent.

### Files and transfers

Open **Files**, click **Browse**, then choose a directory or file. A single click inspects metadata; a directory opens on double-click, Enter, or **Open folder**. **Choose local file** and **Upload** send a file to the selected remote path. **Download verified file** retrieves a selected regular file and checks its SHA-256 before offering it to the browser. **Create folder** uses the existing host `mkdir` operation. The transfer has a 512 MiB GUI upload/download limit; the terminal transfer path remains available for other workflows.

**Transfers** shows server-retained transfer IDs, agent, path, authenticated operator, progress, state and checksum for file transfers started from the GUI Files tab. A running transfer changes to interrupted when its client session goes away or the server restarts. Transfer bytes remain on the normal Undertow stream; the server's transfer table stores metadata only. Browser downloads are staged briefly on the local client, then removed when the client exits. Terminal `upload` and `download` continue to use the existing transfer stream but are not yet indexed in this GUI transfer history.

### Jobs, screenshots and host results

**Jobs** supports explicit background starts, state and output inspection, full output download, cancellation and deletion. The output belongs to the Undertow server and remains available after a browser or client restart. Use a job for long-running work rather than keeping a browser command open. A lost agent's completed output remains readable; controls requiring the agent are disabled.

**Screenshots** first shows retained server history. Click **List screens** to ask the agent which displays are available, then **Capture screen** beside the chosen display. Clicking a historical item views the stored image without contacting the agent. **Download PNG** saves that capture. Opening Screenshots never captures automatically. A Windows capture needs an available interactive desktop; a disconnected or locked desktop can still allow enumeration while capture reports an error.

**Host** shows the last server-retained result for each built-in host operation, including privileges and network information. Clicking **Run** replaces that operation's retained result with the new one. Merely switching tabs or reopening the GUI reads the prior result.

## Manage shared and local settings


**Settings → Client** controls this client's VPN and internal routing modes when it has a TUN device. **Carriers** lists server listeners and starts or stops DNS, QUIC, and WebSocket listeners. The local client refuses to stop the carrier carrying its own control session, including a force-stop request. Use another connected carrier first. **Status** shows session and server details; **Logs** shows recent server worker logs and a live follow view.

**Routes** distinguishes an agent's advertised networks, server routes, this client's saved accepted/custom routes, and routes installed in the local OS. Use an advertised CIDR choice for normal acceptance; enter a custom CIDR only when an agent can reach a network it did not advertise. The agent Overview gives one-click acceptance and toggles for saved routes. Disabling a saved route removes its active server acceptance and local installation while keeping the saved choice for later re-enablement. An operator-only client can inspect routes but cannot install them without a TUN device.

**Forwards** manages this client's agent TCP forwards: choose a connected agent, an agent bind address, and a client target address. The view lists active forwards and requires confirmation to stop one. **Relays** manages child-agent relay listeners separately. Neither page starts an agent-side shell.

**Identity** displays the server-authenticated operator account. Team Leaders can create accounts, promote or demote Team Leaders, disable or enable accounts, and reset passwords. Account changes close the affected client sessions. The final active Team Leader cannot be removed or demoted. See [Operator authentication](operator-authentication.md).

## Payloads and relays

**Payloads** manages reusable profiles and server-built artifacts, including downloads, server HTTPS hosting, and deploy helper generation. A new profile suggests the configured server public host and the connected carrier's port; check that the target can actually reach the address before building. The artifact's profile snapshot does not change when a later profile edit is saved.

**Payloads → Profiles** has **Idle sleep interval** and **Sleep jitter** fields. New profiles default to zero interval, preserving persistent sessions. A build embeds the current profile values; editing a profile later affects future builds only. The artifact build summary displays the interval and jitter selected for that build.

For a child agent, start a relay on the parent, then click **Create child payload** on that listener. The profile form selects the relay carrier. For TCP, omitting the bind listens on all parent IPv4 interfaces at `0.0.0.0:8443`; enter the parent's IP or hostname reachable by the child as its connection destination, since `0.0.0.0` is not a dial address. **Bound** means the local socket opened, not that a child can connect. On Windows, the effective inbound firewall policy must allow the agent executable path and chosen TCP port; elevation alone does not grant access. A different executable path can trigger a new firewall permission prompt or automatic block. See [Windows inbound permission and port scope](topology-and-relays.md#windows-inbound-permission-and-port-scope). Save the profile, build for the chosen platform, and download the artifact. You can also host it on the server's HTTPS payload listener and generate a deploy helper there. Generating or downloading a helper never executes it on an agent.

The relay listener carries Undertow child sessions. To distribute a built artifact through that same listener, select it under **Payloads → Artifacts → Host through an agent**. Choose the connected parent, an active relay, and the parent's child-reachable IP or DNS name. For TCP, **Enable HTTPS downloads** adds an opaque download URL on the relay port. For SMB, **Enable pipe delivery** adds an opaque token on the named pipe and offers a pinned Windows PowerShell helper. The parent requests each artifact from the server over its existing authenticated session; it does not hold the binary. Both paths have explicit disable controls that leave child sessions running. **Preview diagnostic script** generates an optional pinned HEAD check for a workstation the operator can already access. It is not required before deployment, since the child may not exist yet. Generating either helper does not execute it. Artifact revocation, deletion, parent disconnect, or server restart invalidates active relay delivery. See [Topology and relays](topology-and-relays.md) for the relay connection model and [Payload deployment](agent-distribution.md) for the full console workflow.

For a Windows parent and child, **Relays** also offers an **SMB named pipe** carrier. Choose a Windows parent and enter its local bind as `\\.\pipe\NAME`. **Create child payload** then selects an SMB relay profile; enter the child's reachable address as `\\PARENT_HOST\pipe\NAME` and build a Windows artifact. The child appears as a separate `relay-smb` agent in Topology. A browser cannot download from a named pipe directly; use the generated PowerShell helper or download the artifact through the operator client. Follow the [SMB named-pipe relay guide](smb-named-pipe-relays.md) for the full workflow and Windows access checks.

Payload **Retrieval settings** controls the server's public HTTPS host and retrieval path separately from a profile's embedded connection address. Changing the path rotates hosted download tokens and invalidates earlier URLs. The artifact page shows each build's profile snapshot, platform, size, hash, download, hosted URL, helper preview and lifecycle controls. A built artifact is not an agent until an operator deploys and starts it.

## Data ownership and retention

The server owns operational records, operator accounts, and shared audit history. The local client stores the graph layout, module bank references, and bounded console history. Closing a browser tab does not disconnect agents or stop jobs. A client or server restart may interrupt active streams; completed server jobs and screenshots remain available through their retained records. Do not use the browser's local storage as an operational record.

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
