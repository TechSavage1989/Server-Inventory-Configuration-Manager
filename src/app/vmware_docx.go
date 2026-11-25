package main

// A minimal Word (.docx) writer for the migration runbook: headings,
// paragraphs, checklist items and simple tables, in plain WordprocessingML
// with no external template. No Windows dependency.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const vmRunbookDocx = "Migration-Runbook.docx"

type docxBuilder struct {
	body bytes.Buffer
}

func docxEscape(text string) string {
	var buffer bytes.Buffer
	// Drop characters XML 1.0 cannot carry; EscapeText handles the rest.
	clean := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF) {
			return r
		}
		return -1
	}, text)
	_ = xml.EscapeText(&buffer, []byte(clean))
	return buffer.String()
}

func docxRun(text string, bold bool) string {
	properties := ""
	if bold {
		properties = "<w:rPr><w:b/></w:rPr>"
	}
	return `<w:r>` + properties + `<w:t xml:space="preserve">` + docxEscape(text) + `</w:t></w:r>`
}

func (d *docxBuilder) paragraph(style, text string) {
	d.body.WriteString(`<w:p>`)
	if style != "" {
		d.body.WriteString(`<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`)
	}
	d.body.WriteString(docxRun(text, false) + `</w:p>`)
}

func (d *docxBuilder) heading(level int, text string) {
	d.paragraph("Heading"+strconv.Itoa(level), text)
}

// docxCheckbox is a Word check box content control (Word 2010 and later,
// and LibreOffice): clicking it in Word ticks it, and it prints as ☐ or ☒.
const docxCheckbox = `<w:sdt><w:sdtPr><w14:checkbox><w14:checked w14:val="0"/><w14:checkedState w14:val="2612" w14:font="MS Gothic"/><w14:uncheckedState w14:val="2610" w14:font="MS Gothic"/></w14:checkbox></w:sdtPr><w:sdtContent><w:r><w:rPr><w:rFonts w:ascii="MS Gothic" w:eastAsia="MS Gothic" w:hAnsi="MS Gothic" w:hint="eastAsia"/></w:rPr><w:t>☐</w:t></w:r></w:sdtContent></w:sdt>`

// checklist writes numbered steps, each with a check box to tick in Word
// (box) or on paper.
func (d *docxBuilder) checklist(items []string, box bool) {
	for index, item := range items {
		d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="ListStep"/></w:pPr>` + docxRun(fmt.Sprintf("%d. ", index+1), false))
		if box {
			d.body.WriteString(docxCheckbox + docxRun(" ", false))
		}
		d.body.WriteString(docxRun(item, false) + `</w:p>`)
	}
}

func (d *docxBuilder) bullets(items []string) {
	for _, item := range items {
		d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="ListStep"/></w:pPr>` + docxRun("• "+item, false) + `</w:p>`)
	}
}

func (d *docxBuilder) table(header []string, rows [][]string) {
	d.body.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="RunbookTable"/><w:tblW w:w="5000" w:type="pct"/></w:tblPr>`)
	write := func(cells []string, bold bool) {
		d.body.WriteString(`<w:tr>`)
		if bold {
			d.body.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for _, cell := range cells {
			d.body.WriteString(`<w:tc><w:p>` + docxRun(cell, bold) + `</w:p></w:tc>`)
		}
		d.body.WriteString(`</w:tr>`)
	}
	write(header, true)
	for _, row := range rows {
		write(row, false)
	}
	d.body.WriteString(`</w:tbl><w:p/>`)
}

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Segoe UI" w:hAnsi="Segoe UI" w:cs="Segoe UI" w:eastAsia="Segoe UI"/><w:sz w:val="20"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="80"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="120"/></w:pPr><w:rPr><w:b/><w:sz w:val="40"/><w:color w:val="172033"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Subtitle"><w:name w:val="Subtitle"/><w:basedOn w:val="Normal"/><w:rPr><w:color w:val="52647C"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:pageBreakBefore/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="32"/><w:color w:val="1F3A68"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="80"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/><w:color w:val="1F3A68"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:sz w:val="22"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="ListStep"><w:name w:val="List Step"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="40"/><w:ind w:left="360" w:hanging="360"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Note"><w:name w:val="Note"/><w:basedOn w:val="Normal"/><w:pPr><w:pBdr><w:left w:val="single" w:sz="18" w:space="6" w:color="2563EB"/></w:pBdr><w:ind w:left="180"/></w:pPr></w:style>
<w:style w:type="table" w:styleId="RunbookTable"><w:name w:val="Runbook Table"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:color="C7D0DD"/><w:left w:val="single" w:sz="4" w:color="C7D0DD"/><w:bottom w:val="single" w:sz="4" w:color="C7D0DD"/><w:right w:val="single" w:sz="4" w:color="C7D0DD"/><w:insideH w:val="single" w:sz="4" w:color="C7D0DD"/><w:insideV w:val="single" w:sz="4" w:color="C7D0DD"/></w:tblBorders><w:tblCellMar><w:left w:w="80" w:type="dxa"/><w:right w:w="80" w:type="dxa"/></w:tblCellMar></w:tblPr><w:rPr><w:sz w:val="18"/></w:rPr></w:style>
</w:styles>`

// save writes the document as a .docx package.
func (d *docxBuilder) save(path, title string) error {
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	files := []struct{ name, content string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`},
		{"docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + docxEscape(title) + `</dc:title><dc:creator>` + docxEscape(strings.TrimPrefix(vmReportGeneratorLine, "Generated by ")) + `</dc:creator></cp:coreProperties>`},
		{"word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`},
		{"word/styles.xml", docxStyles},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" mc:Ignorable="w14"><w:body>` + d.body.String() + `<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1080" w:right="1080" w:bottom="1080" w:left="1080" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`},
	}
	for _, file := range files {
		writer, err := zipWriter.Create(file.name)
		if err != nil {
			return err
		}
		if _, err := writer.Write([]byte(file.content)); err != nil {
			return err
		}
	}
	if err := zipWriter.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, archive.Bytes(), 0600)
}

// vmRunbookDocxFor returns the Word runbook written beside an HTML runbook,
// or "" when there is none.
func vmRunbookDocxFor(runbookHTML string) string {
	if runbookHTML == "" {
		return ""
	}
	path := strings.TrimSuffix(runbookHTML, vmRunbookReport) + vmRunbookDocx
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return ""
	}
	return path
}

// writeRunbookDocx writes the same runbook as the HTML version, as Word.
func writeRunbookDocx(path string, inventory *vmInventory, plan vmPlan, waves []vmRunbookWave, sizing vmSizing) error {
	profile, _ := vmTargetProfileFor(plan.Target)
	d := &docxBuilder{}
	title := "Migration runbook: VMware to " + vmTargetLabel(plan.Target)
	d.paragraph("Title", title)
	d.paragraph("Subtitle", fmt.Sprintf("Source %s %s, inventory read %s, plan saved %s. %s %s", inventory.ServerType, inventory.Server, vmReportDate(inventory.CollectedAt), vmReportDate(plan.UpdatedAt), vmReportGeneratorLine, inventory.AppVersion))
	d.paragraph("Note", "Work through each wave in order. Keep the old VMs powered off but intact until the application owner signs off, so every step can be rolled back.")
	d.paragraph("Note", "Click a box to tick a step when it is done, then save the document. Printed copies show empty boxes to tick by hand.")

	d.heading(2, "Where each part runs")
	roleRows := [][]string{}
	for _, role := range vmMoveRoles(plan.Target) {
		roleRows = append(roleRows, []string{role.Where, role.Does, role.Needs})
	}
	d.table([]string{"Computer", "What runs there", "What it needs"}, roleRows)

	d.heading(2, "Capacity check")
	d.table([]string{"VMs to move", "vCPUs", "Memory", "Storage used"}, [][]string{{strconv.Itoa(sizing.VMs), strconv.Itoa(sizing.VCPUs), vmFormatBytes(sizing.MemoryBytes), vmFormatBytes(sizing.UsedBytes)}})
	d.bullets(sizing.Notes)

	d.heading(2, "Network mapping")
	networkRows := [][]string{}
	for _, network := range plan.Networks {
		networkRows = append(networkRows, []string{network.Source, network.SourceVLAN, network.TargetName, network.TargetVLAN})
	}
	d.table([]string{"VMware network", "VLAN", "Target " + vmNonEmpty(profile.NetworkLabel, "network"), "Target VLAN"}, networkRows)
	d.heading(2, "Storage mapping")
	storageRows := [][]string{}
	for _, storage := range plan.Storage {
		storageRows = append(storageRows, []string{storage.Source, storage.Target})
	}
	d.table([]string{"Datastore", "Target " + vmNonEmpty(profile.StorageLabel, "storage")}, storageRows)

	d.heading(2, "Waves")
	waveRows := [][]string{}
	for _, wave := range waves {
		names := []string{}
		for _, item := range wave.VMs {
			names = append(names, item.VM.Name)
		}
		waveRows = append(waveRows, []string{wave.Label, strings.Join(names, ", ")})
	}
	d.table([]string{"Wave", "VMs"}, waveRows)

	for _, wave := range waves {
		d.heading(1, wave.Label)
		for _, item := range wave.VMs {
			vm := item.VM
			d.heading(2, vm.Name+" ("+vmReportStatusLabel(item.Readiness.Status)+")")
			details := fmt.Sprintf("%s, %d vCPU, %s memory, %s used", vmNonEmpty(vm.GuestOSRunning, vm.GuestOS), vm.CPUs, vmFormatBytes(vm.MemoryMB<<20), vmFormatBytes(vm.UsedBytes))
			if item.Entry.Notes != "" {
				details += ". Notes: " + item.Entry.Notes
			}
			d.paragraph("Subtitle", details)
			sections := []struct {
				title string
				items []string
				box   bool
			}{{"Network adapters", item.NICs, false}, {"Disks", item.Disks, false}, {"Before cutover", item.Before, true}, {"Cutover", item.Cutover, true}, {"Verify", item.Verify, true}, {"Roll back if needed", item.Rollback, false}}
			for _, section := range sections {
				d.heading(3, section.title)
				if len(section.items) == 0 {
					d.paragraph("", "None")
					continue
				}
				if section.title == "Network adapters" || section.title == "Disks" {
					d.bullets(section.items)
				} else {
					d.checklist(section.items, section.box)
				}
			}
		}
	}
	return d.save(path, title)
}
