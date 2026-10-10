# Windows authentication contexts / tokens

Open **Agents → your Windows agent → Tokens**. Each context is a live Windows token owned by that agent process. Undertow sends only opaque context IDs and identity metadata to operators. Token handles are never sent to the server, written to disk, or placed in Jobs or history.

The agent must advertise and allow the `tokens` capability. Windows agents enable it by default; `--deny tokens` disables management and selected-token execution. Older and non-Windows agents keep their existing execution behaviour and reject supplied token context IDs.

## Manage contexts

**Discover candidates** includes the agent's original process token and accessible process tokens, including other users and LocalSystem when Windows grants the agent access to those process tokens. Discovery uses ordinary Windows access checks without enabling privileges or accessing linked elevated tokens. Candidates expire after two minutes or another discovery. Candidate metadata remains visible with an explicit `expired` state after expiry; navigation between agent tabs does not reset it. Rediscovery, Clear, agent restart, or another real invalidation replaces it. **Import duplicate** creates an independent stored context with a new ID. The owning process need not keep the source token open after import.

The table shows context ID, identity, domain/user, token type, impersonation level, integrity, session ID, elevation state/type, source, creation time, and whether the duplicated primary token has process-launch access rights. This rights indicator does not promise that the caller has the required Windows privilege or that the selected account can execute a particular program. Tokens with only impersonation rights remain available for in-process operations; child-process operations reject them explicitly. Imported and created execution tokens are normalised to primary tokens; their impersonation-level field is `not-applicable`. Discovery is bounded to 128 candidates and the store holds at most 128 contexts.

**Create a context** supports Windows `LogonUserW` interactive, network, batch, and new-credentials logons. Account logon rights and agent process permissions apply. New credentials preserves the local identity and supplies different outbound network credentials; Windows does not validate that password during creation. Clear Domain for a UPN, or use `.` for a local account. The client seals transient logon input to a single-use agent key before forwarding it through the server. Keys expire after one minute; tampering, expiry, replay, and keys from another agent are rejected. The server receives only ciphertext and does not retain creation requests.

Opening Tokens reads the last server-held metadata immediately, including an empty first snapshot; it never waits for the agent. Refresh, list, discovery, import, creation, remove, and clear requests wait in memory for a sleeping/check-in agent's next authenticated check-in. They do not become durable Jobs or survive a server/client restart. Plaintext logon material remains only in the local operator client long enough to seal it; neither plaintext nor a reusable secret enters the server queue. **Use for this session** and **Revert** are server-side session choices and apply immediately while the agent sleeps.

**Remove** closes a stored context. **Clear store** closes all stored contexts and candidates and invalidates pending creation keys. Already-running work holds its own token duplicate and can finish; queued work that has not acquired a token fails when the selected context is unavailable. Contexts survive transport reconnects in the same process, but disappear when the agent process exits. Restarting the server does not recreate agent tokens.

## Choose an execution identity

The Job, Modules, and Shell forms expose **Authentication context**. Choose a stored context, the authenticated operator connection's default, or **Agent process identity**. Console execution/module commands accept `--token-context CONTEXT_ID` or `--token-context process`.

**Use for this session** affects only future work submitted through this authenticated operator connection for this agent. Other connections, including another connection with the same account, keep their own defaults. GUI and console on the same client connection share that default. It is not an agent-wide identity change and does not survive client disconnect/server restart.

An explicit selection takes precedence over the session default. The server freezes the chosen ID when submitting a Job, so changing a default never changes queued work. Job details and audit history record that opaque ID. Selected-identity host commands do not overwrite the shared baseline process-identity inventory.

**Revert to process identity** clears only this connection's default; it also works when the agent is offline. Removed or restarted-agent contexts remain visibly unavailable until the operator chooses a live context or reverts. Execution fails instead of silently falling back to the process identity. Removing/clearing shared contexts does not change another operator's default.

Programs, scripts, interactive shells, background command Jobs, BOF workers, and assembly workers launch with a selected primary token that has process-launch access rights. Undertow inspects the original agent process token's privileges and prepares a short-lived caller token on the launch thread. When `SeImpersonatePrivilege` is present, it uses `CreateProcessWithTokenW` with plain `STARTUPINFO` and inheritable standard handles; otherwise, it uses `CreateProcessAsUserW` only when both `SeAssignPrimaryTokenPrivilege` and `SeIncreaseQuotaPrivilege` are present. Selected-token Shell uses a pipe-backed terminal because its ConPTY attribute requires extended startup information. Disabled privileges are enabled on the isolated caller token and verified before launch. An absent privilege is reported, not assumed to be gained through impersonation. Each launch uses one API; failure is returned without an agent-process-identity fallback. Undertow does not change `TokenSessionId` or require `SeTcbPrivilege`. The launch thread's previous identity is restored immediately after process creation. Worker executables are staged in a random ProgramData directory with a protected ACL granting the selected SID read/execute access, then removed after execution; this permits a different user to launch a worker without granting access to the agent's private directory. Built-in operations, native module entry points, and WASM execution use impersonation restricted to their locked operation thread, then restore the previous thread identity. A native module creating additional threads must follow Windows thread-token semantics; those threads do not automatically inherit impersonation. Existing working directories, environment and module runtime limits still apply; selecting a token does not expand capabilities.

Jump freezes the selected context when **Start jump** is submitted. The same opaque context ID applies to target artifact delivery and the selected WinRM, WMI, Service Control, or Scheduled Task worker. Removing it before either phase acquires the token fails closed. Supplied Jump username/password or NT-hash credentials are a separate override: the GUI and console explicitly select `process` for that mode, and the server rejects a supplied credential combined with another token context.

## Console commands

In an agent console, use:

```text
tokens list
tokens discover
tokens import CANDIDATE_ID
tokens create USER DOMAIN PASSWORD_FILE interactive|network|batch|new_credentials
tokens use CONTEXT_ID
tokens revert
tokens remove CONTEXT_ID
tokens clear
whoami --token-context CONTEXT_ID
exec --token-context CONTEXT_ID whoami.exe /user
job start --token-context CONTEXT_ID whoami.exe /user
shell --token-context process
jump start JUMP_ID --token-context CONTEXT_ID
```

At the main menu, insert the full agent ID after `tokens`. The GUI Console is already bound to its selected agent. `help tokens` shows the syntax. `DOMAIN -` means no domain for UPN logon. Password files are read on the console/client host and must be supplied outside command history; password text is never an argument. The GUI clears its password field after the creation attempt and keeps it out of command history/preferences. The unauthenticated local server console cannot set a session default: use an explicit per-operation ID there, or an authenticated client for `tokens use`.

## Protocol and storage boundary

Live management uses `tokens.undertow.invalid:0`, reached through `GET /v1/agents/{id}/tokens` and `POST` with an allowlisted action. The server caches only returned context/candidate metadata so the workspace can render while the agent sleeps and across tab navigation. A random per-process token-store instance ID invalidates that cache after an agent restart. `creation-key` is an internal client handshake before `create`; its response contains only a public key, opaque key ID and expiry. `create` accepts `sealed_logon`, never plaintext logon fields. The encrypted envelope uses ephemeral X25519, a domain-separated SHA-256 shared-secret derivation, and AES-GCM with key ID as associated data. Agent private keys are volatile and consumed before processing the decrypted request.

Existing exec, interactive, memory-module, and queued Job request headers gain optional `token_context_id`. Missing IDs preserve current behaviour unless an authenticated session default is set; `process` explicitly bypasses that default. Module/source bytes remain unchanged. Servers negotiate the capability before dispatching a selected ID, and agents validate IDs and acquire a fresh duplicate for each operation. Audit records contain the action, result, attribution and opaque ID, not request bodies, sealed payloads, handles, or passwords. Token selection flags are consumed by Undertow rather than passed to child command lines.

The engagement [Credential Store](credential-store.md) can create a context from an authorised stored password reference. The server resolves the encrypted entry, seals a transient logon request to the agent's single-use public key, and forwards only the sealed request. The resulting token remains agent-side. NT hashes cannot be used for Windows `LogonUserW` token creation. `internal/authcontext.CredentialResolver` remains an optional agent-side extension point; stored server credentials do not require a live token handle or a change to token-store IDs.

## Windows UAT

Run the automated tests on Windows:

```powershell
go test ./internal/authcontext ./internal/control ./cmd/undertow -count=1
go test ./internal/pivot -run TestWindowsTokenContextUAT -count=1 -v
```

Automated Windows coverage uses ordinary accessible tokens and checks discovery/import, live mux management, metadata, selected-token identity, selected-token child processes, concurrent thread restoration, removal/stale rejection, disabled capability enforcement, sealed invalid-account logon, and replay rejection. The ordinary-user child-process test requires the Windows test process to hold a supported launch privilege; a machine without one fails that test rather than claiming execution success. Cross-platform unit tests cover resource closure, expiry, limits, concurrent remove/acquire, session isolation, frozen durable queues, metadata-only serialisation, audit persistence, strict management paths and client sealing. The reconnect inventory test checks that token capability negotiation fits the control frame and preserves the validated artifact identity.

The foreign-process UAT is opt-in because it needs a prepared Windows lab. Start one process as a dedicated alternate user and one accessible process as LocalSystem. Compile the identity-reporting worker fixture and provide the exact identities:

```powershell
csc.exe /nologo /target:exe /out:$env:TEMP\undertow-token-identity.exe internal\pivot\testdata\token-identity.cs
$env:UNDERTOW_TOKEN_UAT_PROCESS_IDENTITIES='["LAB\\token-user","NT AUTHORITY\\SYSTEM"]'
$env:UNDERTOW_TOKEN_UAT_IDENTITY_ASSEMBLY="$env:TEMP\undertow-token-identity.exe"
go test ./internal/pivot ./internal/control -run TestWindowsTokenContextUATImportedForeignProcess -count=1 -v
```

Those tests import process tokens rather than creating them with `LogonUserW`. For both identities they verify a direct `exec`, Live shell, a real control-plane background Job, and the .NET worker path against the selected identity. Missing candidates, process-launch privilege errors, identity mismatches, and worker fallback all fail the UAT. A selected-token interactive Shell launches a short-lived worker under the selected primary token; that worker creates ConPTY and the shell under its own identity. Explicit `NoPTY` jobs remain pipe-backed.

Run `TestWindowsTokenContextUATImportedShellStreaming` with `UNDERTOW_TOKEN_UAT_SHELL=1`, `-undertow-token-target-identity`, and `-undertow-token-source-process` against both the alternate-user and LocalSystem process. It sends `who` before Enter and requires immediate echo, then completes `whoami.exe /user`, resizes the terminal, and checks the selected SID. This catches the earlier pipe-backed `cmd` regression that a launch-only shell test missed.

Run `TestWindowsTokenContextUATImportedNativeWorkerIdentity` with `UNDERTOW_TOKEN_UAT_SHELLPOWER=1` and `-undertow-token-shellpower-module` pointing to the packaged `shellpower.module` on the Windows host. Set `-undertow-token-target-identity` and `-undertow-token-source-process` for the alternate-user process, then repeat with the default LocalSystem/LogonUI target. The test runs both one-shot and live ShellPower under the imported primary token and requires PowerShell's managed-thread SID to match it. Selected native modules run in a short-lived worker process because thread impersonation alone does not carry into CLR-managed threads.

For a staged BOF identity check, compile `internal/bof/testdata/examples/identity.c` into a COFF object with an x64 MinGW or MSVC compiler. The `TestWindowsTokenContextUATProcessLaunchStages` test binary accepts `-undertow-token-identity-assembly`, `-undertow-token-identity-bof`, `-undertow-token-target-identity`, and `-undertow-token-source-process`. Run it once against a discoverable LocalSystem process and once against an alternate-user process. It checks basic process creation separately from STDIO, then `exec`, both Shell launch modes, .NET and BOF workers; the worker output must report the selected SID. The test skips when the assembly or BOF fixture path is omitted.

The control-plane background Job UAT accepts `-undertow-token-alternate-identity` and `-undertow-token-alternate-source` on its Windows test binary. It imports that user's process token and a LocalSystem LogonUI token, queues `whoami.exe /user` as a Job for each, and requires the completed Job output to match its selected identity.

An optional alternate-account test uses `UNDERTOW_TOKEN_UAT_LOGON_FILE` pointing to a protected local JSON file containing `user`, `domain`, `password`, and `logon_type`. Only the file path goes in the environment. The test does not print credential fields and skips when the fixture is absent. Use a dedicated lab account with the required logon rights. Test network/new-credentials behaviour against an authorised lab share separately.

| Manual scenario | Expected result |
| --- | --- |
| Two authenticated clients select different contexts and run `whoami` concurrently | Each command uses its own context; baseline process identity remains unchanged |
| Queue work while the agent sleeps, then change the submitting session's default | The Job retains its original ID and executes with that selection on callback |
| Submit discover/import/create/remove/clear while the agent sleeps | The request waits for the next authenticated check-in; no token handle, plaintext credential, or reusable secret appears in a durable queue |
| Leave and reopen Tokens after discovery | Candidate rows remain and transition to `expired` at their agent-side expiry instead of disappearing on navigation |
| Remove a context before queued work dispatches | Work fails with context unavailable; no process-identity fallback |
| Remove/clear during running work | Accepted work finishes with its leased token; future acquisitions fail |
| Disconnect/reconnect the carrier without ending the agent process | Stored contexts remain available |
| Exit/restart the agent with the same enrollment identity | Contexts are empty; old defaults/Jobs fail until explicitly reverted or reselected |
| Restart the server/client | No token is recreated; authenticated connection defaults reset; retained Jobs/audit contain only IDs |
| Interactive, network, batch, new-credentials creation with valid lab credentials | Supported logon succeeds when rights allow; network authentication reflects Windows logon semantics |
| Invalid logon, insufficient process launch rights, old agent, non-Windows agent, denied capability | Clear error without password/handle disclosure or identity fallback |
| GUI refresh, discover/import, use/revert, remove/clear, per-Job/module/shell selection | Live metadata and this session's selection match; no secret appears in preferences/history |
| Jump with an operator default or explicit context | ADMIN$ delivery and the remote-management worker use the frozen context; supplied Jump credentials remain a separate process-context override |

Before release, inspect the operations database, queued requests, audit/history and logon errors using fixture credentials to confirm no plaintext credential or native handle crosses the storage boundary. The successful alternate-account and outbound-network scenarios require a configured Windows lab and are not replaced by the ordinary-process-token tests.
