package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Before and after comparison of two inventory packages of the same server,
// typically taken before and after a move to a new hypervisor. It reads the
// CSV files the collector already writes and reports what was added, removed
// or changed, separating changes a migration is expected to cause (VMware
// Tools removed, new virtual hardware) from ones that need attention.

const (
	compareMaxRows        = 200000
	compareMaxChanges     = 2000
	compareMaxFileBytes   = 256 << 20
	compareReportName     = "Before-After-Comparison.html"
	compareCSVName        = "Before-After-Changes.csv"
	compareSummaryCSVPath = "00-Summary/Server-Summary.csv"
)

var compareExpectedText = regexp.MustCompile(`(?i)vmware|vmtools|vgauth|vm3d|vmci|vmhgfs|vmmemctl|vmrawdsk|vmusb|vmvss|vmxnet|pvscsi|vsock|virtio|viostor|vioscsi|netkvm|balloon|qemu|scale computing|hyper-v|hyperv|^vmic|integration services|nutanix|ngtsvc|amazon|ec2launch|ec2config|awsnvme|^ena$|elastic network adapter|xenbus|xenvif|aws pv|windowsazure|windows azure|rdagent|azure vm agent|guest agent`)

type compareDataset struct {
	Key            string
	Label          string
	File           string
	KeyColumns     []string
	Display        []string
	Compare        []string
	ExpectedFields map[string]bool
	Filter         func(row map[string]string) bool
	Explode        func(row map[string]string) []map[string]string
	ImportantGone  bool
	Important      func(before, after map[string]string) bool
	Normalize      map[string]func(string) string
}

type compareField struct {
	Name   string `json:"name"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type compareChange struct {
	Dataset   string         `json:"dataset"`
	Label     string         `json:"label"`
	Kind      string         `json:"kind"`
	Item      string         `json:"item"`
	Fields    []compareField `json:"fields"`
	Expected  bool           `json:"expected"`
	Important bool           `json:"important"`
}

type compareDatasetResult struct {
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Available   bool            `json:"available"`
	Note        string          `json:"note"`
	BeforeCount int             `json:"beforeCount"`
	AfterCount  int             `json:"afterCount"`
	Added       int             `json:"added"`
	Removed     int             `json:"removed"`
	Changed     int             `json:"changed"`
	Expected    int             `json:"expected"`
	Changes     []compareChange `json:"-"`
}

type comparePackage struct {
	Path         string `json:"path"`
	Computer     string `json:"computer"`
	Domain       string `json:"domain"`
	CollectedAt  string `json:"collectedAt"`
	OS           string `json:"os"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	files        map[string][]byte
	root         string
	zip          *zip.ReadCloser
	zipPrefix    string
}

type compareResult struct {
	Before         comparePackage         `json:"before"`
	After          comparePackage         `json:"after"`
	SameComputer   bool                   `json:"sameComputer"`
	Datasets       []compareDatasetResult `json:"datasets"`
	Attention      []compareChange        `json:"attention"`
	ExpectedCount  int                    `json:"expectedCount"`
	ChangeCount    int                    `json:"changeCount"`
	AttentionCount int                    `json:"attentionCount"`
	Truncated      bool                   `json:"truncated"`
	ReportPath     string                 `json:"reportPath"`
	CSVPath        string                 `json:"csvPath"`
	ComparedAt     time.Time              `json:"comparedAt"`
}

func compareLowPort(row map[string]string) bool {
	port, err := strconv.Atoi(strings.TrimSpace(row["LocalPort"]))
	return err == nil && port > 0 && port < 49152
}

func compareRoutableAddress(address string) bool {
	address = strings.ToLower(strings.TrimSpace(address))
	return address != "" && !strings.HasPrefix(address, "127.") && address != "::1" && !strings.HasPrefix(address, "fe80") && !strings.HasPrefix(address, "169.254.") && !strings.HasPrefix(address, "fec0:0:0:ffff") && address != "0.0.0.0" && address != "::"
}

var compareSplitAddresses = regexp.MustCompile(`[\s;,{}]+`)

func compareGB(value string) string {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number <= 0 {
		return strings.TrimSpace(value)
	}
	return strconv.FormatFloat(number/(1<<30), 'f', 0, 64) + " GB"
}

var compareDatasets = []compareDataset{
	{Key: "summary", Label: "Server", File: "00-Summary/Server-Summary.csv", KeyColumns: nil, Display: []string{"ComputerName"}, Compare: []string{"ComputerName", "FQDN", "Domain", "DomainRole", "OperatingSystem", "Version", "TotalMemoryGB", "LogicalProcessorCount", "Manufacturer", "Model"}, ExpectedFields: map[string]bool{"Manufacturer": true, "Model": true}},
	{Key: "services", Label: "Services", File: "05-Software-Services-Tasks/Services.csv", KeyColumns: []string{"Name"}, Display: []string{"DisplayName", "Name"}, Compare: []string{"State", "StartMode", "StartName", "PathName"}, ImportantGone: true,
		Important: func(before, after map[string]string) bool {
			return strings.EqualFold(before["State"], "Running") && !strings.EqualFold(after["State"], "Running") && strings.HasPrefix(strings.ToLower(after["StartMode"]), "auto")
		}},
	{Key: "applications", Label: "Installed applications", File: "05-Software-Services-Tasks/Installed-Applications.csv", KeyColumns: []string{"DisplayName", "Architecture"}, Display: []string{"DisplayName"}, Compare: []string{"DisplayVersion"}, ImportantGone: true},
	{Key: "features", Label: "Windows roles and features", File: "01-System/Installed-Windows-Roles-and-Features.csv", KeyColumns: []string{"Name"}, Display: []string{"DisplayName", "Name"}, ImportantGone: true},
	{Key: "addresses", Label: "IP addresses", File: "04-Network/IP-Addresses.csv", KeyColumns: []string{"IPAddress"}, Display: []string{"IPAddress"}, Compare: []string{"PrefixLength"}, ImportantGone: true,
		Filter: func(row map[string]string) bool { return compareRoutableAddress(row["IPAddress"]) }},
	{Key: "gateways", Label: "Default gateways", File: "04-Network/Routes.csv", KeyColumns: []string{"NextHop"}, Display: []string{"NextHop"}, ImportantGone: true,
		Filter: func(row map[string]string) bool {
			prefix := strings.TrimSpace(row["DestinationPrefix"])
			return (prefix == "0.0.0.0/0" || prefix == "::/0") && compareRoutableAddress(row["NextHop"])
		}},
	{Key: "dns", Label: "DNS servers", File: "04-Network/DNS-Client-Server-Addresses.csv", KeyColumns: []string{"Server"}, Display: []string{"Server"}, ImportantGone: true,
		Explode: func(row map[string]string) []map[string]string {
			rows := []map[string]string{}
			for _, address := range compareSplitAddresses.Split(row["ServerAddresses"], -1) {
				if compareRoutableAddress(address) {
					rows = append(rows, map[string]string{"Server": address})
				}
			}
			return rows
		}},
	{Key: "tcp", Label: "Listening TCP ports", File: "04-Network/TCP-Listening-Endpoints.csv", KeyColumns: []string{"LocalPort", "ProcessName"}, Display: []string{"LocalPort", "ProcessName"}, ImportantGone: true, Filter: compareLowPort},
	{Key: "udp", Label: "Listening UDP ports", File: "04-Network/UDP-Listening-Endpoints.csv", KeyColumns: []string{"LocalPort", "ProcessName"}, Display: []string{"LocalPort", "ProcessName"}, Filter: compareLowPort},
	{Key: "volumes", Label: "Volumes", File: "03-Storage/Volumes-Readable.csv", KeyColumns: []string{"DriveLetter"}, Display: []string{"DriveLetter", "Label"}, Compare: []string{"Label", "FileSystem", "CapacityBytes"}, ImportantGone: true,
		Filter:    func(row map[string]string) bool { return strings.TrimSpace(row["DriveLetter"]) != "" },
		Normalize: map[string]func(string) string{"CapacityBytes": compareGB}},
	{Key: "disks", Label: "Disks", File: "03-Storage/Disks-Readable.csv", KeyColumns: []string{"Number"}, Display: []string{"Number", "FriendlyName"}, Compare: []string{"PartitionStyle", "CapacityBytes", "Status"}, ImportantGone: true,
		Normalize: map[string]func(string) string{"CapacityBytes": compareGB},
		Important: func(before, after map[string]string) bool {
			return !strings.EqualFold(before["Status"], after["Status"]) && strings.Contains(strings.ToLower(after["Status"]), "offline")
		}},
	{Key: "shares", Label: "File shares", File: "07-Shares-Permissions/SMB-Shares.csv", KeyColumns: []string{"Name"}, Display: []string{"Name"}, Compare: []string{"Path"}, ImportantGone: true},
	{Key: "tasks", Label: "Scheduled tasks", File: "05-Software-Services-Tasks/Scheduled-Tasks.csv", KeyColumns: []string{"TaskPath", "TaskName"}, Display: []string{"TaskPath", "TaskName"}, Compare: []string{"State", "PrincipalUser"},
		Filter: func(row map[string]string) bool {
			return !strings.HasPrefix(strings.ToLower(row["TaskPath"]), `\microsoft\`)
		}},
	{Key: "users", Label: "Local users", File: "06-Accounts-Policy-Security/Local-Users.csv", KeyColumns: []string{"Name"}, Display: []string{"Name"}, Compare: []string{"Enabled"}},
	{Key: "groups", Label: "Local group members", File: "06-Accounts-Policy-Security/Local-Group-Memberships.csv", KeyColumns: []string{"GroupName", "MemberName"}, Display: []string{"GroupName", "MemberName"}},
	{Key: "certificates", Label: "Personal certificates", File: "06-Accounts-Policy-Security/Local-Machine-Certificates.csv", KeyColumns: []string{"Thumbprint"}, Display: []string{"Subject", "Thumbprint"}, Compare: []string{"HasPrivateKey"}, ImportantGone: true,
		Filter: func(row map[string]string) bool {
			store := strings.ToLower(row["StorePath"])
			return strings.HasSuffix(store, `\my`) || strings.HasSuffix(store, `\webhosting`) || strings.HasSuffix(store, `\remote desktop`)
		}},
	{Key: "firewall", Label: "Firewall rules", File: "06-Accounts-Policy-Security/Firewall-Rules.csv", KeyColumns: []string{"Name"}, Display: []string{"DisplayName", "Name"}, Compare: []string{"Enabled", "Action", "Direction"}},
	{Key: "websites", Label: "IIS websites", File: "08-Role-Specific/IIS/Websites.csv", KeyColumns: []string{"Name"}, Display: []string{"Name"}, Compare: []string{"State", "PhysicalPath"}, ImportantGone: true},
	{Key: "printers", Label: "Printers", File: "08-Role-Specific/Print-Server/Printers.csv", KeyColumns: []string{"Name"}, Display: []string{"Name"}, Compare: []string{"DriverName", "PortName", "Shared"}, ImportantGone: true},
}

// openComparePackage accepts an inventory package folder (the one that
// contains 00-Summary), a folder that contains exactly one package, or the
// package ZIP.
func openComparePackage(location string) (*comparePackage, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return nil, errors.New("Choose an inventory package.")
	}
	info, err := os.Stat(location)
	if err != nil {
		return nil, fmt.Errorf("%s could not be opened.", location)
	}
	pkg := &comparePackage{Path: location}
	if !info.IsDir() {
		if !strings.EqualFold(filepath.Ext(location), ".zip") {
			return nil, errors.New("Choose an inventory package folder or its ZIP file.")
		}
		archive, err := zip.OpenReader(location)
		if err != nil {
			return nil, fmt.Errorf("%s is not a readable ZIP file.", filepath.Base(location))
		}
		pkg.zip = archive
		found := false
		for _, file := range archive.File {
			name := strings.ReplaceAll(file.Name, `\`, "/")
			if strings.EqualFold(path.Base(name), "Server-Summary.csv") && strings.EqualFold(path.Base(path.Dir(name)), "00-Summary") {
				pkg.zipPrefix = path.Dir(path.Dir(name))
				if pkg.zipPrefix == "." {
					pkg.zipPrefix = ""
				}
				found = true
				break
			}
		}
		if !found {
			archive.Close()
			return nil, errors.New("The ZIP file is not a server inventory package (no 00-Summary\\Server-Summary.csv).")
		}
	} else {
		root := location
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(compareSummaryCSVPath))); err != nil {
			matches := []string{}
			entries, _ := os.ReadDir(root)
			for _, entry := range entries {
				if entry.IsDir() {
					if _, err := os.Stat(filepath.Join(root, entry.Name(), filepath.FromSlash(compareSummaryCSVPath))); err == nil {
						matches = append(matches, filepath.Join(root, entry.Name()))
					}
				}
			}
			if len(matches) != 1 {
				return nil, errors.New("Choose the inventory package folder: the one that contains 00-Summary.")
			}
			root = matches[0]
		}
		pkg.root = root
		pkg.Path = root
	}
	rows, _, err := pkg.readCSV(compareSummaryCSVPath)
	if err != nil || len(rows) == 0 {
		pkg.Close()
		return nil, errors.New("The package's server summary could not be read.")
	}
	summary := rows[0]
	pkg.Computer, pkg.Domain, pkg.CollectedAt, pkg.OS, pkg.Manufacturer, pkg.Model = summary["ComputerName"], summary["Domain"], summary["CollectedAt"], summary["OperatingSystem"], summary["Manufacturer"], summary["Model"]
	return pkg, nil
}

func (pkg *comparePackage) Close() {
	if pkg != nil && pkg.zip != nil {
		pkg.zip.Close()
		pkg.zip = nil
	}
}

func (pkg *comparePackage) readFile(relative string) ([]byte, error) {
	if pkg.zip != nil {
		want := strings.ToLower(path.Join(pkg.zipPrefix, relative))
		for _, file := range pkg.zip.File {
			if strings.ToLower(strings.ReplaceAll(file.Name, `\`, "/")) != want {
				continue
			}
			if file.UncompressedSize64 > compareMaxFileBytes {
				return nil, errors.New("file too large")
			}
			reader, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer reader.Close()
			return io.ReadAll(io.LimitReader(reader, compareMaxFileBytes))
		}
		return nil, os.ErrNotExist
	}
	full := filepath.Join(pkg.root, filepath.FromSlash(relative))
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if info.Size() > compareMaxFileBytes {
		return nil, errors.New("file too large")
	}
	return os.ReadFile(full)
}

// readCSV returns rows as column maps, plus the header.
func (pkg *comparePackage) readCSV(relative string) ([]map[string]string, []string, error) {
	data, err := pkg.readFile(relative)
	if err != nil {
		return nil, nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	reader := csv.NewReader(bytes.NewReader(data))
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, nil, err
	}
	rows := []map[string]string{}
	for len(rows) < compareMaxRows {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		row := map[string]string{}
		for index, name := range header {
			if index < len(record) {
				row[name] = record[index]
			}
		}
		rows = append(rows, row)
	}
	return rows, header, nil
}

type compareRows struct {
	order []string
	rows  map[string]map[string]string
}

func (dataset compareDataset) load(pkg *comparePackage) (*compareRows, string) {
	raw, header, err := pkg.readCSV(dataset.File)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "not collected"
		}
		return nil, "could not be read"
	}
	columns := map[string]bool{}
	for _, name := range header {
		columns[name] = true
	}
	if dataset.Explode == nil {
		for _, name := range dataset.KeyColumns {
			if !columns[name] {
				return nil, "collected in an older format"
			}
		}
	} else if !columns["ServerAddresses"] {
		return nil, "collected in an older format"
	}
	result := &compareRows{rows: map[string]map[string]string{}}
	add := func(row map[string]string) {
		if dataset.Filter != nil && !dataset.Filter(row) {
			return
		}
		parts := []string{}
		for _, name := range dataset.KeyColumns {
			parts = append(parts, strings.ToLower(strings.TrimSpace(row[name])))
		}
		key := strings.Join(parts, "\x1f")
		if _, exists := result.rows[key]; exists {
			return
		}
		result.rows[key] = row
		result.order = append(result.order, key)
	}
	for _, row := range raw {
		if dataset.Explode != nil {
			for _, item := range dataset.Explode(row) {
				add(item)
			}
			continue
		}
		add(row)
	}
	return result, ""
}

func (dataset compareDataset) display(row map[string]string) string {
	parts := []string{}
	for _, name := range dataset.Display {
		if value := strings.TrimSpace(row[name]); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 2 && strings.EqualFold(parts[0], parts[1]) {
		parts = parts[:1]
	}
	if dataset.Key == "tcp" || dataset.Key == "udp" {
		if len(parts) == 2 {
			return "Port " + parts[0] + " (" + parts[1] + ")"
		}
		return "Port " + strings.Join(parts, " ")
	}
	if len(parts) == 2 {
		return parts[0] + " (" + parts[1] + ")"
	}
	return strings.Join(parts, " ")
}

func (dataset compareDataset) value(row map[string]string, name string) string {
	value := strings.TrimSpace(row[name])
	if normalize := dataset.Normalize[name]; normalize != nil {
		return normalize(value)
	}
	return value
}

func compareDatasetRows(dataset compareDataset, before, after *comparePackage) compareDatasetResult {
	result := compareDatasetResult{Key: dataset.Key, Label: dataset.Label}
	beforeRows, beforeNote := dataset.load(before)
	afterRows, afterNote := dataset.load(after)
	if beforeRows == nil || afterRows == nil {
		switch {
		case beforeRows == nil && afterRows == nil:
			result.Note = "Not in either package (" + beforeNote + ")."
		case beforeRows == nil:
			result.Note = "Not in the before package (" + beforeNote + ")."
		default:
			result.Note = "Not in the after package (" + afterNote + ")."
		}
		return result
	}
	result.Available = true
	result.BeforeCount, result.AfterCount = len(beforeRows.order), len(afterRows.order)
	expectedText := func(row map[string]string) bool {
		for _, name := range append(append([]string{}, dataset.Display...), dataset.KeyColumns...) {
			if compareExpectedText.MatchString(row[name]) {
				return true
			}
		}
		if dataset.Key == "services" && compareExpectedText.MatchString(row["PathName"]) {
			return true
		}
		return false
	}
	if dataset.KeyColumns == nil {
		// One-row datasets (the server summary) compare their only row.
		var b, a map[string]string
		if len(beforeRows.order) > 0 {
			b = beforeRows.rows[beforeRows.order[0]]
		}
		if len(afterRows.order) > 0 {
			a = afterRows.rows[afterRows.order[0]]
		}
		if b != nil && a != nil {
			change := compareChange{Dataset: dataset.Key, Label: dataset.Label, Kind: "changed", Item: dataset.display(a)}
			allExpected := true
			for _, name := range dataset.Compare {
				if dataset.value(b, name) != dataset.value(a, name) {
					change.Fields = append(change.Fields, compareField{Name: name, Before: dataset.value(b, name), After: dataset.value(a, name)})
					if !dataset.ExpectedFields[name] {
						allExpected = false
					}
				}
			}
			if len(change.Fields) > 0 {
				change.Expected = allExpected
				change.Important = !allExpected
				result.Changed = 1
				if change.Expected {
					result.Expected++
				}
				result.Changes = append(result.Changes, change)
			}
		}
		return result
	}
	for _, key := range beforeRows.order {
		b := beforeRows.rows[key]
		a, ok := afterRows.rows[key]
		if !ok {
			change := compareChange{Dataset: dataset.Key, Label: dataset.Label, Kind: "removed", Item: dataset.display(b), Expected: expectedText(b)}
			change.Important = dataset.ImportantGone && !change.Expected
			result.Removed++
			if change.Expected {
				result.Expected++
			}
			result.Changes = append(result.Changes, change)
			continue
		}
		change := compareChange{Dataset: dataset.Key, Label: dataset.Label, Kind: "changed", Item: dataset.display(a), Expected: expectedText(a)}
		for _, name := range dataset.Compare {
			if dataset.value(b, name) != dataset.value(a, name) {
				change.Fields = append(change.Fields, compareField{Name: name, Before: dataset.value(b, name), After: dataset.value(a, name)})
			}
		}
		if len(change.Fields) > 0 {
			change.Important = !change.Expected && dataset.Important != nil && dataset.Important(b, a)
			result.Changed++
			if change.Expected {
				result.Expected++
			}
			result.Changes = append(result.Changes, change)
		}
	}
	for _, key := range afterRows.order {
		if _, ok := beforeRows.rows[key]; ok {
			continue
		}
		a := afterRows.rows[key]
		change := compareChange{Dataset: dataset.Key, Label: dataset.Label, Kind: "added", Item: dataset.display(a), Expected: expectedText(a)}
		result.Added++
		if change.Expected {
			result.Expected++
		}
		result.Changes = append(result.Changes, change)
	}
	return result
}

// comparePackages compares two packages. It does not write anything.
func comparePackages(beforePath, afterPath string) (*compareResult, error) {
	before, err := openComparePackage(beforePath)
	if err != nil {
		return nil, fmt.Errorf("Before: %v", err)
	}
	defer before.Close()
	after, err := openComparePackage(afterPath)
	if err != nil {
		return nil, fmt.Errorf("After: %v", err)
	}
	defer after.Close()
	if filepath.Clean(before.Path) == filepath.Clean(after.Path) {
		return nil, errors.New("Choose two different packages: one from before the move and one from after.")
	}
	result := &compareResult{Before: *before, After: *after, ComparedAt: time.Now(), Attention: []compareChange{}}
	result.Before.files, result.After.files = nil, nil
	result.SameComputer = strings.EqualFold(before.Computer, after.Computer)
	for _, dataset := range compareDatasets {
		item := compareDatasetRows(dataset, before, after)
		for _, change := range item.Changes {
			result.ChangeCount++
			if change.Expected {
				result.ExpectedCount++
			}
			if change.Important {
				result.AttentionCount++
				if len(result.Attention) < 200 {
					result.Attention = append(result.Attention, change)
				}
			}
		}
		if len(item.Changes) > compareMaxChanges {
			item.Changes = item.Changes[:compareMaxChanges]
			result.Truncated = true
		}
		result.Datasets = append(result.Datasets, item)
	}
	return result, nil
}

const compareTemplateText = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><title>Before and after - {{.Result.After.Computer}}</title><style>{{.Style}} .added{background:#e6f6ee;color:#1d6b43}.removed{background:#fde8e8;color:#8c2626}.changed{background:#fff4dc;color:#7a5410}.expected{background:#e8f0fe;color:#245eae}</style></head>
<body><main>
<h1>Before and after: {{.Result.After.Computer}}</h1>
<p class="sub">Compared {{date .Result.ComparedAt}} · {{.Generator}}</p>
{{if not .Result.SameComputer}}<div class="note">The packages are from different computers ({{.Result.Before.Computer}} and {{.Result.After.Computer}}). Every difference is listed, but most are expected.</div>{{end}}
<table><tr><th></th><th>Before</th><th>After</th></tr>
<tr><td>Computer</td><td>{{.Result.Before.Computer}}</td><td>{{.Result.After.Computer}}</td></tr>
<tr><td>Collected</td><td>{{.Result.Before.CollectedAt}}</td><td>{{.Result.After.CollectedAt}}</td></tr>
<tr><td>Hardware</td><td>{{.Result.Before.Manufacturer}} {{.Result.Before.Model}}</td><td>{{.Result.After.Manufacturer}} {{.Result.After.Model}}</td></tr>
<tr><td>Operating system</td><td>{{.Result.Before.OS}}</td><td>{{.Result.After.OS}}</td></tr>
<tr><td>Package</td><td>{{.Result.Before.Path}}</td><td>{{.Result.After.Path}}</td></tr></table>
<div class="cards"><div class="card"><b>{{.Result.AttentionCount}}</b><span>changes that need attention</span></div><div class="card"><b>{{.Result.ExpectedCount}}</b><span>changes expected after a move</span></div><div class="card"><b>{{.Result.ChangeCount}}</b><span>changes in total</span></div></div>
<h2>Needs attention</h2>
{{if .Result.Attention}}<table><tr><th>Area</th><th>Change</th><th>Item</th><th>Details</th></tr>{{range .Result.Attention}}<tr><td>{{.Label}}</td><td><span class="chip {{.Kind}}">{{.Kind}}</span></td><td>{{.Item}}</td><td>{{range .Fields}}{{.Name}}: {{.Before}} → {{.After}}<br>{{end}}</td></tr>{{end}}</table>{{else}}<p>Nothing needs attention: no running services stopped, and no addresses, shares, volumes, listening ports or applications disappeared.</p>{{end}}
<h2>By area</h2>
<table><tr><th>Area</th><th>Before</th><th>After</th><th>Added</th><th>Removed</th><th>Changed</th><th>Expected</th></tr>{{range .Result.Datasets}}<tr><td>{{.Label}}</td>{{if .Available}}<td>{{.BeforeCount}}</td><td>{{.AfterCount}}</td><td>{{.Added}}</td><td>{{.Removed}}</td><td>{{.Changed}}</td><td>{{.Expected}}</td>{{else}}<td colspan="6" class="muted">{{.Note}}</td>{{end}}</tr>{{end}}</table>
{{range .Result.Datasets}}{{if .Changes}}<h2>{{.Label}}</h2><table><tr><th>Change</th><th>Item</th><th>Details</th></tr>{{range .Changes}}<tr><td><span class="chip {{.Kind}}">{{.Kind}}</span>{{if .Expected}} <span class="chip expected">expected</span>{{end}}</td><td>{{.Item}}</td><td>{{range .Fields}}{{.Name}}: {{.Before}} → {{.After}}<br>{{end}}</td></tr>{{end}}</table>{{end}}{{end}}
{{if .Result.Truncated}}<p class="muted">Long lists were shortened to {{.Max}} changes per area. The CSV file lists them all.</p>{{end}}
</main></body></html>`

var compareTemplate = template.Must(template.New("compare").Funcs(vmReportFunctions).Parse(compareTemplateText))

// writeCompareOutputs writes the HTML report and a CSV of every change.
func writeCompareOutputs(folder string, result *compareResult, beforePath, afterPath string) error {
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	var page bytes.Buffer
	if err := compareTemplate.Execute(&page, map[string]any{"Result": result, "Style": template.CSS(vmReportStylesheet), "Generator": vmReportGeneratorLine, "Max": compareMaxChanges}); err != nil {
		return err
	}
	result.ReportPath = filepath.Join(folder, compareReportName)
	if err := os.WriteFile(result.ReportPath, page.Bytes(), 0600); err != nil {
		return err
	}
	// The CSV lists every change, so read the packages again without the
	// per-area limit used for the report.
	before, err := openComparePackage(beforePath)
	if err != nil {
		return err
	}
	defer before.Close()
	after, err := openComparePackage(afterPath)
	if err != nil {
		return err
	}
	defer after.Close()
	rows := [][]string{}
	for _, dataset := range compareDatasets {
		for _, change := range compareDatasetRows(dataset, before, after).Changes {
			details := []string{}
			for _, field := range change.Fields {
				details = append(details, field.Name+": "+field.Before+" -> "+field.After)
			}
			rows = append(rows, []string{change.Label, change.Kind, change.Item, strings.Join(details, "; "), strconv.FormatBool(change.Expected), strconv.FormatBool(change.Important)})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][5] == "true" && rows[j][5] != "true" })
	result.CSVPath = filepath.Join(folder, compareCSVName)
	return writeCSVFile(result.CSVPath, []string{"Area", "Change", "Item", "Details", "ExpectedAfterMove", "NeedsAttention"}, rows)
}
