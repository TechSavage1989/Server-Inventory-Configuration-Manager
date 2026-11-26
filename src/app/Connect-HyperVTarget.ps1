param(
    [Parameter(Mandatory = $true)][string]$ComputerName,
    [Parameter(Mandatory = $true)][ValidateSet('Read', 'Create')][string]$Mode,
    [Parameter(Mandatory = $true)][string]$ResultPath,
    [string]$ScriptPath = '',
    [switch]$CredentialFromInput
)

# Talks to a Hyper-V host or failover cluster that is not this computer, for
# Move VMs run from an admin computer. It uses only PowerShell remoting
# (Invoke-Command over WinRM), the standard management channel that Windows
# Admin Center and SCVMM use: nothing is copied to, installed on or started as
# a service on the host.
#   Read   reads the host: Windows version, Hyper-V default folders, virtual
#          switches, VM names in use, and on a cluster its nodes and Cluster
#          Shared Volumes with their free space. It changes nothing.
#   Create runs the VM creation script written by the application (the file
#          at -ScriptPath, generated from vmHyperVPlan.Script) on the host.
#          That script refuses an existing VM name and never deletes anything.
# With -CredentialFromInput the account arrives as one JSON line on standard
# input, never on a command line, and is used only for this run. The result
# is written to -ResultPath as {"ok":..,"error":..,"result":..}.
# Compatible with Windows PowerShell 3.0 and later.

$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }

function Write-Line { param([string]$Text) [Console]::Out.WriteLine($Text); [Console]::Out.Flush() }
function Write-Log { param([string]$Message) Write-Line ('{0} {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message) }
function Write-Result {
    param([bool]$Ok, [string]$ErrorText, $Result)
    $json = ConvertTo-Json -InputObject @{ ok = $Ok; error = $ErrorText; result = $Result } -Depth 8 -Compress
    [IO.File]::WriteAllText($ResultPath, $json, (New-Object System.Text.UTF8Encoding($false)))
}

$readHost = {
    $ErrorActionPreference = 'Stop'
    $warnings = New-Object System.Collections.ArrayList
    $os = Get-WmiObject -Class Win32_OperatingSystem
    $system = Get-WmiObject -Class Win32_ComputerSystem
    $result = @{
        computer = [string]$env:COMPUTERNAME; domain = [string]$system.Domain
        caption = [string]$os.Caption; version = [string]$os.Version; build = [int]$os.BuildNumber
        systemDrive = [string]$os.SystemDrive
        hyperV = $false; hyperVModule = $false; clusterModule = $false
        vhdPath = ''; vmPath = ''; cluster = ''
        nodes = @(); volumes = @(); switches = @(); vms = @(); warnings = @()
    }
    if (Get-Service -Name vmms -ErrorAction SilentlyContinue) { $result.hyperV = $true }
    if ($result.hyperV -and (Get-Module -ListAvailable -Name Hyper-V)) {
        $result.hyperVModule = $true
        Import-Module Hyper-V
        $vmHost = Get-VMHost
        $result.vhdPath = [string]$vmHost.VirtualHardDiskPath
        $result.vmPath = [string]$vmHost.VirtualMachinePath
        $result.switches = @(Get-VMSwitch | ForEach-Object { @{ name = [string]$_.Name; type = [string]$_.SwitchType } })
        $result.vms = @(Get-VM | ForEach-Object { [string]$_.Name })
    }
    $volumes = New-Object System.Collections.ArrayList
    foreach ($volume in @(Get-WmiObject -Class Win32_Volume -Filter 'DriveType = 3')) {
        if ($volume.DriveLetter) {
            [void]$volumes.Add(@{ path = ([string]$volume.DriveLetter + '\'); label = [string]$volume.Label; freeBytes = [uint64]$volume.FreeSpace; totalBytes = [uint64]$volume.Capacity; csv = $false })
        }
    }
    $clusterService = Get-Service -Name ClusSvc -ErrorAction SilentlyContinue
    if ($clusterService -and $clusterService.Status -eq 'Running') {
        if (Get-Module -ListAvailable -Name FailoverClusters) {
            try {
                Import-Module FailoverClusters
                $result.clusterModule = $true
                $result.cluster = [string](Get-Cluster).Name
                $result.nodes = @(Get-ClusterNode | ForEach-Object { @{ name = [string]$_.Name; state = [string]$_.State } })
                # A clustered VM can run on any node, so offer only the
                # switches every node has. Reading another node from this
                # session can be refused (no CredSSP is used); then the list
                # stays this node's and a warning says so.
                if ($result.hyperVModule) {
                    $common = @{}
                    foreach ($sw in @($result.switches)) { $common[$sw.name.ToLowerInvariant()] = $true }
                    $unread = @()
                    foreach ($node in @(Get-ClusterNode | Where-Object { $_.State -eq 'Up' -and [string]$_.Name -ne [string]$env:COMPUTERNAME })) {
                        try {
                            $there = @{}
                            foreach ($other in @(Get-VMSwitch -ComputerName ([string]$node.Name) -ErrorAction Stop)) { $there[([string]$other.Name).ToLowerInvariant()] = $true }
                            foreach ($key in @($common.Keys)) { if (-not $there.ContainsKey($key)) { $common.Remove($key) } }
                        } catch {
                            $unread += [string]$node.Name
                        }
                    }
                    $result.switches = @(@($result.switches) | Where-Object { $common.ContainsKey($_.name.ToLowerInvariant()) })
                    if ($unread.Count) {
                        [void]$warnings.Add('The switches of ' + ($unread -join ', ') + ' could not be read from ' + $env:COMPUTERNAME + ', so the switch list is this node''s. Check that each switch you choose exists with the same name on every node.')
                    }
                }
                foreach ($csv in @(Get-ClusterSharedVolume)) {
                    foreach ($info in @($csv.SharedVolumeInfo)) {
                        [void]$volumes.Add(@{ path = [string]$info.FriendlyVolumeName; label = [string]$csv.Name; freeBytes = [uint64]$info.Partition.FreeSpace; totalBytes = [uint64]$info.Partition.Size; csv = $true })
                    }
                }
                $names = @($result.vms)
                foreach ($resource in @(Get-ClusterResource | Where-Object { $_.ResourceType -like 'Virtual Machine' })) {
                    $names += [string]$resource.OwnerGroup
                }
                $result.vms = $names
            } catch {
                [void]$warnings.Add('The failover cluster could not be read: ' + $_.Exception.Message)
            }
        } else {
            $result.cluster = 'unknown'
            [void]$warnings.Add('This host is in a failover cluster but the Failover Cluster Module for Windows PowerShell is not installed on it, so its Cluster Shared Volumes cannot be listed.')
        }
    }
    $result.volumes = @($volumes)
    $result.warnings = @($warnings)
    ConvertTo-Json -InputObject $result -Depth 6 -Compress
}

try {
    $invoke = @{ ComputerName = $ComputerName; ErrorAction = 'Stop' }
    if ($CredentialFromInput) {
        $line = [Console]::In.ReadLine()
        if (-not $line) { throw 'No account was given on standard input.' }
        $account = ConvertFrom-Json -InputObject $line
        $line = $null
        $secure = New-Object System.Security.SecureString
        foreach ($character in ([string]$account.password).ToCharArray()) { $secure.AppendChar($character) }
        $secure.MakeReadOnly()
        $invoke.Credential = New-Object System.Management.Automation.PSCredential([string]$account.user, $secure)
        $account = $null
    }
    if ($Mode -eq 'Read') {
        Write-Log ('Reading Hyper-V host ' + $ComputerName + ' through PowerShell remoting.')
        $text = Invoke-Command @invoke -ScriptBlock $readHost
        $parsed = ConvertFrom-Json -InputObject ([string]($text | Select-Object -Last 1))
        Write-Result $true '' $parsed
    } else {
        if (-not $ScriptPath -or -not (Test-Path -LiteralPath $ScriptPath -PathType Leaf)) { throw 'The VM creation script is missing.' }
        $body = [IO.File]::ReadAllText($ScriptPath)
        Write-Log ('Creating the VM on ' + $ComputerName + ' through PowerShell remoting.')
        $lines = @(Invoke-Command @invoke -ScriptBlock ([scriptblock]::Create($body)) | ForEach-Object { [string]$_ })
        Write-Result $true '' @{ lines = $lines }
    }
} catch {
    Write-Result $false $_.Exception.Message $null
    exit 1
}
