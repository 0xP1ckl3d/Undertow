$ErrorActionPreference = 'Stop'
if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) { throw 'Run in an x64 MSVC Developer PowerShell.' }
Push-Location $PSScriptRoot
try {
    foreach ($name in @('hello','arguments','imports','loop','loaderimports')) {
        & cl.exe /nologo /c /GS- /Zl /O1 /W4 "/Fo$name.o" "$name.c"
        if ($LASTEXITCODE -ne 0) { throw "BOF build failed: $name" }
    }
    & cl.exe /nologo /c /GS- /Zl /O1 /W4 "/Fo../../internal/bof/testdata/unsupported_imports.o" "../../internal/bof/testdata/unsupported_imports.c"
    if ($LASTEXITCODE -ne 0) { throw 'BOF build failed: unsupported_imports' }
} finally { Pop-Location }
