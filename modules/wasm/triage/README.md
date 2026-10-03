# Host triage

Reports the agent host's OS, architecture, hostname, user IDs, and privilege context. The module is read-only and works on Windows and Linux.

From the repository root, build with `./modules/wasm/build.ps1 triage` (PowerShell) or `sh modules/wasm/build.sh triage` (POSIX shell). See the [shared build and API guide](../../../docs/wasm-development.md).

```text
use 1
run-wasm modules/wasm/triage/triage.wasm
```

Representative output:

```text
Host triage: workstation (windows/amd64)
User: EXAMPLE\analyst uid=... gid=...
Privilege context:
USER INFORMATION
```

Back to [documentation home](../../../docs/README.md).
