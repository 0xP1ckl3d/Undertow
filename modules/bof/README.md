# Packaged BOFs

This directory holds Windows AMD64 BOFs that the server or VPN client console loads at startup. Run `modules`, `help bof-NAME`, and `use NUMBER` in the console, then invoke a BOF against the selected agent. The [module bank](../../docs/module-bank.md#bofs-windows-amd64-agents) lists **every packaged BOF**, its purpose, original project, and a basic console command.

Each `.x64.o.json` file supplies console help and argument types for its matching object. The `.o` objects are third-party compiled artifacts; use the source repositories linked from the module bank for their source, credits, and build instructions. Undertow's own BOF loader fixtures have moved to `internal/bof/testdata/examples/` and are not preloaded as operator commands.

For import compatibility, one-off loading, typed arguments, and development, see [BOF compatibility](../../docs/bof-compatibility.md).

Back to [documentation home](../../docs/README.md).
