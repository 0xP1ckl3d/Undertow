# Remote port forwarding

Use remote port forwarding when a service runs on your **VPN client** but needs to receive TCP connections at an **agent**. Undertow listens on the selected agent, carries each connection through the server, and connects it to a loopback address on the client:

```text
Other host → AGENT_IP:18080 → agent → Undertow server → VPN client → 127.0.0.1:8080
```

This is useful for a local web service or any other TCP listener you want available from the agent's network. You can expose several ports and use several agents. The service itself stays on the client; it is not copied to the agent.

## Before you start

Follow [Getting started](getting-started.md) to connect a server, agent, and VPN client and accept the server fingerprint. The agent must allow the `listeners` capability (the default); `agent --deny=listeners` disables this feature. Run the VPN client with `--internal`, `--vpn`, or both. For this example, `--internal` keeps the client's ordinary Internet route unchanged:

```sh
# On the Linux client, from the Undertow directory
sudo ./bin/undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

The client needs its normal elevated TUN/Wintun setup, but you do **not** need to accept an agent route or start the server with `--tun` just to forward a service. The client must remain connected while the forward is in use. Replace `SERVER_IP` and `FINGERPRINT` with your deployment values; the example uses the default `token.key` in the client working directory and the server's default self-signed TLS certificate. See [networking modes](networking-modes.md) if you use another carrier.

## Expose one client service

In a **separate terminal on the client**, start a test web service bound to loopback:

```sh
python3 -m http.server 8080 --bind 127.0.0.1
```

In the **client console**, select the agent that should accept incoming connections:

```text
agents
use 1
show
forward add 0.0.0.0:18080 127.0.0.1:8080
forward list
```

The first address is the **agent-side bind**. `0.0.0.0:18080` accepts connections on any agent IPv4 interface; use a specific agent IPv4 address instead if only that interface should listen. The second address is the **client-side target**. Undertow requires a numeric IPv4 loopback target, so the service must listen on `127.0.0.1:8080` or `0.0.0.0:8080` on the client. The two port numbers may differ, as they do here.

From another host that can reach the agent, verify the path:

```sh
curl http://AGENT_IP:18080/
```

Use an agent IP reachable from that host. Its firewall must allow TCP/18080. `forward list` in the selected-agent menu confirms the mapping; a successful `curl` confirms the client service is reachable through it.

## Expose several services or agents

In another client terminal, start a second TCP service, then add a second mapping in the same selected-agent menu:

```sh
python3 -m http.server 8081 --bind 127.0.0.1
```

```text
forward add 0.0.0.0:18081 127.0.0.1:8081
forward list
```

Test each from a host that reaches the agent with `curl http://AGENT_IP:18080/` and `curl http://AGENT_IP:18081/`. Each TCP port needs its own `forward add`. To expose the first client service through a second agent as well, use `back`, `use 2`, then `forward add 0.0.0.0:18080 127.0.0.1:8080`. The same bind port can be used on different agents. Check `show` before adding a mapping so you choose the intended agent.

Tools that listen on several TCP ports need a mapping for each one. This feature carries **TCP only**; it does not carry UDP discovery or broadcast traffic. For example, forwarding TCP listener ports alone does not make UDP name-resolution traffic used by some responder-style tools available through the agent.

## Keep, inspect, and remove forwards

`background` leaves the client console while its worker and forwards continue running. Return with `sudo ./bin/undertow client attach`, then select the agent and run `forward list`. Remove a mapping with its agent-side bind:

```text
forward del 0.0.0.0:18081
forward del 0.0.0.0:18080
```

Forwards belong to the connected client and selected agent. Stopping the client with `quit`, disconnecting the client, or losing the agent connection closes those listeners. They are session scoped: add them again after a reconnect. A client-side service must also stay running for new connections to succeed.

## If a connection fails

| Check | What to do |
| --- | --- |
| Client service | Test `curl http://127.0.0.1:8080/` on the client first. Make sure it listens on loopback or all interfaces. |
| Agent selection | Run `agents`, `use NUMBER`, and `show`; then check `forward list` for that agent. |
| Agent bind | Confirm the agent has the chosen IPv4 address and the bind port is free. A bind conflict is reported when adding the forward. |
| Network path | Allow the agent-side TCP port in its host firewall and any intervening firewall. Test from a host that can reach `AGENT_IP`. |
| Capability or protocol | `--deny=listeners` rejects new agent listeners. Only numeric IPv4 addresses and TCP are supported; the client target must be loopback. |

This is **client service → agent listener** forwarding. For the other direction, where a local port on the **server** connects to a service inside an agent's network, see [server-local TCP forwarding](scenarios.md#3-forward-one-local-tcp-port-without-a-tun).

Back to [documentation home](README.md).
