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

`payload download PAYLOAD_ID` saves the build on the console host through the authenticated control connection. The pipe does not provide an HTTPS artifact URL, so `payload host-agent` cannot target it. Deliver the saved Windows binary through your chosen channel; starting the pipe or building a payload does not launch the child. Use `relay stop \\.\pipe\branch_ops` to close the listener after it is no longer needed.

## Use the browser GUI

1. In **Relays**, select a connected Windows parent and choose **SMB named pipe**. Enter `\\.\pipe\branch_ops` and click **Start relay**.
2. In the new relay row, click **Create child payload**. **Payloads** selects the `Agent relay · SMB named pipe` carrier. Enter a child-reachable UNC path such as `\\PARENT_HOST\pipe\branch_ops`; the parent's local `\\.\pipe\...` path is not copied as a remote address.
3. Save the profile, build a Windows x64 artifact, and download it from **Payloads → Artifacts** for your chosen delivery method.
4. Once the child connects, inspect **Topology**. The child appears with its own agent ID, `relay-smb` carrier, and the parent as its `Via` path. Open its workspace for normal commands and retained records.

The existing **Host through an agent** HTTPS download URL applies to a TCP relay listener. A named pipe has no HTTP URL for a browser or standard `curl`/PowerShell web download, so use the server artifact download or another approved delivery channel for an SMB child payload.

## What the carrier does

The named pipe carries the same framed, end-to-end Undertow relay session as the TCP path. SMB provides access to the pipe, while the child verifies the original server fingerprint and completes normal enrollment. The parent cannot substitute its own identity for the child. The server records the child's carrier as `relay-smb`, its parent, depth, jobs, routes, and lifecycle independently. Nested relays remain subject to the existing depth and loop checks.

`relay stop \\.\pipe\branch_ops` closes the listener. The pipe also closes when the parent disconnects or the server stops. Children may reconnect when the parent and listener return, according to their normal reconnect policy; start the listener again explicitly after the parent reconnects.

## Diagnose a failed connection

| Symptom | Check |
| --- | --- |
| `relay start` fails | The parent must be Windows and connected, the `relay` capability must be allowed, and the name must not already be in use. |
| The child cannot open the pipe | Check the UNC parent host, SMB reachability, Windows credentials, firewall policy, and whether `relay list` still shows the listener. Undertow does not configure those Windows services. |
| The pipe opens but the child is rejected | Check the child's pinned server fingerprint, enrollment credential, and server audit/lifecycle records. Windows SMB login and Undertow enrollment are separate checks. |
| A payload profile will not build | `relay-smb` is for Windows targets. Enter the child-reachable UNC pipe path, then select Windows x64. |

For the general parent/child model, see [Topology and relays](topology-and-relays.md). For binary profiles and downloads, see [Payload deployment](agent-distribution.md). For command navigation, see [Console guide](console.md).

Back to [documentation home](README.md).
