# ShellPower native module

This module adapts the separate ShellPower project to Undertow's native module and native live-shell ABIs. It loads the .NET CLR and Windows PowerShell 5.1 into the remote Undertow agent process and routes PowerShell streams through Undertow. It does not launch `powershell.exe` or `pwsh.exe`. The original CLR host, patching behavior, string obfuscation, and PowerShell stream formatting are preserved.

## What runs where

Undertow transfers the packaged module to the agent, writes its DLL to the agent's temporary directory, and loads it into the agent process. A one-shot module run unloads and deletes that temporary DLL after execution. A live-shell run keeps it loaded for the session and unloads and deletes it when the session closes. Deletion is attempted during normal cleanup; a crash or forced termination can leave the temporary file behind.

Inline source and uploaded `.ps1` contents are transferred as bytes. ShellPower does not create a `.ps1` file on the agent.

## Patching behavior

Before invoking PowerShell, ShellPower attempts the patch set inherited from the original project:

- two native AMSI changes targeting `AmsiOpenSession` and `AmsiScanBuffer` in the agent process;
- disabling the PowerShell ETW provider for the ShellPower AppDomain;
- managed changes for transcription flushing, execution-policy enforcement, and constrained-language policy.

The native AMSI changes modify the loaded `amsi.dll` code in the Undertow agent process. ShellPower does not restore those bytes, so a successful change remains until the agent process exits or another component restores the code. Repeated ShellPower runs recognize an existing ShellPower AMSI patch as success.

The PowerShell ETW state is changed through objects in the created AppDomain. The other managed patches modify JIT-compiled methods resolved through that AppDomain and are attempted again for each new run. A one-shot run unloads its AppDomain during cleanup, while the live shell keeps its AppDomain and runspace for the entire session. CLR code can be shared inside a process, so unloading an AppDomain should not be treated as proof that every managed code byte was restored.

Each technique reports its own failure and execution continues. A warning identifies the technique that could not be applied or recognized; it is not a definitive statement about the effective AMSI state. ShellPower does not claim that a patch succeeded unless its expected bytes or managed change were applied or already present.

## Module run

Build from an x64 MSVC Developer PowerShell:

```powershell
./modules/native/shellpower/build.ps1
```

Run an inline command:

```text
shellpower "$PSVersionTable"
shellpower 'Get-Process | Select-Object -First 5'
```

In the Modules tab, select `module-shellpower`. Choose **Inline PowerShell** to enter a command or complete script source, or choose **PowerShell script (.ps1)** to select a local script. Then choose **Stream foreground** or **Run background**.

The one-shot module accepts UTF-8 and BOM-marked UTF-16LE scripts up to Undertow's 64 KiB native data limit. It returns status 1 when PowerShell reports an error.

## Live shell

In **Agents → Live shell**, choose **ShellPower · in process** to open the same tool as a persistent session. Undertow calls its `undertow_shell_main` entry point and keeps one Windows PowerShell 5.1 runspace alive, so variables, functions, location changes, and imported modules carry across commands. Type `exit` or close the Live shell panel to end it.

The live entry point uses PowerShell's asynchronous invocation API and calls `Stop()` when the terminal disconnects, while keeping every CLR operation on the runspace's owning thread.

The CLR/PowerShell reflection and patch layers are copied from the ShellPower/PowerChell project credited in the source project's README. Undertow-specific entry, argument decoding, output routing, cleanup, and build files live in this directory.
