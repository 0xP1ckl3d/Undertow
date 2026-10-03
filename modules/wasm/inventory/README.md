# Process and network inventory

See the [module bank](../../../docs/module-bank.md) for every shipped module and its console command.

Shows process, service, connection/listener, and neighbour snapshots. Optional `FILTER` matches output lines case-insensitively. Each section shows at most 30 matching lines.

Build from the repository root with `./modules/wasm/build.ps1 inventory` or `sh modules/wasm/build.sh inventory`.

```text
use 1
run-wasm modules/wasm/inventory/inventory.wasm tcp
```

Representative output:

```text
Process, service and network inventory
[connections]
TCP 127.0.0.1:445 0.0.0.0:0 LISTENING 1234
```

See the [host API guide](../../../docs/wasm-development.md) for inventory formats and platform behavior.

Back to [documentation home](../../../docs/README.md).
