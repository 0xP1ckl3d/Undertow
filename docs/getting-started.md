# Getting started: server → client → Windows agent

This is a first-run path for a reachable **Linux server**, a **Linux VPN client**, and a **Windows agent** on a network the client wants to reach. Run each block on the named machine. Replace `SERVER_IP`, `FINGERPRINT`, `10.20.0.0/16`, and `10.20.1.25` with values from your deployment. Commands assume you are in the Undertow repository. Go 1.25 or newer is needed to build; the server and client need elevated privileges for listeners/TUN/routes. Permit UDP/443 and TCP/443 to the server.

## 1. Initialize and start the server

On the **server**:

```sh
sh tools/build-release.sh bin
./bin/undertow init
sudo ./bin/undertow server --tun
```

The release build creates the operator binary **and thin-agent templates** used by `payload build`. Save the fingerprint printed by `init`. It also creates `token.key`, the default enrollment secret. The last command opens the server console. Its `status` command should show QUIC UDP/443 and HTTPS/WebSocket TCP/443 (and direct DNS UDP/53). The HTTPS listener is needed to host payloads. `--tun` lets applications on the **server itself** use agent routes; the separate client creates its own TUN. The server can omit `--tun` when its own applications need no agent route.

Copy `token.key` securely into the Linux client's Undertow directory. Keep `identity.key` and `control.key` on the server. A detached server can start with `server --tun --background`; use `sudo ./bin/undertow server attach` to reopen its console.

## 2. Connect the client and accept the server fingerprint

On the **Linux client**:

```sh
sh tools/build-release.sh bin
sudo ./bin/undertow client --vpn --internal --transport quic --server SERVER_IP:443 --token-file token.key --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

Get `FINGERPRINT` from the server through a trusted channel. Pass an explicit pin each time you start the client. The default TLS certificate is self-signed, hence `--tls-insecure-skip-verify`; Undertow still checks its separate identity fingerprint. If you cannot transfer the fingerprint first, substitute `--trust-on-first-use` for `--fingerprint FINGERPRINT`. After an authenticated connection, Undertow writes `server.fingerprint`; compare the saved value with the server's fingerprint before relying on it. Subsequent connections can load that saved pin automatically. The client creates its own `client.key`.

The command opens the **client console**. `--vpn` routes this client's IPv4 Internet traffic through the server; `--internal` enables routes through agents. Use either flag alone if you need only one path. In another client terminal, `curl -4 https://api.ipify.org` should show the server's public IP when VPN is active. `status` on either host should show the connected client. Type `background` to detach while it runs, `sudo ./bin/undertow client attach` to return, and `quit` in the client console to stop it and remove owned routes.

## 3. Build, host, and deploy a headless Windows agent

In the **server console**:

```text
payload profile create office server=SERVER_IP:443 transport=quic
payload build office windows amd64
payload host PAYLOAD_ID
payload deploy-script PAYLOAD_ID powershell
```

Replace `PAYLOAD_ID` with the **24-character payload ID** from `payload build`. The build also prints a separate **32-character agent ID** for the connection. The profile stores the server address and carrier; the build stamps a fresh agent identity, enrollment credential, and server fingerprint into the executable. The server file path printed by the build is storage on the server, not a Windows install path. `payload host` creates an opaque HTTPS URL. Keep the URL private. `payload show PAYLOAD_ID` and `payload url PAYLOAD_ID` recover the details.

Save the PowerShell text printed by the last command as `deploy.ps1` on the **Windows host**. Run it there:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\deploy.ps1 -Destination .\worker.exe
```

The script downloads, verifies SHA-256, installs, and launches the binary hidden. The packaged agent needs **no connection arguments**, console window, Administrator rights, or inbound port. Its executable contains an enrollment credential, so protect both the executable and deploy script. Build once per endpoint if distinct agent identities are needed. The [payload guide](agent-distribution.md) covers manual download, profiles, hosted URL rotation, and lifecycle commands.

**Alternate delivery:** if you need to wrap the executable or deliver it through your own channel, run `payload download PAYLOAD_ID ./staging/worker.exe` in either the server console or an authenticated client console. This retrieves the built binary over the control connection, verifies its SHA-256, and saves it locally even if it has never been hosted. The output file must not already exist. See [payload deployment](agent-distribution.md#profile--build--host--run) for details.

## 4. Run a command and a module

Back in the **server or client console**:

```text
agents
use 1
show
whoami
exec cmd.exe /c whoami
modules
module-wininfo
```

Use the number shown by `agents`, or select by hostname or agent ID; numbers can change as peers reconnect. `whoami` is a built-in host operation and `exec` starts a Windows command. The local console preloads packaged modules from `modules/`. `module-wininfo` is a bundled Windows native module; `help module-wininfo` shows its usage. If absent, start or attach the console from the repository, or set `UNDERTOW_MODULES_DIR` to the absolute path of its `modules/` directory. The direct-file form is `run-native modules/native/wininfo/wininfo.module`. The [module bank](module-bank.md) also documents packaged BOFs and WASM tools.

## 5. Route client traffic through the agent

In the **client console**, select the Windows agent and inspect its reported networks:

```text
agents
use 1
routes
route accept 10.20.0.0/16
routes
```

Accept a CIDR actually shown by `routes`. If the agent can reach a network it does not report, use `route add 10.20.0.0/16` instead. The route belongs to **this client** and persists in `client-routes.json` across reconnects. Test a real service from a separate client terminal, for example `curl http://10.20.1.25/`; check the public IP again to confirm the VPN path. `route del 10.20.0.0/16` removes the client route. If applications on the **server** also need the network, select the agent in the server console and run `route add 10.20.0.0/16` there.

If no agent appears, check `status` and `agent events AGENT_ID` in the server console, the Windows process, and outbound UDP/443. If route acceptance fails, pick a reported subnet that does not overlap the client's local networks. `help`, `help route`, and `payload` provide context-specific guidance.

## Continue from here

| Goal | Guide |
| --- | --- |
| Other VPN modes and carrier choices | [Networking modes](networking-modes.md) and [scenarios](scenarios.md) |
| Profiles, downloads, hosting, and shutdown | [Payload deployment](agent-distribution.md) |
| Shells, files, jobs, forwards, and console commands | [Console guide](console.md) |
| BOF, native, and WASM tools | [Module bank](module-bank.md), [BOF](bof-compatibility.md), [native](native-modules.md), and [WASM](wasm-development.md) guides |
| Deeper agent networks | [Topology and relays](topology-and-relays.md) |
| All flags and feature index | [CLI reference](cli-reference.md) and [feature catalogue](../features.md) |

## What runs where?

- **Server:** the reachable carrier listeners and local operator API. It creates the server identity and accepts agent and VPN client sessions. Use `--tun` only when applications on the server host itself need routes through an agent; VPN Internet egress uses server sockets.
- **Agent:** a connector on the internal network. It opens TCP, UDP, and ICMP operations for the server. It needs no root/Administrator privilege, virtual adapter, route changes, or inbound port.
- **Client:** a separate, elevated process that creates its own TUN/Wintun. `--vpn` changes Internet routes; `--internal` alone adds only internal routes through agents; both flags combine them.
- **Operator commands:** `status`, `agent list/show/select`, `route add/del/list`, and `session kill` run on the server host against a token protected loopback API.

The key distinction is **which machine's traffic changes**. An agent exposes destinations reachable *from the agent host* and leaves that host's normal networking alone. A client routes applications *on the client host* into the tunnel for its selected prefixes. Starting a client does not expose its local network as an agent; starting an agent does not give its own host VPN Internet egress. Internal access needs a connected agent and a route to it.

## Client routing modes

Run one of these on the elevated **VPN client** host after starting the server and, for internal access, an agent:

```sh
sudo undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
sudo undertow client --vpn --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

`--vpn` installs two IPv4 `/1` routes and verifies public egress. `--internal` alone pins the carrier server route and creates the TUN without changing the Internet/default route or requiring a public egress check. Once connected, type `agents`, `use 1`, and `routes` in the client console; use `route accept CIDR` for an advertised subnet or `route add CIDR` for another network reachable from that agent. This setup needs no server route command. Server configured routes are also supported when an operator wants global route management. Both flags provide VPN Internet egress and agent routes. At least one flag is required. See [deployment scenarios](scenarios.md) for tests.

These examples use **QUIC UDP/443** with the server's automatic self-signed TLS certificate and a separately pinned Undertow fingerprint. The server also starts HTTPS/WebSocket TCP/443 and direct DNS UDP/53 by default; peers choose any active carrier independently. See [networking mode and carrier choices](networking-modes.md#choose-another-carrier). Allow the chosen port through the server firewall. UDP/53 and virtual interfaces commonly need elevated privileges.

## Manual agent alternative

If you want a foreground diagnostic agent instead of the configured Windows payload above, copy `token.key` securely to that host and run `undertow agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --token-file token.key --tls-insecure-skip-verify`. On Linux, `undertow` means `./bin/undertow`; on Windows it means `.\bin\undertow.exe`. A manual agent creates its own `agent.key`, remains in the foreground, and stops with Ctrl+C. A configured payload embeds its own identity and credential and runs headlessly. Keep the server identity on the server.

## Enrollment choices

All modes use an encrypted session and a server identity fingerprint. `--auth` controls *who may enroll*:

| Mode | Server | Agent or VPN client | What to share |
| --- | --- | --- | --- |
| Token (default) | `--auth token --token-file token.key` or `--auth token --token TOKEN_HEX` | same token through either flag | Copy the file securely or share its hex value securely |
| Password | `--auth password --password-file password.key` | same flags | Share the password securely; each host writes its own restricted file |
| Open | `--auth none` | `--auth none` | No enrollment secret; anyone who reaches the carrier listener may enroll |

You can copy `token.key` as a file, or paste the server's hex token into a PowerShell file with `echo "TOKEN_HEX" > token.key`. Undertow accepts PowerShell's UTF-16LE text as well as UTF-8. Use the exact token generated on the server; a different token will fail enrollment. Treat the token as a secret.

To avoid creating a token file on a host, pass its hex value with `--token TOKEN_HEX` on that host. Both server and connecting hosts accept this flag; each must use the same value. Do not combine `--token` with `--token-file`. A literal `--token` value is visible in process listings and may remain in shell history, so a restricted token file is safer on shared hosts.

Password mode requires at least 12 bytes. Use `--password-file` to avoid showing the password in a process command line; `--password TEXT` is available for temporary use. The password file's trailing newline is ignored. Token mode remains the default, and a `--token-file` is irrelevant to password or open mode. Open enrollment still encrypts transport and authenticates the server **when its fingerprint is pinned**; it provides no admission control. **For real deployments, use token or password enrollment.** With `--auth none`, anyone who can reach the listener can enroll, access network paths, and use operations that agents have not restricted with `--deny`.

The server can be initialized once with `undertow init` even when you choose password or open mode. `init` also creates an unused token file; the server identity and fingerprint are what those modes need.

## Server fingerprint

The safest first connection passes the fingerprint printed by `init` or server startup through a trusted channel:

```sh
undertow agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify
```

If distributing the fingerprint is impractical, use `--trust-on-first-use` on the agent or VPN client:

```sh
undertow agent --transport quic --server SERVER_IP:443 --trust-on-first-use --tls-insecure-skip-verify
```

The fingerprint is discovered from the server, then saved to `server.fingerprint` **after the authenticated session succeeds**. Later runs load that file without `--trust-on-first-use`. Use `--fingerprint-file PATH` for a separate pin per server. The first contact can be intercepted, so use a trusted first network or verify the saved fingerprint independently before relying on it. With `--auth none`, first use has no enrollment secret to authenticate the remote endpoint; out-of-band verification is particularly important. If the server identity changes, a saved pin causes connection failure until you deliberately replace it.

## Local operator API

The server listens on `127.0.0.1:47889` for operator commands. It creates `control.key` for that API, separate from `token.key`. Keep it private. Override the listener with `--control-listen` and use matching `--control` plus `--control-token-file` on commands. The control address must be numeric loopback.

Run operator commands as an account allowed to read `control.key`. When the server first creates it under `sudo`, use `sudo` for `status`, `agent list/show/select`, `route`, and `session` commands too, or arrange restricted file access for your operator account.

See [scenarios](scenarios.md) for working topologies and [CLI reference](cli-reference.md) for every flag.

Back to [documentation home](README.md).
