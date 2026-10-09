# Windows SMB named-pipe relays

An agent on Windows can accept child agents through a Windows named pipe instead of a TCP relay listener. A remote Windows child reaches that pipe through the parent host's SMB service. The parent carries the child's Undertow session over its existing connection to the server. The server still authenticates the child as a separate agent, owns its routes and jobs, and shows `relay-smb` and the parent ID in topology.

Nothing starts by default. An operator must start a named-pipe relay on a connected Windows parent, then start a Windows child configured for it. Opening the GUI or selecting the parent does not start a listener or agent shell. TCP relays remain available on the same parent.

## Before you start

- The parent and child must run Windows. The parent needs the `relay` capability; `--deny=relay` blocks both TCP and named-pipe listeners.
- The child must be able to reach the parent's SMB service and authenticate to Windows. Undertow does not enable SMB, change firewall rules, or create a Windows account. The pipe's default ACL admits Windows Authenticated Users for read/write; Undertow's separate enrollment and pinned server identity still gate the child session.
- Obtain the server fingerprint and the child enrollment credential through your usual trusted channel. Each child keeps its own agent identity.
- Choose a pipe name of 1–64 letters, digits, dots, underscores, or hyphens. The parent bind form is `\\.\pipe\NAME`. A remote child uses `\\PARENT_HOST\pipe\NAME`. A child on the same Windows host may use `\\.\pipe\NAME`.

## Start from the terminal console

Select the connected Windows parent in the server or connected client console, then start the pipe:

```text
agents
use 1
relay start \\.\pipe\branch_ops
relay list
topology
```

On the Windows child, run the full agent with a separate identity key:

```powershell
.\undertow.exe agent --transport relay-smb --server '\\PARENT_HOST\pipe\branch_ops' --fingerprint FINGERPRINT --token-file token.key --agent-key child.key
```

For a configured child binary, create and build a Windows profile:

```text
payload profile create branch-pipe server=\\PARENT_HOST\pipe\branch_ops transport=relay-smb
payload build branch-pipe windows amd64
payload download PAYLOAD_ID
```

The `server` field here is the child's pipe address, not the Undertow server's network address. The build still embeds the original server fingerprint and a new artifact enrollment credential. A Linux target is rejected for `relay-smb`.

`payload download PAYLOAD_ID` saves the build on the console host through the authenticated control connection. You can also deliver the Windows artifact through the existing pipe listener. This is an explicit step after building:

```text
payload host-agent PAYLOAD_ID PARENT_AGENT_ID \\.\pipe\branch_ops PARENT_HOST
payload agent-hosts PAYLOAD_ID
payload verify-script-agent HOST_ID powershell
payload deploy-script-agent HOST_ID powershell
```

Use a parent hostname or IP that the child can reach over SMB for `PARENT_HOST`; direct IP addresses are supported. Use `.` only when the helper will run on the parent host itself. The host command authorizes one opaque download token on that same named-pipe listener. It does not start another listener. A Windows PowerShell helper connects to `\\PARENT_HOST\pipe\branch_ops`, pins the temporary TLS certificate, requests the artifact, verifies its SHA-256, and then starts the downloaded binary **only when the operator runs that helper**. The optional `verify-script-agent` helper sends a pinned HEAD request through the same pipe and checks the artifact hash without downloading or starting a payload. It is a diagnostic, not a prerequisite for hosting or deployment. The parent fetches the bytes from the Undertow server over its existing authenticated session. `payload unhost-agent HOST_ID` disables the download token without stopping child sessions. The pipe is not an HTTPS URL, so a browser or `curl` cannot fetch it directly. POSIX shell helpers are not available for named pipes. Use `relay stop \\.\pipe\branch_ops` to close the listener after it is no longer needed.

## Use the browser GUI

1. In **Networking → Relays**, select a connected Windows parent and choose **SMB named pipe**. Enter `\\.\pipe\branch_ops` and click **Start relay**.
2. In the new relay row, click **Create child payload**. **Payloads** selects the `Agent relay · SMB named pipe` carrier. Enter a child-reachable UNC path such as `\\PARENT_HOST\pipe\branch_ops`; the parent's local `\\.\pipe\...` path is not copied as a remote address.
3. Save the profile and build a Windows x64 artifact. In **Payloads → Artifacts → Host through an agent**, select the parent and its named pipe, then enter the parent host that the child can reach over SMB. Click **Enable pipe delivery**. Preview or download the pinned PowerShell helper. You can still download the artifact directly through the operator client for another delivery method.
4. Once the child connects, inspect **Topology**. The child appears with its own agent ID, `relay-smb` carrier, and the parent as its `Via` path. Open its workspace for normal commands and retained records.

For a TCP relay, **Host through an agent** provides a pinned HTTPS URL on the relay port. For an SMB relay, it provides a pipe endpoint and a PowerShell helper. Both use the existing listener and fetch the artifact from the server on demand. Enabling delivery never runs the helper or starts a child agent.

## What the carrier does

The named pipe carries the same framed, end-to-end Undertow relay session as the TCP path. SMB provides access to the pipe, while the child verifies the original server fingerprint and completes normal enrollment. The parent cannot substitute its own identity for the child. The server records the child's carrier as `relay-smb`, its parent, depth, jobs, routes, and lifecycle independently. Nested relays remain subject to the existing depth and loop checks.

`relay stop \\.\pipe\branch_ops` closes the listener and removes its saved configuration. The pipe closes when the parent disconnects or the server stops. The server reopens a previously started pipe after that parent reconnects and reports its relay capability; children may then reconnect according to their normal reconnect policy. A bind or SMB access failure still needs operator attention. Listeners started under older server versions need to be started once after upgrading so their binds can be saved.

## Diagnose a failed connection

| Symptom | Check |
| --- | --- |
| `relay start` fails | The parent must be Windows and connected, the `relay` capability must be allowed, and the name must not already be in use. |
| The child cannot open the pipe | Check the UNC parent host, SMB reachability, Windows credentials, firewall policy, and whether `relay list` still shows the listener. On the child, `Test-NetConnection PARENT_HOST -Port 445` checks SMB transport, and `net use \\PARENT_HOST\IPC$` checks Windows authentication separately from Undertow. A Windows logon error from both `net use` and the pipe helper occurs before the relay protocol runs. A hostname or direct IP may be used, subject to the site's Windows authentication policy. Undertow does not change those Windows services or disable authentication. |
| `The user name or password is incorrect` on a domain-joined child | Check Windows System logs on both hosts for LsaSrv event 6167. [Microsoft documents](https://support.microsoft.com/en-us/servicing/os/windows/docs/2025/10/kerberos-and-ntlm-authentication-failures-due-to-duplicate-sids) Kerberos and NTLM failures between Windows installations with duplicate machine SIDs. Compare each host's local Administrator SID prefix with `((Get-LocalUser -Name Administrator).SID.Value -replace '-500$','')`; different computers should have different prefixes. Correct the Windows image deployment problem through supported OS provisioning, rather than changing Undertow pipe authentication. |
| The pipe opens but the child is rejected | Check the child's pinned server fingerprint, enrollment credential, and server audit/lifecycle records. Windows SMB login and Undertow enrollment are separate checks. |
| A payload profile will not build | `relay-smb` is for Windows targets. Enter the child-reachable UNC pipe path, then select Windows x64. |

For the general parent/child model, see [Topology and relays](topology-and-relays.md). For binary profiles and downloads, see [Payload deployment](agent-distribution.md). For command navigation, see [Console guide](console.md).

Back to [documentation home](README.md).
