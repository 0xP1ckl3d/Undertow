# Deployment scenarios

These commands use `undertow` as shorthand for `./bin/undertow` on Linux or `.\bin\undertow.exe` on Windows. Replace all uppercase placeholders. Use the same enrollment and fingerprint settings on both ends; [getting started](getting-started.md) explains token, password, open enrollment, and trust on first use. On Linux, prefix operator commands with `sudo` when an elevated server owns `control.key`.

## 1. Confirm an unprivileged agent

On the server, run `undertow init`, then `sudo undertow server --listen 0.0.0.0:53`. On an internal network host:

```sh
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

The agent needs no sudo, adapter, or route. On the server:

```sh
undertow status
undertow agent list
undertow agent show AGENT_ID
```

The full agent ID, not its shortened display in the table, is in `undertow status --json` or `agent show`. A connected agent alone does not install a server route. Use scenario 2 or 3 to send traffic through it.

## 2. Reach an internal subnet from the server

Start the server with `--tun` and choose a tunnel prefix that does not overlap any server or agent network:

```sh
sudo undertow server --listen 0.0.0.0:53 --tun --tun-name undertow0 --tunnel-address 172.16.254.1/24
```

Start the unprivileged agent as in scenario 1. On the server, choose the agent ID and add the internal prefix:

```sh
undertow status --json
undertow route add 10.20.0.0/16 --via AGENT_ID
undertow route list
curl http://10.20.0.50/
ping 10.20.0.50
```

Use real destinations that the agent can reach. The server's OS route for that prefix feeds its TUN, then Undertow selects the named agent; the agent opens ordinary target sockets. TCP, UDP, and ICMP echo are supported. The route remains assigned to that agent and becomes inactive when it disconnects. `undertow route del 10.20.0.0/16` removes it. ICMP on Linux may use the system `ping` command when unprivileged ping sockets are unavailable.

## 3. Forward one local TCP port without a TUN

Start an agent as in scenario 1, then start the server with a repeatable local forward:

```sh
undertow server --listen 0.0.0.0:53 --forward 127.0.0.1:18080=10.20.0.50:80 --via-agent AGENT_ID
curl http://127.0.0.1:18080/
```

The left address is the server listener, the right address is reached from the agent. Omit `--via-agent` only when exactly one agent is connected; specify it when several are connected. A forward requires no server TUN. If UDP/53 needs elevation, use sudo for that reason or choose an unprivileged test port on both sides.

## 4. Route a separate client's Internet traffic through the server

The server can run without `--tun`; Internet egress uses server sockets. Start it with `undertow server --listen 0.0.0.0:53`. On a different Linux host, with root privilege:

```sh
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

On Windows, run PowerShell as Administrator and use `.\bin\undertow.exe client --vpn ...`. Confirm the log shows the server public egress address, then test `curl -4 https://api.ipify.org` and another HTTPS site. The client keeps a physical route to the server, adds two IPv4 `/1` routes through its adapter, and removes its owned routes on graceful stop or failed verification. The local adapter's `.1` address is a gateway address, not the public egress IP. Existing more specific LAN routes still take precedence. IPv6 is not routed by this mode.

Use `--verify-url https://YOUR_IP_SERVICE` to choose another public IPv4 check, or `--verify-url ''` to skip it. Skipping verification removes a useful failure signal.

## 5. Combine VPN egress and internal routes

Start an agent and configure a route on the server as in scenario 2. The server does not need its own `--tun` if only a separate VPN client needs the internal prefix; it still needs the configured route and a connected agent.

```sh
undertow route add 10.20.0.0/16 --via AGENT_ID
sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

On the VPN client, traffic for `10.20.0.0/16` uses the selected agent and ordinary Internet traffic exits through server sockets. Test both an internal target and `curl -4 https://api.ipify.org`. A more specific route already installed on the client can override its VPN route, so choose a nonlocal test prefix or adjust your test topology.

## 6. Multiple agents and route ownership

Give each agent a distinct `--agent-key` file so each has its own stable identity. On the server:

```sh
undertow status --json
undertow agent select AGENT_ID
undertow route add 10.20.0.0/16 --via AGENT_ID
undertow route add 10.30.0.0/16 --via OTHER_AGENT_ID
undertow route list
undertow session kill AGENT_ID
```

Selection chooses the default agent for operations that permit it; an explicit `--via` is clearer when configuring routes. `session kill` disconnects that session, and a running agent may reconnect. Observe route activation in `route list`; routes are not silently reassigned to another identity.

## 7. Foreground, background, and diagnostic probes

`--foreground` is the default for server, agent, and VPN client. To detach, use `--background`; the process writes a log and a protected PID/control state file in the current directory. Stop it gracefully with the same command mode and `--stop` from that directory, or supply the original `--pid-file`:

```sh
sudo undertow server --listen 0.0.0.0:53 --tun --background
sudo undertow server --stop
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --background
undertow agent --stop
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --background
sudo undertow client --stop
```

Use `--log-file PATH --pid-file PATH` on start to separate multiple instances, and the same `--pid-file` on stop. Prefer a file for passwords; command line passwords can be read from process listings. A background start waits for the server to listen, the agent to connect, or the VPN to become active before reporting success. If readiness times out, it stops the child and reports an error. The log contains diagnostics without the terminal banner. Graceful stop withdraws owned routes. A forced kill can leave OS route state and should be followed by manual inspection.

To test the encrypted transport without target sockets, start a dedicated server with `--probe-echo` and an unused UDP port, then run an agent with `--probe` against that port:

```sh
undertow server --listen 127.0.0.1:1053 --control-listen 127.0.0.1:47890 --probe-echo
undertow agent --server 127.0.0.1:1053 --fingerprint FINGERPRINT --probe --probe-count 5 --probe-size 64
```

Run the second command on the same host for this loopback example. `--probe-echo` is diagnostic mode and does not run normal pivot streams. Use a separate identity and token file if you want an isolated test instance.

## 8. Interactive consoles and agent commands

Run the server and agent normally. Current agent capabilities are allowed by default; add `--deny=exec` where operators must not launch programs, or combine names such as `--deny=exec,upload,download`:

```sh
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

On the server host, open the operator console. Use `sudo` if the server created `control.key` with root only access:

```sh
sudo undertow console
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

The VPN continues carrying traffic while the prompt is open. At the main menu, type `agents` to list numbered agents, then `use 1` to enter one. Type `help` at either level for that menu's commands. In the agent menu, `routes` lists its advertised and locally accepted routes; accept a detected subnet with `route accept 192.168.0.0/22`. If the agent can also reach `10.10.0.0/16` through a router on that interface, add it manually with `route add 10.10.0.0/16`; it need not be advertised. `route del 10.10.0.0/16` removes the local acceptance. `exec /usr/bin/id` runs on the selected agent; `back` returns to the main menu. These route choices affect only this VPN client, are saved to its `client-routes.json`, and return after reconnect, including in background mode. Alternatively, start the agent with `--advertise-route 10.10.0.0/16` so clients can discover and accept it. Global server route changes remain in the server console. Use `internal on` at the client main menu for global server configured pivot routes; accepted local routes work in either internal setting. Type `background` to leave the console without interrupting the VPN, then `sudo undertow client attach` to return. Type `quit` to stop the VPN and remove its routes; Ctrl+C asks before stopping. Up/Down cycle through command history and Tab completes top-level commands. Important connection and agent changes appear in the console; routine diagnostics go to `undertow-client.log` or the chosen `--log-file`. Agent execution runs the executable and arguments directly, with no implicit shell or Windows console popup. Start an agent with `--deny=exec`, `--deny=upload`, or `--deny=download` to restrict each operation independently. With `--auth none`, any reachable VPN client can join and execute on agents that allow it; use token or password enrollment for real deployments.

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
