# Windows deployments

The **Deployments** view coordinates an existing Windows source agent, a target hostname or IP, an existing Windows artifact, and one Windows management method. The durable record retains the selected build and profile, artifact SHA-256, source and target, method and execution context, requesting operator, timestamps, progress, linked Job, install path, method identifier, and any resulting agent. Retrieval capabilities and script source are never stored in the deployment record or Job metadata.

## Workflow

**Create** records the request. **Prepare** requires a connected, inventory-ready Windows source and revalidates the artifact. **Start** revalidates both again, reserves the record, delivers the artifact, and starts one immediate source-agent PowerShell Job. Successful enrolment completes the record. The state flow is `created → prepared → dispatching → waiting → completed`, with `failed` available after dispatch begins. A server restart while dispatching records an uncertain failure and does not replay the action.

Choose one delivery route at start:

- `direct-share` streams the artifact file from the server through the source agent's authenticated Undertow channel. The source writes it to the target administrative share, and the transfer protocol verifies SHA-256 before the method starts. This requires the source identity to access the target share, but no HTTP or relay payload host.
- `server` uses the artifact's existing server HTTPS host and its current opaque path.
- `agent-host:HOST_ID` uses an existing HTTPS relay or SMB named-pipe artifact host on the selected source agent.

Hosting is never enabled automatically. Hosted retrieval details exist only in the in-memory script sent to the source agent. Deployment scripts are immediate-only and are not placed in the durable sleeping-agent queue.

The default target path is `C:\ProgramData\Undertow\Deployments\DEPLOYMENT_ID\FILENAME`. An operator may provide another absolute `.exe` path. Undertow records the resolved path. Direct-share delivery refuses to overwrite an existing file through the transfer protocol.

## Methods and contexts

- **WinRM** runs the verified helper in a remote PowerShell session, or starts a direct-share binary already on the target.
- **WMI** stages a short helper through the administrative share for hosted delivery and starts it with `Win32_Process.Create`. Direct-share starts the verified binary directly.
- **Service Control** installs and starts a persistent LocalSystem service. Only Windows artifacts built with service support are eligible. The service name is retained in the deployment record.
- **Scheduled Task** creates, runs, and removes a one-shot task. LocalSystem and the source identity's interactive context are supported. The task name is retained for history even after cleanup.

WinRM and WMI use the source agent's current Windows identity. Named-account creation is disabled until Undertow has a separate transient credential integration; older stage-one records remain readable and cannot be started. No password is stored in a deployment, Job, or audit record.

## Result correlation

Undertow links a newly enrolled agent automatically only when it reports the selected artifact, connects after the record entered `waiting`, and exactly one waiting record matches the target hostname. For an IP target, the observed address must match and the child must arrive through the selected source agent's relay lineage. Otherwise the operator can link a matching connected Windows agent manually. One enrolled agent can be the result of only one deployment record. The result appears as a deployment edge in Topology without changing observed relay lineage.

Console commands:

```text
deployments [SOURCE_AGENT]
deploy create [SOURCE_AGENT] TARGET ARTIFACT_ID METHOD CONTEXT
deploy show ID
deploy prepare ID
deploy start ID direct-share|server|agent-host:HOST_ID [INSTALL_PATH]
deploy link ID AGENT_ID
```

When an agent is selected with `use`, omit `SOURCE_AGENT`. Jobs and their retained output are available through the normal Jobs view. Operator mutations appear in History through the existing audit system.
