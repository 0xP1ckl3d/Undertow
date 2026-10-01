# Host triage

Reports the agent host's OS, architecture, hostname, user IDs, and privilege context. The module is read-only and works on Windows and Linux.

From the repository root, build with `./examples/wasm/build.ps1 triage` (PowerShell) or `sh examples/wasm/build.sh triage` (POSIX shell). See the [shared build and API guide](../../../docs/wasm-development.md).

```text
use 1
run-wasm examples/wasm/triage/triage.wasm
```

Representative output:

```text
Host triage: workstation (windows/amd64)
User: EXAMPLE\analyst uid=... gid=...
Privilege context:
USER INFORMATION
```
