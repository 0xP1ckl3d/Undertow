# Interactive consoles

Undertow has two interactive consoles. Run `undertow server` in a terminal on the **server host** to start its worker and open the operator console. Run `undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify` on the **VPN client host** to start its worker and open the client console when using the server's default self-signed TLS certificate. Both consoles can select an agent, so commands inside its menu do not require a long agent ID. The client console manages only that client's accepted routes and internal routing mode. The server needs `--tun` only if applications on the server host itself need routed agent access.

See [getting started](getting-started.md) for enrollment, fingerprint, and privilege setup. In the examples below, `undertow` means `./bin/undertow` on Linux or `.\bin\undertow.exe` on Windows. Use `sudo` for server console access if the elevated server owns `control.key`; a VPN client needs elevation to install routes.

## Navigate either console

| Command | Action |
| --- | --- |
| `help` | Show the full command menu for the current level. The main menu shows server/client controls; `use NUMBER` opens agent actions. |
| `help TOPIC` | Show detailed usage for a command or group, such as `help relay`, `help route`, `help run-script`, or `help run-wasm`. |
| `clear` or `cls` | Clear the interactive screen without changing the selected agent or worker. |
| `status` or `status --json` | Show active server listeners, each peer's carrier, agents, VPN clients, routes, and counters. |
| `agents` | List connected agents by current number, hostname, short ID, virtual IP, carrier and relay parent. |
| `use NUMBER` | Enter the numbered agent's menu. An unambiguous hostname or ID prefix also works. `select NUMBER` is an alias within the console. |
| `back` | Return from an agent menu to the main menu. |
| `routes` | Show routes; from an agent menu, filter to that agent. |
| `quit` or `exit` | Detach the server console without stopping its worker. In the VPN client console, stop the VPN and remove its owned routes. |

## Server listeners and agent topology

The default server opens DNS UDP/53, WebSocket TCP/443 and QUIC UDP/443. Use these commands in the **server** console:

```text
transports
stop transport quic
start transport quic self-signed
start transport websocket tls-cert ./server.crt tls-key ./server.key
topology
```

`transports` shows network, listen address, TLS mode and active session count. An active carrier cannot be stopped normally; the error shows agent and client counts. `stop transport NAME force` deliberately closes its sessions, while other carriers continue. A stopped listener can be restarted. `start transport NAME listen IP:PORT` chooses a nondefault address; transport names ignore case. Certificate paths are resolved on the server worker host. `status` and `undertow status` also show the listener table and each peer's carrier.

Select a parent agent to open a child-agent relay:

```text
agents
use 1
relay start 10.20.1.15:8443
relay list
topology
relay stop 10.20.1.15:8443
```

No agent listens for children until `relay start` succeeds. The omitted bind defaults to loopback `127.0.0.1:8443`; specify an internal interface for another host. The child runs `undertow agent --transport relay --server 10.20.1.15:8443 --fingerprint FINGERPRINT --token-file token.key` with its own agent key. `topology` shows it under the selected parent; it can be selected and controlled as an independent agent. `--deny=relay` on a parent rejects the listener command. See [topology and relay guidance](topology-and-relays.md).

Agent numbers can change after connections change. Run `agents` again before selecting by number. Type `help` after `use` to see that agent's menu. Quote a path or argument containing spaces with single or double quotes. The console parses quotes; it does not expand shell variables or run shell syntax.

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
| `job start PROGRAM [ARGS]` | Selected agent | Start a task that keeps running while you use or detach the console. |
| `jobs` | Either menu | List numbered tasks; inside an agent, list that agent's tasks. |
| `jobs NUMBER` | Either menu | Show a task from the last `jobs` list. |
| `job show NUMBER|ID` | Either menu | Show task state, timestamps, exit status, and output size. |
| `job output NUMBER|ID` | Either menu | Read retained output, including while the task runs. |
| `job cancel NUMBER|ID` | Either menu | Stop a running task. |
| `show` | Selected agent | Show detailed agent telemetry and discovered networks. |
| `agent show AGENT_ID` | Main menu | Show that agent's detailed telemetry. |
| `exec AGENT_ID PROGRAM [ARGS]` | Main menu | Start one program directly on the named agent. |
| `HOST_OP AGENT_ID [ARGS]` | Main menu | Run a built-in host operation on the named agent. See the table below. |
| `logs` / `logs follow` | Either menu | Show recent worker logs or follow new lines until Enter. |
| `background` / `quit` / `exit` | Either menu | Detach the console while the server continues. |
| `stop` | Either menu | Gracefully stop the server worker. |

`route add` on the server affects server managed routing. The server's optional proxy TUN is needed for the server host to send ordinary IP traffic to that route. Agent selection in the interactive menu is local to the console; the separate `undertow agent select AGENT_ID` command changes the server's API selection.

Each agent also reports structured IPv4 routes when available. The client console's `routes` command labels directly attached networks and networks reached through a gateway, including the interface and route source. It shows the agent's default route separately for information. Use `route accept CIDR` in the selected-agent menu for a reported candidate, or `route add CIDR` for another route you know that agent can reach. Both install only on this VPN client. A prefix overlapping this client's existing local networks is rejected.

After detaching, run `sudo undertow server attach` to return. A server started with `--background` can be attached the same way. If startup used custom `--pid-file`, `--control-listen`, `--control-token-file`, or `--log-file` paths, supply the matching `--pid-file`, `--control`, `--control-token-file`, or `--log-file` on `server attach`. `undertow console` remains available to connect directly to a server's loopback API, including a `server --foreground` worker. The control credential stays on the server host and differs from the enrollment token shared with agents and clients.

## VPN client console

A terminal launch of `client --internal` or `client --vpn` opens the console by default. Internal-only access can be configured entirely here after the agent connects; no server `route add` is required. For example:

```text
sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
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
| `route del CIDR` | Either menu | Remove a locally accepted or manual route. |
| `internal on` / `internal off` / `internal status` | Either menu | Change or inspect use of server configured global pivot routes for new flows. Explicitly accepted client routes remain active. |
| `vpn on` / `vpn off` / `vpn status` | Either menu | Change or inspect Internet egress through Undertow without stopping the client. Enabling verifies public egress and rolls back its two `/1` routes on failure. |
| `upload LOCAL REMOTE` | Selected agent | Copy a client file to the agent. |
| `download REMOTE LOCAL` | Selected agent | Copy an agent file to the client. |
| `upload AGENT_ID LOCAL REMOTE` | Main menu | Upload to the named agent. |
| `download AGENT_ID REMOTE LOCAL` | Main menu | Download from the named agent. |
| `HOST_OP AGENT_ID [ARGS]` | Main menu | Run a built-in host operation on the named agent. See the table below. |
| `background` | Either menu | Detach the console while the VPN and its routes keep running. |

Accepted and manual routes are saved locally in `client-routes.json` by default and reapplied after reconnect. Change the path with `client --routes-file PATH`. The client can add a routed subnet that is reachable from an agent even if it is absent from that agent's directly attached subnet list. Make sure the agent actually has a route to the target subnet.

`vpn on|off` changes only Undertow's Internet routes; accepted agent routes stay installed. `internal on|off` changes server configured routes for new flows. Both settings survive carrier reconnects while the client worker runs. Restarting the client uses the startup flags again.

`vpn status` shows whether Internet egress is enabled, the carrier (`dns`, `quic`, or `websocket`), and the last public IPv4 address verified on this connection. `internal status` lists currently installed accepted and server routes with their agent hostname and full ID, for example `10.10.10.0/24 via TALON (d24bb3...)`. The `routes` and server route views use the same agent label.

After `background`, run `undertow client attach` to return. A VPN started with `client --background` can be attached the same way. Use the same `--pid-file PATH` on `attach` or `--stop` if startup used a custom PID file. `undertow client --stop` gracefully stops a detached client and removes its owned routes. `quit` in an attached VPN console does the same. A nonterminal invocation runs without a prompt unless `--interactive` is supplied.

### Expose a client TCP service on an agent

In the VPN client console, select the agent that should listen and add a forward. For a web server listening on the **client** at `0.0.0.0:8080`:

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

For a command that should continue while you use the console, run `job start /usr/bin/find /srv -type f` on a selected Linux agent or `job start powershell.exe -NoProfile -File C:\\Scripts\\audit.ps1` on a Windows agent. The server assigns a job ID. `jobs` lists running and finished tasks with numbers. Use `jobs 1` or `job show 1` for details, `job output 1` to read output accumulated so far, and `job cancel 1` to stop one. `jobs show 1` and `jobs output 1` also work. Numbers refer to the last list shown in that console; full IDs remain valid if the list changes. Jobs continue through client console detach/reattach while the agent remains connected. They use the agent's `interactive` capability. The server retains up to 256 KiB of the latest output per job and marks truncated output. A VPN client sees only its own jobs; the server operator sees all jobs. The server holds up to 512 job records for its lifetime.

For a memory-backed script, select an agent and use `run-script bash ./check.sh` or `run-script powershell ./audit.ps1`. The file is read on the console machine and sent through Undertow to the interpreter's stdin on the agent; the agent creates no script file. Add `--background` before the language to create a job. Scripts require the independent `scripts` capability, have a 1 MiB source limit and a 10 minute runtime limit. Foreground output streams separately from stdout and stderr; background output is retained in the shared job manager.

To run a WASI module, use `run-wasm ./tool.wasm option`, `run-wasm --stdin ./input.txt ./tool.wasm`, or `run-wasm --background ./long-task.wasm` in a selected-agent console. The module is instantiated from memory and receives explicit arguments and optional stdin. It has no preopened WASI filesystem or WASI network sockets, but the public `undertow_host_v1` imports let it read agent-side files and make bounded outbound network requests with the agent process's privileges. Agent limits are 4 MiB for module bytes, 64 KiB for stdin, 16 MiB guest linear memory, 4 MiB combined output, two minutes of execution and two simultaneous runs. The independent `wasm` capability controls both foreground and background runs; denying `hostops` does not disable these WASM imports. Background runs use the same job IDs, ownership, output retention and cancellation as other tasks. See the [WASM developer guide](wasm-development.md) for the host API and its limits.

On a Windows amd64 agent, `run-native ./tool.module option` loads a native Windows DLL module. `run-native --data ./payload.bin ./tool.module` supplies opaque bytes, and `run-native --background ./tool.module` starts a standard job. Use `job stop NUMBER` to request cooperative cancellation. See the [native module guide](native-modules.md).

To reuse a compatible BOF, inspect it locally with `undertow bof inspect ./tool.o`, then run `run-bof ./tool.o` on a selected Windows amd64 agent. Use `--format` or a sidecar manifest for Beacon arguments. `run-bof --background ./tool.o` uses the same jobs and `native` capability; `job stop NUMBER` terminates its isolated worker. See the [BOF compatibility guide](bof-compatibility.md).

For frequent use, `load bof ./tool.o tool` registers `tool` locally. Then `help tool` shows its sidecar metadata and `tool ARG... --background` runs it on the selected agent. Switch agents without reloading. `bofs` lists registrations, `unload bof tool` removes one, and closing the console process clears them all. If the object has no sidecar, supply its format once while loading, for example `load bof ./tool.o tool --format "zi"`.

Uploads and downloads pass through the server and verify SHA-256. The destination parent directory must exist and the destination file must not already exist. Relative local paths resolve from the console's working directory; relative remote paths resolve from the agent process's working directory. The client console shows bytes, total, percentage, and current rate while data moves, then reports size and SHA-256 on completion. Press Ctrl-] during a transfer to cancel it; partial temporary files are removed. An attached console receives progress over the local loopback control socket, so progress updates do not consume DNS control frames. Transfers have a 30 minute limit. Agents can reject these independently with `--deny=upload` or `--deny=download`.

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
