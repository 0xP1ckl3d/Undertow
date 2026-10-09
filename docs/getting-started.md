# Getting started: server → client → Windows agent

This first-run path connects a reachable **Linux server**, a **Linux operator client**, and a **Windows agent** on a remote network. You will use the GUI for deployment and everyday work, with terminal equivalents alongside it. Run each block on the named machine. Replace `SERVER_IP`, `FINGERPRINT`, `10.20.0.0/16`, and `10.20.1.25` with your values. Commands assume the Undertow repository and Go 1.25 or newer. Server listeners and client TUN/routes need elevation. Permit UDP/443 and TCP/443 to the server, and UDP/53 if using its DNS listener.

## 1. Initialize and start the server

On the **server**:

```sh
sh tools/build-release.sh bin
./bin/undertow init
umask 077
printf '%s\n' 'REPLACE_WITH_A_UNIQUE_LONG_PASSWORD' > leader.password
./bin/undertow operators bootstrap --operations-db operations.db --id leader --display-name 'Team Leader' --password-file leader.password
sudo ./bin/undertow server
```

The release build creates the operator binary **and thin-agent templates** used to build payloads. Save the fingerprint printed by `init`. It also creates `token.key`, the enrollment secret. The last command opens the server console. Its `status` should show QUIC UDP/443, HTTPS/WebSocket TCP/443, and direct DNS UDP/53. HTTPS is needed for hosted payload delivery. This walkthrough routes applications on a separate client, so the server needs no TUN. Add `--tun` only if applications on the **server itself** need agent routes.

Copy `token.key` securely into the Linux client's Undertow directory. Create a private copy of the operator password file there for the first connection. Keep `identity.key`, `control.key`, and `operations.db` on the server. A detached server can start with `server --background`; use `sudo ./bin/undertow server attach` to reopen its console. See [Operator authentication](operator-authentication.md) for account management and password rotation.

## 2. Connect the client and accept the server fingerprint

On the **Linux client**:

```sh
sh tools/build-release.sh bin
sudo ./bin/undertow client --internal --transport quic --server SERVER_IP:443 --token-file token.key --fingerprint FINGERPRINT --tls-insecure-skip-verify --operator leader --operator-password-file leader.password
```

Get `FINGERPRINT` from the server through a trusted channel. Pass an explicit pin each time you start the client. The default TLS certificate is self-signed, hence `--tls-insecure-skip-verify`; Undertow still checks its separate identity fingerprint. If you cannot transfer the fingerprint first, substitute `--trust-on-first-use` for `--fingerprint FINGERPRINT`. After an authenticated connection, Undertow writes `server.fingerprint`; compare the saved value with the server's fingerprint before relying on it. Subsequent connections can load that saved pin automatically. The client creates its own `client.key`.

Open the printed **GUI available:** URL in a browser **on the client machine**, copying the whole URL including its `#` suffix. Open **Settings → Status** to confirm your session and **Settings → Identity** to confirm the authenticated Team Leader. Keep the client worker running while using the GUI.

`--internal` enables remote-network routing while preserving your normal Internet route. Add `--vpn` for server IPv4 Internet egress; verify it from another client terminal with `curl -4 https://api.ipify.org`. If you only need host operations, substitute `--operator-only` and omit `sudo`; that mode creates no TUN and cannot complete the routing step below.

The terminal console remains available. `background` detaches it, `sudo ./bin/undertow client attach` returns, and `quit` stops the client and removes owned routes. Closing the browser leaves the worker running. Recover the GUI link with `sudo ./bin/undertow client gui` in an OS terminal. Use `--no-gui` for terminal-only operation.

## 3. Build, host, and deploy a headless Windows agent

In the **client GUI**:

1. Open **Payloads → Profiles → New**. Name the profile `office`, choose **QUIC**, and enter `SERVER_IP` in **Server host or IP**. The GUI adds the listener port; use an address the Windows endpoint can reach.
2. Keep the listener's self-signed TLS option selected for this setup. Leave the sleep interval at zero for an immediately available first agent, then click **Create profile**.
3. Open **Build payload**, select `office` and **Windows · x64**, and click **Build payload**. Templates remain on the server; your client does not compile the agent.
4. In **Artifacts**, select the new build and click **Host on HTTPS listener**. Check its download URL and SHA-256. The endpoint must reach HTTPS as well as its QUIC callback address.
5. Under **Deploy helper**, select **PowerShell**, click **Generate preview**, review the script, then **Download script**. Generating it does not run anything on the endpoint.

**Terminal equivalent**, in the server or authenticated client console:

```text
payload profile create office server=SERVER_IP:443 transport=quic
payload build office windows amd64
payload host PAYLOAD_ID
payload deploy-script PAYLOAD_ID powershell
```

Replace `PAYLOAD_ID` with the **24-character payload ID** from `payload build`. Each running copy creates its own **32-character agent ID** when it connects. The profile stores the server address and carrier; the build stamps an enrollment credential and server fingerprint into the executable. The server file path printed by the build is storage on the server, not a Windows install path. `payload host` creates an opaque HTTPS URL. Keep the URL private. `payload show PAYLOAD_ID` and `payload url PAYLOAD_ID` recover the details.

Copy the downloaded helper to the **Windows host** as `deploy.ps1`, or save the PowerShell text printed by the console under that name. Run it there:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\deploy.ps1 -Destination .\worker.exe
```

The script downloads, verifies SHA-256, installs, and launches the binary hidden. The packaged agent needs **no connection arguments**, console window, Administrator rights, or inbound port. Its executable contains an enrollment credential, so protect both the executable and deploy script. Each running copy gets a distinct agent ID. The [payload guide](agent-distribution.md) covers manual download, profiles, hosted URL rotation, and lifecycle commands.

**Alternate delivery:** select **Download verified binary** in the artifact detail, or run `payload download PAYLOAD_ID ./staging/worker.exe` in either console. This retrieves the binary over the control connection and verifies SHA-256 even if it has never been hosted. Deliver it through your own channel and run it without connection arguments. The console output file must not already exist. See [Payload deployment](agent-distribution.md#build-and-deliver-from-the-gui).

## 4. Run a command and a module

In the **GUI**, open **Agents**, select the Windows record, and read **Overview** for hostname, ID, platform, carrier, and capabilities. Open **Host → Identity → Run** for your first built-in operation. Open **Console** and type `exec cmd.exe /c whoami` for a one-shot OS command.

Next, open the agent's **Modules** tab, select `module-wininfo`, read its help, and click **Stream foreground**. Output appears there and in Console history. If it is absent, check the client module-bank path as described below. The [GUI guide](gui.md) continues with live shells, files, Jobs, screenshots, and other workflows.

**Terminal equivalent**, in the server or client console:

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

In **GUI Routes**, choose the agent under **Routes accepted by this client**, select **Advertised**, choose its actual remote CIDR, and click **Accept and install route**. Overview also offers acceptance. Check **Saved local routes** for **Installed** and **Topology** for the accepted path. For a known reachable but unadvertised network, choose **Custom CIDR**.

**Terminal equivalent**, in the client console:

```text
agents
use 1
routes
route accept 10.20.0.0/16
routes
```

Accept a CIDR actually shown by the agent. The route belongs to **this client** and persists in `client-routes.json` across reconnects. Test from a separate client terminal, for example `curl http://10.20.1.25/` or `ping 10.20.1.25`. If you added `--vpn`, check public egress too. In Routes, **Disable** turns a saved route off and **Enable** restores it; **Remove** or console `route del 10.20.0.0/16` deletes it. An enabled route keeps a check-in agent connected while applications may need it. Server-host access needs server `--tun` and its own configured route.

If no agent appears, check `status` and `agent events AGENT_ID` in the server console, the Windows process, and outbound UDP/443. If route acceptance fails, pick a reported subnet that does not overlap the client's local networks. `help`, `help route`, and `payload` provide context-specific guidance.

## Finish your first session

Download any Job output or screenshots you need to keep. Remove the test route in **Networking → Routes**, then use **Agents → your agent → Overview → Agent lifecycle → Shut down agent** if this was a temporary deployment. Confirm the shutdown and remove the delivered executable from the endpoint through your normal cleanup process. In **Payloads → Artifacts**, unhost the download when no longer needed; unhosting stops delivery without stopping an already running process.

Closing the browser leaves the client running. Type `quit` in its terminal console to stop it, and `stop` in the server console when you are finished with the server. The [GUI cleanup guide](gui.md#settings-history-and-cleanup) explains session kill, shutdown, archive, and retained state.

## Continue from here

| Goal | Guide |
| --- | --- |
| Learn every GUI page and agent tab | [GUI user guide](gui.md) |
| Other VPN modes and carrier choices | [Networking modes](networking-modes.md) and [scenarios](scenarios.md) |
| Profiles, downloads, hosting, and shutdown | [Payload deployment](agent-distribution.md) |
| Shells, files, jobs, forwards, and console commands | [Console guide](console.md) |
| BOF, native, WASM, and .NET Framework tools | [Module bank](module-bank.md), [BOF](bof-compatibility.md), [native](native-modules.md), [WASM](wasm-development.md), and [.NET assembly](assembly-modules.md) guides |
| Deeper agent networks | [Topology and relays](topology-and-relays.md) |
| Host a child payload on its parent agent's TCP relay | [Agent-hosted payloads](agent-distribution.md#host-a-payload-through-a-connected-agent) |
| Use a Windows SMB named-pipe child carrier | [SMB named-pipe relays](smb-named-pipe-relays.md) |
| All flags and feature index | [CLI reference](cli-reference.md) and [feature catalogue](../features.md) |

## What runs where?

- **Server:** the reachable carrier listeners and local operator API. It creates the server identity and accepts agent and VPN client sessions. Use `--tun` only when applications on the server host itself need routes through an agent; VPN Internet egress uses server sockets.
- **Agent:** a connector on the internal network. It opens TCP, UDP, and ICMP operations for the server. It needs no root/Administrator privilege, virtual adapter, route changes, or inbound port.
- **Client:** hosts your GUI and console. Routing modes create their own elevated TUN/Wintun: `--vpn` changes Internet routes, `--internal` adds internal paths, and both combine them. `--operator-only` needs no TUN or elevation but cannot install routes.
- **Operator commands:** `status`, `agent list/show/select`, `route add/del/list`, and `session kill` run on the server host against a token protected loopback API.

The key distinction is **which machine's traffic changes**. An agent exposes destinations reachable *from the agent host* and leaves that host's normal networking alone. A client routes applications *on the client host* into the tunnel for its selected prefixes. Starting a client does not expose its local network as an agent; starting an agent does not give its own host VPN Internet egress. Internal access needs a connected agent and a route to it.

## Client routing modes

Run one of these on the elevated **VPN client** host after starting the server and, for internal access, an agent:

```sh
sudo undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --operator leader --operator-password-file leader.password
sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --operator leader --operator-password-file leader.password
sudo undertow client --vpn --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --operator leader --operator-password-file leader.password
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
| Open | `--auth none` | `--auth none` | No enrollment secret; agent enrollment is open, but clients still need operator accounts |

You can copy `token.key` as a file, or paste the server's hex token into a PowerShell file with `echo "TOKEN_HEX" > token.key`. Undertow accepts PowerShell's UTF-16LE text as well as UTF-8. Use the exact token generated on the server; a different token will fail enrollment. Treat the token as a secret.

To avoid creating a token file on a host, pass its hex value with `--token TOKEN_HEX` on that host. Both server and connecting hosts accept this flag; each must use the same value. Do not combine `--token` with `--token-file`. A literal `--token` value is visible in process listings and may remain in shell history, so a restricted token file is safer on shared hosts.

Password mode requires at least 12 bytes. Use `--password-file` to avoid showing the password in a process command line; `--password TEXT` is available for temporary use. The password file's trailing newline is ignored. Token mode remains the default, and a `--token-file` is irrelevant to password or open mode. Open enrollment still encrypts transport and authenticates the server **when its fingerprint is pinned**; it provides no admission control. **For real deployments, use token or password enrollment.** With `--auth none`, agent enrollment is open; client connections still need operator credentials.

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

See [scenarios](scenarios.md) for working topologies, [remote port forwarding](remote-port-forwarding.md) to expose a client service through an agent, and [CLI reference](cli-reference.md) for every flag.

Back to [documentation home](README.md).
