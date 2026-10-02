# Sift Native for Undertow

This Windows AMD64 `undertow_native_v1` module scans files for sensitive data from an existing Undertow agent. It embeds the [Stratus Sift](https://github.com/Stratus-Security/Sift) default rule catalogue pinned to commit `cf6a3ac42e98e78e54e9cdfeea56a9dfb0c21635`: 112 effective rules, 313 patterns, and 101 ignore entries. The JSON rules live in `upstream-rules/`; `generate_rules.py` converts them to `generated_rules.h` at build time. PCRE2 is linked into the module so target machines need no regex runtime.

## Usage

```text
use 1
run-native examples/native/sift/sift.module --help
run-native examples/native/sift/sift.module local C:\Shares --json
run-native examples/native/sift/sift.module local C:\Shares\config.env
run-native examples/native/sift/sift.module network --device FILE01 --share Finance
run-native --background examples/native/sift/sift.module domain --max-hosts 100 --json
```

Content findings include the **actual matched value**, byte offset, rule, and severity. Generic assignment findings extend the evidence through the value because the upstream pattern itself captures only a short prefix. JSON Lines output adds `validator` and `validated`; `validated: false` means a named upstream post-match validator has not yet been ported, so the regex match is reported without that additional check. The module applies native validators for Luhn, IBAN, Australian TFN, Australian Medicare, GitHub PAT structure, and Slack token structure. Treat output and saved Undertow jobs as sensitive.

`local` accepts a file or directory. `network` enumerates disk shares on one device, or scans an explicit share. `domain` discovers hosts in the joined Windows domain through NetAPI. Network and domain scans use the agent's current Windows identity. `--enum-only` suppresses file content reads. `--max-files`, `--max-findings`, `--max-read-mib`, `--max-depth`, and `--max-hosts` are optional scan limits; none is set by default. The final summary reports each limit reached, cancellation, errors, and the output cap. File content is read from the first and last 256 KiB, with UTF-16LE ASCII projection.

Directory traversal processes one file at a time and keeps one enumeration frame per active directory level. It does not collect the file tree or write scan logs on the agent. Each content read uses at most 512 KiB plus the UTF-16 projection buffer. Undertow currently imposes a two-minute native runtime limit, a 4 MiB native output limit, and retains only the last 256 KiB of background job output. This module stops at approximately 3 MiB of emitted findings so the final summary can report `output_limit_hit` before Undertow's 4 MiB limit. A completed scan with `output_limit_hit` or another limit flag is incomplete; background output retention can also discard earlier findings even if the scan completed. For complete results with the current transport, use foreground streaming and split large roots into smaller scans, or add controller-side output persistence and a longer native runtime before running multi-hour background scans.

## Build and test

From an x64 MSVC Developer PowerShell with Go and Python on `PATH`:

```powershell
.\examples\native\sift\build.ps1
.\examples\native\sift\test.ps1
```

`build.ps1` generates the C rule table, compiles the native DLL with static PCRE2, packages `sift.module`, and removes build intermediates. `test.ps1` runs a local ABI smoke runner against synthetic tokens and checksum fixtures, then removes its temporary files.

## Scope and parity

The bundled patterns, severities, metadata targets, content extension scopes, keyword prefilters, entropy thresholds, path exclusions, and ignore entries come from the pinned upstream catalogue. Three leading variable-length lookbehinds are translated to PCRE2 `\K` at generation time so their reported match remains the secret value. Regex engine semantics can still differ from .NET for some edge cases. This module does not yet implement every upstream validator or output aggregation rule. It scans text and UTF-16LE ASCII projections; upstream's document/archive extraction and cloud connectors are outside this native module. Domain discovery uses NetAPI instead of Sift's LDAP crawler, and network mode currently supports one device rather than a subnet.

The copied Sift rule catalogue and profile are subject to the upstream [AGPL-3.0-only license](UPSTREAM-LICENSE.txt). The bundled PCRE2 source is under its [BSD-3-Clause with PCRE2 exception license](pcre2/LICENCE.md). See those files when distributing the module or its source.
