# Interactive consoles

Client consoles authenticate with the server using `--operator ID --operator-password-file PATH` in addition to the existing transport enrollment flags. The GUI launched by that client uses the same authenticated account. Team Leaders can type `operators me`, `operators list`, `operators create ID "Display Name" operator|team_leader PASSWORD_FILE`, `operators role ID operator|team_leader`, `operators disable ID`, `operators enable ID`, `operators reset ID PASSWORD_FILE`, or `operators revoke ID`. Operators can type `operators me`; account changes require a Team Leader. Password files are read on the client host. See [Operator authentication](operator-authentication.md).

Undertow has two interactive consoles. Run `undertow server` in a terminal on the **server host** to start its worker and open the operator console. Run `undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify` on the **VPN client host** to start its worker and open the client console when using the server's default self-signed TLS certificate. Use `client --operator-only` to connect and operate agents without a TUN device. Both consoles can select and operate agents, manage deployment payloads, inspect topology, and start or stop relay and server carrier listeners. The client console manages that client's routes, VPN mode, and agent-side forwards; it refuses to stop the server carrier carrying its own session. The server needs `--tun` only if applications on the server host itself need routed agent access.

See [getting started](getting-started.md) for enrollment, fingerprint, and privilege setup. In the examples below, `undertow` means `./bin/undertow` on Linux or `.\bin\undertow.exe` on Windows. Use `sudo` for server console access if the elevated server owns `control.key`; a VPN client needs elevation to install routes.

## Navigate either console

| Command | Action |
| --- | --- |
| `help` | Show the full command menu for the current level. The main menu shows server/client controls; `use NUMBER` opens agent actions. |
| `help TOPIC` | Show detailed usage for a command or group, such as `help relay`, `help route`, `help run-script`, or `help run-wasm`. |
| `clear` or `cls` | Clear the interactive screen without changing the selected agent or worker. |
| `status` or `status --json` | Show active server listeners, each peer's carrier, agents, VPN clients, routes, and counters. |
| `agents` | List connected and intentionally sleeping agents by current number, hostname, ID, virtual IP, carrier, state and relay parent. Select either state with `use`. |
| `use NUMBER` | Enter the numbered agent's menu. An unambiguous hostname or ID prefix also works. `select NUMBER` is an alias within the console. |
| `back` | Return from an agent menu to the main menu. |
| `routes` | Show routes; from an agent menu, filter to that agent. |
| `quit` or `exit` | Detach the server console without stopping its worker. In the VPN client console, stop the VPN and remove its owned routes. |

## Team coordination

The connected **client** console can read and send the same server-retained messages and assignments as the GUI. These commands use the account authenticated when the client connected; an agent selection is not needed. The server console has no operator session for team chat, so use an authenticated client.

```text
team
team say I will review the new route
team roster
team dm alice
team dm alice Please check the transfer result
team tasks
team task add alice "Review route acceptance" "Confirm the client path"
team task show TASK_ID
team task start TASK_ID
team task done TASK_ID
team task reopen TASK_ID
team task cancel TASK_ID
```

`team` shows recent shared messages and task activity. `team dm ID` shows only the conversation between your account and that operator; `team dm ID MESSAGE` sends to that operator. Only the sender and recipient can read a direct conversation. Task changes may be made by the assignee, creator, or a Team Leader. Messages and assignments persist on the server across client restarts; task changes also appear in the shared team timeline. See [Team conversations and assignments](gui.md#team-conversations-and-assignments) for the browser workflow.

## Payload deployment

Type `payload` for the four-step profile → build → host → run guide. Both the server console and an authenticated client console can manage profiles and payloads, including downloading built binaries:

```text
payload profile create office server=SERVER_IP:443 transport=quic sleep-seconds=30 sleep-jitter=20
payload profiles
payload profile show office
payload profile edit office routes=10.20.0.0/16
payload build office windows amd64
payload list
payload show PAYLOAD_ID
payload download PAYLOAD_ID
payload download PAYLOAD_ID ./staging/worker.exe
payload host PAYLOAD_ID
payload url PAYLOAD_ID
payload hosted
payload deploy-script PAYLOAD_ID powershell
payload unhost PAYLOAD_ID
payload revoke PAYLOAD_ID
payload delete PAYLOAD_ID
agents
use 1
show
agent events
agent sleep
agent sleep 30 20
session kill
agent shutdown
```

`payload build` prints a 24-character **payload ID** for the build and the binary's **server file** path. Each running copy creates a separate 32-character agent ID when it connects. The server file is not an endpoint install path. `payload host` prints the endpoint **download URL** and opaque **download path**; `payload show` and `payload url` retrieve them again. Use `payload retrieval-path` to view the public prefix and `payload retrieval-path set /downloads/` to change it without restarting. Changing it rotates hosted download tokens, permanently invalidating earlier URLs; `payload hosted` lists the new URLs. Use `payload profile delete NAME` to remove a reusable profile. Editing one leaves existing payloads unchanged. Verify the download's SHA-256 before launching it with no arguments. `session kill` closes a connection and permits reconnect; `agent shutdown` stops the configured process. `payload unhost`, `revoke`, and `delete` separately control download, future enrollment, and the server-side file/record. Normal packaged-agent operation writes no local runtime files; the server retains bounded lifecycle events. Older `agent profile/build/artifacts/host` forms still work as aliases. See [payload deployment](agent-distribution.md) for authentication and endpoint state.

`sleep-seconds` accepts `0–86400`, with `0` preserving the existing persistent session; `sleep-jitter` accepts `0–50` percent. `payload profile show NAME` displays the values embedded in future builds. In the selected agent menu, `agent sleep` shows the effective values and `agent sleep SECONDS JITTER` saves an override for that agent across callbacks. At the top level, use `agent sleep AGENT_ID` and `agent sleep AGENT_ID SECONDS JITTER`. Commands submitted while an agent is intentionally sleeping are retained for its next check-in. A live stream waits for that callback and keeps the session open while active. A started background job likewise keeps a live connection until it finishes; output is not yet delivered in chunks over later check-ins. Relay listeners, forwards, and accepted routes also keep the agent connected while active. Older binaries without sleep support must be rebuilt before these live controls can update them. See [Idle sleep](agent-distribution.md#idle-sleep) for the full lifecycle and carrier behavior.

For a child behind a TCP parent relay, use `payload host-agent PAYLOAD_ID PARENT_AGENT_ID RELAY_BIND PUBLIC_HOST` after starting the relay. `payload agent-hosts [PAYLOAD_ID]` lists active URLs, `payload deploy-script-agent HOST_ID powershell|shell` prints a pinned helper, and `payload unhost-agent HOST_ID` disables one URL without stopping the relay. `payload verify-script-agent HOST_ID powershell|shell` prints an optional HEAD-only diagnostic for a workstation already reachable by the operator; it does not delay or enable hosting. The parent fetches each artifact from the server over its existing session and serves downloads on the relay's existing address and port. These commands work in either console. Follow the [payload deployment guide](agent-distribution.md#host-a-payload-through-a-connected-agent) for a complete example.

## Server listeners and agent topology

The default server opens DNS UDP/53, WebSocket TCP/443 and QUIC UDP/443. These commands work in the **server or connected client** console:

```text
transports
stop transport quic
start transport quic self-signed
start transport websocket tls-cert ./server.crt tls-key ./server.key
topology
```

`transports` shows network, listen address, TLS mode and active session count. An active carrier cannot be stopped normally; the error shows agent and client counts. `stop transport NAME force` deliberately closes its sessions, while other carriers continue. A client console refuses to stop its own active carrier even with `force`; connect it through another carrier first. A stopped listener can be restarted. `start transport NAME listen IP:PORT` chooses a nondefault address; transport names ignore case. Certificate paths are resolved on the server worker host. `status` and `undertow status` also show the listener table and each peer's carrier. Bare `stop` shuts down the server worker only in its attached server console; use `quit` or `background` on a client.

Select a parent agent in either console to open a child-agent relay:

```text
agents
use 1
relay start 10.20.1.15:8443
relay list
topology
relay stop 10.20.1.15:8443
```

No agent listens for children until `relay start` succeeds. With no bind argument, TCP listens on `0.0.0.0:8443` on the parent. Use a specific parent interface if you want to limit where it listens. The child must dial the parent's reachable IP or hostname, such as `10.20.1.15:8443`; `0.0.0.0` is never a child destination. The child runs `undertow agent --transport relay --server 10.20.1.15:8443 --fingerprint FINGERPRINT --token-file token.key` with its own agent key. `topology` shows it under the selected parent; it can be controlled as an independent agent. `--deny=relay` on a parent rejects the listener command. See [topology and relay guidance](topology-and-relays.md).

For a Windows parent, `relay start \\.\pipe\NAME` opens a named pipe. A Windows child uses `--transport relay-smb --server \\PARENT_HOST\pipe\NAME`; build a Windows payload with `transport=relay-smb`. To deliver that artifact over the same pipe, use `payload host-agent PAYLOAD_ID PARENT_AGENT_ID \\.\pipe\NAME PARENT_HOST`, then `payload deploy-script-agent HOST_ID powershell`. This creates a pinned helper, not a browser URL; `payload download PAYLOAD_ID` still retrieves the build locally. See the [SMB named-pipe guide](smb-named-pipe-relays.md).

Agent numbers can change after connections change. Run `agents` again before selecting by number. Type `help` after `use` to see that agent's menu. Quote a path or argument containing spaces with single or double quotes. The console parses quotes; it does not expand shell variables or run shell syntax.

At startup the console loads packaged BOFs, native modules, and WASM modules from the local `modules/` directory. Type `help` to see them, then `help bof-winver`, `help module-wininfo`, or `help wasm-triage` for usage. Loaded commands can run against any selected agent and can use trailing `--background`. See [local module bank](module-bank.md) for where to place additional artifacts and sidecar help files.

When input is an interactive terminal, Up and Down recall commands. Help uses colour on supported terminals and honours `NO_COLOR`. Tab completes command names and local file paths for `run-script`, `run-wasm` (including `--stdin`), `run-native` (including `--data`), `upload`, and the local destination of `download`. Paths are read from the **console host**, not the agent; remote file arguments do not use local path completion. Tab can enter directories and complete quoted names containing spaces. `help TOPIC` and `route`/`relay` subcommands also complete. The consoles announce an agent connection or loss without flooding the prompt with routine transport logs. In the VPN client console, Ctrl+C asks for confirmation before stopping the VPN.

## Server console

Start the server on the server host; its console opens automatically. Use `sudo undertow server --tun` only when applications on the **server host** need routed access through an agent. After an agent joins, enter these commands in the console:

```text
agents
use 1
show
route add 10.20.0.0/16
routes
back
status
background
```

| Command | Where | Action |
| --- | --- | --- |
| `route add CIDR` | Selected agent | Create a global internal route via this agent. |
| `route add CIDR AGENT_ID` | Main menu | Create a global internal route via the named agent. |
| `route del CIDR` | Either menu | Remove a global internal route. |
| `exec PROGRAM [ARGS]` | Selected agent | Start one program directly on this agent. |
| `shell [PROGRAM ARGS]` | Selected agent | Open a live command session; Ctrl-] closes the shell and returns to the Undertow menu. |
| `run-script [--background] bash|powershell LOCAL_FILE` | Selected agent | Stream local script source to the interpreter without a script file on the agent; background runs appear in `jobs`. |
| `run-wasm [--background] [--stdin LOCAL_FILE] MODULE_FILE [ARGS]` | Selected agent | Instantiate a WASI module in agent memory. Output streams live or enters `jobs`. |
| `run-native [--background] [--data LOCAL_FILE] MODULE_FILE [ARGS]` | Selected Windows amd64 agent | Load a native `.module` DLL with direct Windows API access. Output streams live or enters `jobs`. |
| `run-bof [--background] [--format FORMAT] OBJECT.o [ARGS]` | Selected Windows amd64 agent | Load a compatible BOF COFF object with Beacon API imports. Output streams live or enters `jobs`. |
| `load bof FILE [NAME] [--format FORMAT]` | Any console menu | Register a BOF as a local command for this console session. |
| `bofs`; `unload bof NAME` | Any console menu | List or remove local BOF commands. |
| `load module FILE [NAME]`; `load wasm FILE [NAME]` | Any console menu | Register a native or WASM module as a local command. |
| `modules`; `unload module|wasm NAME` | Any console menu | List all loaded artifacts or remove a native/WASM command. |
| `job start PROGRAM [ARGS]` | Selected agent | Start a task that keeps running while you use or detach the console. |
| `jobs` | Either menu | List numbered tasks; inside an agent, list that agent's tasks. |
| `jobs NUMBER` | Either menu | Show a task from the last `jobs` list. |
| `job show NUMBER|ID` | Either menu | Show task state, timestamps, exit status, and output size. |
| `job output NUMBER|ID` | Either menu | Show small output; offer a download when it is too large for the console. |
| `job save NUMBER|ID [LOCAL_FILE]` | Either menu | Download complete output to a file on the console host. |
| `job delete NUMBER|ID` | Either menu | Delete a finished job and its server output to free space. |
| `job cancel NUMBER|ID` | Either menu | Stop a running task. |
| `show` | Selected agent | Show detailed agent telemetry and discovered networks. |
| `agent show AGENT_ID` | Main menu | Show that agent's detailed telemetry. |
| `agent rename AGENT_ID "NAME"` | Main menu | Set a server-shared nickname without changing the agent's hostname or ID. Use `""` to clear it; omit the ID inside a selected agent. |
| `exec AGENT_ID PROGRAM [ARGS]` | Main menu | Start one program directly on the named agent. |
| `HOST_OP AGENT_ID [ARGS]` | Main menu | Run a built-in host operation on the named agent. See the table below. |
| `logs` / `logs follow` | Either menu | Show recent worker logs or follow new lines until Enter. |
| `background` / `quit` / `exit` | Either menu | Detach the console while the server continues. |
| `stop` | Either menu | Gracefully stop the server worker. |

`route add` on the server affects server managed routing. The server's optional proxy TUN is needed for the server host to send ordinary IP traffic to that route. Agent selection in the interactive menu is local to the console; the separate `undertow agent select AGENT_ID` command changes the server's API selection.

After detaching, run `sudo undertow server attach` to return. A server started with `--background` can be attached the same way. If startup used custom `--pid-file`, `--control-listen`, `--control-token-file`, or `--log-file` paths, supply the matching `--pid-file`, `--control`, `--control-token-file`, or `--log-file` on `server attach`. `undertow console` remains available to connect directly to a server's loopback API, including a `server --foreground` worker. The control credential stays on the server host and differs from the enrollment token shared with agents and clients.

## VPN client console

A terminal launch of `client --internal` or `client --vpn` opens the console by default. Internal-only access can be configured entirely here after the agent connects; no server `route add` is required. Start the client from its own terminal, then use the commands below at its prompt:

```sh
sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

```text
agents
use 1
routes
route accept 10.20.0.0/16
route add 10.30.0.0/16
upload ./notes.txt /tmp/notes.txt
download /tmp/result.txt ./result.txt
back
background
```

| Command | Where | Action |
| --- | --- | --- |
| `route accept CIDR` | Selected agent | Accept one subnet advertised by that agent on this client. |
| `route accept CIDR AGENT_ID` | Main menu | Accept a named agent's advertised subnet. |
| `route add CIDR` | Selected agent | Add a manual local route via that agent, even when it has not advertised the subnet. |
| `route add CIDR AGENT_ID` | Main menu | Add a manual local route via a named agent. |
| `route del CIDR` / `route delete CIDR` | Either menu | Remove a locally accepted or manual route, including one owned by another agent. |
| `internal on` / `internal off` / `internal status` | Either menu | Change or inspect use of server configured global pivot routes for new flows. Explicitly accepted client routes remain active. |
| `vpn on` / `vpn off` / `vpn status` | Either menu | Change or inspect Internet egress through Undertow without stopping the client. Enabling verifies public egress and rolls back its two `/1` routes on failure. |
| `upload LOCAL REMOTE` | Selected agent | Copy a client file to the agent. |
| `download REMOTE LOCAL` | Selected agent | Copy an agent file to the client. |
| `upload AGENT_ID LOCAL REMOTE` | Main menu | Upload to the named agent. |
| `download AGENT_ID REMOTE LOCAL` | Main menu | Download from the named agent. |
| `HOST_OP AGENT_ID [ARGS]` | Main menu | Run a built-in host operation on the named agent. See the table below. |
| `background` | Either menu | Detach the console while the VPN and its routes keep running. |

Accepted and manual routes are saved locally in `client-routes.json` by default and reapplied after reconnect. Change the path with `client --routes-file PATH`. The client can add a routed subnet that is reachable from an agent even if it is absent from that agent's directly attached subnet list. Make sure the agent actually has a route to the target subnet.

If a saved route belongs to an agent that has disconnected, `route accept CIDR` in another agent's menu transfers that route to the selected agent. A connected owner blocks reassignment; use `route del CIDR` to remove the saved route first. This works from either menu.

Each agent also reports structured IPv4 routes when available. In the client console, `routes` labels directly attached networks and networks reached through a gateway, including the interface and route source. It shows the agent's default route separately for information. Use `route accept CIDR` in the selected-agent menu for a reported candidate, or `route add CIDR` for another route you know that agent can reach. Both install only on this VPN client. A prefix overlapping this client's existing local networks is rejected.

`vpn on|off` changes only Undertow's Internet routes; accepted agent routes stay installed. `internal on|off` changes server configured routes for new flows. Both settings survive carrier reconnects while the client worker runs. Restarting the client uses the startup flags again.

`vpn status` shows whether Internet egress is enabled, the carrier (`dns`, `quic`, or `websocket`), and the last public IPv4 address verified on this connection. `internal status` lists currently installed accepted and server routes with their agent hostname and full ID, for example `10.10.10.0/24 via TALON (d24bb3...)`. The `routes` and server route views use the same agent label.

After `background`, run `undertow client attach` to return. A VPN started with `client --background` can be attached the same way. Use the same `--pid-file PATH` on `attach` or `--stop` if startup used a custom PID file. `undertow client --stop` gracefully stops a detached client and removes its owned routes. `quit` in an attached VPN console does the same. A nonterminal invocation runs without a prompt unless `--interactive` is supplied.

### Expose a client TCP service on an agent

For a complete setup, multiple services, and troubleshooting, see [remote port forwarding](remote-port-forwarding.md).

In the VPN client console, select the agent that should listen and add a forward. For a web server listening on the **client** at `127.0.0.1:8080`:

```text
agents
use 1
forward add 0.0.0.0:8080 127.0.0.1:8080
forward list
forward del 0.0.0.0:8080
```

`0.0.0.0:8080` is the **agent-side bind**; hosts able to reach that agent address can connect. `127.0.0.1:8080` is the target on the VPN client, so the client web server can listen on either `127.0.0.1:8080` or `0.0.0.0:8080`. Test from a host able to reach the agent: `curl http://AGENT_IP:8080/`. The agent must allow TCP listener binds (`--deny=listeners` disables them), and its firewall must allow the chosen port. A busy port produces a bind error.

From the main VPN menu, use `forward add AGENT_ID AGENT_BIND CLIENT_TARGET`, `forward list [AGENT_ID]`, or `forward del AGENT_ID AGENT_BIND`. Forwards are scoped to the VPN client's current session and the named agent. Detaching the console with `background` keeps them active. A client or agent disconnect closes its listeners; add them again after reconnect. Only numeric IPv4 addresses are accepted, and the client target must be loopback. The forward carries TCP; it does not forward UDP.

## Running an agent program and transferring files

In an agent menu, `exec whoami` starts that executable directly. To run PowerShell explicitly on a Windows agent, use `exec powershell.exe -NoProfile -Command whoami`. `exec` has no implicit operating system shell, so shell operators are not interpreted unless you explicitly start a shell program. One-shot execution has a 30 second limit and captures up to 32 KiB each of standard output and standard error. The agent's `--deny=exec` setting rejects it.

Use `shell` in the selected-agent menu for a long-lived session. On a Linux agent this opens `/bin/sh` with a PTY; on Windows it opens `cmd.exe` with ConPTY, which provides terminal echo, line editing, resize handling, and VT output. Use `shell /bin/bash` or `shell powershell.exe -NoProfile` to choose another program. Input and output stream in both directions until the program exits or you press Ctrl-]. The console remains connected to Undertow and other agent sessions continue. An attached client console can open a new shell after detaching and reattaching. The separate `--deny=interactive` agent setting blocks live sessions without changing one-shot `exec` or host operations.

For a command that should continue while you use the console, run `job start /usr/bin/find /srv -type f` on a selected Linux agent or `job start powershell.exe -NoProfile -File C:\\Scripts\\audit.ps1` on a Windows agent. The server assigns a job ID. `jobs` lists running and finished tasks with numbers. Use `job show 1` for details, `job output 1` for small output, `job save 1` to download complete output, `job cancel 1` to stop one, and `job delete 1` to remove a finished job and free its server output space. Jobs continue through client console detach and reattach while the agent remains connected. The server keeps output in memory through 256 KiB, then spills the complete stream to `jobs-output/AGENT_ID/JOB_ID.out` on the server. An interactive `job output` asks whether to download when output exceeds 64 KiB. `job save` defaults to `outputs/jobs/AGENT_ID/` on the client; the repository ignores that directory. Server defaults are 512 MiB per job and 4 GiB total, configurable with `--job-output-limit-mib` and `--job-output-total-mib`. A job fails with an explicit error if it exceeds either limit. A VPN client sees its own jobs after reconnecting with the same key; the server operator sees all jobs. The server holds up to 512 job records for its lifetime.

For a memory-backed script, select an agent and use `run-script bash ./check.sh` or `run-script powershell ./audit.ps1`. The file is read on the console machine and sent through Undertow to the interpreter's stdin on the agent; the agent creates no script file. Add `--background` before the language to create a job. Scripts require the independent `scripts` capability, have a 1 MiB source limit and a 10 minute runtime limit. Foreground output streams separately from stdout and stderr; background output is retained in the shared job manager.

To run a WASI module, use `run-wasm ./tool.wasm option`, `run-wasm --stdin ./input.txt ./tool.wasm`, or `run-wasm --background ./long-task.wasm` in a selected-agent console. The module is instantiated from memory and receives explicit arguments and optional stdin. It has no preopened WASI filesystem or WASI network sockets, but the public `undertow_host_v1` imports let it read agent-side files and make bounded outbound network requests with the agent process's privileges. Agent limits are 4 MiB for module bytes, 64 KiB for stdin, 16 MiB guest linear memory, 4 MiB combined output, two minutes of execution and two simultaneous runs. The independent `wasm` capability controls both foreground and background runs; denying `hostops` does not disable these WASM imports. Background runs use the same job IDs, ownership, output retention and cancellation as other tasks. See the [WASM developer guide](wasm-development.md) for the host API and its limits.

On a Windows amd64 agent, `run-native ./tool.module option` loads a native Windows DLL module. `run-native --data ./payload.bin ./tool.module` supplies opaque bytes, and `run-native --background ./tool.module` starts a standard job. Use `job stop NUMBER` to request cooperative cancellation. See the [native module guide](native-modules.md).

To reuse a compatible BOF, inspect it locally with `undertow bof inspect ./tool.o`, then run `run-bof ./tool.o` on a selected Windows amd64 agent. Use `--format` or a sidecar manifest for Beacon arguments. `run-bof --background ./tool.o` uses the same jobs and `native` capability; `job stop NUMBER` terminates its isolated worker. See the [BOF compatibility guide](bof-compatibility.md).

For frequent use, `load bof ./tool.o tool` registers `tool` locally. Then `help tool` shows its argument information and `tool ARG... --background` runs it on the selected agent. Switch agents without reloading. `bofs` lists registrations, `unload bof tool` removes one, and closing the console process clears them all. Without a sidecar or `--format`, supplied arguments default to ANSI strings. For typed arguments, supply one format code per argument while loading, for example `load bof ./tool.o tool --format zi` followed by `tool server01 5`. Type `help load` for the format codes and binary argument syntax.

Uploads and downloads pass through the server and verify SHA-256. A supplied local destination needs an existing parent directory, and a destination file must not already exist. Omit LOCAL from download to save under `outputs/downloads/AGENT_ID/` on this client. Relative local paths resolve from the console's working directory; relative remote paths resolve from the agent process's working directory. The client console shows bytes, total, percentage, and current rate while data moves, then reports size and SHA-256 on completion. Press Ctrl-] during a transfer to cancel it; partial temporary files are removed. An attached console receives progress over the local loopback control socket, so progress updates do not consume DNS control frames. Transfers have a 30 minute limit. Agents can reject these independently with `--deny=upload` or `--deny=download`.

### Windows display screenshots

In the VPN client console, select a Windows agent and run `screens` to see its numbered displays, resolution, and foreground application. `screenshot` captures **all** displays as separate PNG files; `screenshot 2` captures only display 2. Files land in `outputs/screenshots/` on the client that issued the command. Use `screenshot --output ./evidence` or `screenshot 2 --output ./evidence` to choose a directory. The console prints each saved absolute path and SHA-256. The server relays the bytes without saving the images. The ignored client output tree is:

```text
outputs/
  downloads/AGENT_ID/    Default agent file downloads
  downloads/payloads/    Default payload downloads
  screenshots/           Screen captures
  jobs/AGENT_ID/         Saved job output
```

The agent needs an interactive Windows desktop session with accessible displays. A service in Session 0 or a locked or disconnected desktop may have no capturable screen; `screens` or `screenshot` reports that condition. Screen capture requires the agent's `hostops` and `download` capabilities. From the main menu, use `screens AGENT_ID` or `screenshot AGENT_ID [NUMBER] [--output DIRECTORY]`.

When the Linux client runs through `sudo`, new output directories and saved files are owned by the user who invoked `sudo`, so they can review captures and downloads without switching to root.

## Built-in agent host operations

After `agents` and `use NUMBER`, these commands run on the selected agent without requiring an operating-system-specific command line. They also work at either main menu by inserting an agent ID after the command name, for example `ls AGENT_ID /tmp`.

| Command in selected-agent menu | Result |
| --- | --- |
| `pwd` | Agent process working directory. |
| `ls [PATH]` | Names in a directory; defaults to the working directory. Directory names end with a path separator. |
| `stat PATH` | File or directory size, mode, modification time, and type; does not follow a final symlink. |
| `mkdir PATH` | Create one directory with restricted permissions. Its parent must exist. |
| `rm PATH` | Remove one file or an empty directory. It does not recursively delete. |
| `whoami` | User running the agent process. |
| `ps` | Process list. |
| `privileges` | Current user IDs and capabilities on Linux, or user/group/privilege report on Windows. |
| `env [NAME]` | Sorted process environment, or one named variable. |
| `interfaces` | Interfaces, addresses, MTUs, flags, and MAC addresses. |
| `dns` | DNS resolver configuration. |
| `route-table` | Host IPv4 and IPv6 routing tables. |

The agent's `--deny=hostops` setting blocks these built-in operations independently of arbitrary `exec`. For example, `--deny=exec,upload` leaves built-in `pwd`, `interfaces`, and `route-table` available. Results are capped at 32 KiB. On Linux, inventory uses `/proc`, `/etc/resolv.conf`, and installed `ps`/`ip` tools. On Windows, it uses installed `tasklist.exe`, `whoami.exe`, `ipconfig.exe`, and `route.exe`; those process windows remain hidden. If an OS tool is missing, the command reports its error. Avoid sharing `env` or `privileges` output publicly because it may contain secrets.

For full launch flags, see the [CLI reference](cli-reference.md). For deployment walkthroughs, see [scenarios](scenarios.md).

Back to [documentation home](README.md).
