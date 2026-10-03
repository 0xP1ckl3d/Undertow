# Enterprise host posture

Reports domain-related environment, DNS configuration, and policy locations. It reads selected Windows policy Registry keys or reports Linux Kerberos, SSSD, and Samba configuration file metadata.

Build from the repository root with `./modules/wasm/build.ps1 enterprise-posture` or `sh modules/wasm/build.sh enterprise-posture`.

```text
use 1
run-wasm modules/wasm/enterprise-posture/enterprise-posture.wasm
```

Representative output:

```text
Enterprise posture: workstation (windows)
USERDOMAIN=EXAMPLE
DNS configuration:
Windows IP Configuration
```

See the [host API guide](../../../docs/wasm-development.md) for extending the assessment.

Back to [documentation home](../../../docs/README.md).
