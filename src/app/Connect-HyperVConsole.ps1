param(
    [string]$ComputerName = '',
    [Parameter(Mandatory = $true)][string]$VMId,
    [ValidateSet('Console', 'Files')][string]$Mode = 'Console',
    [switch]$CredentialFromInput
)

# Shows a Hyper-V VM's console in Server Inventory and Migration Manager's
# console window (AGENTS rule 63). Console mode reads one JSON request per
# line on standard input and writes one JSON answer per line: the screen
# through Msvm_VirtualSystemManagementService.GetVirtualSystemThumbnailImage,
# keys through Msvm_Keyboard and the pointer through Msvm_SyntheticMouse. It
# uses CIM (WinRM, then DCOM) for a remote host and never changes a VM
# setting. Files mode copies one file into or out of the VM with PowerShell
# Direct on this host and never replaces an existing file. Accounts arrive as
# one JSON line on standard input, never on a command line, and are never
# written anywhere. Compatible with Windows PowerShell 3.0 and later.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
try { [Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Answer {
    param($Value)
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject $Value -Depth 6 -Compress))
    [Console]::Out.Flush()
}

$script:Namespace = 'root\virtualization\v2'
$script:Session = $null

function Get-Cim {
    param([string]$Class, [string]$Filter = '')
    $arguments = @{ Namespace = $script:Namespace; ClassName = $Class }
    if ($Filter) { $arguments.Filter = $Filter }
    if ($script:Session) { $arguments.CimSession = $script:Session }
    return @(Get-CimInstance @arguments)
}

function Get-FirstCim {
    param([string]$Class, [string]$Filter = '')
    try { $found = @(Get-Cim -Class $Class -Filter $Filter) } catch { return $null }
    if ($found.Count -gt 0) { return $found[0] }
    return $null
}

function Invoke-Method {
    param($Instance, [string]$Name, [hashtable]$Arguments = @{})
    $result = Invoke-CimMethod -InputObject $Instance -MethodName $Name -Arguments $Arguments
    if ($result -and $null -ne $result.ReturnValue -and [int]$result.ReturnValue -ne 0 -and [int]$result.ReturnValue -ne 4096) {
        throw ($Name + ' returned ' + $result.ReturnValue)
    }
    return $result
}

if ($VMId -notmatch '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$') {
    Write-Answer @{ ok = $false; error = 'The VM ID is not a GUID.' }
    exit 2
}

$given = $null
$credential = $null
$line = [Console]::In.ReadLine()
if ($line) { $given = ConvertFrom-Json -InputObject $line }
$line = $null
if ($CredentialFromInput -and $given -and [string]$given.user -and [string]$given.password) {
    $credential = New-Object System.Management.Automation.PSCredential([string]$given.user, (ConvertTo-SecureString -String ([string]$given.password) -AsPlainText -Force))
}

if ($Mode -eq 'Files') {
    # One copy with PowerShell Direct; the guest account is required.
    try {
        if (-not $credential) { throw 'Type a user name and password of Windows in the VM.' }
        $direction = [string]$given.direction
        $local = [string]$given.local
        $guest = [string]$given.guest
        $given = $null
        $session = New-PSSession -VMId $VMId -Credential $credential
        try {
            if ($direction -eq 'to') {
                if (-not (Test-Path -LiteralPath $local -PathType Leaf)) { throw ('The file ' + $local + ' does not exist on this computer.') }
                $exists = Invoke-Command -Session $session -ScriptBlock { param($p) Test-Path -LiteralPath $p } -ArgumentList $guest
                if ($exists) { throw 'That file already exists in the VM; choose another name. Files are never replaced.' }
                Copy-Item -LiteralPath $local -Destination $guest -ToSession $session
                $size = (Get-Item -LiteralPath $local).Length
            } else {
                if (Test-Path -LiteralPath $local) { throw ($local + ' already exists on this computer; choose another name.') }
                $isFile = Invoke-Command -Session $session -ScriptBlock { param($p) Test-Path -LiteralPath $p -PathType Leaf } -ArgumentList $guest
                if (-not $isFile) { throw 'That file does not exist in the VM.' }
                Copy-Item -LiteralPath $guest -Destination $local -FromSession $session
                $size = (Get-Item -LiteralPath $local).Length
            }
        } finally {
            Remove-PSSession -Session $session -ErrorAction SilentlyContinue
        }
        Write-Answer @{ ok = $true; bytes = [int64]$size }
        exit 0
    } catch {
        Write-Answer @{ ok = $false; error = $_.Exception.Message }
        exit 1
    }
}

try {
    $computer = $ComputerName.Trim()
    $remote = $computer -and -not ($computer -eq '.' -or $computer -eq 'localhost' -or $computer -ieq $env:COMPUTERNAME)
    if ($remote) {
        $sessionArguments = @{ ComputerName = $computer; ErrorAction = 'Stop' }
        if ($credential) { $sessionArguments.Credential = $credential }
        try {
            $script:Session = New-CimSession @sessionArguments
        } catch {
            $first = $_.Exception.Message
            try {
                $sessionArguments.SessionOption = New-CimSessionOption -Protocol Dcom
                $script:Session = New-CimSession @sessionArguments
            } catch {
                throw ('Could not connect to ' + $computer + ': ' + $first)
            }
        }
    } else {
        $computer = $env:COMPUTERNAME
    }
    $credential = $null
    $given = $null
    $vm = Get-FirstCim -Class 'Msvm_ComputerSystem' -Filter ("Name='" + $VMId + "'")
    if (-not $vm) {
        if (-not (Get-FirstCim -Class 'Msvm_VirtualSystemManagementService')) { throw ('Hyper-V is not installed on ' + $computer + '.') }
        throw ('Hyper-V on ' + $computer + ' has no VM with this ID. It may have moved to another host.')
    }
    $service = Get-FirstCim -Class 'Msvm_VirtualSystemManagementService'
} catch {
    Write-Answer @{ ok = $false; error = $_.Exception.Message }
    exit 1
}

function Get-Settings {
    return Get-FirstCim -Class 'Msvm_VirtualSystemSettingData' -Filter ("VirtualSystemIdentifier='" + $VMId + "' AND VirtualSystemType='Microsoft:Hyper-V:System:Realized'")
}

function Get-Info {
    $current = Get-FirstCim -Class 'Msvm_ComputerSystem' -Filter ("Name='" + $VMId + "'")
    if (-not $current) { throw 'The VM no longer exists on this host.' }
    $video = Get-FirstCim -Class 'Msvm_VideoHead' -Filter ("SystemName='" + $VMId + "'")
    $width = 1024; $height = 768
    if ($video) {
        if ([int]$video.CurrentHorizontalResolution -gt 0) { $width = [int]$video.CurrentHorizontalResolution }
        if ([int]$video.CurrentVerticalResolution -gt 0) { $height = [int]$video.CurrentVerticalResolution }
    }
    $settings = Get-Settings
    $generation = 0
    if ($settings -and [string]$settings.VirtualSystemSubType -match 'Type-(\d)') { $generation = [int]$Matches[1] }
    # A synthetic mouse (generation 2, or generation 1 with integration
    # services) is moved to absolute positions; a generation 1 VM without it
    # has only the PS/2 mouse. Either is offered; a refused call says why.
    $script:Mouse = Get-FirstCim -Class 'Msvm_SyntheticMouse' -Filter ("SystemName='" + $VMId + "'")
    if (-not $script:Mouse) { $script:Mouse = Get-FirstCim -Class 'Msvm_Ps2Mouse' -Filter ("SystemName='" + $VMId + "'") }
    $mouseReady = $false
    if ($script:Mouse) { $mouseReady = $true }
    $enhancedState = 0
    try { if ($null -ne $current.EnhancedSessionModeState) { $enhancedState = [int]$current.EnhancedSessionModeState } } catch { }
    $enhancedHost = $null
    try {
        $hostSettings = Get-FirstCim -Class 'Msvm_VirtualSystemManagementServiceSettingData'
        if ($hostSettings -and $null -ne $hostSettings.EnhancedSessionModeEnabled) { $enhancedHost = [bool]$hostSettings.EnhancedSessionModeEnabled }
    } catch { }
    return @{ ok = $true; name = [string]$current.ElementName; state = [int]$current.EnabledState; width = $width; height = $height; generation = $generation; mouse = $mouseReady; host = $computer; enhancedState = $enhancedState; enhancedHost = $enhancedHost }
}

$script:Keyboard = $null
$script:Mouse = $null
function Get-Keyboard {
    if (-not $script:Keyboard) { $script:Keyboard = Get-FirstCim -Class 'Msvm_Keyboard' -Filter ("SystemName='" + $VMId + "'") }
    if (-not $script:Keyboard) { throw 'The VM has no keyboard device to send keys to.' }
    return $script:Keyboard
}
function Get-Mouse {
    if (-not $script:Mouse) { $script:Mouse = Get-FirstCim -Class 'Msvm_SyntheticMouse' -Filter ("SystemName='" + $VMId + "'") }
    if (-not $script:Mouse) { $script:Mouse = Get-FirstCim -Class 'Msvm_Ps2Mouse' -Filter ("SystemName='" + $VMId + "'") }
    if (-not $script:Mouse) { throw 'The VM has no mouse device Hyper-V can move yet.' }
    return $script:Mouse
}

while ($true) {
    $line = [Console]::In.ReadLine()
    if ($null -eq $line) { break }
    if (-not $line.Trim()) { continue }
    try {
        $request = ConvertFrom-Json -InputObject $line
        switch ([string]$request.op) {
            'info' { Write-Answer (Get-Info) }
            'frame' {
                $settings = Get-Settings
                if (-not $settings) { throw 'The VM settings could not be read.' }
                $w = [uint16]$request.w; $h = [uint16]$request.h
                $result = Invoke-Method -Instance $service -Name 'GetVirtualSystemThumbnailImage' -Arguments @{ TargetSystem = $settings; WidthPixels = $w; HeightPixels = $h }
                $data = [byte[]]$result.ImageData
                if (-not $data -or $data.Length -lt 2) { throw 'Hyper-V sent no picture. The VM may be starting or turned off.' }
                Write-Answer @{ ok = $true; w = [int]$w; h = [int]$h; data = [Convert]::ToBase64String($data) }
            }
            'key' {
                $method = 'ReleaseKey'
                if ($request.down) { $method = 'PressKey' }
                [void](Invoke-Method -Instance (Get-Keyboard) -Name $method -Arguments @{ keyCode = [uint32]$request.vk })
                Write-Answer @{ ok = $true }
            }
            'type' {
                [void](Invoke-Method -Instance (Get-Keyboard) -Name 'TypeText' -Arguments @{ asciiText = [string]$request.text })
                Write-Answer @{ ok = $true }
            }
            'cad' {
                [void](Invoke-Method -Instance (Get-Keyboard) -Name 'TypeCtrlAltDel')
                Write-Answer @{ ok = $true }
            }
            'mouse' {
                $m = Get-Mouse
                try {
                    [void](Invoke-Method -Instance $m -Name 'SetAbsolutePosition' -Arguments @{ horizontalPosition = [int32]$request.x; verticalPosition = [int32]$request.y })
                } catch {
                    # A generation 1 VM whose synthetic mouse driver is not
                    # loaded refuses it; its PS/2 mouse may still answer.
                    $ps2 = Get-FirstCim -Class 'Msvm_Ps2Mouse' -Filter ("SystemName='" + $VMId + "'")
                    if (-not $ps2 -or [string]$m.CimClass.CimClassName -eq 'Msvm_Ps2Mouse') { throw }
                    $m = $ps2
                    $script:Mouse = $ps2
                    [void](Invoke-Method -Instance $m -Name 'SetAbsolutePosition' -Arguments @{ horizontalPosition = [int32]$request.x; verticalPosition = [int32]$request.y })
                }
                foreach ($change in @($request.buttons)) {
                    if ($null -eq $change) { continue }
                    # root\virtualization\v2 names it SetButtonState(buttonIndex, isDown);
                    # there is no SetButton, which is why clicks did nothing.
                    [void](Invoke-Method -Instance $m -Name 'SetButtonState' -Arguments @{ buttonIndex = [uint32]$change.button; isDown = [bool]$change.down })
                }
                if ($request.wheel) {
                    [void](Invoke-Method -Instance $m -Name 'SetScrollPosition' -Arguments @{ scrollPositionDelta = [int32]$request.wheel })
                }
                Write-Answer @{ ok = $true }
            }
            'quit' { Write-Answer @{ ok = $true }; break }
            default { Write-Answer @{ ok = $false; error = 'Unknown request.' } }
        }
        if ([string]$request.op -eq 'quit') { break }
    } catch {
        Write-Answer @{ ok = $false; error = $_.Exception.Message }
    }
}
if ($script:Session) { Remove-CimSession -CimSession $script:Session -ErrorAction SilentlyContinue }
exit 0
