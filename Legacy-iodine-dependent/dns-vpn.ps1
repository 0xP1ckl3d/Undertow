#Requires -Version 5.1

<#
dns-vpn.ps1
Windows iodine DNS VPN client

Examples:

  .\dns-vpn.ps1 client -p "Password123-" -s 203.0.113.10
  .\dns-vpn.ps1 client -p "Password123-" -s 203.0.113.10 --background

  .\dns-vpn.ps1 client --status
  .\dns-vpn.ps1 client --stop

  .\dns-vpn.ps1 -h
  .\dns-vpn.ps1 client -h
#>

$ErrorActionPreference = 'Stop'


# ============================================================
# CONFIGURATION
# ============================================================

$Version = '1.7'

$TopDomain = 'tunnel.test'

$TunnelGateway = '10.253.53.1'
$TunnelNetwork = '10.253.53.0/24'
$TunnelPrefix  = '10.253.53.'

#
# Force a conservative downstream DNS fragment size.
#
# This skips iodine's fragment-size autoprobe, which can stall
# on Windows even after authentication and TAP configuration.
#
$FragmentSize = 1130

#
# The current server/client path has already demonstrated that NULL queries
# work. Force that type so Windows does not spend time autodetecting query
# types on every connection.
#
$DnsQueryType = 'NULL'

#
# The standard tap0901/tap0801 adapter is dedicated to dns-vpn. We leave the
# driver installed, but disable the adapter whenever the tunnel is idle.
#
$DisableTapWhenIdle = $true

$StateRoot = Join-Path $env:ProgramData 'dns-vpn'
$StateFile = Join-Path $StateRoot 'client.json'

$StdoutFile = Join-Path $StateRoot 'iodine.stdout.log'
$StderrFile = Join-Path $StateRoot 'iodine.stderr.log'

$TapClassPath = 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4D36E972-E325-11CE-BFC1-08002BE10318}'

$SupportedTapIds = @(
    'tap0901',
    'tap0801'
)

$TapInstallerUrl = 'https://build.openvpn.net/downloads/releases/tap-windows-9.9.2_3.exe'


# ============================================================
# OUTPUT
# ============================================================

function Write-Ok {
    param([string]$Message)

    Write-Host '[+] ' -ForegroundColor Green -NoNewline
    Write-Host $Message
}


function Write-Info {
    param([string]$Message)

    Write-Host '[*] ' -ForegroundColor Cyan -NoNewline
    Write-Host $Message
}


function Write-Warn {
    param([string]$Message)

    Write-Host '[!] ' -ForegroundColor Yellow -NoNewline
    Write-Host $Message
}


function Write-Fail {
    param([string]$Message)

    Write-Host '[-] ' -ForegroundColor Red -NoNewline
    Write-Host $Message
}


function Write-Heading {
    param([string]$Title)

    Write-Host ''
    Write-Host '============================================================' -ForegroundColor Cyan
    Write-Host " $Title" -ForegroundColor Cyan
    Write-Host '============================================================' -ForegroundColor Cyan
    Write-Host ''
}


function Fail {
    param([string]$Message)

    Write-Fail $Message
    exit 1
}


# ============================================================
# HELP
# ============================================================

function Show-TopHelp {

@"
dns-vpn Windows Client $Version

Usage:

  .\dns-vpn.ps1 client [options]

Examples:

  .\dns-vpn.ps1 client -p "Password123-" -s 203.0.113.10

  .\dns-vpn.ps1 client -p "Password123-" -s 203.0.113.10 --background

  .\dns-vpn.ps1 client --status

  .\dns-vpn.ps1 client --stop

Help:

  .\dns-vpn.ps1 -h
  .\dns-vpn.ps1 client -h

"@
}


function Show-ClientHelp {

@"
dns-vpn Windows Client

Usage:

  .\dns-vpn.ps1 client -p PASSWORD -s SERVER_IP [options]

Options:

  -p, --password PASSWORD
      Iodine password.

  -s, --server SERVER_IP
      Public IPv4 address of the dns-vpn server.

  -f, --foreground
      Run attached to the console.
      Ctrl+C disconnects and restores normal routing.
      Default mode.

  -b, --background
      Connect, display the result, then leave the tunnel running.

  --status
      Show current tunnel status.

  --stop
      Disconnect and remove routes installed by dns-vpn.

  -h, --help
      Show this help.


Requirements:

  - Administrator PowerShell.
  - iodine.exe.
  - TAP-Windows adapter with ComponentId tap0901 or tap0801.


Connection behaviour:

  - Server transport is pinned to the original network.
  - Existing physical DNS servers remain reachable.
  - DNS tunnelling is forced using iodine -r.
  - DNS query type is forced to NULL.
  - Fragment autoprobing is skipped.
  - Downstream fragment size: $FragmentSize bytes.
  - Existing Windows default route is never deleted.
  - A preferred temporary default route is added through iodine.
  - Disconnect removes only routes created by dns-vpn.

"@
}


# ============================================================
# ADMIN
# ============================================================

function Test-Administrator {

    $Identity = [Security.Principal.WindowsIdentity]::GetCurrent()

    $Principal = New-Object `
        Security.Principal.WindowsPrincipal($Identity)

    return $Principal.IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator
    )
}


function Require-Administrator {

    if (-not (Test-Administrator)) {

        Write-Fail 'Administrator privileges are required.'
        Write-Host ''
        Write-Host 'Run PowerShell as Administrator.'

        exit 1
    }
}


# ============================================================
# STATE
# ============================================================

function Ensure-StateDirectory {

    if (-not (Test-Path $StateRoot)) {

        New-Item `
            -Path $StateRoot `
            -ItemType Directory `
            -Force |
        Out-Null
    }
}


function Save-State {
    param($State)

    Ensure-StateDirectory

    $State |
        ConvertTo-Json -Depth 10 |
        Set-Content `
            -LiteralPath $StateFile `
            -Encoding UTF8
}


function Read-State {

    if (-not (Test-Path $StateFile)) {
        return $null
    }

    try {

        return (
            Get-Content `
                -LiteralPath $StateFile `
                -Raw |
            ConvertFrom-Json
        )

    }
    catch {

        return $null
    }
}


# ============================================================
# IODINE DISCOVERY
# ============================================================

function Get-IodinePath {

    $Candidates = @()

    if ($PSScriptRoot) {

        $Candidates += Join-Path $PSScriptRoot 'iodine.exe'
        $Candidates += Join-Path $PSScriptRoot 'iodine.exe.exe'
    }

    $Command = Get-Command `
        iodine.exe `
        -CommandType Application `
        -ErrorAction SilentlyContinue

    if ($Command) {
        $Candidates += $Command.Source
    }

    $Candidates += 'C:\Program Files\dns-vpn\iodine.exe'
    $Candidates += 'C:\Tools\iodine\iodine.exe'

    foreach ($Candidate in $Candidates) {

        if (
            $Candidate -and
            (Test-Path -LiteralPath $Candidate)
        ) {

            return (Resolve-Path $Candidate).Path
        }
    }

    return $null
}


# ============================================================
# TAP CHECK
# ============================================================

function Get-TapEntries {

    $Results = @()

    if (-not (Test-Path $TapClassPath)) {
        return @()
    }

    $Keys = Get-ChildItem `
        -LiteralPath $TapClassPath `
        -ErrorAction SilentlyContinue |
    Where-Object {
        $_.PSChildName -match '^\d{4}$'
    }


    foreach ($Key in $Keys) {

        try {

            $Properties = Get-ItemProperty `
                -LiteralPath $Key.PSPath `
                -ErrorAction Stop

            $Results += [pscustomobject]@{

                Key = $Key.PSChildName

                ComponentId = [string]$Properties.ComponentId

                Name = [string]$Properties.DriverDesc

                NetCfgInstanceId = [string]$Properties.NetCfgInstanceId
            }

        }
        catch {
        }
    }

    return @($Results)
}


function Get-CompatibleTapAdapters {

    $Entries = @(
        Get-TapEntries |
        Where-Object {
            $_.ComponentId -in $SupportedTapIds
        }
    )

    $Results = @()


    foreach ($Entry in $Entries) {

        $GuidText = ([string]$Entry.NetCfgInstanceId).Trim('{}')

        $Adapter = Get-NetAdapter `
            -IncludeHidden `
            -ErrorAction SilentlyContinue |
        Where-Object {
            ([string]$_.InterfaceGuid).Trim('{}') -ieq $GuidText
        } |
        Select-Object -First 1


        $Results += [pscustomobject]@{

            Key = $Entry.Key

            ComponentId = $Entry.ComponentId

            Name = $Entry.Name

            NetCfgInstanceId = $Entry.NetCfgInstanceId

            Adapter = $Adapter
        }
    }


    return @($Results)
}


function Show-TapInstructions {

    Write-Host ''
    Write-Host 'Install the standard TAP-Windows driver:' -ForegroundColor Yellow
    Write-Host ''

    Write-Host "`$TapUrl='$TapInstallerUrl'"
    Write-Host '$TapInstaller=Join-Path $env:TEMP ''tap-windows.exe'''
    Write-Host 'Invoke-WebRequest -Uri $TapUrl -OutFile $TapInstaller'
    Write-Host 'Start-Process -FilePath $TapInstaller -Verb RunAs -Wait'

    Write-Host ''
    Write-Host 'Then rerun dns-vpn.'
    Write-Host ''
}


function Check-Dependencies {

    $Failed = $false

    $Iodine = Get-IodinePath


    if (-not $Iodine) {

        Write-Fail 'iodine.exe was not found.'

        Write-Host ''
        Write-Host 'Place iodine.exe:' -ForegroundColor Yellow
        Write-Host ''
        Write-Host '  - beside dns-vpn.ps1'
        Write-Host '  - somewhere in PATH'
        Write-Host '  - C:\Program Files\dns-vpn\iodine.exe'
        Write-Host '  - C:\Tools\iodine\iodine.exe'
        Write-Host ''

        $Failed = $true

    }
    else {

        Write-Ok "iodine.exe: $Iodine"
    }


    $TapEntries = @(Get-TapEntries)

    $Compatible = @(Get-CompatibleTapAdapters)


    if ($Compatible.Count -eq 0) {

        Write-Fail 'No iodine-compatible TAP adapter was found.'

        $Other = @(
            $TapEntries |
            Where-Object {

                $_.ComponentId -match '(?i)tap|wintun|ovpn' -or
                $_.Name -match '(?i)tap|openvpn|wintun|nordvpn'
            }
        )


        if ($Other.Count -gt 0) {

            Write-Host ''
            Write-Warn 'Other VPN adapters were detected:'
            Write-Host ''

            $Other |
                Select-Object `
                    Key,
                    ComponentId,
                    Name |
                Format-Table -AutoSize
        }


        Show-TapInstructions

        $Failed = $true

    }
    else {

        foreach ($Tap in $Compatible) {

            $AdapterName = if ($Tap.Adapter) {
                $Tap.Adapter.Name
            }
            else {
                $Tap.Name
            }

            Write-Ok (
                "Compatible TAP adapter: " +
                "$AdapterName [$($Tap.ComponentId)]"
            )
        }
    }


    if ($Failed) {
        exit 1
    }


    return [pscustomobject]@{

        Iodine = $Iodine

        Taps = $Compatible
    }
}


# ============================================================
# ROUTING HELPERS
# ============================================================

function Get-BestRoute {
    param(
        [Parameter(Mandatory)]
        [string]$RemoteAddress
    )


    $Results = @(
        Find-NetRoute `
            -RemoteIPAddress $RemoteAddress `
            -ErrorAction Stop
    )


    $Route = $Results |
        Where-Object {

            $_.CimClass.CimClassName -eq 'MSFT_NetRoute' -or
            $_.PSObject.Properties.Name -contains 'DestinationPrefix'
        } |
        Select-Object -First 1


    return $Route
}


function Add-ManagedHostRoute {
    param(
        [string]$Address,
        [int]$InterfaceIndex,
        [string]$NextHop
    )

    if (-not $NextHop) {
        $NextHop = '0.0.0.0'
    }


    $Existing = @(
        Get-NetRoute `
            -DestinationPrefix "$Address/32" `
            -AddressFamily IPv4 `
            -ErrorAction SilentlyContinue |
        Where-Object {

            $_.InterfaceIndex -eq $InterfaceIndex -and
            $_.NextHop -eq $NextHop
        }
    )


    if ($Existing.Count -gt 0) {
        return $false
    }


    New-NetRoute `
        -DestinationPrefix "$Address/32" `
        -InterfaceIndex $InterfaceIndex `
        -NextHop $NextHop `
        -RouteMetric 1 `
        -PolicyStore ActiveStore `
        -ErrorAction Stop |
    Out-Null


    return $true
}


function Remove-ManagedRoute {
    param(
        [string]$DestinationPrefix,
        [int]$InterfaceIndex,
        [string]$NextHop
    )

    $Routes = @(
        Get-NetRoute `
            -DestinationPrefix $DestinationPrefix `
            -InterfaceIndex $InterfaceIndex `
            -AddressFamily IPv4 `
            -ErrorAction SilentlyContinue |
        Where-Object {
            $_.NextHop -eq $NextHop
        }
    )


    foreach ($Route in $Routes) {

        $Route |
            Remove-NetRoute `
                -Confirm:$false `
                -ErrorAction SilentlyContinue
    }
}


# ============================================================
# TAP CLEANUP
# ============================================================

function Clear-DnsVpnTapState {
    param(
        [int]$InterfaceIndex
    )


    if (-not $InterfaceIndex) {
        return
    }


    #
    # Remove only addresses and routes owned by this tool. Do not touch
    # unrelated OpenVPN, NordVPN, Wintun or DCO adapters.
    #

    Get-NetIPAddress `
        -InterfaceIndex $InterfaceIndex `
        -AddressFamily IPv4 `
        -ErrorAction SilentlyContinue |
    Where-Object {
        $_.IPAddress -like "$TunnelPrefix*"
    } |
    ForEach-Object {

        $_ |
            Remove-NetIPAddress `
                -Confirm:$false `
                -ErrorAction SilentlyContinue
    }


    Get-NetRoute `
        -InterfaceIndex $InterfaceIndex `
        -AddressFamily IPv4 `
        -ErrorAction SilentlyContinue |
    Where-Object {

        (
            $_.DestinationPrefix -eq '0.0.0.0/0' -and
            $_.NextHop -eq $TunnelGateway
        ) -or
        $_.DestinationPrefix -eq $TunnelNetwork
    } |
    ForEach-Object {

        $_ |
            Remove-NetRoute `
                -Confirm:$false `
                -ErrorAction SilentlyContinue
    }
}


function Enable-DnsVpnTap {
    param(
        [int]$InterfaceIndex
    )


    $Adapter = Get-NetAdapter `
        -InterfaceIndex $InterfaceIndex `
        -IncludeHidden `
        -ErrorAction SilentlyContinue


    if (
        $Adapter -and
        $Adapter.Status -eq 'Disabled'
    ) {

        $Adapter |
            Enable-NetAdapter `
                -Confirm:$false `
                -ErrorAction Stop

        Start-Sleep -Milliseconds 500
    }
}


function Disable-DnsVpnTap {
    param(
        [int]$InterfaceIndex
    )


    if (-not $DisableTapWhenIdle) {
        return
    }


    $Adapter = Get-NetAdapter `
        -InterfaceIndex $InterfaceIndex `
        -IncludeHidden `
        -ErrorAction SilentlyContinue


    if ($Adapter) {

        $Adapter |
            Disable-NetAdapter `
                -Confirm:$false `
                -ErrorAction SilentlyContinue
    }
}


# ============================================================
# IODINE OUTPUT
# ============================================================

function Show-IodineLog {

    Write-Host ''

    if (Test-Path $StderrFile) {

        Get-Content `
            -LiteralPath $StderrFile `
            -Tail 30 `
            -ErrorAction SilentlyContinue
    }

    if (Test-Path $StdoutFile) {

        Get-Content `
            -LiteralPath $StdoutFile `
            -Tail 30 `
            -ErrorAction SilentlyContinue
    }

    Write-Host ''
}


function Get-IodineOutput {

    $Text = ''


    if (Test-Path $StderrFile) {

        $Text += (
            Get-Content `
                -LiteralPath $StderrFile `
                -Raw `
                -ErrorAction SilentlyContinue
        )
    }


    $Text += "`n"


    if (Test-Path $StdoutFile) {

        $Text += (
            Get-Content `
                -LiteralPath $StdoutFile `
                -Raw `
                -ErrorAction SilentlyContinue
        )
    }


    return $Text
}


# ============================================================
# CLIENT CLEANUP
# ============================================================

function Remove-ClientState {
    param(
        $State,
        [switch]$Quiet
    )


    if (-not $State) {
        return
    }


    if (-not $Quiet) {
        Write-Info 'Disconnecting DNS VPN...'
    }


    # --------------------------------------------------------
    # Stop iodine first so it cannot re-apply TAP state while
    # cleanup is in progress.
    # --------------------------------------------------------

    if ($State.IodinePid) {

        Stop-Process `
            -Id $State.IodinePid `
            -Force `
            -ErrorAction SilentlyContinue
    }


    # --------------------------------------------------------
    # Remove the temporary VPN default route.
    # --------------------------------------------------------

    if (
        $State.DefaultRouteAdded -and
        $State.TunnelInterfaceIndex
    ) {

        Remove-ManagedRoute `
            -DestinationPrefix '0.0.0.0/0' `
            -InterfaceIndex $State.TunnelInterfaceIndex `
            -NextHop $TunnelGateway
    }


    # --------------------------------------------------------
    # Remove dns-vpn-owned TAP addressing/routes and restore the
    # adapter metric.
    # --------------------------------------------------------

    if ($State.TunnelInterfaceIndex) {

        Clear-DnsVpnTapState `
            -InterfaceIndex $State.TunnelInterfaceIndex


        if ($null -ne $State.OriginalInterfaceMetric) {

            try {

                if ($State.OriginalAutomaticMetric -eq 'Enabled') {

                    Set-NetIPInterface `
                        -InterfaceIndex $State.TunnelInterfaceIndex `
                        -AddressFamily IPv4 `
                        -AutomaticMetric Enabled `
                        -ErrorAction SilentlyContinue
                }
                else {

                    Set-NetIPInterface `
                        -InterfaceIndex $State.TunnelInterfaceIndex `
                        -AddressFamily IPv4 `
                        -AutomaticMetric Disabled `
                        -InterfaceMetric $State.OriginalInterfaceMetric `
                        -ErrorAction SilentlyContinue
                }

            }
            catch {
            }
        }


        Disable-DnsVpnTap `
            -InterfaceIndex $State.TunnelInterfaceIndex
    }


    # --------------------------------------------------------
    # Remove DNS host routes we created.
    # --------------------------------------------------------

    if ($State.DnsRoutes) {

        foreach ($Route in $State.DnsRoutes) {

            Remove-ManagedRoute `
                -DestinationPrefix "$($Route.Address)/32" `
                -InterfaceIndex $Route.InterfaceIndex `
                -NextHop $Route.NextHop
        }
    }


    # --------------------------------------------------------
    # Remove EC2/server host route if we created one.
    # --------------------------------------------------------

    if (
        $State.ServerRouteAdded -and
        $State.Server
    ) {

        Remove-ManagedRoute `
            -DestinationPrefix "$($State.Server)/32" `
            -InterfaceIndex $State.PhysicalInterfaceIndex `
            -NextHop $State.PhysicalNextHop
    }


    Remove-Item `
        -LiteralPath $StateFile `
        -Force `
        -ErrorAction SilentlyContinue


    if (-not $Quiet) {

        Write-Ok 'Disconnected.'
        Write-Ok 'dns-vpn routes and TAP tunnel state cleaned up.'
        Write-Ok 'Normal routing restored.'
    }
}


# ============================================================
# STATUS
# ============================================================

function Show-ClientStatus {

    Require-Administrator

    Write-Heading 'DNS VPN CLIENT STATUS'


    $State = Read-State


    if (-not $State) {

        Write-Fail 'DNS VPN client is not running.'
        return
    }


    $Process = Get-Process `
        -Id $State.IodinePid `
        -ErrorAction SilentlyContinue


    if (-not $Process) {

        Write-Fail 'iodine is no longer running.'
        Write-Warn 'Removing stale dns-vpn state.'

        Remove-ClientState `
            $State `
            -Quiet

        return
    }


    Write-Ok 'DNS VPN client is connected.'

    Write-Host ''

    Write-Host ('  {0,-20} {1}' -f 'PID:', $State.IodinePid)
    Write-Host ('  {0,-20} {1}' -f 'Mode:', $State.Mode)
    Write-Host ('  {0,-20} {1}' -f 'Server:', $State.Server)
    Write-Host ('  {0,-20} {1}' -f 'Physical:', $State.PhysicalInterfaceAlias)
    Write-Host ('  {0,-20} {1}' -f 'Tunnel adapter:', $State.TunnelInterfaceAlias)
    Write-Host ('  {0,-20} {1}' -f 'Tunnel IP:', $State.TunnelIPAddress)
    Write-Host ('  {0,-20} {1}' -f 'Gateway:', $TunnelGateway)
    Write-Host ('  {0,-20} {1}' -f 'DNS type:', $DnsQueryType)
    Write-Host ('  {0,-20} {1}' -f 'Fragment size:', $FragmentSize)

    Write-Host ''


    try {

        $PublicIP = (
            Invoke-RestMethod `
                -Uri 'https://api.ipify.org' `
                -TimeoutSec 10
        ).ToString().Trim()

        Write-Ok "Current public IP: $PublicIP"

    }
    catch {

        Write-Warn 'Unable to determine current public IP.'
    }
}


# ============================================================
# STOP
# ============================================================

function Stop-Client {

    Require-Administrator

    Write-Heading 'STOP DNS VPN CLIENT'


    $State = Read-State


    if (-not $State) {

        #
        # Even without a saved state file, clean any stale dns-vpn state
        # from compatible TAP adapters left by an interrupted/failed run.
        #

        $Deps = Check-Dependencies


        foreach ($Tap in @($Deps.Taps)) {

            if ($Tap.Adapter) {

                Clear-DnsVpnTapState `
                    -InterfaceIndex $Tap.Adapter.ifIndex

                Disable-DnsVpnTap `
                    -InterfaceIndex $Tap.Adapter.ifIndex
            }
        }


        Write-Warn 'No managed DNS VPN client is running.'
        Write-Ok 'Any stale dns-vpn TAP state has been cleaned.'

        return
    }


    Remove-ClientState $State
}


# ============================================================
# BACKGROUND MONITOR
# ============================================================

function Start-BackgroundMonitor {

    $PowerShell = Join-Path $PSHOME 'powershell.exe'

    if (-not (Test-Path $PowerShell)) {

        $PowerShell = (
            Get-Command powershell.exe `
                -ErrorAction SilentlyContinue
        ).Source
    }


    if (-not $PowerShell) {
        return
    }


    $Arguments = @(
        '-NoProfile'
        '-ExecutionPolicy'
        'Bypass'
        '-File'
        "`"$PSCommandPath`""
        '__monitor'
    ) -join ' '


    Start-Process `
        -FilePath $PowerShell `
        -ArgumentList $Arguments `
        -WindowStyle Hidden |
    Out-Null
}


function Run-Monitor {

    while ($true) {

        $State = Read-State


        if (-not $State) {
            return
        }


        $Process = Get-Process `
            -Id $State.IodinePid `
            -ErrorAction SilentlyContinue


        if (-not $Process) {

            Remove-ClientState `
                $State `
                -Quiet

            return
        }


        Start-Sleep -Seconds 2
    }
}


# ============================================================
# CLIENT CONNECT
# ============================================================

function Start-Client {
    param(
        [Parameter(Mandatory)]
        [string]$Password,

        [Parameter(Mandatory)]
        [string]$Server,

        [ValidateSet('foreground','background')]
        [string]$Mode = 'foreground'
    )


    Require-Administrator
    Ensure-StateDirectory


    Write-Heading 'DNS VPN CLIENT'


    #
    # Dependency checks happen before any route modification.
    #
    $Deps = Check-Dependencies

    $Iodine = $Deps.Iodine

    $CompatibleTaps = @($Deps.Taps)


    Write-Host ''


    # --------------------------------------------------------
    # Validate IPv4.
    # --------------------------------------------------------

    $ParsedIP = $null


    if (
        -not [Net.IPAddress]::TryParse(
            $Server,
            [ref]$ParsedIP
        )
    ) {

        Fail "Invalid server IP: $Server"
    }


    if (
        $ParsedIP.AddressFamily -ne
        [Net.Sockets.AddressFamily]::InterNetwork
    ) {

        Fail 'Server must be an IPv4 address.'
    }


    # --------------------------------------------------------
    # Existing session.
    # --------------------------------------------------------

    $ExistingState = Read-State


    if ($ExistingState) {

        $ExistingProcess = Get-Process `
            -Id $ExistingState.IodinePid `
            -ErrorAction SilentlyContinue


        if ($ExistingProcess) {

            Fail (
                'A DNS VPN client is already connected. ' +
                'Use --status or --stop.'
            )
        }


        Write-Warn 'Removing stale dns-vpn state.'

        Remove-ClientState `
            $ExistingState `
            -Quiet
    }


    # --------------------------------------------------------
    # Select and prepare the dedicated iodine TAP adapter.
    # --------------------------------------------------------

    $TapEntry = $CompatibleTaps |
        Where-Object {
            $_.Adapter
        } |
        Select-Object -First 1


    if (
        -not $TapEntry -or
        -not $TapEntry.Adapter
    ) {

        Fail (
            'Compatible TAP registry entry exists, but Windows ' +
            'did not expose the adapter.'
        )
    }


    $TapAdapter = $TapEntry.Adapter

    $TapIndex = [int]$TapAdapter.ifIndex


    #
    # The driver/adapter remains installed between sessions. Clean only
    # the IP address and routes owned by dns-vpn, then enable it for iodine.
    #

    Clear-DnsVpnTapState `
        -InterfaceIndex $TapIndex


    Write-Info (
        "Preparing TAP adapter `"$($TapAdapter.Name)`"..."
    )


    Enable-DnsVpnTap `
        -InterfaceIndex $TapIndex


    $TapIPInterface = Get-NetIPInterface `
        -InterfaceIndex $TapIndex `
        -AddressFamily IPv4 `
        -ErrorAction SilentlyContinue


    $OriginalAutomaticMetric = if ($TapIPInterface) {

        $TapIPInterface.AutomaticMetric.ToString()
    }
    else {

        $null
    }


    $OriginalInterfaceMetric = if ($TapIPInterface) {

        [int]$TapIPInterface.InterfaceMetric
    }
    else {

        $null
    }


    # --------------------------------------------------------
    # Current physical route to server.
    # --------------------------------------------------------

    $PhysicalRoute = Get-BestRoute $Server


    if (-not $PhysicalRoute) {

        Disable-DnsVpnTap `
            -InterfaceIndex $TapIndex

        Fail "Could not determine a route to $Server."
    }


    $PhysicalInterfaceIndex = [int]$PhysicalRoute.InterfaceIndex
    $PhysicalNextHop = [string]$PhysicalRoute.NextHop


    if (-not $PhysicalNextHop) {
        $PhysicalNextHop = '0.0.0.0'
    }


    if ($PhysicalNextHop -eq '0.0.0.0') {

        Disable-DnsVpnTap `
            -InterfaceIndex $TapIndex

        Fail (
            "Route discovery returned an on-link route for public server " +
            "$Server. Refusing to install a broken transport route."
        )
    }


    $PhysicalAdapter = Get-NetAdapter `
        -InterfaceIndex $PhysicalInterfaceIndex `
        -ErrorAction Stop


    Write-Ok (
        "Transport: $Server via $PhysicalNextHop " +
        "dev `"$($PhysicalAdapter.Name)`""
    )


    # --------------------------------------------------------
    # Runtime state.
    # --------------------------------------------------------

    $State = [pscustomobject]@{

        Version = $Version

        Mode = $Mode

        IodinePid = $null

        Server = $Server

        PhysicalInterfaceIndex = $PhysicalInterfaceIndex

        PhysicalInterfaceAlias = $PhysicalAdapter.Name

        PhysicalNextHop = $PhysicalNextHop

        ServerRouteAdded = $false

        DnsRoutes = @()

        TunnelInterfaceIndex = $TapIndex

        TunnelInterfaceAlias = $TapAdapter.Name

        TunnelIPAddress = $null

        OriginalAutomaticMetric = $OriginalAutomaticMetric

        OriginalInterfaceMetric = $OriginalInterfaceMetric

        DefaultRouteAdded = $false
    }


    $ConnectionCompleted = $false


    try {

        # ====================================================
        # PIN SERVER TRANSPORT
        # ====================================================

        $State.ServerRouteAdded = Add-ManagedHostRoute `
            -Address $Server `
            -InterfaceIndex $PhysicalInterfaceIndex `
            -NextHop $PhysicalNextHop


        # ====================================================
        # PIN PHYSICAL DNS SERVERS
        # ====================================================

        $DnsServers = @(

            (
                Get-DnsClientServerAddress `
                    -InterfaceIndex $PhysicalInterfaceIndex `
                    -AddressFamily IPv4 `
                    -ErrorAction SilentlyContinue
            ).ServerAddresses

        ) |
        Where-Object {

            $_ -and
            $_ -notmatch '^127\.' -and
            $_ -ne $Server

        } |
        Select-Object -Unique


        foreach ($Dns in $DnsServers) {

            try {

                $DnsRoute = Get-BestRoute $Dns


                if (-not $DnsRoute) {
                    continue
                }


                $DnsNextHop = [string]$DnsRoute.NextHop


                if (-not $DnsNextHop) {
                    $DnsNextHop = '0.0.0.0'
                }


                $Added = Add-ManagedHostRoute `
                    -Address $Dns `
                    -InterfaceIndex ([int]$DnsRoute.InterfaceIndex) `
                    -NextHop $DnsNextHop


                if ($Added) {

                    $State.DnsRoutes += [pscustomobject]@{

                        Address = $Dns

                        InterfaceIndex = [int]$DnsRoute.InterfaceIndex

                        NextHop = $DnsNextHop
                    }
                }

            }
            catch {

                Write-Warn "Unable to pin DNS server $Dns."
            }
        }


        # ====================================================
        # LOGS
        # ====================================================

        Set-Content `
            -LiteralPath $StdoutFile `
            -Value '' `
            -Encoding ASCII

        Set-Content `
            -LiteralPath $StderrFile `
            -Value '' `
            -Encoding ASCII


        # ====================================================
        # START IODINE
        # ====================================================

        Write-Host ''
        Write-Info 'Connecting to DNS VPN...'


        $OldPassword = $env:IODINE_PASS

        $env:IODINE_PASS = $Password


        try {

            $Process = Start-Process `
                -FilePath $Iodine `
                -ArgumentList @(
                    '-f'
                    '-r'
                    '-T'
                    $DnsQueryType
                    '-m'
                    "$FragmentSize"
                    $Server
                    $TopDomain
                ) `
                -RedirectStandardOutput $StdoutFile `
                -RedirectStandardError $StderrFile `
                -WindowStyle Hidden `
                -PassThru

        }
        finally {

            if ($null -eq $OldPassword) {

                Remove-Item `
                    Env:\IODINE_PASS `
                    -ErrorAction SilentlyContinue
            }
            else {

                $env:IODINE_PASS = $OldPassword
            }
        }


        $State.IodinePid = $Process.Id


        # ====================================================
        # WAIT FOR IODINE NEGOTIATION
        #
        # Query type and fragment size are fixed above, so there is no
        # reason for Windows to spend tens of seconds probing them.
        #
        # Redirected Windows iodine output can be buffered, so readiness
        # uses two signals:
        #
        #   1. the selected TAP receives 10.253.53.x; and
        #   2. iodine reports connection completion OR the TAP address has
        #      remained present for a short grace period.
        #
        # ICMP to 10.253.53.1 is checked afterwards but is not used as the
        # sole readiness gate.
        # ====================================================

        Write-Host '    Negotiating' -NoNewline


        $TunnelAddress = $null

        $TunnelWorking = $false

        $TunnelAddressSeenAt = $null


        for ($i = 0; $i -lt 30; $i++) {

            $CurrentProcess = Get-Process `
                -Id $Process.Id `
                -ErrorAction SilentlyContinue


            if (-not $CurrentProcess) {

                Write-Host ''

                Show-IodineLog

                throw 'iodine exited before tunnel establishment.'
            }


            $IodineOutput = Get-IodineOutput


            if ($IodineOutput -match 'Bad password') {

                Write-Host ''

                Show-IodineLog

                throw 'Server rejected the password.'
            }


            if (
                $IodineOutput -match
                'No suitable DNS query type found'
            ) {

                Write-Host ''

                Show-IodineLog

                throw (
                    'iodine could not establish the forced DNS transport.'
                )
            }


            $TunnelAddress = Get-NetIPAddress `
                -InterfaceIndex $TapIndex `
                -AddressFamily IPv4 `
                -ErrorAction SilentlyContinue |
            Where-Object {

                $_.IPAddress -like "$TunnelPrefix*" -and
                $_.IPAddress -ne $TunnelGateway

            } |
            Select-Object -First 1


            if ($TunnelAddress) {

                if ($null -eq $TunnelAddressSeenAt) {

                    $TunnelAddressSeenAt = Get-Date
                }


                $LogComplete = (
                    $IodineOutput -match
                    'Connection setup complete,\s*transmitting data'
                )


                $AddressStable = (
                    ((Get-Date) - $TunnelAddressSeenAt).TotalSeconds -ge 3
                )


                if (
                    $LogComplete -or
                    $AddressStable
                ) {

                    $TunnelWorking = $true

                    break
                }
            }


            Write-Host '.' -NoNewline

            Start-Sleep -Seconds 1
        }


        Write-Host ''


        if (-not $TunnelWorking) {

            Show-IodineLog

            throw (
                'iodine did not complete tunnel negotiation within 30 seconds.'
            )
        }


        # ====================================================
        # TAP DETAILS
        # ====================================================

        $TunnelIndex = $TapIndex


        $TunnelAdapter = Get-NetAdapter `
            -InterfaceIndex $TunnelIndex `
            -ErrorAction Stop


        $State.TunnelInterfaceIndex = $TunnelIndex

        $State.TunnelInterfaceAlias = $TunnelAdapter.Name

        $State.TunnelIPAddress = $TunnelAddress.IPAddress


        Set-NetIPInterface `
            -InterfaceIndex $TunnelIndex `
            -AddressFamily IPv4 `
            -AutomaticMetric Disabled `
            -InterfaceMetric 1


        Write-Ok 'DNS tunnel connected.'

        Write-Host ''

        Write-Host (
            '  {0,-20} {1}' -f
            'Tunnel IP:',
            $TunnelAddress.IPAddress
        )

        Write-Host (
            '  {0,-20} {1}' -f
            'Gateway:',
            $TunnelGateway
        )

        Write-Host (
            '  {0,-20} {1}' -f
            'Transport:',
            "$Server`:53/UDP"
        )

        Write-Host (
            '  {0,-20} {1}' -f
            'Physical:',
            $PhysicalAdapter.Name
        )

        Write-Host (
            '  {0,-20} {1}' -f
            'Tunnel adapter:',
            $TunnelAdapter.Name
        )

        Write-Host (
            '  {0,-20} {1}' -f
            'DNS type:',
            $DnsQueryType
        )

        Write-Host (
            '  {0,-20} {1}' -f
            'Fragment size:',
            $FragmentSize
        )


        Write-Host ''
        Write-Info 'Checking tunnel endpoint...'


        & ping.exe `
            -n 1 `
            -w 3000 `
            $TunnelGateway `
            *> $null


        if ($LASTEXITCODE -eq 0) {

            Write-Ok "Tunnel endpoint reachable: $TunnelGateway"
        }
        else {

            Write-Warn (
                "Tunnel interface is up, but $TunnelGateway did not answer ICMP."
            )

            if ($TunnelAdapter.DriverInformation) {

                Write-Warn (
                    "TAP driver: " +
                    $TunnelAdapter.DriverInformation
                )
            }


            Write-Info (
                'No Internet default route will be installed while the ' +
                'tunnel data path is failing.'
            )


            Write-Host ''
            Write-Info 'Local TAP diagnostics:'
            Write-Host ''


            Get-NetIPAddress `
                -InterfaceIndex $TunnelIndex `
                -AddressFamily IPv4 `
                -ErrorAction SilentlyContinue |
            Select-Object `
                InterfaceAlias,
                IPAddress,
                PrefixLength |
            Format-Table -AutoSize


            Get-NetRoute `
                -InterfaceIndex $TunnelIndex `
                -AddressFamily IPv4 `
                -ErrorAction SilentlyContinue |
            Sort-Object DestinationPrefix |
            Select-Object `
                DestinationPrefix,
                NextHop,
                RouteMetric |
            Format-Table -AutoSize


            # Save enough state for --stop and for clean Ctrl+C teardown.
            Save-State $State

            $ConnectionCompleted = $true


            Write-Warn 'DNS VPN data path is not usable yet.'
            Write-Info (
                'The iodine process is being left running for diagnosis; ' +
                'the normal Windows default route is unchanged.'
            )


            if ($Mode -eq 'background') {

                Start-BackgroundMonitor

                Write-Info '.\dns-vpn.ps1 client --status'
                Write-Info '.\dns-vpn.ps1 client --stop'

                return
            }


            Write-Info 'Press Ctrl+C to disconnect and clean up.'
            Write-Host ''


            try {

                while (
                    Get-Process `
                        -Id $Process.Id `
                        -ErrorAction SilentlyContinue
                ) {

                    Start-Sleep -Seconds 1
                }

            }
            finally {

                $CurrentState = Read-State

                if ($CurrentState) {

                    Write-Host ''

                    Remove-ClientState $CurrentState
                }
            }


            return
        }


        # ====================================================
        # ADD PREFERRED DEFAULT ROUTE
        # ====================================================

        Write-Info (
            "Routing Internet traffic through " +
            "$($TunnelAdapter.Name)..."
        )


        $ExistingDefault = @(
            Get-NetRoute `
                -DestinationPrefix '0.0.0.0/0' `
                -InterfaceIndex $TunnelIndex `
                -AddressFamily IPv4 `
                -ErrorAction SilentlyContinue |
            Where-Object {

                $_.NextHop -eq $TunnelGateway
            }
        )


        if ($ExistingDefault.Count -eq 0) {

            New-NetRoute `
                -DestinationPrefix '0.0.0.0/0' `
                -InterfaceIndex $TunnelIndex `
                -NextHop $TunnelGateway `
                -RouteMetric 1 `
                -PolicyStore ActiveStore |
            Out-Null


            $State.DefaultRouteAdded = $true
        }


        # ====================================================
        # SAVE STATE
        # ====================================================

        Save-State $State

        $ConnectionCompleted = $true


        # ====================================================
        # RESULT
        # ====================================================

        Write-Host ''
        Write-Host '============================================================' -ForegroundColor Green
        Write-Host '                 DNS VPN CONNECTED' -ForegroundColor Green
        Write-Host '============================================================' -ForegroundColor Green
        Write-Host ''


        Get-NetRoute `
            -DestinationPrefix '0.0.0.0/0' `
            -AddressFamily IPv4 |
        Sort-Object RouteMetric |
        Select-Object `
            DestinationPrefix,
            NextHop,
            InterfaceAlias,
            RouteMetric |
        Format-Table -AutoSize


        Write-Info 'Testing Internet access...'


        try {

            $PublicIP = (
                Invoke-RestMethod `
                    -Uri 'https://api.ipify.org' `
                    -TimeoutSec 30
            ).ToString().Trim()


            Write-Ok 'Internet routing working.'

            Write-Ok "Public IP: $PublicIP"


            if ($PublicIP -eq $Server) {

                Write-Ok (
                    'Internet traffic is exiting through ' +
                    'the DNS VPN server.'
                )
            }
            else {

                Write-Warn (
                    "Public IP $PublicIP does not match " +
                    "server $Server."
                )
            }

        }
        catch {

            Write-Warn (
                'Tunnel is connected but the Internet ' +
                'egress test failed.'
            )
        }


        Write-Host ''


        # ====================================================
        # BACKGROUND
        # ====================================================

        if ($Mode -eq 'background') {

            Start-BackgroundMonitor


            Write-Ok 'DNS VPN is running in the background.'

            Write-Info (
                '.\dns-vpn.ps1 client --status'
            )

            Write-Info (
                '.\dns-vpn.ps1 client --stop'
            )


            return
        }


        # ====================================================
        # FOREGROUND
        # ====================================================

        Write-Info 'Foreground mode.'

        Write-Info 'Press Ctrl+C to disconnect.'

        Write-Host ''


        try {

            while (

                Get-Process `
                    -Id $Process.Id `
                    -ErrorAction SilentlyContinue

            ) {

                Start-Sleep -Seconds 1
            }

        }
        finally {

            $CurrentState = Read-State


            if ($CurrentState) {

                Write-Host ''

                Remove-ClientState $CurrentState
            }
        }

    }
    catch {

        #
        # Failure cleanup must not mask the original exception.
        #

        if ($State.IodinePid) {

            Stop-Process `
                -Id $State.IodinePid `
                -Force `
                -ErrorAction SilentlyContinue
        }


        if ($State.DefaultRouteAdded) {

            Remove-ManagedRoute `
                -DestinationPrefix '0.0.0.0/0' `
                -InterfaceIndex $TapIndex `
                -NextHop $TunnelGateway
        }


        Clear-DnsVpnTapState `
            -InterfaceIndex $TapIndex


        if ($State.DnsRoutes) {

            foreach ($Route in $State.DnsRoutes) {

                Remove-ManagedRoute `
                    -DestinationPrefix "$($Route.Address)/32" `
                    -InterfaceIndex $Route.InterfaceIndex `
                    -NextHop $Route.NextHop
            }
        }


        if ($State.ServerRouteAdded) {

            Remove-ManagedRoute `
                -DestinationPrefix "$Server/32" `
                -InterfaceIndex $PhysicalInterfaceIndex `
                -NextHop $PhysicalNextHop
        }


        if ($null -ne $OriginalInterfaceMetric) {

            try {

                if ($OriginalAutomaticMetric -eq 'Enabled') {

                    Set-NetIPInterface `
                        -InterfaceIndex $TapIndex `
                        -AddressFamily IPv4 `
                        -AutomaticMetric Enabled `
                        -ErrorAction SilentlyContinue
                }
                else {

                    Set-NetIPInterface `
                        -InterfaceIndex $TapIndex `
                        -AddressFamily IPv4 `
                        -AutomaticMetric Disabled `
                        -InterfaceMetric $OriginalInterfaceMetric `
                        -ErrorAction SilentlyContinue
                }

            }
            catch {
            }
        }


        Disable-DnsVpnTap `
            -InterfaceIndex $TapIndex


        Remove-Item `
            -LiteralPath $StateFile `
            -Force `
            -ErrorAction SilentlyContinue


        Write-Fail $_.Exception.Message

        Write-Ok 'Failure cleanup completed.'

        exit 1
    }
}


# ============================================================
# ARGUMENT PARSER
# ============================================================

function Main {

    $Command = $null

    $Password = $null

    $Server = $null

    $Mode = 'foreground'

    $Action = 'start'


    $Index = 0


    while ($Index -lt $args.Count) {

        $Argument = $args[$Index]


        switch ($Argument) {


            'client' {

                $Command = 'client'

                $Index++
            }


            '__monitor' {

                $Command = '__monitor'

                $Index++
            }


            '-p' {

                if (($Index + 1) -ge $args.Count) {

                    Fail 'Missing password after -p.'
                }

                $Password = $args[$Index + 1]

                $Index += 2
            }


            '--password' {

                if (($Index + 1) -ge $args.Count) {

                    Fail 'Missing password after --password.'
                }

                $Password = $args[$Index + 1]

                $Index += 2
            }


            '-s' {

                if (($Index + 1) -ge $args.Count) {

                    Fail 'Missing server address after -s.'
                }

                $Server = $args[$Index + 1]

                $Index += 2
            }


            '--server' {

                if (($Index + 1) -ge $args.Count) {

                    Fail 'Missing server address after --server.'
                }

                $Server = $args[$Index + 1]

                $Index += 2
            }


            '-b' {

                $Mode = 'background'

                $Index++
            }


            '--background' {

                $Mode = 'background'

                $Index++
            }


            '-f' {

                $Mode = 'foreground'

                $Index++
            }


            '--foreground' {

                $Mode = 'foreground'

                $Index++
            }


            '--status' {

                $Action = 'status'

                $Index++
            }


            '--stop' {

                $Action = 'stop'

                $Index++
            }


            '-h' {

                $Action = 'help'

                $Index++
            }


            '--help' {

                $Action = 'help'

                $Index++
            }


            default {

                Fail "Unknown argument: $Argument"
            }
        }
    }


    # --------------------------------------------------------
    # INTERNAL MONITOR
    # --------------------------------------------------------

    if ($Command -eq '__monitor') {

        Require-Administrator

        Run-Monitor

        return
    }


    # --------------------------------------------------------
    # HELP
    # --------------------------------------------------------

    if ($Action -eq 'help') {

        if ($Command -eq 'client') {

            Show-ClientHelp

        }
        else {

            Show-TopHelp
        }

        return
    }


    if (-not $Command) {

        Show-TopHelp

        return
    }


    if ($Command -ne 'client') {

        Fail "Unknown command: $Command"
    }


    # --------------------------------------------------------
    # ACTION
    # --------------------------------------------------------

    switch ($Action) {


        'status' {

            Show-ClientStatus

            return
        }


        'stop' {

            Stop-Client

            return
        }


        'start' {

            if (-not $Password) {

                Fail (
                    'Password required. ' +
                    'Use -p "Password123-"'
                )
            }


            if (-not $Server) {

                Fail (
                    'Server IP required. ' +
                    'Use -s 203.0.113.10'
                )
            }


            Start-Client `
                -Password $Password `
                -Server $Server `
                -Mode $Mode
        }
    }
}


Main @args