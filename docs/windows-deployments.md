# Windows Jump

**Jump** uses an existing Windows agent to place and start an existing Undertow Windows artifact on another Windows host. It keeps one server-owned record for the complete operation and reuses Undertow's authenticated agent channel, Transfers, Jobs, History, enrolment, and Topology data.

Jump does not create a new build, enable an artifact host, or make the target retrieve a payload. The server streams the selected artifact through the source agent to the target's administrative share and verifies its SHA-256 before the selected Windows management method starts it.

## Before creating a Jump

You need:

- A Windows source agent that is connected or intentionally sleeping within its expected check-in window. A lost agent cannot be used for a new Jump.
- Retained source inventory from a current agent build.
- An active, non-revoked Windows artifact built by the same Undertow version as the source agent. Service Control also requires an artifact marked as service capable.
- A target hostname or IPv4 address reachable from the source host.
- Access from the source host to the target administrative share and the selected management interface, using either the source agent identity or supplied Windows credentials.

Jump uses the Windows identity of the source agent process by default. At Start, an operator may instead supply `USER`, `DOMAIN\USER`, or `USER@DOMAIN` with either a password or NT hash. A bare username is treated as a local account on the target. Password authentication requires `jump-credentials`; NT-hash authentication also requires the newer `jump-nt-hash` capability. Older sources can continue using their process identity. The account name is retained with the Jump record. Passwords and NT hashes are held only in server memory until dispatch, travel inside the authenticated source-agent streams, and are never written to the Jump, Job, Transfer, History, audit, command line, or queued-request records.

Password authentication lets Windows select Kerberos or NTLM according to the target name, account form, and host policy. NT-hash mode performs NTLM authentication directly for SMB delivery and the selected Windows RPC interface. It supports WMI, Service Control, and a LocalSystem Scheduled Task. WinRM does not accept an NT hash in this workflow, and current-user Scheduled Task needs a password or the source identity. If the server restarts before a sleeping source dispatches, the supplied secret is intentionally unavailable and that queued attempt fails clearly; create a new Jump to retry.

## Operator workflow

### Browser GUI

1. Open **Jump** and choose the source Windows agent.
2. Enter the target hostname or IPv4 address.
3. Select an existing Windows artifact. The selected profile and SHA-256 appear below it.
4. Choose **WinRM**, **WMI**, **Service Control**, or **Scheduled Task**, then choose an allowed execution context.
5. Select **Create jump**. This records the request and does not contact the target.
6. Select **Prepare jump**. Undertow validates the source inventory, source and artifact version, artifact state and hash, and method compatibility. Preparation does not contact the target.
7. Review the method prerequisites. Keep **Source agent identity**, select **Supplied username and password**, or select **Supplied username and NT hash** for a compatible method. Leave the install path blank for a generated path under `C:\Windows\Temp`, or enter an absolute Windows `.exe` path.
8. Select **Start jump**. If the source agent is sleeping, the linked Job remains queued until its next authenticated check-in.

The detail view shows the current state, progress or error text, source agent, build and profile, requesting operator, timestamps, install path, Transfer, Job, service or task identifier, and resulting agent. The Job contains method output; the Transfer shows delivery and hash verification. Opening the Jump page or a record never starts an operation.

### Console

From a selected source agent:

```text
use SOURCE_AGENT
jump create TARGET ARTIFACT_ID METHOD CONTEXT
jump prepare JUMP_ID
jump start JUMP_ID [INSTALL_PATH]
jump show JUMP_ID
```

From the top-level console, supply the source explicitly:

```text
jump create SOURCE_AGENT TARGET ARTIFACT_ID METHOD CONTEXT
```

Inspection and result-correlation commands are available in either context:

```text
jumps [SOURCE_AGENT]
jump show JUMP_ID
jump link JUMP_ID AGENT_ID
```

The older `deploy` and `deployments` command names remain accepted as compatibility aliases.

To use another Windows identity from the console, keep the password in a file on the console host:

```text
jump start JUMP_ID [INSTALL_PATH] --username DOMAIN\USER --password-file PATH
jump start JUMP_ID [INSTALL_PATH] --username DOMAIN\USER --nt-hash-file PATH
```

The console reads the selected secret file for this request and does not place the password or hash in command history. The two secret-file flags are mutually exclusive. The GUI uses masked inputs and clears the secret after submission.

## States and retry behavior

| State | Meaning |
| --- | --- |
| `created` | The server recorded the request. No target action has started. A preparation failure leaves the record here so the cause can be corrected and Prepare retried. |
| `prepared` | Source and artifact checks passed. A Start preflight failure leaves the record here. |
| `dispatching` | Start has claimed the record. The source may be transferring and launching now, or its durable Job may be queued for the next check-in. |
| `waiting` | The method Job was accepted. Undertow is waiting for matching agent enrolment, or the launch outcome is explicitly uncertain and needs review. |
| `completed` | A matching resulting agent enrolled and was linked to the Jump record. |
| `failed` | A definite failure occurred after dispatch began. The record and linked Job retain the reason. |

Start can claim a prepared record only once. A duplicate Start returns a conflict and does not launch another copy. Prepare and Start both revalidate mutable inputs, including artifact availability, revocation, hash, version compatibility, and source readiness.

A queued Job that uses the source identity survives a server restart. A queued Job with supplied credentials retains its metadata but fails after restart because its in-memory password or NT hash is deliberately cleared. If the server restarts after dispatch began and cannot prove whether execution reached the target, the record is marked failed with an explicit uncertainty warning. If the source check-in ends during execution, the record remains waiting with an uncertain outcome so a late enrolment can still correlate. Undertow does not silently replay an uncertain action.

## Methods and execution contexts

| Method | Context | Target prerequisites | Launch behavior |
| --- | --- | --- | --- |
| **WinRM** | Management identity | WinRM enabled; the selected identity authorized for the WS-Management session and `ADMIN$` | A native WSMan session invokes `Win32_Process.Create` for the staged agent. No remote shell or task is created. |
| **WMI** | Management identity | Remote WMI/DCOM access and `ADMIN$` for the selected identity | Native COM invokes `Win32_Process.Create` for the staged agent. No shell or Scheduled Task is created, and the removed `wmic.exe` tool is not used. |
| **Service Control** | LocalSystem | Remote Service Control Manager access and `ADMIN$`; service-capable artifact | Creates and starts a demand-start LocalSystem service. The service and binary remain after success. |
| **Scheduled Task** | Current user or LocalSystem | Remote Task Scheduler access and `ADMIN$` | Creates, runs, and removes a one-shot task. Current-user context requires a resolvable domain identity and an interactive session on the target. |

NT-hash authentication applies to WMI, Service Control, and LocalSystem Scheduled Task. It authenticates SMB and RPC directly and does not create a Windows logon session or put the hash in a process argument.

A source-local account cannot be mapped to a different target for a current-user Scheduled Task. Source-identity and password launches use the existing native Windows programs and COM paths. NT-hash launches authenticate SMB and Windows RPC directly. Neither path creates a PowerShell process, a `cmd.exe` command chain, a generated helper, or a second temporary executable.

## Delivery, paths, and cleanup

The only delivery value is `agent-channel`. Undertow streams the selected server artifact over the source agent's existing authenticated connection. The source writes it directly to the final target path through an administrative share, and the existing Transfer verifies the selected artifact SHA-256 before method execution.

When no path is supplied, Undertow generates:

```text
C:\Windows\Temp\<random>.exe
```

That path is written through `\\TARGET\ADMIN$\Temp\<random>.exe`. An override must be an absolute Windows `.exe` path; another drive is mapped through its administrative share, such as `D:\Tools\agent.exe` to `\\TARGET\D$\Tools\agent.exe`. IPv6 targets are rejected while this delivery path depends on Windows administrative-share UNC addressing.

If launch fails before it is accepted, Undertow attempts to remove the staged binary. One-shot tasks are removed after launch. A Service Control start failure removes the failed service and staged binary. After a successful launch, the agent binary remains. A successfully started service also remains because it is the requested installation.

## Enrolment correlation and Topology

Once the method Job is accepted, the Jump waits for enrolment. Undertow links a new agent automatically only when all of these are true:

- It reports the exact selected artifact.
- Its first connection is after the Jump entered `waiting`.
- Its hostname matches the target, or its target IP and source lineage satisfy the stricter IP match.
- Exactly one waiting Jump matches.

If a callback is late or ambiguous, use **Link resulting agent** in the GUI or `jump link JUMP_ID AGENT_ID`. Manual linking still requires a connected, inventory-ready Windows agent with the selected artifact and a matching target. Completion records the `deployed_from` relationship. Topology displays that deployment relationship while preserving the agent's observed relay lineage.

## HTTP API

The user-facing name is Jump; the stable API path remains `/v1/deployments` for compatibility.

| Request | Purpose |
| --- | --- |
| `GET /v1/deployments?source_agent_id=ID` | List all records or filter by source agent. |
| `POST /v1/deployments` | Create a Jump record. |
| `GET /v1/deployments/{id}` | Read one record. |
| `POST /v1/deployments/{id}/prepare` | Validate a created record. |
| `POST /v1/deployments/{id}/start` | Claim and queue or execute a prepared record. |
| `POST /v1/deployments/{id}/link` | Link a matching enrolled agent manually. |

Create request:

```json
{
  "source_agent_id": "SOURCE_AGENT_ID",
  "target": "app01",
  "artifact_id": "ARTIFACT_ID",
  "method": "winrm",
  "context": "current-user"
}
```

Start request; omit `install_path` to generate the default:

```json
{
  "delivery": "agent-channel",
  "install_path": "C:\\Windows\\Temp\\agent.exe"
}
```

Credentialed Start request; omit both credential fields to use the source agent identity:

```json
{
  "delivery": "agent-channel",
  "username": "DOMAIN\\operator",
  "password": "target account password"
}
```

NT-hash Start request; send `nt_hash` instead of `password`:

```json
{
  "delivery": "agent-channel",
  "username": "DOMAIN\\operator",
  "nt_hash": "32 hexadecimal characters"
}
```

Manual link request:

```json
{
  "agent_id": "RESULT_AGENT_ID"
}
```

Each record retains source and target, artifact and profile snapshot, artifact SHA-256, method and context, the optional supplied account name, requesting operator and client source, timestamps, state, progress or error, Job and Transfer IDs, delivery and resolved path, service or task name, and the optional resulting agent and relationship. Passwords, NT hashes, retrieval tokens, helper content, and artifact bytes are not stored in the Jump record or audit entry. Operator mutations use the existing History and audit model.

Back to [documentation home](README.md).
