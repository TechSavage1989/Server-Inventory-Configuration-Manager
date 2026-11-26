# New migration wizard

Owner feedback (2026-11-26): "there are so many options that can be chosen and you have to jump around the console to multiple areas to make selections and it's not intuitive", and "choosing any particular migration or replication or restore type should ultimately expose the specific configurations and options and features for that type and exclude others".

Examples from that night: the Receive panel needed for replication, the destination folder set in another tool, Prepare on VMware separate from Move, thumbprint and pairing fields, waves in Plan and runbook versus Migration plans, and a guide that links out to other tools.

## Goal

One **New migration** tool, the first thing in Migration and replication, that takes an operator from "what am I moving" to a running, monitored migration on one sequence of screens. Every value has a default, so for the common case the operator only picks VMs, presses Next, types the plan name and presses Start. Only the options for the chosen way of moving are shown, and advanced options stay folded away. The separate tools stay where they are, listed under **Tools (advanced)**.

## Screens

A step bar across the top (1 What, 2 Where, 3 How, 4 When, 5 Review, 6 Status). Back and Next are at the bottom. A step that cannot go on says why next to Next, and gives a fix inside the step (for example, the vCenter password field), never a link to another tool.

1. **What**
   - Choose the kind of source: VMs on VMware or Hyper-V, Windows or Linux servers (any hardware), file shares, or disk image files.
   - For VMs, the inventory read in Discovery is used. If none is open, or the vCenter password is not held in this session, the sign-in fields are shown right here.
   - A VM list with search, **Select by** (host, cluster, folder, wave, readiness) and readiness badges. Blocked and system VMs cannot be ticked, and the list says why. Many VMs at once.
2. **Where**
   - Target choices that make sense for the source: this Hyper-V host or its cluster, another Hyper-V host or cluster, HyperCore, Nutanix AHV, Proxmox VE, a physical server.
   - For this Hyper-V host, the disk and VM folders are filled in from the host's defaults, or a Cluster Shared Volume on a cluster (with **High availability** ticked).
   - Free space is checked against the chosen VMs.
   - The network map shows only the networks the chosen VMs use. Each has a switch (pre-filled when a switch has the same name or the host has only one external switch) and a VLAN (pre-filled from the source). The map is saved into the plan, so Move VMs, Migration plans and the runbook all use it.
3. **How**
   - The recommended way of moving, worked out from the source, the target and where this application runs, with its **Pick this when** text from `migrationModes`.
   - Other compatible ways are shown as alternatives. Choosing one shows only that way's options; for a Migration plan that is the sync interval, VMs copied at once, bandwidth limits, application-consistent copies, keeping MAC addresses, starting the new VMs and disconnecting the old adapters, folded under **Advanced**.
4. **When**
   - Start the first copies now or at a time you choose.
   - Waves: everything in one wave, the waves already in the plan, or **Suggest waves**. Each VM's wave can be changed in a small table.
   - Each wave has a cutover choice: **When I press Cut over** (the default, after a test) or a date and time.
5. **Review**
   - One summary of every choice, plus checks that block Start: this computer is a Hyper-V host, the vCenter session is live, there is enough space, every network is mapped, and no VM is in another plan.
   - VMware snapshots: VMs with snapshots are listed with **Remove snapshots on N VMs** (typed number of VMs, the existing batch Prepare), because copies refuse a VM with snapshots.
   - Type the plan's name and press **Start**. That saves the network map and waves, creates the migration plan and schedules any timed cutovers, all with that one confirmation.
6. **Status**
   - The live view of what was started: clickable tiles (all, copying, ready, testing, scheduled, done, failed) that filter the list, and a progress bar, speed and time left per VM.
   - Per VM: Test (an isolated NAME-TEST VM), End test, Cut over.
   - Per wave: Cut over the wave now, or at a time.
   - **Open in All servers** opens the shared console, filtered to this plan, where every job of every type is monitored together.

## Phase 1 (this change)

- VMs from VMware or Hyper-V to **this Hyper-V host or its failover cluster** run end to end in the wizard, through the existing Migration plans engine (`movebatch.go`). The wizard adds no new vSphere or Hyper-V change; every rule of Move VMs and Migration plans still applies.
- Every other source and target works through steps 1 to 3: the wizard picks the right way, explains why, and **Continue in** opens that tool with the selection carried over. Each of these is labelled as a hand-off, so it is not confused with the end-to-end path.

## Phase 2

- Done (step 1): run a batch to a remote Hyper-V host or cluster from an admin computer. Where connects to the host inline and the migration plan gets `remoteHost` and `node`; every round, VM creation and test uses rule 39's remoting and administrative-share path.
- Done (step 2): Windows and Linux servers to this Hyper-V host. What lists the agents the replication console knows and installs the agent on new Windows servers inline; Where takes only the copy folder (this computer's receiver is started and paired for the jobs); Start creates one job per server through All jobs and servers' bulk `createjob` with `local:true` after the number of servers is typed; Status shows those servers with the console's own actions. Other targets for servers still hand off to Replicate servers.
- Done (step 3): HyperCore, Nutanix and Proxmox. Where connects to the platform inline, and How lists every chosen VM with where it stands and opens its Move panel. Each new VM's receiver shows its own pairing code on its console and the boot-media receiver takes no pairing requests, so these moves are paired one VM at a time by design; the wizard does not batch them.

## Rules

- No new endpoint. The wizard uses only `/api/vmware/connect`, `/api/vmware/plan`, `/api/vmware/prepare`, `/api/plans` and the state.
- The plan name typed in Review is the confirmation for creating the plan and for every timed cutover sent with it. Cut over now in Status asks for the plan name again.
- Ids start with `nw`. The tool is `data-tool="wizard"` with the pane `#tabWizard`. It is the Migration tab's default tool.

## Guided migration folded in (2026-11-26)

The separate Guided migration tool was removed. Its step list now appears in How for every way that continues in its own tool: each step with the computer it runs on, a live check (blocking the wrong computer or too little space), a fix inside the step and an Open button. What shows this computer's facts. A way that runs end to end in the wizard needs no step list.
