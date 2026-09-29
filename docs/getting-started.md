# Getting started

## What runs where?

- **Server:** the reachable UDP listener and local operator API. It creates the server identity and accepts agent and VPN client sessions. It can create a proxy TUN/Wintun for routed internal access, but VPN Internet egress itself uses server sockets.
- **Agent:** a connector on the internal network. It opens TCP, UDP, and ICMP operations for the server. It needs no root/Administrator privilege, virtual adapter, route changes, or inbound port.
- **VPN client:** a separate, elevated process that creates its own TUN/Wintun and changes its host's IPv4 routes. It can use server Internet egress alone, or include configured agent routes with `--internal`.
- **Operator commands:** `status`, `agent list/show/select`, `route add/del/list`, and `session kill` run on the server host against a token protected loopback API.

The key distinction is **which machine's traffic changes**. An agent exposes destinations reachable *from the agent host* and leaves that host's normal networking alone. A VPN client redirects applications *on the client host* into the tunnel. Starting a VPN client does not expose its local network as an agent; starting an agent does not give its own host VPN Internet egress. A combined VPN and internal deployment needs both processes plus a server route to the agent.

Undertow uses **direct UDP DNS**, addressed to a numeric server IP and port. The default synthetic domain is `t.undertow.invalid`; it must match on both ends. Public DNS delegation is not required. Allow the chosen UDP port through the server firewall. UDP/53 and virtual interfaces commonly need elevated privileges.

## Build and first session

Build Linux with `mkdir -p bin && go build -o bin/undertow ./cmd/undertow`. In Windows PowerShell use:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -o .\bin\undertow.exe .\cmd\undertow
```

In the examples below, `undertow` means `./bin/undertow` on Linux or `.\bin\undertow.exe` on Windows. Run from a directory containing your credential files, or use absolute paths.

On the server:

```sh
undertow init --identity identity.key --token-file token.key
sudo undertow server --listen 0.0.0.0:53 --identity identity.key --token-file token.key
```

The server prints a fingerprint. Copy `token.key` securely to the agent host, then run there:

```sh
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

On the server, `sudo undertow status` should show the agent under **Agents** when the elevated server created `control.key`. A connected VPN client appears under **VPN clients**; it needs no agent route for Internet egress. Stop either foreground process with Ctrl+C. The server identity stays on the server. Each agent and VPN client creates its **own** Ed25519 key file (`agent.key` or `client.key`) on first run. Never copy `identity.key` to a client.

## Enrollment choices

All modes use an encrypted session and a server identity fingerprint. `--auth` controls *who may enroll*:

| Mode | Server | Agent or VPN client | What to share |
| --- | --- | --- | --- |
| Token (default) | `--auth token --token-file token.key` | same flags | Copy `token.key` securely |
| Password | `--auth password --password-file password.key` | same flags | Share the password securely; each host writes its own restricted file |
| Open | `--auth none` | `--auth none` | No enrollment secret; anyone who reaches UDP listener may enroll |

Password mode requires at least 12 bytes. Use `--password-file` to avoid showing the password in a process command line; `--password TEXT` is available for temporary use. The password file's trailing newline is ignored. Token mode remains the default, and a `--token-file` is irrelevant to password or open mode. Open enrollment still encrypts transport and authenticates the server **when its fingerprint is pinned**; it provides no admission control. **For real deployments, use token or password enrollment.** With `--auth none`, anyone who can reach the listener can enroll, access network paths, and execute programs on agents that have not set `--deny-exec`.

The server can be initialized once with `undertow init` even when you choose password or open mode. `init` also creates an unused token file; the server identity and fingerprint are what those modes need.

## Server fingerprint

The safest first connection passes the fingerprint printed by `init` or server startup through a trusted channel:

```sh
undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
```

If distributing the fingerprint is impractical, use `--trust-on-first-use` on the agent or VPN client:

```sh
undertow agent --server SERVER_IP:53 --trust-on-first-use --token-file token.key
```

The fingerprint is discovered from the server, then saved to `server.fingerprint` **after the authenticated session succeeds**. Later runs load that file without `--trust-on-first-use`. Use `--fingerprint-file PATH` for a separate pin per server. The first contact can be intercepted, so use a trusted first network or verify the saved fingerprint independently before relying on it. With `--auth none`, first use has no enrollment secret to authenticate the remote endpoint; out-of-band verification is particularly important. If the server identity changes, a saved pin causes connection failure until you deliberately replace it.

## Local operator API

The server listens on `127.0.0.1:47889` for operator commands. It creates `control.key` for that API, separate from `token.key`. Keep it private. Override the listener with `--control-listen` and use matching `--control` plus `--control-token-file` on commands. The control address must be numeric loopback.

Run operator commands as an account allowed to read `control.key`. When the server first creates it under `sudo`, use `sudo` for `status`, `agent list/show/select`, `route`, and `session` commands too, or arrange restricted file access for your operator account.

See [scenarios](scenarios.md) for working topologies and [CLI reference](cli-reference.md) for every flag.
