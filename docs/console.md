# Interactive consoles

Undertow has two interactive consoles. Run `undertow console` on the **server host** to manage global routes and connected agents. Run `undertow client --vpn --server SERVER_IP:53` in a terminal on the **VPN client host** to open its local console after the VPN connects. That console manages only its own accepted routes and internal routing mode. Both consoles can select an agent, so commands inside its menu do not require a long agent ID.

See [getting started](getting-started.md) for enrollment, fingerprint, and privilege setup. In the examples below, `undertow` means `./bin/undertow` on Linux or `.\bin\undertow.exe` on Windows. Use `sudo` for server console access if the elevated server owns `control.key`; a VPN client needs elevation to install routes.

## Navigate either console

| Command | Action |
| --- | --- |
| `help` | Show the commands available at the current menu. |
| `status` or `status --json` | Show agents, VPN clients, routes, and connection counters. |
| `agents` | List connected agents by current number, hostname, short ID, and virtual IP. |
| `use NUMBER` | Enter the numbered agent's menu. An unambiguous hostname or ID prefix also works. `select NUMBER` is an alias within the console. |
| `back` | Return from an agent menu to the main menu. |
| `routes` | Show routes; from an agent menu, filter to that agent. |
| `quit` or `exit` | Leave the server console. In the VPN client console, stop the VPN and remove its owned routes. |

Agent numbers can change after connections change. Run `agents` again before selecting by number. Type `help` after `use` to see that agent's menu. Quote a path or argument containing spaces with single or double quotes. The console parses quotes; it does not expand shell variables or run shell syntax.

When input is an interactive terminal, Up and Down recall commands and Tab completes the first command word. The consoles announce an agent connection or loss without flooding the prompt with routine transport logs. In the VPN client console, Ctrl+C asks for confirmation before stopping the VPN.

## Server console

Start the server separately, then on the server host run:

```text
undertow console
agents
use 1
route add 10.20.0.0/16
routes
back
status
quit
```

| Command | Where | Action |
| --- | --- | --- |
| `route add CIDR` | Selected agent | Create a global internal route via this agent. |
| `route add CIDR AGENT_ID` | Main menu | Create a global internal route via the named agent. |
| `route del CIDR` | Either menu | Remove a global internal route. |
| `exec PROGRAM [ARGS]` | Selected agent | Start one program directly on this agent. |
| `shell [PROGRAM ARGS]` | Selected agent | Open a live command session; Ctrl-] closes the shell and returns to the Undertow menu. |
| `run-script [--background] bash|powershell LOCAL_FILE` | Selected agent | Stream local script source to the interpreter without a script file on the agent; background runs appear in `jobs`. |
| `job start PROGRAM [ARGS]` | Selected agent | Start a task that keeps running while you use or detach the console. |
| `jobs` | Either menu | List tasks; inside an agent, list that agent's tasks. |
| `job show ID` | Either menu | Show task state, timestamps, exit status, and output size. |
| `job output ID` | Either menu | Read retained output, including while the task runs. |
| `job cancel ID` | Either menu | Stop a running task. |
| `show` | Selected agent | Show detailed agent telemetry and discovered networks. |
| `agent show AGENT_ID` | Main menu | Show that agent's detailed telemetry. |
| `exec AGENT_ID PROGRAM [ARGS]` | Main menu | Start one program directly on the named agent. |
| `HOST_OP AGENT_ID [ARGS]` | Main menu | Run a built-in host operation on the named agent. See the table below. |

`route add` on the server affects server managed routing. The server's optional proxy TUN is needed for the server host to send ordinary IP traffic to that route. Agent selection in the interactive menu is local to the console; the separate `undertow agent select AGENT_ID` command changes the server's API selection.

Each agent also reports structured IPv4 routes when available. The client console's `routes` command labels directly attached networks and networks reached through a gateway, including the interface and route source. It shows the agent's default route separately for information. Use `route accept CIDR` in the selected-agent menu for a reported candidate, or `route add CIDR` for another route you know that agent can reach. Both install only on this VPN client. A prefix overlapping this client's existing local networks is rejected.

The server console uses the server's loopback API. If its address or token file was customized, pass `undertow console --control IP:PORT --control-token-file PATH`. This control credential stays on the server host. The enrollment token used by clients and agents is separate.

## VPN client console

A terminal launch of `client --internal` or `client --vpn` opens the console by default. Internal-only access can be configured entirely here after the agent connects; no server `route add` is required. For example:

```text
sudo undertow client --internal --server SERVER_IP:53 --token-file token.key
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
| `internal on` / `internal off` | Either menu | Enable or disable use of server configured global pivot routes for new flows. Explicitly accepted client routes remain active. |
| `upload LOCAL REMOTE` | Selected agent | Copy a client file to the agent. |
| `download REMOTE LOCAL` | Selected agent | Copy an agent file to the client. |
| `upload AGENT_ID LOCAL REMOTE` | Main menu | Upload to the named agent. |
| `download AGENT_ID REMOTE LOCAL` | Main menu | Download from the named agent. |
| `HOST_OP AGENT_ID [ARGS]` | Main menu | Run a built-in host operation on the named agent. See the table below. |
| `background` | Either menu | Detach the console while the VPN and its routes keep running. |

Accepted and manual routes are saved locally in `client-routes.json` by default and reapplied after reconnect. Change the path with `client --routes-file PATH`. The client can add a routed subnet that is reachable from an agent even if it is absent from that agent's directly attached subnet list. Make sure the agent actually has a route to the target subnet.

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

Use `shell` in the selected-agent menu for a long-lived session. On a Linux agent this opens `/bin/sh` with a PTY; on Windows it opens `cmd.exe` through interactive pipes. Use `shell /bin/bash` or `shell powershell.exe -NoProfile` to choose another program. Input and output stream in both directions until the program exits or you press Ctrl-]. The console remains connected to Undertow and other agent sessions continue. An attached client console can open a new shell after detaching and reattaching. The separate `--deny=interactive` agent setting blocks live sessions without changing one-shot `exec` or host operations.

For a command that should continue while you use the console, run `job start /usr/bin/find /srv -type f` on a selected Linux agent or `job start powershell.exe -NoProfile -File C:\\Scripts\\audit.ps1` on a Windows agent. The server assigns a job ID. `jobs` lists running and finished tasks; `job output ID` reads output accumulated so far; `job show ID` reports start/end times and exit code; `job cancel ID` stops one task. Jobs continue through client console detach/reattach while the agent remains connected. They use the agent's `interactive` capability. The server retains up to 256 KiB of the latest output per job and marks truncated output. A VPN client sees only its own jobs; the server operator sees all jobs. The server holds up to 512 job records for its lifetime.

For a memory-backed script, select an agent and use `run-script bash ./check.sh` or `run-script powershell ./audit.ps1`. The file is read on the console machine and sent through Undertow to the interpreter's stdin on the agent; the agent creates no script file. Add `--background` before the language to create a job. Scripts require the independent `scripts` capability, have a 1 MiB source limit and a 10 minute runtime limit. Foreground output streams separately from stdout and stderr; background output is retained in the shared job manager.

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
