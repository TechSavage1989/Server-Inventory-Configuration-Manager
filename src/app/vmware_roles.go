package main

// Where each part of a move runs. A move involves up to three computers: the
// one running this application to drive the move, the machine being moved and
// the new host or machine. The runbook (HTML and Word) opens with this table so
// nobody installs the application on the wrong computer. The VMware tab's
// "Which computer runs what" panel (vmRoleRows in index.html) shows the same
// roles; keep the two matching. No Windows dependency.

type vmMoveRole struct {
	// Where names the computer.
	Where string
	// Does lists what runs there, in order.
	Does string
	// Needs lists what that computer needs.
	Needs string
}

func vmMoveRoles(target string) []vmMoveRole {
	profile, _ := vmTargetProfileFor(target)
	short := vmNonEmpty(profile.Short, "the target")
	source := vmMoveRole{
		Where: "The VMware VM being moved",
		Does:  "Nothing has to be installed or run in it. Its IP settings are recorded from vCenter before each copy. With BitLocker on, save its recovery keys from your admin computer over PowerShell remoting (Migration and replication, Prepare and fix up) before the move.",
		Needs: "VMware Tools running, so the guest can be quiesced and shut down cleanly.",
	}
	after := vmMoveRole{
		Where: "The new " + vmTargetMachine(target) + ", after it starts",
		Does:  "Nothing has to be installed in it either: Prepare and fix up (Migration and replication) reaches it from the Hyper-V host through PowerShell Direct, or over the network with PowerShell remoting, to put back the IP settings, remove VMware Tools and hidden old adapters and bring disks online. Running this application inside it still works as a fallback. Then collect again and compare (Compare before and after).",
		Needs: "Its network and storage drivers, installed before the move.",
	}
	switch target {
	case vmTargetHyperV:
		return []vmMoveRole{
			{Where: "The Hyper-V host that will run the VM", Does: "Run this application here as administrator: read vCenter or ESXi (Discovery), plan, then Copy disks and Cut over in Move VMs. The disks are written here as VHDX files and the Hyper-V VM is created here. Or run it on your admin computer and connect to the Hyper-V host or failover cluster from Move VMs (PowerShell remoting): the disks are then written onto the chosen node and the VM is created there, highly available if asked.", Needs: "The Hyper-V role and its PowerShell module, HTTPS access to vCenter and the ESXi hosts, and room for the disks. From an admin computer instead: PowerShell remoting (WinRM) to the host and an account that is an administrator on it."},
			source,
			after,
		}
	case vmTargetScale, vmTargetNutanix, vmTargetProxmox, vmTargetPhysical:
		console := vmMoveRole{
			Where: "Your admin computer or jump box (Windows)",
			Does:  "Run this application here: read vCenter or ESXi (Discovery), plan, build the receiver media, then (in Move VMs) " + map[bool]string{true: "connect to " + short + ", upload the media, create the VM, ", false: ""}[target != vmTargetPhysical] + "check the receiver, Copy disks and Cut over. The disks pass through this computer on their way to the receiver.",
			Needs: "The Windows ADK and its Windows PE add-on (for the receiver media), HTTPS access to vCenter, the ESXi hosts" + map[bool]string{true: ", " + short, false: ""}[target != vmTargetPhysical] + " and the receiver, and a fast link to both.",
		}
		receiver := vmMoveRole{
			Where: "The new VM on " + short + ", while it boots the receiver media",
			Does:  "Nothing to install or type: it starts from the receiver ISO, shows its address, pairing code and certificate thumbprint on its console and writes each disk onto its own blank disk.",
			Needs: "Disks at least as large as the VMware disks (created for you) and a network that reaches your admin computer.",
		}
		if target == vmTargetPhysical {
			receiver.Where = "The new physical server, while it boots the receiver media"
			receiver.Does = "Boot it from the receiver ISO (USB stick or virtual media). It shows its address, pairing code and certificate thumbprint on its console and writes each disk onto a blank disk."
			receiver.Needs = "Blank disks at least as large as the VMware disks, and the storage and network drivers you added to the media."
		}
		return []vmMoveRole{console, source, receiver, after}
	default:
		return []vmMoveRole{
			{Where: "Your admin computer", Does: "Run this application here to read vCenter or ESXi and plan. Copy the VM with " + vmNonEmpty(profile.Tool, "the platform's migration tool") + ".", Needs: "HTTPS access to vCenter."},
			source,
			after,
		}
	}
}

func vmTargetMachine(target string) string {
	switch target {
	case vmTargetPhysical:
		return "server"
	case vmTargetAWS:
		return "instance"
	}
	return "VM"
}
