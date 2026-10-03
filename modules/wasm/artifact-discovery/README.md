# Configuration and credential artifact discovery

See the [module bank](../../../docs/module-bank.md) for every shipped module and its console command.

Reports names, sizes, and modes of potentially interesting files under an agent-side root. It does not print file contents. The default root is the agent user's home directory; an argument overrides it. A local stdin file can add one case-insensitive filename pattern per line.

Build from the repository root with `./modules/wasm/build.ps1 artifact-discovery` or `sh modules/wasm/build.sh artifact-discovery`.

```text
use 1
run-wasm --stdin ./patterns.txt modules/wasm/artifact-discovery/artifact-discovery.wasm /home/analyst
```

Representative output:

```text
Interesting file names under /home/analyst (metadata only):
/home/analyst/.ssh mode=drwx------ size=4096
```

See the [host API guide](../../../docs/wasm-development.md) for traversal bounds.

Back to [documentation home](../../../docs/README.md).
