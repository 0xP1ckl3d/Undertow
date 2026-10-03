# Startup and autorun audit

See the [module bank](../../../docs/module-bank.md) for every shipped module and its console command.

Lists service startup state and user autoruns. On Windows it also reads common Run and RunOnce Registry keys. On Linux it inspects systemd unit files and the user's desktop autostart directory.

Build from the repository root with `./modules/wasm/build.ps1 persistence-audit` or `sh modules/wasm/build.sh persistence-audit`.

```text
use 1
run-wasm modules/wasm/persistence-audit/persistence-audit.wasm
```

Representative output:

```text
Startup and autorun audit: workstation
SERVICE_NAME: ExampleService
STATE: 4 RUNNING
```

See the [host API guide](../../../docs/wasm-development.md) for Registry access and platform behavior.

Back to [documentation home](../../../docs/README.md).
