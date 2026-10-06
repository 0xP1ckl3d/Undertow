# .NET Framework assemblies

Undertow supports a fourth module type: a managed Windows .NET Framework 4.x assembly. It complements portable WASM, Undertow native `.module` DLLs, and BOF `.o` objects. The first assembly runtime targets **Windows amd64 agents** and accepts pure-IL AnyCPU or x64 `.exe` and `.dll` PE assemblies. x86-only, mixed-mode, .NET 6+, and single-file .NET applications are outside this subset.

The agent needs the .NET Framework 4.x CLR. Undertow embeds a dedicated x64 managed worker in the agent binary. For each run it writes that fixed worker executable to a unique temporary directory, starts it directly, sends the assembly bytes and UTF-8 arguments over stdin, and removes the worker directory when it exits. The operator's assembly is loaded with `Assembly.Load(byte[])` and is never written to the agent filesystem. PowerShell is not used for assembly execution. The worker uses the agent's Windows identity and permissions. Dependencies must already be resolvable by the installed CLR; Undertow does not package companion DLLs.

## Entry point and arguments

An `.exe` uses its managed entry point. A `.dll` must have exactly one static method named `Main` across its types. Supported signatures are `Main()` and `Main(string[] args)`, returning `void` or `int`; a `Task` or `Task<int>` result is also awaited. Missing or ambiguous DLL entry points and unsupported signatures produce errors. Arguments are passed as Unicode `string[]` in order. Standard `Console.Out` and `Console.Error` appear in Undertow's output streams. A nonzero integer result becomes the run's exit status.

The worker consumes stdin for assembly transfer, so interactive `Console.ReadLine` input is unavailable. Foreground output streams to the console; background output uses normal Jobs retention and `job output`. `job stop` or a lost session terminates the worker. Assemblies share the agent's `native` capability and two-run native concurrency limit.

## Run or load

```text
undertow> use 1
undertow[AGENT]> run-assembly ./tool.exe first "two words"
undertow[AGENT]> run-assembly --background ./tool.dll audit
undertow[AGENT]> job output 1

undertow> load assembly ./tool.exe tool
undertow> help tool
undertow> use 1
undertow[AGENT]> tool first "two words"
undertow[AGENT]> tool --background audit
```

At the main menu, use `run-assembly AGENT_ID FILE [ARGS]`. `load assembly FILE [NAME]` registers bytes locally for the current console session and transfers them only when invoked. `unload assembly NAME` removes the registration. The GUI Modules page can import an `.exe` or `.dll` as a `.NET Framework` module and run it foreground or background.

Put packaged assemblies below `modules/assembly/` on the **console host** and restart or reattach the console to preload them. `modules/assembly/audit.exe` becomes `assembly-audit`. The repository includes untested managed reference examples from [GhostPack/Certify](https://github.com/GhostPack/Certify), [GhostPack/Rubeus](https://github.com/GhostPack/Rubeus), and [GhostPack/Seatbelt](https://github.com/GhostPack/Seatbelt); see `modules/assembly/README.md` and each sidecar for attribution and license notes. `PowerChell.exe` is intentionally not packaged here because it is an unmanaged C++ host without a CLR header. An optional `audit.exe.json` or `audit.json` sidecar can set `name`, `description`, `usage`, `help`, and `source`. Aliases use lowercase letters, digits, and hyphens and cannot shadow built-in commands. See [Local module bank](module-bank.md).

## Build example

On Windows with the .NET Framework compiler:

```powershell
& "$env:SystemRoot\Microsoft.NET\Framework64\v4.0.30319\csc.exe" /nologo /target:exe /out:hello.exe hello.cs
```

```csharp
using System;
class Hello {
    static int Main(string[] args) {
        Console.WriteLine("Hello " + String.Join(" ", args));
        return 0;
    }
}
```

Copy `hello.exe` to `modules/assembly/hello.exe`, or run it directly. The loader checks PE and CLR headers before transfer; the worker reports CLR load and invocation failures through stderr. A missing or incompatible CLR produces a worker startup or loader error. If the agent cannot create or execute files in its temporary directory, the worker startup error identifies that failure.

## Rebuilding the worker

The fixed worker source is `internal/pivot/assemblyworker/worker.cs`. On a Windows development host, run `internal/pivot/assemblyworker/build.ps1` before building the Go agent if you change that source. The build uses the x64 .NET Framework compiler and produces the worker executable embedded by Go. The worker accepts one bounded binary request (`UTA1`, little-endian argument count and UTF-8 strings, followed by the assembly length and bytes); its protocol is internal to Undertow and does not change the operator commands.
