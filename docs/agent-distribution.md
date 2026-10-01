# Configured thin agents

`undertow` remains the full server, client, and operator console. `undertow-agent` is a separate remote executable with the same agent connection loop and agent-side services. It has no operator console and accepts no normal command-line options. The release build creates prebuilt templates for Windows amd64 and Linux amd64/arm64. The server stamps a selected profile into a template, so the live server needs no Go compiler.

## Build and deploy

Build release binaries before deploying the server:

```sh
sh tools/build-release.sh bin
```

On Windows, use `tools/build-release.ps1`. Keep the `undertow-agent-PLATFORM-ARCH` templates and `undertow-agent-templates.json` manifest alongside the full `undertow` binary, or pass `--agent-templates DIRECTORY` to the server. The manifest records the template hashes and build identity; the server rejects a missing, changed, or mismatched template. The profile and artifact database defaults to `agent-distribution` under the server's working directory; set `--agent-store DIRECTORY` for a dedicated persistent location. The store contains enrollment secrets and should be accessible only to the server operator. Back it up with the server identity and enrollment configuration.

In a server or client console:

```text
agent profile create office server=undertow.example.com:443 transport=quic
agent profile list
agent profile show office
agent build office windows amd64
agent artifacts
agent host ARTIFACT_ID
```

The server fills in its pinned fingerprint, enrollment secret, DNS domain, WebSocket path, and other known settings. If its listener binds `0.0.0.0` or `::`, provide a reachable `server=HOST:PORT` because the public address cannot be inferred safely. If the server uses a self-signed TLS certificate, the profile defaults to skipping TLS certificate verification while still pinning Undertow's server identity. Use a valid TLS certificate for HTTPS artifact downloads when possible.

`agent host` requires an active WebSocket HTTPS listener. It returns the artifact's filename, size, SHA-256, and retrieval URL. Creating an artifact does not host it. The URL serves only that explicitly hosted artifact. `agent unhost ARTIFACT_ID` revokes future downloads without deleting the artifact; `agent delete ARTIFACT_ID` removes its record and file. `agent hosted` shows the hosted subset.

Download the executable, verify its SHA-256, and run it with no arguments. Each installation creates its own identity key in the user's Undertow agent configuration directory, scoped to the installed executable path. The profile contains connection and enrollment settings, not a shared agent identity. The agent connects, reports inventory, serves the usual capabilities, and reconnects after a lost session.

Optional scripts are generated with `agent deploy-script ARTIFACT_ID powershell` or `agent deploy-script ARTIFACT_ID shell`. They download the selected hosted artifact, verify SHA-256, place it at a chosen destination, and start it without agent flags. They contain no persistence mechanism or second reconnect loop. Manual download and launch always work.

## Profile management

`agent profile edit NAME FIELD=VALUE` changes a logical profile. Existing artifacts keep the exact profile snapshot stamped at build time; build a new artifact to deploy the change. `agent profile delete NAME` removes the logical profile but leaves generated artifacts. Each artifact has a unique ID, profile ID, Undertow build version, platform, architecture, creation time, and hash. Connected agents include profile and artifact IDs in inventory.

Fields accepted by create and edit include `server`, `transport`, `domain`, `fingerprint`, `auth`, `payload-profile`, `websocket-path`, `tls-server-name`, `tls-insecure-skip-verify`, `deny`, and comma-separated `routes`. Use `token-file=PATH` or `password-file=PATH` for a custom enrollment secret. Those paths are read by the local console, so the secret does not need to be typed into its history. `agent profile show`, `agent artifacts`, and status omit credentials. Anyone who can read a generated binary may extract its embedded enrollment secret; handle it as sensitive material and revoke or rotate enrollment if it is exposed.

## Format and compatibility

The stamped executable ends with a bounded JSON snapshot, SHA-256 of that snapshot, a four-byte length, and a 16-byte magic/version footer. The reader rejects missing, oversized, malformed, altered, or unsupported profile formats before connecting. The artifact hash covers the whole executable. The profile format is explicitly versioned independently of the Undertow build version. Build artifacts from templates released with the matching operator framework; old artifacts are immutable and do not inherit later profile edits.

The full `undertow agent --transport ... --server ... --fingerprint ...` command remains available for manual deployment, development, troubleshooting, and probes. Both normal CLI agents and configured thin agents call the same internal agent runtime for transport dialing, authentication, identity validation, inventory, mux, capabilities, agent services, and reconnect behavior.
