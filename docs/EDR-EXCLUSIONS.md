# Endpoint protection (EDR) and the replication agent

The replication agent reads whole disks, takes Volume Shadow Copies and sends
data over the network, which is close to what ransomware and data theft look
like. This page says what the application runs on each computer, how it is
signed, what endpoint protection may still notice, and which exclusions to add
in SentinelOne, Microsoft Defender for Endpoint and CrowdStrike Falcon.

Add the exclusions **before the first round**. A product that ends the agent
during a round leaves the job "not answering" until the service runs again.

## What changed in 3.2.34

On 2026-11-25 SentinelOne ended and quarantined the 3.2.33 agent on Windows
Server 2022 with "Lateral movement: Shadow Copy Deletion detected"
(classification Ransomware). The 3.2.33 agent took each shadow copy by starting
hidden PowerShell (`-ExecutionPolicy Bypass`) that called WMI
`Win32_ShadowCopy.Create`, and removed it with `Win32_ShadowCopy.Delete()`.
Because the service had been created remotely by push install, SentinelOne
attributed it to the console computer as lateral movement.

From 3.2.34 the agent:

- takes shadow copies through the VSS requester interface
  (`IVssBackupComponents`, `vss_windows.go`), the way backup software does: a
  non-persistent, auto-release backup set of type *copy* (it does not change
  application backup history). Windows removes the set when the round ends or
  the agent process ends for any reason, so the agent **never deletes a shadow
  copy** and none can be left behind;
- starts **no PowerShell** and no WMI command line. It starts only
  `netsh.exe advfirewall` (its own firewall rule, at service start) and
  `shutdown.exe` (only at cutover when "shut down after cutover" is ticked);
- records how its previous run ended. If it was ended from outside during a
  round, the job raises a *stopped* alert and the console shows the reason and
  this page.

A test (`TestAgentCodeStartsNoPowerShellAndDeletesNoShadowCopies`) fails the
build if agent code starts PowerShell or uses `Win32_ShadowCopy`, `vssadmin`,
`DeleteSnapshots`, `wmic` or `-EncodedCommand`.

## Signing

Every release is signed with Azure Artifact Signing and the release fails if
signing does not happen (see `CODE_SIGNING.md`).

| File | Where it runs | Signed |
| --- | --- | --- |
| `Server-Inventory-Configuration-Manager.exe` | Admin computer (console), Hyper-V hosts, migration targets | Authenticode, timestamped |
| `SIMM-Agent.exe` in `C:\Program Files\Server Inventory and Migration Manager Agent\` | Each Windows server being replicated | Same file as the console EXE, copied by push install or `--agent-install`; same signature and SHA-256 as the release |
| `X:\Receiver\` on receiver media | New VM or server booted from the ISO | Same EXE; Windows PE itself comes from your own Windows ADK |
| PowerShell scripts the console writes to disk and runs (restore, file share migration, guest preparation, receiver media, Hyper-V read and copy, disk fix-ups) | Admin computer or the server the console runs on | Authenticode-signed at release (3.2.34 and later) |
| `Export-ServerInventory.ps1`, `Run-Inventory.ps1` (inventory collection) | Computer being inventoried | Not signed: their exact bytes are the protected baseline |
| `simm-agent-linux-amd64` | Linux servers | Linux has no Authenticode; check it against `simm-agent-linux-amd64.sha256.txt` from the release |

Check a file on a server:

```powershell
Get-AuthenticodeSignature "C:\Program Files\Server Inventory and Migration Manager Agent\SIMM-Agent.exe" | Format-List Status, SignerCertificate, TimeStamperCertificate
Get-FileHash "C:\Program Files\Server Inventory and Migration Manager Agent\SIMM-Agent.exe"
```

The SHA-256 must equal the release's `Server-Inventory-Configuration-Manager.exe.sha256.txt`.

## What endpoint protection may still see

| Behaviour | Why | When |
| --- | --- | --- |
| A service created remotely through the service manager and the administrative share | Push install (**Install agents**). Same pattern as PsExec | Once per server, from the console. Avoid it by installing locally: copy the EXE to the server and run `SIMM-Agent.exe --agent-install` as administrator, then add the server in the console with the key and thumbprint it prints |
| Volume Shadow Copy backup sessions (VSS writers freeze and thaw) | Each round | Every round, on every NTFS and ReFS volume of the job's disks |
| Raw reads of `\\.\PhysicalDriveN` and shadow copy devices | Copying disks | Every round |
| `TerminateProcess` of one process of this same application (same executable path or name) that still listens on the receiver's port, after checking its path again | **Close the other copy and start receiving**, when a copy left running without a window holds port 44380 | Only when the operator chooses that button; no other process is ever ended |
| A listening HTTPS port (44390) and large outbound HTTPS transfers to the receiver (44380) | Console commands, and sending disk data | Always, and during rounds |
| `netsh advfirewall firewall add rule name="Server Inventory and Migration Manager replication agent"` | The agent's own inbound rule | Service start |
| `shutdown.exe /s` | "Shut down after cutover" | Cutover only |
| One TCP connection to each address of a pasted receiver block (at most 8, the receiver's own port, nothing sent) | Picking the receiver address that answers from this computer | When receiver connection details with several addresses are pasted |
| A receiver listening on 44380 when the application opens | A receiver left running starts again by itself after a restart or update | Only when it was running when the application closed; **Stop receiving** ends that |

The console itself starts Windows PowerShell with `-ExecutionPolicy Bypass`
(process scope only, never changing machine policy) for inventory collection,
restore, file share migration, guest preparation and Hyper-V steps. These run
only when an operator starts them, on the computer running the console.

### Prepare and fix up from outside the machine

**Prepare and fix up** needs nothing installed in the machine being moved.
It records a VMware VM's network settings from vCenter (what VMware Tools
reports, read-only, no sign-in to the VM), and reads or fixes the machine
from outside with PowerShell's own remoting:

| Behaviour | Where | Why |
| --- | --- | --- |
| `powershell.exe -ExecutionPolicy RemoteSigned -File Invoke-GuestRemote.ps1` (signed script, never Bypass, no hidden-window or encoded-command flags) | The computer running the application (Hyper-V host or admin computer) | Sends the guest engine's text to the machine and brings its result back |
| `Invoke-Command -VMName` (PowerShell Direct over the VM bus) | Hyper-V host, into the new VM | After the cutover: reads the VM and puts back its IP settings, removes VMware Tools and hidden adapters, brings disks online, activates Windows. Needs no network |
| `Invoke-Command -ComputerName` over WinRM (TCP 5985, Kerberos), running in `wsmprovhost.exe` | Admin computer to the VMware VM, or to the remote Hyper-V host that then uses PowerShell Direct | Before the cutover: records settings and saves BitLocker recovery keys |
| `Invoke-Command -VMId` (PowerShell Direct), repeated every 15 seconds for up to 15 minutes with a harmless call until Windows answers | Hyper-V host (or the remote node over WinRM), into the VM the cutover just created | The automatic fix-up after a cutover (no-touch cutover): the same fix-up steps, chosen without prompts. `Get-VMNetworkAdapter` on the host reads the new adapters' MAC addresses |
| `msiexec.exe /x <VMware Tools product code> /qn` | Inside the new VM | Uninstalls VMware Tools. When it refuses outside VMware, Tools' own services and drivers are set not to start (registry `Start` 4, values saved first) and hidden VMware devices are removed through SetupAPI |
| `reg.exe export`, `pnputil.exe -f -d oemNN.inf`, `sc.exe delete <name>` | Inside the new VM | Only after the VMware Tools uninstaller refused: each key is exported to the work folder, then VMware's own driver packages (never storage, network or mouse), Tools' services and drivers by exact name, its Windows Installer and uninstall entries, `HKLM\SOFTWARE\VMware, Inc.` and `Program Files\VMware\VMware Tools` are removed. Files still in use are deleted at the next restart (`MoveFileEx`) |
| `shutdown.exe /r /t 60` | Inside the new VM | Only when removing VMware Tools needs a restart, after the fix-up result has been returned |
| Data files only in `%ProgramData%\Server Inventory and Migration Manager\Guest-Migration` and a per-run folder in `%TEMP%` that is removed | Inside the machine | The recording and the undo data for IP changes. BitLocker keys come back to the application's output folder and are deleted from the machine |

No program is copied into the machine, and no service, scheduled task,
WMI process creation, credential delegation or encoded command is used.
VMware guest operations (running programs through VMware Tools) are
deliberately not used, because endpoint protection flags programs started
by `vmtoolsd.exe`. The accounts reach PowerShell only as one JSON line on
standard input and stay in memory until **Forget the account** or exit.

### Move VMs from one console (remote Hyper-V host or cluster)

When **Move VMs** connects from an admin computer to a Hyper-V host or
failover cluster, nothing is copied to the host as a program, no service is
created and no remote process is started by hand. It uses only the standard
management channels Windows Admin Center and SCVMM use:

| Behaviour | Where | Why |
| --- | --- | --- |
| `powershell.exe -ExecutionPolicy RemoteSigned -File Connect-HyperVTarget.ps1` (signed script, never Bypass, no hidden-window or encoded-command flags) | Admin computer | Reads the host, creates the VM and, for migration plans, makes and removes test VMs on differencing disks, all through PowerShell remoting (Invoke-Command) |
| `Invoke-Command` over WinRM (TCP 5985, Kerberos), running in `wsmprovhost.exe` | Hyper-V host or cluster node | Reads Windows version, Hyper-V default folders, switches, VM names and the cluster's nodes and Cluster Shared Volumes; at cutover runs `New-VM`, `Add-VMHardDiskDrive`, `Add-VMNetworkAdapter` and, when asked, `Add-ClusterVirtualMachineRole` |
| SMB writes of `.vhdx` files and their `.copy.json` records to `\\node\C$\...` (or the drive's `$` share) | Admin computer to node | The copy rounds. Writes only inside the folder chosen for the VM's disks |
| HTTPS to a receiver on the node (44380), only when **Through a receiver on the node** is chosen | Admin computer to node | The operator starts that receiver on the node themselves |

The account typed for the host reaches PowerShell only as one JSON line on
standard input and is kept in memory until **Forget** or exit. If your EDR
watches remote PowerShell, allow `wsmprovhost.exe` running the Hyper-V and
FailoverClusters cmdlets above for the admin computers you migrate from.

### Build Hyper-V hosts

**Build Hyper-V hosts** sets up new Hyper-V nodes and failover clusters from
an admin computer. Like Move VMs from one console, it copies no program to
the nodes, creates no service or scheduled task and starts no remote process
by hand. Every change is previewed and approved first.

| Behaviour | Where | Why |
| --- | --- | --- |
| `powershell.exe -ExecutionPolicy RemoteSigned -File Build-HyperVHost.ps1` (signed script, never Bypass, no hidden-window or encoded-command flags) | Admin computer | Reads the nodes and runs each approved step |
| `Invoke-Command` over WinRM (TCP 5985, Kerberos), running in `wsmprovhost.exe` | Each new node | `Install-WindowsFeature` (Hyper-V, Failover Clustering, Multipath I/O), `New-VMSwitch`, `Set-VMSwitchTeam`, `New-NetLbfoTeam` (Windows Server 2012 R2), `Add-VMNetworkAdapter -ManagementOS`, `Set-VMNetworkAdapterVlan`, `New-NetIPAddress`, `Set-NetAdapterAdvancedProperty` (jumbo frames), the iSCSI initiator service, `New-IscsiTargetPortal`, `Connect-IscsiTarget`, MPIO claims, `Initialize-Disk`, `New-Partition` and `Format-Volume` on empty disks only, `Set-VMHost`, `Enable-VMMigration` |
| `Restart-Computer` from the admin computer | Each new node | Only the approved Restart nodes step, for nodes waiting on a restart after installing roles |
| FailoverClusters cmdlets (`Test-Cluster`, `New-Cluster`, `Add-ClusterNode`, `Add-ClusterDisk`, `Add-ClusterSharedVolume`, `Set-ClusterQuorum`, cluster network settings) over RPC with the signed-in account | Admin computer to nodes | The cluster steps run on the admin computer, never in a remote session, so no credential is passed on between computers (no CredSSP) |

The nodes' account and a cloud witness key reach PowerShell only as one JSON
line on standard input and are kept in memory until **Use my Windows
account** or exit. If your EDR watches remote PowerShell, allow
`wsmprovhost.exe` running the cmdlets above on new Hyper-V nodes while they
are being built.

### Migration plans (on a Hyper-V host)

A **Migration plan** runs inside the console on the Hyper-V host and does
the same work as Move VMs, on a schedule, for many VMs. It adds no agent, no
service and no program on the source VMs or hosts.

| Behaviour | Where | Why |
| --- | --- | --- |
| HTTPS to vCenter or ESXi (443): one snapshot named "Server Inventory and Migration Manager replication" per VM per round, created and removed again, plus Range reads of the VMDK files | Hyper-V host to vCenter/ESXi | Copy rounds every interval the operator chose |
| VHDX writes under the plan's copy folder (or the host's virtual hard disk folder) | Hyper-V host | The copied disks |
| `powershell.exe` with `Import-Module Hyper-V`, `New-VHD -Differencing`, `New-VM`, `Remove-VM` of the test VM by its ID | Hyper-V host | Test cutover and End test, only when the operator chooses them |
| `New-VM`, `Add-VMHardDiskDrive`, `Add-VMNetworkAdapter`, optionally `Add-ClusterVirtualMachineRole` | Hyper-V host | Cutover, now or at the time the operator scheduled |
| A vSphere reconfigure of the old VM's network adapters (not connected), with a JSON record saved first | vCenter/ESXi | Only when **After cutover, set the old VMware VM's adapters to not connect** is ticked |

## Exclusions

Use the narrowest form your product allows. Prefer the signer (certificate)
where supported, because it survives upgrades. Otherwise use the path and
process, and add the hash of each new release.

Paths:

- Agent executable: `C:\Program Files\Server Inventory and Migration Manager Agent\SIMM-Agent.exe`
- Agent data (job records, logs, change tracking): `C:\ProgramData\Server Inventory and Migration Manager Agent\`
- Agent service name: `SIMMReplicationAgent`
- Console executable: wherever `Server-Inventory-Configuration-Manager.exe` is kept on the admin computer
- Console data: `%LOCALAPPDATA%\Server Inventory Configuration Manager\`
- Receiver folder on a Hyper-V host: the folder chosen in **Disk copy**

Signer: the subject shown by `Get-AuthenticodeSignature` for the release EXE
(the Azure Artifact Signing certificate).

### SentinelOne

1. Threats: open the detection, choose **Mark as threat: False positive**, then
   restore `SIMM-Agent.exe` from quarantine (or reinstall the agent).
2. Sentinels, Exclusions, New exclusion:
   - Type **Path**, `C:\Program Files\Server Inventory and Migration Manager Agent\SIMM-Agent.exe`,
     mode **Interoperability - Extended** (covers the process and its child
     processes). Apply to the server group being migrated.
   - Type **Path**, folder `C:\ProgramData\Server Inventory and Migration Manager Agent\`,
     **Suppress alerts** (or Interoperability).
   - If your policy uses certificate exclusions: type **Certificate**, the
     signer above.
3. On the console computer, add the console EXE the same way if push install
   itself is blocked.
4. Start the agent: `Start-Service SIMMReplicationAgent`. The job resumes where
   it stopped.

### Microsoft Defender for Endpoint / Microsoft Defender Antivirus

- Indicators, Certificates: **Allow** the signer's certificate (exported from
  the EXE's Digital Signatures tab), or Indicators, File hashes: **Allow** the
  release SHA-256.
- Antivirus exclusions (Intune, Group Policy or
  `Add-MpPreference -ExclusionProcess "C:\Program Files\Server Inventory and Migration Manager Agent\SIMM-Agent.exe"`
  and `-ExclusionPath "C:\ProgramData\Server Inventory and Migration Manager Agent"`).
- Attack surface reduction: if "Block process creations originating from
  PSExec and WMI commands" is in Block mode, push install's remote service
  creation may be blocked. Install locally, or add the console EXE as an ASR
  exclusion. "Use advanced protection against ransomware" may also need the
  agent EXE as an exclusion.

### CrowdStrike Falcon

- Endpoint security, Exclusions: a **Machine learning exclusion** for
  `C:\Program Files\Server Inventory and Migration Manager Agent\SIMM-Agent.exe`
  (and the console EXE on the admin computer).
- An **Indicator of attack (IOA) exclusion** for the agent's image file name
  `\Device\HarddiskVolume*\Program Files\Server Inventory and Migration Manager Agent\SIMM-Agent.exe`
  if a behaviour detection names it (for example volume shadow copy or raw
  disk access).
- Or a **Sensor visibility exclusion** for the agent data folder if your
  policy allows.

## Shadow copies left by 3.2.33

The 3.2.33 agent took persistent shadow copies. If it was ended during a round
one or two may remain on the server. They were not recorded, so the agent does
not remove them. List them with `vssadmin list shadows`: ours have type
**ClientAccessible** and the time of the interrupted round. Windows' own
Previous Versions copies have type **ClientAccessibleWriters**; leave those.
Remove ours one at a time with `vssadmin delete shadows /shadow={ID}`
(security software may ask you to confirm this on the console).

## When the agent stops answering

- The console's Replication tab says **Not answering** with the reason. *Actively
  refused* means nothing listens on port 44390: the service is not running.
- On the server: `Get-Service SIMMReplicationAgent`; Windows' System log
  (Service Control Manager events 7031 and 7034 name a crash); the Application
  log, source *SIMM Replication Agent* (1001 RPO, 1002 failing, 1003 cutover,
  1004 a round cut short because the agent was ended or crashed); and
  `C:\ProgramData\Server Inventory and Migration Manager Agent\Logs\` (one log per
  job, `agent-crash.log` for crashes, `agent-errors.log` for internal errors
  the agent caught).
- From 3.2.34 Windows restarts the service after every failure (1, 2, then
  every 5 minutes) and the agent reports how its previous run ended.
