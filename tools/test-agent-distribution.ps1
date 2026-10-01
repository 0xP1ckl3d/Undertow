$ErrorActionPreference = "Stop"
$workspace = (Get-Location).Path
$root = Join-Path $workspace (".local/agent-e2e-" + [guid]::NewGuid().ToString("N").Substring(0,8))
New-Item -ItemType Directory -Force -Path $root | Out-Null
$env:APPDATA = Join-Path $root "appdata"
New-Item -ItemType Directory -Force -Path $env:APPDATA | Out-Null
$operator = Join-Path $workspace "bin/undertow.exe"
$templates = Join-Path $workspace "bin"
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
try {
  $unconfigured = Start-Process -FilePath (Join-Path $templates "undertow-agent-windows-amd64.exe") -PassThru -Wait -WindowStyle Hidden -RedirectStandardError (Join-Path $root "unconfigured.err")
  if ($unconfigured.ExitCode -eq 0 -or (Get-Content (Join-Path $root "unconfigured.err") -Raw) -notmatch "missing embedded agent profile") { throw "Unconfigured thin agent did not fail cleanly" }
  $server = Start-Process -FilePath $operator -ArgumentList @("server", "--foreground", "--transport", "websocket,quic,dns", "--websocket-listen", "127.0.0.1:$wsPort", "--quic-listen", "127.0.0.1:$quicPort", "--dns-listen", "127.0.0.1:$dnsPort", "--control-listen", "127.0.0.1:$controlPort", "--auth", "token", "--token-file", $enrollmentFile, "--tls-self-signed", "--identity", $identity, "--agent-store", $store, "--agent-templates", $templates, "--control-token-file", $tokenFile) -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $root "server.out") -RedirectStandardError (Join-Path $root "server.err")
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
  $consoleLines = @("agent profile create console-check server=127.0.0.1:$quicPort transport=quic", "agent profile list", "agent profile show console-check", "agent profile edit console-check routes=10.20.0.0/16", "agent profile delete console-check", "quit")
  $consoleOutput = ($consoleLines | & $operator console --control "127.0.0.1:$controlPort" --control-token-file $tokenFile 2>$null | Out-String)
  if ($consoleOutput -notmatch "Created profile console-check" -or $consoleOutput -notmatch "Existing artifacts retain" -or $consoleOutput -notmatch "Deleted profile console-check") { throw "Server console profile commands failed: $consoleOutput" }
  foreach ($target in @(@{ transport = "websocket"; port = $wsPort }, @{ transport = "quic"; port = $quicPort }, @{ transport = "dns"; port = $dnsPort })) {
    $name = "e2e-$($target.transport)"
    $body = @{ name = $name; server = "127.0.0.1:$($target.port)"; transport = $target.transport; denied_capabilities = "upload"; advertised_routes = @("10.20.0.0/16") } | ConvertTo-Json
    $profile = Invoke-RestMethod -Uri "$base/v1/agent-profiles" -Headers $headers -Method Post -ContentType application/json -Body $body
    $body = @{ profile = $name; platform = "windows"; architecture = "amd64" } | ConvertTo-Json
    $artifact = Invoke-RestMethod -Uri "$base/v1/agent-artifacts" -Headers $headers -Method Post -ContentType application/json -Body $body
    $hosted = Invoke-RestMethod -Uri "$base/v1/agent-artifacts/$($artifact.id)/host" -Headers $headers -Method Post
    $download = Join-Path $root $artifact.filename
    Invoke-WebRequest -Uri $hosted.retrieval -SkipCertificateCheck -OutFile $download
    $actual = (Get-FileHash -Algorithm SHA256 -Path $download).Hash.ToLowerInvariant()
    if ($actual -ne $artifact.sha256) { throw "Retrieved hash mismatch" }
    $agent = Start-Process -FilePath $download -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $root "$name.out") -RedirectStandardError (Join-Path $root "$name.err")
    $connected = $null
    for ($i=0; $i -lt 300; $i++) {
      $status = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers
      $connected = @($status.agents | Where-Object { $_.artifact_id -eq $artifact.id }) | Select-Object -First 1
      if ($connected) { break }
      Start-Sleep -Milliseconds 200
    }
    if (-not $connected) { throw "Agent did not connect: $(Get-Content (Join-Path $root "$name.err") -Raw)" }
    if ($connected.profile -ne $name) { throw "Inventory metadata missing" }
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
    }
    Write-Output "PASS $($target.transport) artifact=$($artifact.id) sha256=$actual agent=$($connected.id) exec=$($result.stdout.Trim())"
    Stop-Process -Id $agent.Id -Force
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
  $agent = Start-Process -FilePath (Join-Path $store "artifacts/$($badArtifact.filename)") -PassThru -WindowStyle Hidden -RedirectStandardError (Join-Path $root "bad-pin.err")
  Start-Sleep -Seconds 3
  $status = Invoke-RestMethod -Uri "$base/v1/status" -Headers $headers
  if (@($status.agents | Where-Object { $_.artifact_id -eq $badArtifact.id }).Count -ne 0) { throw "Wrong fingerprint connected" }
  if ((Get-Content (Join-Path $root "bad-pin.err") -Raw) -notmatch "fingerprint mismatch") { throw "Wrong fingerprint did not report mismatch" }
  Write-Output "PASS configured artifact rejects wrong fingerprint"
} finally {
  if ($agent -and -not $agent.HasExited) { Stop-Process -Id $agent.Id -Force }
  if ($server -and -not $server.HasExited) { Stop-Process -Id $server.Id -Force }
}
