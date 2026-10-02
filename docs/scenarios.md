# Deployment scenarios

These deeper deployment examples use DNS on agents and clients; [Networking modes and transports](networking-modes.md) uses QUIC for the short path and explains WebSocket and DNS alternatives. The server starts DNS, WebSocket and QUIC listeners together by default, and each agent or client chooses independently. For child agents behind another agent, see [topology and relays](topology-and-relays.md). These commands use `undertow` as shorthand for `./bin/undertow` on Linux or `.\bin\undertow.exe` on Windows. Replace all uppercase placeholders. Use the same enrollment and fingerprint settings on both ends; [Getting started](getting-started.md) explains token, password, open enrollment, and trust on first use. On Linux, prefix operator commands with `sudo` when an elevated server owns `control.key`.

## 1. Confirm an unprivileged agent

On the server, run `undertow init`, then `sudo undertow server`. On an internal network host:

```sh
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

The agent needs no sudo, adapter, or route. In the **server console** that opened with `server`:

```text
status
agents
use 1
show
```

`agents` gives each connection a number for the current console view; `show` displays its full ID, transport, capabilities, and reported networks. A connected agent alone does not install a server route. Use scenario 2 or 3 to send traffic through it. For scripts, `undertow status --json` and `undertow agent show AGENT_ID` provide the same information without the console.

## 2. Reach an internal subnet from the server

Start the server with `--tun` and choose a tunnel prefix that does not overlap any server or agent network:

```sh
sudo undertow server --tun --tun-name undertow0 --tunnel-address 172.16.254.1/24
```

Start the unprivileged agent as in scenario 1. In the **server console**, select it and add the internal prefix:

```text
agents
use 1
show
route add 10.20.0.0/16
routes
```

From a **separate server terminal**, test `curl http://10.20.0.50/` and `ping 10.20.0.50` using real destinations the agent can reach. The server's OS route for that prefix feeds its TUN, then Undertow selects the named agent; the agent opens ordinary target sockets. TCP, UDP, and ICMP echo are supported. The route remains assigned to that agent and becomes inactive when it disconnects. Use `route del 10.20.0.0/16` in the server console to remove it. ICMP on Linux may use the system `ping` command when unprivileged ping sockets are unavailable. For scripts, use `undertow route add 10.20.0.0/16 --via AGENT_ID`, `undertow route list`, and `undertow route del 10.20.0.0/16` on the server host.

## 3. Forward one local TCP port without a TUN

For this variant, start the server with a **server-local** forward, then connect the agent as in scenario 1. The forward is a startup flag:

```sh
sudo undertow server --forward 127.0.0.1:18080=10.20.0.50:80
```

After the agent connects, run `curl http://127.0.0.1:18080/` in a **separate server terminal**. The left address is the server listener, and the right address is reached from the agent. With several agents, restart the server with `--via-agent AGENT_ID` to select one explicitly. A forward requires no server TUN.

## 4. Route a separate client's Internet traffic through the server

The server can run without `--tun`; Internet egress uses server sockets. Start it with `sudo undertow server`. On a different Linux host, with root privilege:

```sh
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

On Windows, run PowerShell as Administrator and use `.\bin\undertow.exe client --vpn ...`. In the client console, run `vpn status` to see the public egress check, then test `curl -4 https://api.ipify.org` and another HTTPS site from a separate terminal. The client keeps a physical route to the server, adds two IPv4 `/1` routes through its adapter, and removes its owned routes on graceful stop or failed verification. The local adapter's `.1` address is a gateway address, not the public egress IP. Existing more specific LAN routes still take precedence. IPv6 is not routed by this mode.

Use `--verify-url https://YOUR_IP_SERVICE` to choose another public IPv4 check, or `--verify-url ''` to skip it. Skipping verification removes a useful failure signal.

## 4a. Reach internal routes while keeping client Internet egress

Start the server and an agent as in scenario 1. On the elevated **VPN client** host, start internal-only mode. No server route command is required:

```sh
sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

In the client console, select the connected agent and accept a network it advertises:

```text
agents
use 1
routes
route accept 10.20.0.0/16
```

For a network the agent can reach but has not advertised, use `route add 10.20.0.0/16` instead. The route belongs only to this client and persists across reconnects. The client creates its TUN/Wintun and pins the DNS server route, but does not install either IPv4 `/1` route, change the default Internet route, or require public egress verification. Test `curl http://10.20.0.50/` on the client and confirm normal public Internet access still uses its existing connection. Server `route add` remains available when the server operator wants to manage a route globally.

## 5. Combine VPN egress and internal routes

Start a server and agent as in scenario 1. The server does not need its own `--tun` when only a separate VPN client needs the internal prefix. Start the client with both modes:

```sh
sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

In the **client console**, select the agent and accept a reported route (or add a known reachable prefix manually):

```text
agents
use 1
routes
route accept 10.20.0.0/16
```

Traffic for `10.20.0.0/16` now uses the selected agent and ordinary Internet traffic exits through server sockets. Test both an internal target and `curl -4 https://api.ipify.org` from a separate client terminal. A more specific route already installed on the client can override its VPN route, so choose a nonlocal test prefix. The client route belongs to this client; use a global server route only when other clients or the server host need the same path. `--vpn` alone, `--internal` alone, and this combined mode are the three supported routing choices.

## 6. Multiple agents and route ownership

Give each agent a distinct `--agent-key` file so each has its own stable identity. In the **server console**, select each connected agent before assigning its network:

```text
agents
use 1
show
route add 10.20.0.0/16
back
use 2
show
route add 10.30.0.0/16
back
routes
```

Check each agent's reported network in `show` before adding its route; the numbers may change after a reconnect. To disconnect one session for a reconnect test, select that agent and run `session kill`. Watch `routes` as it reconnects: a route becomes inactive while its agent is absent and is not silently reassigned. For automation, use the full IDs with `undertow route add CIDR --via AGENT_ID`, `undertow route list`, and `undertow session kill AGENT_ID` on the server host.

## 7. Foreground, background, and diagnostic probes

In a terminal, `server` and `client` open their consoles while a separate worker stays running. Type `background` to detach and use `undertow server attach` or `undertow client attach` to return. The agent runs in the foreground by default. For deliberately detached startup, use `--background`; for a foreground server or client worker without a console, use `--foreground`. Detached workers write a log and protected PID/control state file in the current directory. Stop one gracefully with its console command (`stop` on the server, `quit` on the client) or with `--stop` from that directory, supplying the original `--pid-file` when customized:

```sh
sudo undertow server --tun --background
sudo undertow server --stop
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --background
undertow agent --stop
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --background
sudo undertow client --stop
```

Use `--log-file PATH --pid-file PATH` on start to separate multiple instances, and the same paths on `server attach` or the same `--pid-file` on stop. Prefer a file for passwords; command line passwords can be read from process listings. A background start waits for the server to listen, the agent to connect, or the VPN to become active before reporting success. If readiness times out, it stops the child and reports an error. The log contains diagnostics without the terminal banner. Graceful stop withdraws owned routes. A forced kill can leave OS route state and should be followed by manual inspection.

To test the encrypted transport without target sockets, start a dedicated server with `--probe-echo` and an unused UDP port, then run an agent with `--probe` against that port:

```sh
undertow server --transport dns --listen 127.0.0.1:1053 --control-listen 127.0.0.1:47890 --probe-echo
undertow agent --server 127.0.0.1:1053 --fingerprint FINGERPRINT --probe --probe-count 5 --probe-size 64
```

Run the second command on the same host for this loopback example. `--probe-echo` is diagnostic mode and does not run normal pivot streams. Use a separate identity and token file if you want an isolated test instance.

## 8. Interactive consoles and agent commands

Run the server and agent normally. The server terminal opens its operator console. Current agent capabilities are allowed by default; add `--deny=exec` where operators must not launch arbitrary programs, or combine names such as `--deny=exec,upload,download`. Built-in host operations remain available unless `hostops` is also denied:

```sh
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

On the server host, use the console opened by `undertow server`. After detaching, reconnect with:

```sh
sudo undertow server attach
```

Inside the console:

```text
status
agents
use 1
help
route add 10.20.0.0/16
routes
exec /usr/bin/id
back
quit
```

To manage the same server from a VPN client, start the VPN from a terminal. The console opens by default; the client uses its normal enrollment mode and needs no extra operator file:

```sh
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

The VPN continues carrying traffic while the prompt is open. At the main menu, type `agents` to list numbered agents, then `use 1` to enter one. Type `help` at either level for that menu's commands. In the agent menu, `routes` lists its advertised and locally accepted routes; accept a detected subnet with `route accept 192.168.0.0/22`. If the agent can also reach `10.10.0.0/16` through a router on that interface, add it manually with `route add 10.10.0.0/16`; it need not be advertised. `route del 10.10.0.0/16` removes the local acceptance. `exec /usr/bin/id` runs once on the selected agent. `shell` opens a long-lived command session; press Ctrl-] to return to Undertow. `back` returns to the main menu. These route choices affect only this VPN client, are saved to its `client-routes.json`, and return after reconnect, including in background mode. Alternatively, start the agent with `--advertise-route 10.10.0.0/16` so clients can discover and accept it. Global server route changes remain in the server console. Use `internal on` at the client main menu for global server configured pivot routes; accepted local routes work in either internal setting. Type `background` to leave the console without interrupting the VPN, then `sudo undertow client attach` to return. Type `quit` to stop the VPN and remove its routes; Ctrl+C asks before stopping. Up/Down cycle through command history and Tab completes top-level commands. Important connection and agent changes appear in the console; routine diagnostics go to `undertow-client.log` or the chosen `--log-file`. Agent execution runs the executable and arguments directly, with no implicit shell or Windows console popup. Start an agent with `--deny=exec`, `--deny=hostops`, `--deny=interactive`, `--deny=upload`, or `--deny=download` to restrict each operation independently. With `--auth none`, any reachable VPN client can join and execute on agents that allow it; use token or password enrollment for real deployments.

To copy a file to the selected agent and retrieve it again:

```text
agents
use 1
upload ./example.txt ./incoming.txt
download ./incoming.txt ./retrieved.txt
```

The local paths above refer to the VPN client host; the remote paths refer to the agent host. Use absolute remote paths when you are unsure of the agent's working directory. Parent directories must already exist. Undertow refuses to replace an existing destination file and reports a SHA-256 digest after each successful transfer. Paths with spaces need quotes, for example `upload "./local report.txt" "C:\Users\operator\incoming report.txt"` on a Windows agent. `--deny=upload,download` denies file transfers without disabling one-shot exec.

When an agent and VPN client run on the **same host**, do not accept a route that the agent itself needs for its outbound connection. That can loop the agent's socket traffic back into the VPN. Run the agent on a separate host for that route, or keep its outbound path outside the accepted prefix.

## 9. Expose a VPN client web server on one agent

Start a TCP web server on the VPN client at port 8080, then start its VPN console. Select the agent that should receive incoming connections:

```text
agents
use 1
forward add 0.0.0.0:8080 127.0.0.1:8080
forward list
```

From another host that can reach the selected agent, run `curl http://AGENT_IP:8080/`. The agent's `0.0.0.0:8080` listener accepts connections on all its IPv4 interfaces and forwards them to the VPN client's loopback service. The agent's firewall must allow TCP/8080. Stop the listener with `forward del 0.0.0.0:8080`. The listener remains active when the console is detached with `background`, and closes when either endpoint disconnects. It is session scoped; add it again after reconnect. Use `agent --deny=listeners` to disallow agent-side forwarding while retaining other capabilities. See [console commands](console.md#expose-a-client-tcp-service-on-an-agent) for main-menu syntax and limits.

## 10. Run a local script without storing it on the agent

Prepare a Bash script on the VPN client, or a PowerShell script on either console machine, then select an agent with that interpreter installed:

```text
VPN client: printf 'id\n' > check.sh
VPN client console: agents
VPN client console: use 1
VPN client console: run-script bash ./check.sh
VPN client console: run-script --background bash ./check.sh
VPN client console: jobs
VPN client console: job output JOB_ID
VPN client console: job cancel JOB_ID
```

For a Windows agent, replace the command with `run-script powershell ./audit.ps1`. Undertow sends source through the encrypted connection directly to the interpreter's stdin; the agent does not need the local path or a temporary script file. The independent `scripts` capability controls this operation. A 1 MiB source and 10 minute runtime limit apply.

## 11. Run an in-memory WASI module

Place a WASI `.wasm` module on the VPN client machine and select an agent:

```text
VPN client console: agents
VPN client console: use 1
VPN client console: run-wasm ./tool.wasm audit
VPN client console: run-wasm --stdin ./input.txt ./tool.wasm
VPN client console: run-wasm --background ./long-task.wasm
VPN client console: jobs
VPN client console: job output JOB_ID
```

The agent instantiates the module in a pure-Go runtime without a temporary module file. The guest has no preopened WASI filesystem or WASI network sockets, but `undertow_host_v1` imports permit agent-side file reads and bounded outbound network requests with the agent process's privileges. `--deny=wasm` disables the module and its imports without disabling scripts, one-shot exec or other jobs. Module, stdin, runtime, memory, output and concurrency limits apply as listed in [the console reference](console.md); see the [WASM developer guide](wasm-development.md) for host operations.
