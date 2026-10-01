# Privilege and configuration audit

Shows the current privilege context and selected system path metadata. On Linux it flags world-writable systemd unit files. On Windows it lists selected configuration directory metadata; Windows ACLs require separate evaluation.

Build from the repository root with `./examples/wasm/build.ps1 privilege-audit` or `sh examples/wasm/build.sh privilege-audit`.

```text
use 1
run-wasm examples/wasm/privilege-audit/privilege-audit.wasm
```

Representative output:

```text
Privilege and configuration audit: workstation
uid=1000 euid=1000 gid=1000 egid=1000 groups=[1000]
/etc/passwd mode=-rw-r--r-- size=2412
```

See the [host API guide](../../../docs/wasm-development.md) for extending the checks.
