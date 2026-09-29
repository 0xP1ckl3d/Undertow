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

Use `--log-file PATH --pid-file PATH` on start to separate multiple instances, and the same `--pid-file` on stop. Prefer a file for passwords; command line passwords can be read from process listings. A background start reports process creation; inspect its log and `status` for operational readiness. Graceful stop withdraws owned routes. A forced kill can leave OS route state and should be followed by manual inspection.

To test the encrypted transport without target sockets, start a dedicated server with `--probe-echo` and an unused UDP port, then run an agent with `--probe` against that port:

```sh
undertow server --listen 127.0.0.1:1053 --control-listen 127.0.0.1:47890 --probe-echo
undertow agent --server 127.0.0.1:1053 --fingerprint FINGERPRINT --probe --probe-count 5 --probe-size 64
```

Run the second command on the same host for this loopback example. `--probe-echo` is diagnostic mode and does not run normal pivot streams. Use a separate identity and token file if you want an isolated test instance.

## 8. Interactive consoles and agent commands

Run the server and agent normally. Agent command execution is enabled by default; add `--deny-exec` on any agent where operators must not launch programs:

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
route add 10.20.0.0/16 AGENT_ID
routes
exec AGENT_ID /usr/bin/id
quit
```

To manage the same server from a VPN client, start the VPN in the foreground with `--interactive`. The client uses its normal enrollment mode; no extra operator file is needed:

```sh
sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key --interactive
```

The VPN continues carrying traffic while the prompt is open. In that console, `status`, `routes`, `exec`, and `internal on|off` work through the encrypted client session. `routes` lists advertised and locally accepted routes. Accept a detected subnet with `route accept 192.168.0.0/22 AGENT_ID`. If the agent can also reach `10.10.0.0/16` through a router on that interface, add it manually with `route add 10.10.0.0/16 AGENT_ID`; it need not be advertised. `route del 10.10.0.0/16` removes the local acceptance. These choices affect only this VPN client, are saved to its `client-routes.json`, and return after reconnect, including in background mode. Alternatively, start the agent with `--advertise-route 10.10.0.0/16` so clients can discover and accept it. Global server route changes and agent selection require the server console. Use `internal on` for global server configured pivot routes; accepted local routes work in either internal setting. `quit` stops the interactive VPN and cleans its owned routes. Agent execution runs the executable and arguments directly, with no implicit shell. Start an agent with `--deny-exec` if it should refuse client commands. With `--auth none`, any reachable VPN client can join and execute on agents that allow it; use token or password enrollment for real deployments.
