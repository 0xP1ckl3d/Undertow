$ErrorActionPreference = "Stop"
$workspace = (Get-Location).Path
$root = Join-Path $workspace (".local/agent-e2e-" + [guid]::NewGuid().ToString("N").Substring(0,8))
New-Item -ItemType Directory -Force -Path $root | Out-Null
$env:APPDATA = Join-Path $root "appdata"
New-Item -ItemType Directory -Force -Path $env:APPDATA | Out-Null
$operator = Join-Path $workspace "bin/undertow.exe"
$templates = Join-Path $workspace "bin"
$thinTemplate = Join-Path $templates "undertow-agent-windows-amd64.exe"
if ((Get-Item $thinTemplate).Length -ge (Get-Item $operator).Length) { throw "Thin agent template is not smaller than the operator framework" }
$pe = [System.IO.File]::ReadAllBytes($thinTemplate)
$peOffset = [BitConverter]::ToInt32($pe, 0x3c)
$subsystem = [BitConverter]::ToUInt16($pe, $peOffset + 24 + 68)
if ($subsystem -ne 2) { throw "Windows thin agent is not a non-console GUI subsystem executable" }
$identity = Join-Path $root "server.key"
$enrollmentFile = Join-Path $root "enrollment.key"
$tokenFile = Join-Path $root "control.key"
$store = Join-Path $root "store"
$init = (& $operator init --identity $identity --token-file $enrollmentFile 2>$null | Out-String)
$fingerprint = [regex]::Match($init, '[0-9a-f]{64}').Value
if (-not $fingerprint) { throw "Cannot read server fingerprint: $init" }
function Free-Port {
  $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
  $listener.Start()
  $port = $listener.LocalEndpoint.Port
  $listener.Stop()
  return $port
}
function Free-UDPPort {
  $socket = [Net.Sockets.UdpClient]::new([Net.IPEndPoint]::new([Net.IPAddress]::Loopback, 0))
  $port = $socket.Client.LocalEndPoint.Port
  $socket.Dispose()
  return $port
}
$wsPort = Free-Port
$quicPort = Free-UDPPort
$dnsPort = Free-UDPPort
$controlPort = Free-Port
$server = $null
$agent = $null
function Endpoint-Snapshot([string]$path) {
  return @(
    Get-ChildItem -LiteralPath $path -Force -Recurse |
      ForEach-Object { $_.FullName.Substring($path.Length) } |
      Sort-Object
  )
}
function Prepare-Endpoint([string]$name, [string]$source) {
  $endpoint = Join-Path $root "endpoint-$name"
  New-Item -ItemType Directory -Force -Path $endpoint | Out-Null
  foreach ($folder in @('appdata', 'localappdata', 'temp', 'home')) {
    New-Item -ItemType Directory -Force -Path (Join-Path $endpoint $folder) | Out-Null
  }
  $binary = Join-Path $endpoint ([IO.Path]::GetFileName($source))
  Copy-Item -LiteralPath $source -Destination $binary
  $env:APPDATA = Join-Path $endpoint 'appdata'
  $env:LOCALAPPDATA = Join-Path $endpoint 'localappdata'
  $env:USERPROFILE = Join-Path $endpoint 'home'
  $env:TEMP = Join-Path $endpoint 'temp'
  $env:TMP = $env:TEMP
  return @{ Path = $endpoint; Binary = $binary; Snapshot = @(Endpoint-Snapshot $endpoint) }
}
function Assert-EndpointUnchanged($endpoint) {
  $after = @(Endpoint-Snapshot $endpoint.Path)
  $difference = Compare-Object -ReferenceObject $endpoint.Snapshot -DifferenceObject $after
  if ($difference) { throw "Packaged agent changed endpoint filesystem: $($difference | Out-String)" }
}
try {
  $unconfigured = Start-Process -FilePath (Join-Path $templates "undertow-agent-windows-amd64.exe") -PassThru -Wait -WindowStyle Hidden -RedirectStandardError (Join-Path $root "unconfigured.err")
  if ($unconfigured.ExitCode -eq 0 -or (Get-Item (Join-Path $root "unconfigured.err")).Length -ne 0) { throw "Unconfigured thin agent did not exit quietly" }
  $server = Start-Process -FilePath $operator -ArgumentList @("server", "--foreground", "--transport", "websocket,quic,dns", "--websocket-listen", "127.0.0.1:$wsPort", "--quic-listen", "127.0.0.1:$quicPort", "--dns-listen", "127.0.0.1:$dnsPort", "--control-listen", "127.0.0.1:$controlPort", "--auth", "token", "--token-file", $enrollmentFile, "--tls-self-signed", "--identity", $identity, "--agent-store", $store, "--agent-templates", $templates, "--payload-retrieval-path", "/dl/", "--control-token-file", $tokenFile) -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $root "server.out") -RedirectStandardError (Join-Path $root "server.err")
  $base = "http://127.0.0.1:$controlPort"
  $ready = $false
  for ($i=0; $i -lt 60; $i++) {
    if (Test-Path $tokenFile) {
      $headers = @{ Authorization = "Bearer $((Get-Content $tokenFile -Raw).Trim())" }
      try { $null = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers; $ready = $true; break } catch { }
    }
    Start-Sleep -Milliseconds 200
  }
  if (-not $ready) { throw "Server did not start: $(Get-Content (Join-Path $root 'server.err') -Raw)" }
  $consoleLines = @("payload retrieval-path", "payload profile create console-check server=127.0.0.1:$quicPort transport=quic", "payload profiles", "payload profile show console-check", "payload profile edit console-check routes=10.20.0.0/16", "payload profile delete console-check", "quit")
  $consoleOutput = ($consoleLines | & $operator console --control "127.0.0.1:$controlPort" --control-token-file $tokenFile 2>$null | Out-String)
  if ($consoleOutput -notmatch 'Public payload download prefix: /dl/' -or $consoleOutput -notmatch 'Profile "console-check" created' -or $consoleOutput -notmatch 'Existing payloads keep' -or $consoleOutput -notmatch 'Profile "console-check" deleted') { throw "Server console payload commands failed: $consoleOutput" }
  foreach ($target in @(@{ transport = "websocket"; port = $wsPort }, @{ transport = "quic"; port = $quicPort }, @{ transport = "dns"; port = $dnsPort })) {
    $name = "e2e-$($target.transport)"
    $body = @{ name = $name; server = "127.0.0.1:$($target.port)"; transport = $target.transport; denied_capabilities = "upload"; advertised_routes = @("10.20.0.0/16") } | ConvertTo-Json
    $profile = Invoke-RestMethod -Uri "$base/v1/agent-profiles" -Headers $headers -Method Post -ContentType application/json -Body $body
    $body = @{ profile = $name; platform = "windows"; architecture = "amd64" } | ConvertTo-Json
    $artifact = Invoke-RestMethod -Uri "$base/v1/agent-artifacts" -Headers $headers -Method Post -ContentType application/json -Body $body
    if (-not $artifact.agent_id) { throw "Artifact did not record its embedded agent identity" }
    $hosted = Invoke-RestMethod -Uri "$base/v1/agent-artifacts/$($artifact.id)/host" -Headers $headers -Method Post
    if ($artifact.filename -notmatch "^$($artifact.id)(\.exe)?$" -or $hosted.retrieval.Contains($artifact.id) -or $hosted.retrieval.Contains($name) -or $hosted.retrieval -notmatch '/dl/[0-9a-f]{48}$') { throw "Artifact naming or retrieval URL exposed management metadata" }
    try { Invoke-WebRequest -Uri ($hosted.retrieval -replace '/dl/', '/.undertow/artifacts/') -SkipCertificateCheck -OutFile (Join-Path $root 'old-route.bin') | Out-Null; throw 'Product-labelled retrieval route remained active' } catch { if ($_.Exception.Message -eq 'Product-labelled retrieval route remained active') { throw } }
    $download = Join-Path $root $artifact.filename
    Invoke-WebRequest -Uri $hosted.retrieval -SkipCertificateCheck -OutFile $download
    $actual = (Get-FileHash -Algorithm SHA256 -Path $download).Hash.ToLowerInvariant()
    if ($actual -ne $artifact.sha256) { throw "Retrieved hash mismatch" }
    $endpoint = Prepare-Endpoint $name $download
    $agent = Start-Process -FilePath $endpoint.Binary -WorkingDirectory $endpoint.Path -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $root "$name.out") -RedirectStandardError (Join-Path $root "$name.err")
    $connected = $null
    for ($i=0; $i -lt 300; $i++) {
      $status = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers
      $connected = @($status.agents | Where-Object { $_.artifact_id -eq $artifact.id }) | Select-Object -First 1
      if ($connected) { break }
      Start-Sleep -Milliseconds 200
    }
    if (-not $connected) { throw "Agent did not connect: $(Get-Content (Join-Path $root "$name.err") -Raw)" }
    if ($connected.profile -ne $name -or $connected.id -ne $artifact.agent_id) { throw "Inventory metadata or embedded identity missing" }
    Assert-EndpointUnchanged $endpoint
    if ($connected.reconnect_policy -ne "progressive") { throw "Configured agent does not report progressive reconnect" }
    if (-not $connected.undertow_version -or -not $connected.profile_id) { throw "Artifact build metadata missing" }
    if ($connected.advertised_routes -notcontains "10.20.0.0/16") { throw "Advertised route missing" }
    if ($connected.capabilities.allowed -contains "upload") { throw "Denied capability was allowed" }
    $execBody = @{ argv = @("cmd", "/c", "echo", "undertow-e2e") } | ConvertTo-Json
    $result = Invoke-RestMethod -Uri "$base/v1/agents/$($connected.id)/exec" -Headers $headers -Method Post -ContentType application/json -Body $execBody
    if ($target.transport -eq "websocket") {
      $oldSession = $connected.session_id
      Invoke-RestMethod -Uri "$base/v1/transports/websocket?force=true" -Headers $headers -Method Delete | Out-Null
      $restart = @{ listen = "127.0.0.1:$wsPort"; tls_mode = "self-signed" } | ConvertTo-Json
      Invoke-RestMethod -Uri "$base/v1/transports/websocket" -Headers $headers -Method Post -ContentType application/json -Body $restart | Out-Null
      $reconnected = $null
      for ($i=0; $i -lt 150; $i++) {
        $status = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers
        $reconnected = @($status.agents | Where-Object { $_.artifact_id -eq $artifact.id -and $_.session_id -ne $oldSession }) | Select-Object -First 1
        if ($reconnected) { break }
        Start-Sleep -Milliseconds 200
      }
      if (-not $reconnected) { throw "WebSocket agent did not reconnect" }
      Assert-EndpointUnchanged $endpoint
      Invoke-RestMethod -Uri "$base/v1/sessions/$($reconnected.id)/kill" -Headers $headers -Method Post | Out-Null
      $afterKill = $null
      for ($i=0; $i -lt 150; $i++) {
        $status = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers
        $afterKill = @($status.agents | Where-Object { $_.artifact_id -eq $artifact.id -and $_.session_id -ne $reconnected.session_id }) | Select-Object -First 1
        if ($afterKill) { break }
        Start-Sleep -Milliseconds 200
      }
      if (-not $afterKill) { throw "Session kill did not permit agent reconnect" }
      Assert-EndpointUnchanged $endpoint
    }
    Write-Output "PASS $($target.transport) artifact=$($artifact.id) sha256=$actual agent=$($connected.id) exec=$($result.stdout.Trim())"
    $current = @( (Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers).agents | Where-Object { $_.artifact_id -eq $artifact.id } ) | Select-Object -First 1
    Invoke-RestMethod -Uri "$base/v1/agents/$($current.id)/shutdown" -Headers $headers -Method Post | Out-Null
    if (-not $agent.WaitForExit(10000)) { throw "Configured agent did not exit after acknowledged shutdown" }
    Assert-EndpointUnchanged $endpoint
    if ((Get-Item (Join-Path $root "$name.out")).Length -ne 0 -or (Get-Item (Join-Path $root "$name.err")).Length -ne 0) { throw "Packaged agent emitted local diagnostics" }
    $events = Invoke-RestMethod -Uri "$base/v1/agents/$($current.id)/events" -Headers $headers
    if (@($events | Where-Object { $_.kind -eq 'shutdown_acknowledged' }).Count -eq 0) { throw "Server did not record shutdown acknowledgement" }
    Invoke-RestMethod -Uri "$base/v1/agent-artifacts/$($artifact.id)/host" -Headers $headers -Method Delete | Out-Null
    try { Invoke-WebRequest -Uri $hosted.retrieval -SkipCertificateCheck -OutFile (Join-Path $root 'unhosted.bin') | Out-Null; throw "Unhosted artifact was retrievable" } catch { if ($_.Exception.Message -eq 'Unhosted artifact was retrievable') { throw } }
    Invoke-RestMethod -Uri "$base/v1/agent-artifacts/$($artifact.id)/revoke" -Headers $headers -Method Post | Out-Null
    $agent = $null
  }
  $agent = Start-Process -FilePath $operator -ArgumentList @("agent", "--foreground", "--transport", "websocket", "--server", "127.0.0.1:$wsPort", "--fingerprint", $fingerprint, "--auth", "token", "--token-file", $enrollmentFile, "--tls-insecure-skip-verify", "--agent-key", (Join-Path $root "cli-agent.key"), "--deny", "upload", "--advertise-route", "10.20.0.0/16") -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $root "cli-agent.out") -RedirectStandardError (Join-Path $root "cli-agent.err")
  $cliAgent = $null
  for ($i=0; $i -lt 150; $i++) {
    $status = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers
    $cliAgent = @($status.agents | Where-Object { -not $_.artifact_id }) | Select-Object -First 1
    if ($cliAgent) { break }
    Start-Sleep -Milliseconds 200
  }
  if (-not $cliAgent) { throw "Full CLI agent did not connect" }
  if ($cliAgent.advertised_routes -notcontains "10.20.0.0/16" -or $cliAgent.capabilities.allowed -contains "upload") { throw "Full CLI agent configuration differs" }
  Write-Output "PASS full CLI agent=$($cliAgent.id)"
  Stop-Process -Id $agent.Id -Force
  $agent = $null
  $badBody = @{ name = "bad-pin"; server = "127.0.0.1:$wsPort"; transport = "websocket"; fingerprint = ("0" * 64) } | ConvertTo-Json
  Invoke-RestMethod -Uri "$base/v1/agent-profiles" -Headers $headers -Method Post -ContentType application/json -Body $badBody | Out-Null
  $badBuild = @{ profile = "bad-pin"; platform = "windows"; architecture = "amd64" } | ConvertTo-Json
  $badArtifact = Invoke-RestMethod -Uri "$base/v1/agent-artifacts" -Headers $headers -Method Post -ContentType application/json -Body $badBuild
  $badEndpoint = Prepare-Endpoint 'bad-pin' (Join-Path $store "artifacts/$($badArtifact.filename)")
  $agent = Start-Process -FilePath $badEndpoint.Binary -WorkingDirectory $badEndpoint.Path -PassThru -WindowStyle Hidden -RedirectStandardError (Join-Path $root "bad-pin.err")
  Start-Sleep -Seconds 3
  $status = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers
  if (@($status.agents | Where-Object { $_.artifact_id -eq $badArtifact.id }).Count -ne 0) { throw "Wrong fingerprint connected" }
  if ((Get-Item (Join-Path $root "bad-pin.err")).Length -ne 0) { throw "Wrong fingerprint leaked a local diagnostic" }
  Assert-EndpointUnchanged $badEndpoint
  Write-Output "PASS configured artifact rejects wrong fingerprint quietly"
} finally {
  if ($agent -and -not $agent.HasExited) { Stop-Process -Id $agent.Id -Force }
  if ($server -and -not $server.HasExited) { Stop-Process -Id $server.Id -Force }
}
