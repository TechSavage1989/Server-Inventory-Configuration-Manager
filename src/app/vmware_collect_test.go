package main

import (
	"context"
	"crypto/tls"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
)

// The simulator (vcsim) is VMware's own vCenter model, so these tests drive
// the real property collector code paths without a real vCenter.
func TestCollectVSphereReadsSimulatedVCenter(t *testing.T) {
	model := simulator.VPX()
	model.Datacenter = 1
	model.Cluster = 1
	model.Host = 1
	model.Machine = 2
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	user := server.URL.User.Username()
	password, _ := server.URL.User.Password()
	inventory, err := collectVSphere(context.Background(), vmConnectOptions{Server: server.URL.Host, User: user, Password: password, insecureForTest: true, Timeout: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.ServerType != "vCenter Server" {
		t.Fatalf("server type %q", inventory.ServerType)
	}
	if len(inventory.Hosts) < 2 || len(inventory.Clusters) != 1 || len(inventory.Datastores) == 0 || len(inventory.Networks) == 0 {
		t.Fatalf("unexpected counts: hosts=%d clusters=%d datastores=%d networks=%d", len(inventory.Hosts), len(inventory.Clusters), len(inventory.Datastores), len(inventory.Networks))
	}
	if len(inventory.VMs) == 0 {
		t.Fatal("no VMs read")
	}
	for _, vm := range inventory.VMs {
		if vm.Name == "" || vm.Datacenter == "" || vm.CPUs == 0 || len(vm.Disks) == 0 || len(vm.NICs) == 0 {
			t.Fatalf("incomplete VM: %+v", vm)
		}
		if vm.Disks[0].Controller == "" || vm.Disks[0].Datastore == "" || vm.Disks[0].CapacityBytes == 0 {
			t.Fatalf("incomplete disk: %+v", vm.Disks[0])
		}
		if vm.NICs[0].Network == "" || vm.NICs[0].MAC == "" {
			t.Fatalf("incomplete NIC: %+v", vm.NICs[0])
		}
		if len(vm.Readiness) != 2 || vm.readinessFor(vmTargetHyperV) == nil || vm.readinessFor(vmTargetScale) == nil {
			t.Fatalf("readiness missing for %s", vm.Name)
		}
	}
}

func TestCollectVSphereReadsSimulatedESXi(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	password, _ := server.URL.User.Password()
	inventory, err := collectVSphere(context.Background(), vmConnectOptions{Server: server.URL.Host, User: server.URL.User.Username(), Password: password, insecureForTest: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.ServerType != "ESXi host" || len(inventory.Hosts) != 1 || len(inventory.VMs) == 0 {
		t.Fatalf("unexpected ESXi inventory: %s hosts=%d vms=%d", inventory.ServerType, len(inventory.Hosts), len(inventory.VMs))
	}
}

func TestCollectVSphereRejectsWrongPassword(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	model.Service.Listen = &url.URL{User: url.UserPassword("administrator", "right-password")}
	server := model.Service.NewServer()
	defer server.Close()
	_, err := collectVSphere(context.Background(), vmConnectOptions{Server: server.URL.Host, User: "administrator", Password: "wrong", insecureForTest: true}, nil)
	if err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if err.Error() != "vCenter or ESXi refused the user name or password." {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("the error repeats the password: %v", err)
	}
}

// Without the test switch, a self-signed certificate is refused and its
// thumbprint returned; the confirmed thumbprint is then accepted, and a
// different one is refused as a change.
func TestCollectVSphereRequiresConfirmedThumbprint(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	password, _ := server.URL.User.Password()
	options := vmConnectOptions{Server: server.URL.Host, User: server.URL.User.Username(), Password: password}
	_, err := collectVSphere(context.Background(), options, nil)
	certificate, ok := err.(*vmCertificateError)
	if !ok {
		t.Fatalf("expected a certificate error, got %v", err)
	}
	if vmNormalizeThumbprint(certificate.Thumbprint) == "" || certificate.Mismatch {
		t.Fatalf("bad certificate details: %+v", certificate)
	}
	options.Thumbprint = strings.ToLower(strings.ReplaceAll(certificate.Thumbprint, ":", ""))
	if _, err := collectVSphere(context.Background(), options, nil); err != nil {
		t.Fatalf("confirmed thumbprint refused: %v", err)
	}
	options.Thumbprint = strings.Repeat("AB", 32)
	_, err = collectVSphere(context.Background(), options, nil)
	if certificate, ok := err.(*vmCertificateError); !ok || !certificate.Mismatch {
		t.Fatalf("a different thumbprint was not refused as a change: %v", err)
	}
}

func TestVMServerURLRejectsOtherSchemes(t *testing.T) {
	for _, value := range []string{"http://vc.example.com", "ftp://x", "vc example", "", "a/b"} {
		if _, err := vmServerURL(value); err == nil {
			t.Fatalf("%q accepted", value)
		}
	}
	for value, host := range map[string]string{"vc.example.com": "vc.example.com", "https://vc.example.com/ui": "vc.example.com", "10.0.0.5:8443": "10.0.0.5:8443", "[2001:db8::5]": "[2001:db8::5]"} {
		parsed, err := vmServerURL(value)
		if err != nil || parsed.Host != host || parsed.Path != "/sdk" {
			t.Fatalf("%q -> %v %v", value, parsed, err)
		}
	}
}
