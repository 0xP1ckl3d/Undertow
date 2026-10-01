# Process and network inventory

Shows process, service, connection/listener, and neighbour snapshots. Optional `FILTER` matches output lines case-insensitively. Each section shows at most 30 matching lines.

Build from the repository root with `./examples/wasm/build.ps1 inventory` or `sh examples/wasm/build.sh inventory`.

```text
use 1
run-wasm examples/wasm/inventory/inventory.wasm tcp
```

Representative output:

```text
Process, service and network inventory
[connections]
TCP 127.0.0.1:445 0.0.0.0:0 LISTENING 1234
```

See the [host API guide](../../../docs/wasm-development.md) for inventory formats and platform behavior.
