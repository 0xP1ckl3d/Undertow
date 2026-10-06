# Jump

**Jump** coordinates an existing Windows source agent, a target hostname or IPv4 address, an existing Windows artifact, and one native Windows management method. The durable record retains the build and profile, SHA-256, source and target, method and context, requesting operator, timestamps, progress, linked Job and Transfer, resolved install path, method identifier, and resulting agent relationship.

## Workflow

**Create** records intent. **Prepare** validates the retained Windows source and selected artifact without touching the target. **Start** revalidates both, queues one durable Job when the source is sleeping, streams the artifact through the authenticated source-agent channel, and invokes the selected method. The state flow is `created → prepared → dispatching → waiting → completed`, with `failed` available after dispatch begins. A server restart during dispatch records an uncertain result and does not silently replay the action.

Jump always uses agent-channel delivery. The source writes directly to a unique final path under `\\TARGET\ADMIN$\Temp` and the transfer verifies SHA-256 before execution. There is no target-side HTTP or SMB-pipe retrieval, generated script, PowerShell process, secondary temporary executable, or automatically enabled artifact host. An operator may provide another absolute `.exe` path. IPv6 targets are rejected while delivery depends on Windows administrative-share UNC paths.

If a method fails before launch is accepted, Jump removes the uploaded executable. One-shot tasks are also removed. A Service Control start failure removes the failed service and executable. A successfully started service and its agent binary remain because the service is the requested result.

## Methods and contexts

- **WinRM** uses WinRS as the transport, creates and runs a short target-local one-shot task, and removes that task after launch. The source identity must be authorized for WinRM and have `ADMIN$` access.
- **WMI** calls `Win32_Process.Create` through Windows COM. It does not depend on the removed `wmic.exe` command-line tool.
- **Service Control** creates and starts a LocalSystem service with demand start. Only service-capable Windows artifacts are eligible. The opaque service name is retained in the Jump record.
- **Scheduled Task** creates, runs, and removes a one-shot task through the remote Task Scheduler interface. LocalSystem is supported. Current-user context requires a domain identity that maps on the target and an interactive target session; a source-local account is rejected for a different target.

The management worker runs as a noninteractive source-agent Job with ordinary pipes. It does not use a pseudo console or `cmd.exe` command chain. Endpoint-visible service and task identifiers are derived from the random record ID and do not contain product branding.

## Result correlation

Undertow links a newly enrolled agent automatically only when it reports the selected artifact, connects after the record entered `waiting`, and exactly one waiting record matches the target. Otherwise an operator can link a matching enrolled Windows agent manually. The relationship appears in Topology while observed relay lineage remains intact.

Console commands:

```text
jumps [SOURCE_AGENT]
jump create [SOURCE_AGENT] TARGET ARTIFACT_ID METHOD CONTEXT
jump show ID
jump prepare ID
jump start ID [INSTALL_PATH]
jump link ID AGENT_ID
```

The earlier `deploy` and `deployments` command names remain accepted as compatibility aliases. Jobs retain method output, Transfers retain delivery progress and verification, and operator mutations appear in History through the existing audit system.
