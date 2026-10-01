$ErrorActionPreference = 'Stop'
if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) { throw 'Run in an x64 MSVC Developer PowerShell.' }
Push-Location $PSScriptRoot
try {
    foreach ($name in @('hello','arguments','imports','loop')) {
        & cl.exe /nologo /c /GS- /Zl /O1 /W4 "/Fo$name.o" "$name.c"
        if ($LASTEXITCODE -ne 0) { throw "BOF build failed: $name" }
    }
} finally { Pop-Location }
