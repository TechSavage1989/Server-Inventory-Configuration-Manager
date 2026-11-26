package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The Word discovery and readiness report (rule 44): the same content as
// the interactive HTML report, made to be navigated in Word. A clickable
// table of contents, a bookmark on every host, VM, datastore, network and
// issue with links between them, colour-coded readiness, summary tiles and
// bar charts drawn as shaded tables, and page numbers. Plain
// WordprocessingML with no template, macros or external parts, and no
// fields that ask to be updated when the file opens.

// Status colours: fill and text, matching the HTML report.
var vmDocxStatus = map[string][2]string{
	vmStatusReady:     {"E6F6EE", "1D6B43"},
	vmStatusReview:    {"FFF4DC", "7A5410"},
	vmStatusBlocked:   {"FDE8E8", "8C2626"},
	vmStatusSkip:      {"EDF1F7", "52647C"},
	vmSeverityBlocker: {"FDE8E8", "8C2626"},
	vmSeverityWarning: {"FFF4DC", "7A5410"},
	vmSeverityInfo:    {"E8F0FE", "245EAE"},
	"":                {"F4F6FA", "52647C"},
}

// Bar colours, stronger than the cell fills.
var vmDocxBar = map[string]string{vmStatusReady: "2F9E63", vmStatusReview: "E0A526", vmStatusBlocked: "D64545", vmStatusSkip: "A3B1C2"}

const (
	vmDocxPortraitWidth  = 10080 // twips inside the margins of a Letter page
	vmDocxLandscapeWidth = 13680
)

// vmDocxCell is one table cell: its content (runs and paragraphs as
// WordprocessingML) and an optional fill.
type vmDocxCell struct {
	XML  string
	Fill string
}

type vmDocx struct {
	body      bytes.Buffer
	bookmarks map[string]string
	nextMark  int
	nextID    int
	toc       []vmDocxTOC
	width     int
	footer    string
	header    string
}

type vmDocxTOC struct {
	Level int
	Text  string
	Mark  string
}

func newVMDocx() *vmDocx {
	return &vmDocx{bookmarks: map[string]string{}, width: vmDocxPortraitWidth}
}

// mark returns the Word bookmark name for a report anchor (Word allows 40
// characters, letters, digits and underscores).
func (d *vmDocx) mark(anchor string) string {
	if name, ok := d.bookmarks[anchor]; ok {
		return name
	}
	d.nextMark++
	name := "r" + strconv.Itoa(d.nextMark)
	d.bookmarks[anchor] = name
	return name
}

// id returns a new bookmark ID.
func (d *vmDocx) id() string {
	d.nextID++
	return strconv.Itoa(d.nextID)
}

// Inline content.

func dxText(text string) string {
	return `<w:r><w:t xml:space="preserve">` + docxEscape(text) + `</w:t></w:r>`
}

func dxStyled(text string, bold bool, color string, size int) string {
	props := ""
	if bold {
		props += `<w:b/>`
	}
	if color != "" {
		props += `<w:color w:val="` + color + `"/>`
	}
	if size > 0 {
		props += `<w:sz w:val="` + strconv.Itoa(size) + `"/><w:szCs w:val="` + strconv.Itoa(size) + `"/>`
	}
	return `<w:r><w:rPr>` + props + `</w:rPr><w:t xml:space="preserve">` + docxEscape(text) + `</w:t></w:r>`
}

func dxBold(text string) string  { return dxStyled(text, true, "", 0) }
func dxMuted(text string) string { return dxStyled(text, false, "52647C", 0) }

// link is a run that jumps to a report anchor's bookmark; text without an
// anchor is plain.
func (d *vmDocx) link(anchor, text string) string {
	if anchor == "" {
		return dxText(text)
	}
	return `<w:hyperlink w:anchor="` + d.mark(anchor) + `" w:history="1"><w:r><w:rPr><w:rStyle w:val="Hyperlink"/></w:rPr><w:t xml:space="preserve">` + docxEscape(text) + `</w:t></w:r></w:hyperlink>`
}

// chip is a status or severity label in its colours.
func dxChip(kind, text string) string {
	colors := vmDocxStatus[kind]
	return `<w:r><w:rPr><w:b/><w:color w:val="` + colors[1] + `"/><w:sz w:val="16"/><w:shd w:val="clear" w:color="auto" w:fill="` + colors[0] + `"/></w:rPr><w:t xml:space="preserve"> ` + docxEscape(text) + ` </w:t></w:r>`
}

func dxPara(style, inner string) string {
	props := ""
	if style != "" {
		props = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + props + inner + `</w:p>`
}

// Block content.

func (d *vmDocx) para(style, inner string) { d.body.WriteString(dxPara(style, inner)) }

func (d *vmDocx) heading(level int, anchor, text string) {
	mark := ""
	if anchor != "" {
		mark = d.mark(anchor)
	} else {
		mark = d.mark("heading-" + strconv.Itoa(len(d.toc)+1) + "-" + text)
	}
	id := d.id()
	d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading` + strconv.Itoa(level) + `"/></w:pPr><w:bookmarkStart w:id="` + id + `" w:name="` + mark + `"/>` + dxText(text) + `<w:bookmarkEnd w:id="` + id + `"/></w:p>`)
	d.toc = append(d.toc, vmDocxTOC{Level: level, Text: text, Mark: mark})
}

// anchorHere puts a bookmark on an empty, tiny paragraph so a link can jump
// to content that has no heading of its own.
func (d *vmDocx) anchorHere(anchor string) {
	id := d.id()
	d.body.WriteString(`<w:p><w:pPr><w:spacing w:after="0"/><w:rPr><w:sz w:val="2"/></w:rPr></w:pPr><w:bookmarkStart w:id="` + id + `" w:name="` + d.mark(anchor) + `"/><w:bookmarkEnd w:id="` + id + `"/></w:p>`)
}

func (d *vmDocx) note(text string) { d.para("Note", dxText(text)) }

// table writes a table of the full width with relative column widths.
// header may be nil. Every row repeats its header on each page.
func (d *vmDocx) table(weights []int, header []string, rows [][]vmDocxCell) {
	d.body.WriteString(vmDocxTable(d.width, weights, header, rows, "ReportTable"))
	d.body.WriteString(`<w:p><w:pPr><w:spacing w:after="60"/></w:pPr></w:p>`)
}

func vmDocxTable(width int, weights []int, header []string, rows [][]vmDocxCell, style string) string {
	total := 0
	for _, w := range weights {
		total += w
	}
	widths := make([]int, len(weights))
	for i, w := range weights {
		widths[i] = width * w / total
	}
	var out strings.Builder
	out.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="` + style + `"/><w:tblW w:w="` + strconv.Itoa(width) + `" w:type="dxa"/><w:tblLayout w:type="fixed"/><w:tblLook w:val="04A0" w:firstRow="1" w:lastRow="0" w:firstColumn="0" w:lastColumn="0" w:noHBand="0" w:noVBand="1"/></w:tblPr><w:tblGrid>`)
	for _, w := range widths {
		out.WriteString(`<w:gridCol w:w="` + strconv.Itoa(w) + `"/>`)
	}
	out.WriteString(`</w:tblGrid>`)
	cell := func(index int, content vmDocxCell, head bool) {
		out.WriteString(`<w:tc><w:tcPr><w:tcW w:w="` + strconv.Itoa(widths[index]) + `" w:type="dxa"/>`)
		if content.Fill != "" {
			out.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + content.Fill + `"/>`)
		}
		out.WriteString(`</w:tcPr>`)
		xml := content.XML
		if !strings.HasPrefix(xml, "<w:p>") && !strings.HasPrefix(xml, "<w:p ") && !strings.HasPrefix(xml, "<w:tbl>") {
			style := "Cell"
			if head {
				style = "CellHead"
			}
			xml = dxPara(style, xml)
		}
		if strings.HasSuffix(xml, "</w:tbl>") {
			xml += `<w:p><w:pPr><w:spacing w:after="0"/><w:rPr><w:sz w:val="2"/></w:rPr></w:pPr></w:p>`
		}
		out.WriteString(xml + `</w:tc>`)
	}
	if header != nil {
		out.WriteString(`<w:tr><w:trPr><w:tblHeader/><w:cantSplit/></w:trPr>`)
		for i, text := range header {
			cell(i, vmDocxCell{XML: dxText(text), Fill: "E9EEF5"}, true)
		}
		out.WriteString(`</w:tr>`)
	}
	for _, row := range rows {
		out.WriteString(`<w:tr><w:trPr><w:cantSplit/></w:trPr>`)
		for i := range weights {
			content := vmDocxCell{}
			if i < len(row) {
				content = row[i]
			}
			cell(i, content, false)
		}
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl>`)
	return out.String()
}

// kv writes label and value pairs, two pairs to a row.
func (d *vmDocx) kv(pairs [][2]string) {
	rows := [][]vmDocxCell{}
	for i := 0; i < len(pairs); i += 2 {
		row := []vmDocxCell{{XML: dxStyled(pairs[i][0], true, "52647C", 17), Fill: "F4F6FA"}, {XML: pairs[i][1]}}
		if i+1 < len(pairs) {
			row = append(row, vmDocxCell{XML: dxStyled(pairs[i+1][0], true, "52647C", 17), Fill: "F4F6FA"}, vmDocxCell{XML: pairs[i+1][1]})
		}
		rows = append(rows, row)
	}
	d.table([]int{18, 32, 18, 32}, nil, rows)
}

// tiles writes summary figures, four to a row, each linking to its detail.
func (d *vmDocx) tiles(tiles []vmReportTile, anchorFor func(vmReportTile) string) {
	const perRow = 4
	rows := [][]vmDocxCell{}
	for start := 0; start < len(tiles); start += perRow {
		row := []vmDocxCell{}
		for i := start; i < start+perRow; i++ {
			if i >= len(tiles) {
				row = append(row, vmDocxCell{XML: dxPara("Cell", "")})
				continue
			}
			tile := tiles[i]
			colors := vmDocxStatus[tile.Tone]
			if tile.Tone == "" || tile.Tone == "info" {
				colors = [2]string{"F4F6FA", "172033"}
			}
			anchor := anchorFor(tile)
			value := dxStyled(tile.Value, true, colors[1], 40)
			label := dxStyled(tile.Label, true, "172033", 18)
			if anchor != "" {
				value = `<w:hyperlink w:anchor="` + d.mark(anchor) + `" w:history="1">` + value + `</w:hyperlink>`
				label = d.link(anchor, tile.Label)
			}
			xml := dxPara("TileValue", value) + dxPara("TileLabel", label)
			if tile.Detail != "" {
				xml += dxPara("TileLabel", dxStyled(tile.Detail, false, "52647C", 16))
			}
			row = append(row, vmDocxCell{XML: xml, Fill: colors[0]})
		}
		rows = append(rows, row)
	}
	d.body.WriteString(vmDocxTable(d.width, []int{1, 1, 1, 1}, nil, rows, "TileTable"))
	d.body.WriteString(`<w:p><w:pPr><w:spacing w:after="60"/></w:pPr></w:p>`)
}

// bar draws one stacked bar as a shaded one-row table.
func vmDocxBarXML(width int, parts []vmDocxBarPart) string {
	total := 0
	for _, part := range parts {
		total += part.Value
	}
	if total == 0 {
		return dxPara("Cell", dxMuted("No VMs"))
	}
	weights, cells := []int{}, []vmDocxCell{}
	for _, part := range parts {
		if part.Value == 0 {
			continue
		}
		weight := part.Value * 1000 / total
		if weight < 30 {
			weight = 30
		}
		weights = append(weights, weight)
		text := ""
		if weight >= 70 {
			text = strconv.Itoa(part.Value)
		}
		cells = append(cells, vmDocxCell{XML: dxPara("BarCell", dxStyled(text, true, "FFFFFF", 15)), Fill: vmDocxBar[part.Status]})
	}
	return vmDocxTable(width, weights, nil, [][]vmDocxCell{cells}, "BarTable")
}

type vmDocxBarPart struct {
	Status string
	Value  int
}

// landscape and portrait end the current section and start one in the
// other orientation.
func (d *vmDocx) section(landscape bool) {
	d.body.WriteString(`<w:p><w:pPr><w:sectPr>` + d.sectProps(d.width == vmDocxLandscapeWidth) + `</w:sectPr></w:pPr></w:p>`)
	if landscape {
		d.width = vmDocxLandscapeWidth
	} else {
		d.width = vmDocxPortraitWidth
	}
}

func (d *vmDocx) sectProps(landscape bool) string {
	size := `<w:pgSz w:w="12240" w:h="15840"/>`
	if landscape {
		size = `<w:pgSz w:w="15840" w:h="12240" w:orient="landscape"/>`
	}
	return `<w:headerReference w:type="default" r:id="rIdHeader"/><w:footerReference w:type="default" r:id="rIdFooter"/>` + size + `<w:pgMar w:top="1080" w:right="1080" w:bottom="1080" w:left="1080" w:header="500" w:footer="500" w:gutter="0"/>`
}

const vmDocxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Segoe UI" w:hAnsi="Segoe UI" w:cs="Segoe UI" w:eastAsia="Segoe UI"/><w:color w:val="172033"/><w:sz w:val="19"/><w:szCs w:val="19"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="80" w:line="264" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
<w:style w:type="character" w:default="1" w:styleId="DefaultParagraphFont"><w:name w:val="Default Paragraph Font"/></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:before="1800" w:after="120"/></w:pPr><w:rPr><w:b/><w:color w:val="14274A"/><w:sz w:val="56"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Subtitle"><w:name w:val="Subtitle"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="360"/></w:pPr><w:rPr><w:color w:val="52647C"/><w:sz w:val="26"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:pageBreakBefore/><w:pBdr><w:bottom w:val="single" w:sz="12" w:space="4" w:color="2563EB"/></w:pBdr><w:spacing w:before="0" w:after="160"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:color w:val="14274A"/><w:sz w:val="36"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="280" w:after="100"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:color w:val="1F3A68"/><w:sz w:val="27"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:pBdr><w:top w:val="single" w:sz="4" w:space="6" w:color="DBE3ED"/></w:pBdr><w:spacing w:before="320" w:after="80"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:color w:val="172033"/><w:sz w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="3"/></w:pPr><w:rPr><w:b/><w:color w:val="334156"/><w:sz w:val="20"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Lead"><w:name w:val="Lead"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="160"/></w:pPr><w:rPr><w:color w:val="52647C"/><w:sz w:val="20"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Note"><w:name w:val="Note"/><w:basedOn w:val="Normal"/><w:pPr><w:pBdr><w:left w:val="single" w:sz="24" w:space="8" w:color="2563EB"/></w:pBdr><w:shd w:val="clear" w:color="auto" w:fill="F3F8FF"/><w:spacing w:before="80" w:after="160"/><w:ind w:left="200" w:right="100"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Fix"><w:name w:val="Fix"/><w:basedOn w:val="Normal"/><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F3F8FF"/><w:spacing w:after="80"/><w:ind w:left="200" w:right="100"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="IssueTitle"><w:name w:val="Issue Title"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:pBdr><w:left w:val="single" w:sz="24" w:space="8" w:color="E0A526"/></w:pBdr><w:spacing w:before="200" w:after="60"/><w:ind w:left="200"/></w:pPr><w:rPr><w:b/><w:sz w:val="20"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="IssueBlocker"><w:name w:val="Issue Blocker"/><w:basedOn w:val="IssueTitle"/><w:pPr><w:pBdr><w:left w:val="single" w:sz="24" w:space="8" w:color="D64545"/></w:pBdr></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="ListItem"><w:name w:val="List Item"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="50"/><w:ind w:left="360" w:hanging="200"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Cell"><w:name w:val="Cell"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="20" w:after="20" w:line="240" w:lineRule="auto"/></w:pPr><w:rPr><w:sz w:val="17"/><w:szCs w:val="17"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="CellHead"><w:name w:val="Cell Head"/><w:basedOn w:val="Cell"/><w:rPr><w:b/><w:color w:val="334156"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="BarCell"><w:name w:val="Bar Cell"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="0" w:after="0" w:line="240" w:lineRule="auto"/><w:jc w:val="center"/></w:pPr><w:rPr><w:sz w:val="15"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="TileValue"><w:name w:val="Tile Value"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="60" w:after="0" w:line="240" w:lineRule="auto"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="TileLabel"><w:name w:val="Tile Label"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="20" w:line="240" w:lineRule="auto"/></w:pPr><w:rPr><w:sz w:val="17"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="TOCHeading"><w:name w:val="TOC Heading"/><w:basedOn w:val="Normal"/><w:pPr><w:pageBreakBefore/><w:spacing w:after="200"/></w:pPr><w:rPr><w:b/><w:color w:val="14274A"/><w:sz w:val="36"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="TOC1"><w:name w:val="toc 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:spacing w:before="120" w:after="40"/></w:pPr><w:rPr><w:b/><w:sz w:val="21"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="TOC2"><w:name w:val="toc 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:spacing w:after="30"/><w:ind w:left="300"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Footer"><w:name w:val="footer"/><w:basedOn w:val="Normal"/><w:pPr><w:tabs><w:tab w:val="right" w:pos="13680"/></w:tabs><w:spacing w:after="0"/></w:pPr><w:rPr><w:color w:val="6B7C91"/><w:sz w:val="15"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Header"><w:name w:val="header"/><w:basedOn w:val="Normal"/><w:pPr><w:jc w:val="right"/><w:spacing w:after="0"/></w:pPr><w:rPr><w:color w:val="6B7C91"/><w:sz w:val="15"/></w:rPr></w:style>
<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/><w:rPr><w:color w:val="1D4ED8"/><w:u w:val="none"/></w:rPr></w:style>
<w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/><w:tblPr><w:tblInd w:w="0" w:type="dxa"/><w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/><w:right w:w="108" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>
<w:style w:type="table" w:styleId="ReportTable"><w:name w:val="Report Table"/><w:basedOn w:val="TableNormal"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:color="DBE3ED"/><w:left w:val="single" w:sz="4" w:color="DBE3ED"/><w:bottom w:val="single" w:sz="4" w:color="DBE3ED"/><w:right w:val="single" w:sz="4" w:color="DBE3ED"/><w:insideH w:val="single" w:sz="4" w:color="E6EBF2"/></w:tblBorders><w:tblCellMar><w:top w:w="30" w:type="dxa"/><w:left w:w="80" w:type="dxa"/><w:bottom w:w="30" w:type="dxa"/><w:right w:w="80" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>
<w:style w:type="table" w:styleId="TileTable"><w:name w:val="Tile Table"/><w:basedOn w:val="TableNormal"/><w:tblPr><w:tblBorders><w:insideV w:val="single" w:sz="36" w:color="FFFFFF"/><w:insideH w:val="single" w:sz="36" w:color="FFFFFF"/></w:tblBorders><w:tblCellMar><w:top w:w="40" w:type="dxa"/><w:left w:w="140" w:type="dxa"/><w:bottom w:w="80" w:type="dxa"/><w:right w:w="100" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>
<w:style w:type="table" w:styleId="BarTable"><w:name w:val="Bar Table"/><w:basedOn w:val="TableNormal"/><w:tblPr><w:tblBorders><w:insideV w:val="single" w:sz="8" w:color="FFFFFF"/></w:tblBorders><w:tblCellMar><w:top w:w="10" w:type="dxa"/><w:left w:w="0" w:type="dxa"/><w:bottom w:w="10" w:type="dxa"/><w:right w:w="0" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>
</w:styles>`

// save writes the .docx package. The table of contents is a TOC field whose
// result is already filled in with links, so it works without updating;
// Update Field in Word adds page numbers.
func (d *vmDocx) save(path, title, cover string) error {
	var toc strings.Builder
	toc.WriteString(`<w:p><w:pPr><w:pStyle w:val="TOCHeading"/></w:pPr>` + dxText("Contents") + `</w:p>`)
	toc.WriteString(`<w:p><w:pPr><w:pStyle w:val="Lead"/></w:pPr>` + dxText("Select any entry, name or figure to go to it. In Word, View > Navigation Pane lists every host, VM, datastore and network. Right-click the contents and choose Update Field to add page numbers.") + `</w:p>`)
	first := true
	for _, entry := range d.toc {
		if entry.Level > 2 {
			continue
		}
		style := "TOC" + strconv.Itoa(entry.Level)
		toc.WriteString(`<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`)
		if first {
			toc.WriteString(`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText xml:space="preserve"> TOC \o "1-2" \h \z \u </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r>`)
			first = false
		}
		toc.WriteString(`<w:hyperlink w:anchor="` + entry.Mark + `" w:history="1"><w:r><w:rPr><w:rStyle w:val="Hyperlink"/></w:rPr><w:t xml:space="preserve">` + docxEscape(entry.Text) + `</w:t></w:r></w:hyperlink></w:p>`)
	}
	if !first {
		toc.WriteString(`<w:p><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>`)
	}
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` + cover + toc.String() + d.body.String() + `<w:sectPr>` + d.sectProps(d.width == vmDocxLandscapeWidth) + `</w:sectPr></w:body></w:document>`
	footer := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:pPr><w:pStyle w:val="Footer"/><w:pBdr><w:top w:val="single" w:sz="4" w:space="4" w:color="DBE3ED"/></w:pBdr></w:pPr>` + dxText(d.footer+"   ·   Page ") + `<w:fldSimple w:instr=" PAGE "><w:r><w:t>1</w:t></w:r></w:fldSimple>` + dxText(" of ") + `<w:fldSimple w:instr=" NUMPAGES "><w:r><w:t>1</w:t></w:r></w:fldSimple></w:p></w:ftr>`
	header := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:pPr><w:pStyle w:val="Header"/></w:pPr>` + dxText(d.header) + `</w:p></w:hdr>`
	files := []struct{ name, content string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/><Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/><Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`},
		{"docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + docxEscape(title) + `</dc:title><dc:creator>` + docxEscape(strings.TrimPrefix(vmReportGeneratorLine, "Generated by ")) + `</dc:creator></cp:coreProperties>`},
		{"word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/><Relationship Id="rIdFooter" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/><Relationship Id="rIdHeader" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/></Relationships>`},
		{"word/settings.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:defaultTabStop w:val="720"/><w:characterSpacingControl w:val="doNotCompress"/><w:compat><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat></w:settings>`},
		{"word/styles.xml", vmDocxStyles},
		{"word/footer1.xml", footer},
		{"word/header1.xml", header},
		{"word/document.xml", document},
	}
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
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

// writeVMReportDocx writes the Word report.
func writeVMReportDocx(path string, model *vmReportModel) error {
	d := newVMDocx()
	inv, words := model.Inventory, model.Words
	d.header = model.Title + " · " + inv.Server
	d.footer = strings.TrimPrefix(vmReportGeneratorLine, "Generated by ") + " " + inv.AppVersion

	// Cover.
	var cover strings.Builder
	cover.WriteString(dxPara("Title", dxText(model.Title)))
	cover.WriteString(dxPara("Subtitle", dxText(inv.ServerType+" "+inv.Server)))
	coverRows := [][]vmDocxCell{
		{{XML: dxStyled("Source", true, "52647C", 17), Fill: "F4F6FA"}, {XML: dxText(strings.TrimSpace(inv.ServerType + " " + inv.Server))}},
		{{XML: dxStyled("Product", true, "52647C", 17), Fill: "F4F6FA"}, {XML: dxText(inv.Product)}},
		{{XML: dxStyled("Read", true, "52647C", 17), Fill: "F4F6FA"}, {XML: dxText(strings.TrimSpace(vmReportDate(inv.CollectedAt) + " by " + inv.CollectedBy))}},
		{{XML: dxStyled("Contents", true, "52647C", 17), Fill: "F4F6FA"}, {XML: dxText(fmt.Sprintf("%d VMs, %d hosts, %d %s, %d %s", len(model.VMs), len(inv.Hosts), len(inv.Datastores), strings.ToLower(words.Storage), len(inv.Networks), strings.ToLower(words.Networks)))}},
		{{XML: dxStyled("Written", true, "52647C", 17), Fill: "F4F6FA"}, {XML: dxText(vmReportDate(model.Generated) + " by " + d.footer)}},
	}
	cover.WriteString(vmDocxTable(vmDocxPortraitWidth, []int{22, 78}, nil, coverRows, "ReportTable"))
	cover.WriteString(dxPara("Note", dxText("Read-only: nothing in "+words.Platform+" was changed to write this report. Readiness is a guide based on the configuration "+words.Platform+" reports; confirm against your target's supported guest list and test a migration before moving production VMs.")))

	vmDocxOverview(d, model)
	d.section(true)
	vmDocxVMTable(d, model)
	d.section(false)
	vmDocxIssues(d, model)
	vmDocxHosts(d, model)
	vmDocxStorage(d, model)
	vmDocxNetworks(d, model)
	vmDocxVMs(d, model)
	vmDocxEnvironment(d, model)
	return d.save(path, model.Title+" - "+inv.Server, cover.String())
}

func vmDocxOverview(d *vmDocx, model *vmReportModel) {
	words := model.Words
	d.heading(1, "overview", "Overview")
	d.para("Lead", dxText(fmt.Sprintf("%d virtual machines on %d hosts, with their readiness to move to each target. Select any figure, bar label or name to go to it.", len(model.VMs), len(model.Inventory.Hosts))))
	d.heading(2, "overview-focus", "Readiness for "+model.Focus.Label)
	d.tiles(model.Tiles, func(tile vmReportTile) string {
		if tile.Status != "" {
			return "issues-" + tile.Target
		}
		return "vms"
	})
	d.heading(2, "overview-targets", "Readiness by target")
	rows := [][]vmDocxCell{}
	barWidth := d.width * 50 / 100
	for _, target := range model.Targets {
		bar := vmDocxBarXML(barWidth-60, []vmDocxBarPart{{vmStatusReady, target.Ready}, {vmStatusReview, target.Review}, {vmStatusBlocked, target.Blocked}, {vmStatusSkip, target.Skip}})
		rows = append(rows, []vmDocxCell{
			{XML: d.link("issues-"+target.ID, target.Label)},
			{XML: bar},
			{XML: dxStyled(strconv.Itoa(target.Ready), true, vmDocxStatus[vmStatusReady][1], 0), Fill: vmDocxStatus[vmStatusReady][0]},
			{XML: dxStyled(strconv.Itoa(target.Review), true, vmDocxStatus[vmStatusReview][1], 0), Fill: vmDocxStatus[vmStatusReview][0]},
			{XML: dxStyled(strconv.Itoa(target.Blocked), true, vmDocxStatus[vmStatusBlocked][1], 0), Fill: vmDocxStatus[vmStatusBlocked][0]},
			{XML: dxStyled(strconv.Itoa(target.Skip), true, vmDocxStatus[vmStatusSkip][1], 0), Fill: vmDocxStatus[vmStatusSkip][0]},
		})
	}
	d.table([]int{20, 50, 7, 8, 8, 7}, []string{"Target", "Every VM", "Ready", "Review", "Blocked", "Not moved"}, rows)
	d.para("Lead", dxChip(vmStatusReady, "Ready")+dxText("  nothing to fix first   ")+dxChip(vmStatusReview, "Review")+dxText("  warnings to check   ")+dxChip(vmStatusBlocked, "Blocked")+dxText("  must be fixed to move   ")+dxChip(vmStatusSkip, "Not moved")+dxText("  "+words.Platform+" system VMs"))

	d.heading(2, "overview-env", "Environment")
	d.tiles(model.EnvTiles, func(tile vmReportTile) string { return strings.TrimPrefix(tile.Href, "#") })

	d.heading(2, "overview-issues", "Top issues for "+model.Focus.Label)
	issues := model.IssuesFor(model.Focus.ID)
	if len(issues) == 0 {
		d.para("", dxMuted("No warnings or blockers for "+model.Focus.Label+"."))
	}
	for i, issue := range issues {
		if i == 8 {
			break
		}
		d.para("ListItem", dxChip(issue.Severity, vmReportSeverityLabel(issue.Severity))+dxText(" ")+d.link(issue.Anchor, issue.Message)+dxMuted(fmt.Sprintf("  (%d VM%s)", len(issue.VMs), vmPluralS(len(issue.VMs)))))
	}
	d.anchorHere("attention")
	d.heading(2, "", "Needs attention in the environment")
	if len(model.Watch) == 0 {
		d.para("", dxMuted("Nothing found: no expired or over-allocated licenses, host alarms, disconnected hosts or hosts in maintenance mode."))
	}
	for _, note := range model.Watch {
		d.para("ListItem", dxChip(vmStatusReview, "Check")+dxText(" "+note))
	}
}

func vmPluralS(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func vmDocxStatusCell(status string) vmDocxCell {
	colors := vmDocxStatus[status]
	return vmDocxCell{XML: dxStyled(vmReportStatusLabel(status), true, colors[1], 16), Fill: colors[0]}
}

func vmDocxVMTable(d *vmDocx, model *vmReportModel) {
	d.heading(1, "vms", "VMs and readiness")
	d.para("Lead", dxText("Every VM with its readiness for each target. Select a VM for its findings, disks and network adapters, or a host to open it."))
	header := []string{"VM", "Power", "Guest OS", "vCPU", "Memory", "Used", "Host"}
	weights := []int{13, 5, 17, 5, 7, 7, 13}
	for _, profile := range vmTargetProfiles {
		header = append(header, profile.Short)
		weights = append(weights, 7)
	}
	rows := [][]vmDocxCell{}
	for _, vm := range model.VMs {
		row := []vmDocxCell{
			{XML: d.link(vm.Anchor, vm.VM.Name)},
			{XML: dxText(vmReportPower(vm.VM.PowerState))},
			{XML: dxText(vmReportGuest(vm.VM))},
			{XML: dxText(strconv.Itoa(vm.VM.CPUs))},
			{XML: dxText(vmFormatBytes(vm.VM.MemoryMB << 20))},
			{XML: dxText(vmFormatBytes(vm.VM.UsedBytes))},
			{XML: d.link(vm.HostAnchor, vm.VM.Host)},
		}
		for _, status := range vm.Statuses {
			row = append(row, vmDocxStatusCell(status.Status))
		}
		rows = append(rows, row)
	}
	d.table(weights, header, rows)
	if len(model.Templates) > 0 {
		d.heading(2, "", "Templates")
		rows = nil
		for _, vm := range model.Templates {
			rows = append(rows, []vmDocxCell{{XML: d.link(vm.Anchor, vm.VM.Name)}, {XML: dxText(vmReportGuest(vm.VM))}, {XML: dxText(vmFormatBytes(vm.VM.ProvisionedBytes))}, {XML: dxText(vm.VM.Host)}})
		}
		d.table([]int{25, 40, 15, 20}, []string{"Template", "Guest OS", "Provisioned", "Host"}, rows)
	}
}

func vmDocxIssues(d *vmDocx, model *vmReportModel) {
	d.heading(1, "issues", "Issues and fixes")
	d.para("Lead", dxText("Every warning and blocker for each target, grouped by issue, with the fix and the VMs it applies to. Information-only notes are with each VM."))
	for _, group := range model.Issues {
		target := group.Target
		d.heading(2, "issues-"+target.ID, target.Label)
		d.para("", dxChip(vmStatusReady, fmt.Sprintf("%d ready", target.Ready))+dxText("  ")+dxChip(vmStatusReview, fmt.Sprintf("%d review", target.Review))+dxText("  ")+dxChip(vmStatusBlocked, fmt.Sprintf("%d blocked", target.Blocked))+dxText("  ")+dxChip(vmStatusSkip, fmt.Sprintf("%d not moved", target.Skip)))
		if len(group.Issues) == 0 {
			d.para("", dxMuted("No warnings or blockers for "+target.Label+"."))
		}
		for _, issue := range group.Issues {
			style := "IssueTitle"
			if issue.Severity == vmSeverityBlocker {
				style = "IssueBlocker"
			}
			id := d.id()
			d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr><w:bookmarkStart w:id="` + id + `" w:name="` + d.mark(issue.Anchor) + `"/>` + dxChip(issue.Severity, vmReportSeverityLabel(issue.Severity)) + dxText(" "+issue.Message) + `<w:bookmarkEnd w:id="` + id + `"/></w:p>`)
			if issue.Action != "" {
				d.para("Fix", dxStyled("Fix: ", true, "1D4ED8", 0)+dxText(issue.Action))
			}
			if issue.Varies {
				rows := [][]vmDocxCell{}
				for _, vm := range issue.VMs {
					rows = append(rows, []vmDocxCell{{XML: d.link(vm.Anchor, vm.Name)}, {XML: dxText(vmNonEmpty(vm.Message, issue.Message))}})
				}
				d.table([]int{22, 78}, []string{"VM", "Detail"}, rows)
				continue
			}
			var list strings.Builder
			list.WriteString(dxMuted(fmt.Sprintf("%d VM%s: ", len(issue.VMs), vmPluralS(len(issue.VMs)))))
			for i, vm := range issue.VMs {
				if i > 0 {
					list.WriteString(dxText(", "))
				}
				list.WriteString(d.link(vm.Anchor, vm.Name))
			}
			d.para("", list.String())
		}
	}
}

func vmDocxHosts(d *vmDocx, model *vmReportModel) {
	inv, words := model.Inventory, model.Words
	d.heading(1, "hosts", words.Hosts+" and clusters")
	d.para("Lead", dxText(fmt.Sprintf("%d hosts with %d physical cores and %s memory.", len(inv.Hosts), model.Cores, vmFormatBytes(model.MemoryBytes))))
	if len(inv.Clusters) > 0 {
		d.heading(2, "", "Clusters")
		rows := [][]vmDocxCell{}
		for _, c := range inv.Clusters {
			rows = append(rows, []vmDocxCell{{XML: d.link(vmReportClusterID(c.Datacenter, c.Name), c.Name)}, {XML: dxText(c.Datacenter)}, {XML: dxText(strconv.Itoa(c.Hosts))}, {XML: dxText(strconv.Itoa(c.CPUCores))}, {XML: dxText(vmFormatBytes(c.MemoryBytes))}, {XML: dxText(vmYesNo(c.HA))}, {XML: dxText(vmYesNo(c.DRS))}, {XML: dxText(vmNonEmpty(c.EVCMode, "Off"))}})
		}
		d.table([]int{20, 16, 8, 8, 12, 8, 8, 20}, []string{"Cluster", "Datacenter", "Hosts", "Cores", "Memory", "HA", "DRS", "EVC mode"}, rows)
	}
	for _, cluster := range model.Relations.Clusters {
		anchor := ""
		if !vmReportStandalone(cluster) {
			anchor = vmReportClusterID(cluster.Datacenter, cluster.Name)
		}
		d.heading(2, anchor, cluster.Name)
		if len(cluster.Hosts) == 0 {
			d.para("", dxMuted("No hosts were read in this cluster."))
			continue
		}
		rows := [][]vmDocxCell{}
		for _, host := range cluster.Hosts {
			mp := vmReportPercent(host.MemoryMB<<20, host.Host.MemoryBytes)
			state := vmStatusReady
			if !strings.EqualFold(host.Host.ConnectionState, "connected") || host.Host.Maintenance {
				state = vmStatusReview
			}
			rows = append(rows, []vmDocxCell{{XML: d.link(host.Anchor, host.Host.Name)}, {XML: dxText(strings.TrimSpace(host.Host.Vendor + " " + host.Host.Model))}, {XML: dxText(fmt.Sprintf("%d cores", host.Host.Cores))}, {XML: dxText(vmFormatBytes(host.Host.MemoryBytes))}, {XML: dxText(host.Host.Version)}, {XML: dxText(host.Host.ConnectionState), Fill: vmDocxStatus[state][0]}, {XML: dxText(strconv.Itoa(len(host.VMs)))}, {XML: dxText(fmt.Sprintf("%d%%", mp)), Fill: vmDocxMeterFill(mp)}})
		}
		d.table([]int{24, 18, 9, 10, 9, 11, 7, 12}, []string{"Host", "Model", "CPU", "Memory", words.Version, "State", "VMs", "Memory allocated"}, rows)
	}
	for _, host := range model.Relations.Hosts {
		vmDocxHost(d, model, host)
	}
}

func vmDocxMeterFill(percent int) string {
	switch {
	case percent >= 90:
		return vmDocxStatus[vmStatusBlocked][0]
	case percent >= 75:
		return vmDocxStatus[vmStatusReview][0]
	}
	return ""
}

func vmYesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func vmDocxHost(d *vmDocx, model *vmReportModel, rel *vmRelHost) {
	words, h := model.Words, rel.Host
	d.heading(3, rel.Anchor, h.Name)
	crumb := d.link("hosts", words.Hosts)
	if h.Cluster != "" {
		crumb += dxMuted(" / ") + d.link(vmReportClusterID(h.Datacenter, h.Cluster), h.Cluster)
	}
	d.para("Lead", crumb+dxMuted(fmt.Sprintf("  ·  %d VMs, %d powered on", len(rel.VMs), rel.PoweredOn)))
	mp := vmReportPercent(rel.MemoryMB<<20, h.MemoryBytes)
	pairs := [][2]string{
		{"Hardware", dxText(strings.TrimSpace(h.Vendor + " " + h.Model))},
		{"CPU", dxText(fmt.Sprintf("%d × %s (%d cores, %d threads)", h.Sockets, h.CPUModel, h.Cores, h.Threads))},
		{"Memory", dxText(vmFormatBytes(h.MemoryBytes))},
		{words.Version, dxText(strings.TrimSpace(h.Version + " " + h.Build))},
		{"State", dxText(h.ConnectionState + map[bool]string{true: ", maintenance mode"}[h.Maintenance])},
		{"Allocated", dxText(fmt.Sprintf("%d vCPU, %s memory (%d%%)", rel.VCPUs, vmFormatBytes(rel.MemoryMB<<20), mp))},
	}
	if h.License != nil {
		pairs = append(pairs, [2]string{"License", dxText(h.License.Name + " (" + h.License.Key + ")")})
	}
	if details := h.Details; details != nil {
		pairs = append(pairs, [][2]string{
			{"Serial number", dxText(details.SerialNumber)}, {"BIOS", dxText(strings.TrimSpace(details.BIOSVendor + " " + details.BIOSVersion + " " + details.BIOSDate))},
			{"Management IP", dxText(details.ManagementIP)}, {"DNS servers", dxText(strings.Join(details.DNSServers, ", "))},
			{"Default gateway", dxText(details.DefaultGateway)}, {"NTP", dxText(strings.Join(details.NTPServers, ", "))},
			{"Usage now", dxText(fmt.Sprintf("CPU %d%%, memory %d%%", details.CPUUsagePercent, details.MemUsagePercent))}, {"Certificate expires", dxText(details.CertificateExpiry)},
		}...)
	}
	stores := ""
	for i, ds := range rel.Datastores {
		if i > 0 {
			stores += dxText(", ")
		}
		stores += d.link(ds.Anchor, ds.Name)
	}
	pairs = append(pairs, [2]string{words.Storage, vmNonEmpty(stores, dxMuted("Not reported"))})
	d.kv(pairs)
	if details := h.Details; details != nil {
		for _, alarm := range details.Alarms {
			d.para("ListItem", dxChip(vmSeverityWarning, "Alarm")+dxText(" "+alarm))
		}
		for _, issue := range details.ConfigIssues {
			d.para("ListItem", dxChip(vmSeverityWarning, "Issue")+dxText(" "+issue))
		}
	}
	d.heading(4, "", fmt.Sprintf("Virtual machines on this host (%d)", len(rel.VMs)))
	rows := [][]vmDocxCell{}
	for _, vm := range rel.VMs {
		disks := []string{}
		for _, disk := range vm.Disks {
			disks = append(disks, disk.Disk.Label+" "+vmFormatBytes(disk.Disk.CapacityBytes)+" on "+vmNonEmpty(disk.Disk.Datastore, "RDM"))
		}
		rows = append(rows, []vmDocxCell{{XML: d.link(vm.Anchor, vm.VM.Name)}, {XML: dxText(vmReportPower(vm.VM.PowerState))}, {XML: dxText(vmReportGuest(vm.VM))}, {XML: dxText(fmt.Sprintf("%d vCPU, %s", vm.VM.CPUs, vmFormatBytes(vm.VM.MemoryMB<<20)))}, {XML: dxText(strings.Join(disks, "; "))}, vmDocxStatusCell(vmReportStatusOf(vm.VM, model.Focus.ID))})
	}
	if len(rows) == 0 {
		d.para("", dxMuted("No VMs on this host."))
	} else {
		d.table([]int{18, 7, 24, 15, 26, 10}, []string{"VM", "Power", "Guest OS", "Size", "Disks", model.Focus.Short}, rows)
	}
	details := h.Details
	if details == nil {
		return
	}
	if len(details.PhysicalNICs) > 0 {
		d.heading(4, "", "Physical adapters")
		rows = nil
		for _, nic := range details.PhysicalNICs {
			speed := "Down"
			if nic.SpeedMb > 0 {
				speed = fmt.Sprintf("%d Mb/s", nic.SpeedMb)
			}
			rows = append(rows, []vmDocxCell{{XML: dxText(nic.Device)}, {XML: dxText(speed)}, {XML: dxText(nic.MAC)}, {XML: dxText(nic.Driver + " " + nic.Firmware)}, {XML: dxText(nic.Switch)}})
		}
		d.table([]int{14, 14, 24, 28, 20}, []string{"Adapter", "Speed", "MAC", "Driver / firmware", "Switch"}, rows)
	}
	if len(details.Switches) > 0 {
		d.heading(4, "", "Virtual switches")
		rows = nil
		for _, item := range details.Switches {
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Name)}, {XML: dxText(item.Kind)}, {XML: dxText(strconv.Itoa(item.MTU))}, {XML: dxText(strings.Join(item.Uplinks, ", "))}, {XML: dxText(strings.Join(item.Portgroups, ", "))}})
		}
		d.table([]int{18, 14, 8, 20, 40}, []string{"Switch", "Kind", "MTU", "Uplinks", "Port groups"}, rows)
	}
	if len(details.VMkernel) > 0 {
		d.heading(4, "", "VMkernel ports")
		rows = nil
		for _, item := range details.VMkernel {
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Device)}, {XML: dxText(item.Portgroup)}, {XML: dxText(item.IP + " / " + item.Mask)}, {XML: dxText(strconv.Itoa(item.MTU))}, {XML: dxText(strings.Join(item.Services, ", "))}})
		}
		d.table([]int{12, 24, 28, 8, 28}, []string{"Port", "Port group", "Address", "MTU", "Services"}, rows)
	}
	if len(details.StorageAdapters)+len(details.LUNs) > 0 {
		d.heading(4, "", "Storage adapters and devices")
		rows = nil
		for _, item := range details.StorageAdapters {
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Device)}, {XML: dxText(item.Kind)}, {XML: dxText(item.Model)}, {XML: dxText(item.Driver)}, {XML: dxText(item.Status)}})
		}
		d.table([]int{14, 16, 34, 18, 18}, []string{"Adapter", "Kind", "Model", "Driver", "Status"}, rows)
		rows = nil
		for _, item := range details.LUNs {
			rows = append(rows, []vmDocxCell{{XML: dxText(item.DisplayName) + dxPara("Cell", dxMuted(item.CanonicalName))}, {XML: dxText(strings.TrimSpace(item.Vendor + " " + item.Model))}, {XML: dxText(vmFormatBytes(item.CapacityBytes))}, {XML: dxText(vmYesNo(item.SSD))}, {XML: dxText(vmYesNo(item.Local))}, {XML: dxText(item.State)}})
		}
		d.table([]int{38, 20, 12, 8, 8, 14}, []string{"Device", "Vendor / model", "Capacity", "SSD", "Local", "State"}, rows)
	}
	if len(details.Services) > 0 {
		d.heading(4, "", "Services")
		rows = nil
		for _, item := range details.Services {
			state := vmStatusSkip
			if item.Running {
				state = vmStatusReady
			}
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Label)}, {XML: dxText(item.Key)}, {XML: dxText(map[bool]string{true: "Running", false: "Stopped"}[item.Running]), Fill: vmDocxStatus[state][0]}, {XML: dxText(item.Policy)}})
		}
		d.table([]int{40, 25, 15, 20}, []string{"Service", "Key", "State", "Start policy"}, rows)
	}
	if len(details.Packages) > 0 {
		d.heading(4, "", fmt.Sprintf("Installed software packages (%d)", len(details.Packages)))
		rows = nil
		for _, item := range details.Packages {
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Name)}, {XML: dxText(item.Version)}, {XML: dxText(item.Vendor)}, {XML: dxText(item.Acceptance)}})
		}
		d.table([]int{28, 36, 14, 22}, []string{"Package", "Version", "Vendor", "Acceptance"}, rows)
	}
}

func vmDocxStorage(d *vmDocx, model *vmReportModel) {
	words := model.Words
	d.heading(1, "datastores", words.Storage+" and their disks")
	d.para("Lead", dxText(fmt.Sprintf("%s used of %s.", vmFormatBytes(model.StorageCapacity-model.StorageFree), vmFormatBytes(model.StorageCapacity))))
	rows := [][]vmDocxCell{}
	for _, item := range model.Relations.Datastores {
		ds := item.Datastore
		p := vmReportPercent(ds.CapacityBytes-ds.FreeBytes, ds.CapacityBytes)
		rows = append(rows, []vmDocxCell{{XML: d.link(item.Anchor, ds.Name)}, {XML: dxText(ds.Type)}, {XML: dxText(vmFormatBytes(ds.CapacityBytes))}, {XML: dxText(vmFormatBytes(ds.FreeBytes))}, {XML: dxText(fmt.Sprintf("%d%%", p)), Fill: vmDocxMeterFill(p)}, {XML: dxText(strconv.Itoa(item.VMs))}, {XML: dxText(strconv.Itoa(len(item.Disks)))}})
	}
	d.table([]int{28, 10, 14, 14, 12, 10, 12}, []string{words.Datastore, "Type", "Capacity", "Free", "Used", "VMs", "Disks"}, rows)
	for _, item := range model.Relations.Datastores {
		ds := item.Datastore
		d.heading(3, item.Anchor, ds.Name)
		hosts := ""
		for i, host := range item.Hosts {
			if i > 0 {
				hosts += dxText(", ")
			}
			hosts += d.link(host.Anchor, host.Name)
		}
		p := vmReportPercent(ds.CapacityBytes-ds.FreeBytes, ds.CapacityBytes)
		d.kv([][2]string{{"Type", dxText(ds.Type)}, {"Used", dxText(fmt.Sprintf("%d%%: %s free of %s", p, vmFormatBytes(ds.FreeBytes), vmFormatBytes(ds.CapacityBytes)))}, {"Provisioned", dxText(vmFormatBytes(ds.ProvisionedBytes))}, {"Shared", dxText(vmYesNo(ds.Shared))}, {"Mounted on", vmNonEmpty(hosts, dxMuted("Not reported"))}, {"VMs", dxText(fmt.Sprintf("%d (%d disks, %s)", item.VMs, len(item.Disks), vmFormatBytes(item.DiskBytes)))}})
		if len(item.Disks) == 0 {
			continue
		}
		rows = nil
		for _, disk := range item.Disks {
			rows = append(rows, []vmDocxCell{{XML: d.link(disk.VMAnchor, disk.VM)}, {XML: dxText(disk.Disk.Label)}, {XML: dxText(vmFormatBytes(disk.Disk.CapacityBytes))}, {XML: dxText(disk.Disk.Provisioning)}, {XML: dxText(disk.Disk.Mode)}})
		}
		d.table([]int{26, 20, 14, 20, 20}, []string{"VM", "Disk", "Size", "Provisioning", "Mode"}, rows)
	}
}

func vmDocxNetworks(d *vmDocx, model *vmReportModel) {
	words := model.Words
	d.heading(1, "networks", words.Networks+" and their VMs")
	rows := [][]vmDocxCell{}
	for _, item := range model.Relations.Networks {
		n := item.Network
		rows = append(rows, []vmDocxCell{{XML: d.link(item.Anchor, n.Name)}, {XML: dxText(n.Kind)}, {XML: dxText(n.Switch)}, {XML: dxText(n.VLAN)}, {XML: dxText(strconv.Itoa(item.VMs))}, {XML: dxText(strconv.Itoa(len(item.Adapters)))}})
	}
	d.table([]int{30, 15, 20, 11, 12, 12}, []string{words.Network, "Kind", "Switch", "VLAN", "VMs", "Adapters"}, rows)
	for _, item := range model.Relations.Networks {
		d.heading(3, item.Anchor, item.Network.Name)
		d.para("Lead", dxText(fmt.Sprintf("%s · switch %s · VLAN %s · %d VMs", item.Network.Kind, vmNonEmpty(item.Network.Switch, "none"), vmNonEmpty(item.Network.VLAN, "none"), item.VMs)))
		if len(item.Adapters) == 0 {
			continue
		}
		rows = nil
		for _, nic := range item.Adapters {
			rows = append(rows, []vmDocxCell{{XML: d.link(nic.VMAnchor, nic.VM)}, {XML: dxText(nic.NIC.Label)}, {XML: dxText(nic.NIC.MAC)}, {XML: dxText(vmYesNo(nic.NIC.Connected))}, {XML: dxText(strings.Join(nic.NIC.IPAddresses, ", ") + map[bool]string{true: " (static)"}[nic.NIC.Static])}})
		}
		d.table([]int{22, 18, 20, 10, 30}, []string{"VM", "Adapter", "MAC", "Connected", "Guest addresses"}, rows)
	}
}

func vmDocxVMs(d *vmDocx, model *vmReportModel) {
	words := model.Words
	d.heading(1, "vm-pages", "Virtual machines")
	d.para("Lead", dxText("Each VM's readiness for every target with what to fix, and its disks and network adapters."))
	all := append(append([]vmReportVM{}, model.VMs...), model.Templates...)
	for _, vm := range all {
		m := vm.VM
		d.heading(3, vm.Anchor, m.Name)
		d.para("Lead", d.link("vms", "VMs")+dxMuted(" / ")+d.link(vm.HostAnchor, vmNonEmpty(m.Host, "host not read"))+dxMuted(fmt.Sprintf("  ·  %s · %s · %d vCPU · %s · %s used", vmReportPower(m.PowerState), vmReportGuest(m), m.CPUs, vmFormatBytes(m.MemoryMB<<20), vmFormatBytes(m.UsedBytes))))
		d.kv([][2]string{{"Firmware", dxText(vmReportFirmware(m))}, {words.Tools, dxText(vmReportTools(m))}, {"Hardware version", dxText(vmNonEmpty(m.ConfigVersion, m.HardwareVersion))}, {"IP address", dxText(m.IPAddress)}, {"Guest name", dxText(m.GuestHostName)}, {"Snapshots", dxText(strconv.Itoa(len(m.Snapshots)))}})
		rows := [][]vmDocxCell{}
		for _, status := range vm.Statuses {
			var findings strings.Builder
			for _, finding := range status.Findings {
				findings.WriteString(dxPara("Cell", dxChip(finding.Severity, vmReportSeverityLabel(finding.Severity))+dxText(" "+finding.Message)))
				if finding.Action != "" {
					findings.WriteString(dxPara("Cell", dxStyled("Fix: ", true, "1D4ED8", 0)+dxMuted(finding.Action)))
				}
			}
			if findings.Len() == 0 {
				findings.WriteString(dxPara("Cell", dxMuted("Nothing to fix first.")))
			}
			label := vmReportStatusLabel(status.Status)
			if status.Generation > 0 {
				label += fmt.Sprintf(", Gen %d", status.Generation)
			}
			cell := vmDocxStatusCell(status.Status)
			cell.XML = dxStyled(label, true, vmDocxStatus[status.Status][1], 16)
			rows = append(rows, []vmDocxCell{{XML: dxBold(status.Label)}, cell, {XML: findings.String()}})
		}
		d.table([]int{16, 12, 72}, []string{"Target", "Readiness", "Findings and fixes"}, rows)
		if len(vm.Disks) > 0 {
			rows = nil
			for _, disk := range vm.Disks {
				rows = append(rows, []vmDocxCell{{XML: dxText(disk.Disk.Label)}, {XML: dxText(vmFormatBytes(disk.Disk.CapacityBytes))}, {XML: dxText(disk.Disk.Provisioning + map[bool]string{true: " (" + disk.Disk.RDM + " RDM)"}[disk.Disk.RDM != ""])}, {XML: dxText(fmt.Sprintf("%s %d:%d", disk.Disk.Controller, disk.Disk.Bus, disk.Disk.Unit))}, {XML: d.link(disk.DatastoreAnchor, disk.Disk.Datastore)}})
			}
			d.table([]int{18, 12, 22, 20, 28}, []string{"Disk", "Size", "Type", "Controller", words.Datastore}, rows)
		}
		if len(vm.NICs) > 0 {
			rows = nil
			for _, nic := range vm.NICs {
				rows = append(rows, []vmDocxCell{{XML: dxText(nic.NIC.Label)}, {XML: dxText(nic.NIC.AdapterType)}, {XML: d.link(nic.NetworkAnchor, nic.NIC.Network)}, {XML: dxText(nic.NIC.MAC)}, {XML: dxText(strings.Join(nic.NIC.IPAddresses, ", ") + map[bool]string{true: " (static)"}[nic.NIC.Static])}})
			}
			d.table([]int{18, 12, 24, 20, 26}, []string{"Adapter", "Type", words.Network, "MAC", "Guest addresses"}, rows)
		}
	}
}

func vmDocxEnvironment(d *vmDocx, model *vmReportModel) {
	inv := model.Inventory
	title := "vCenter, licenses and more"
	if model.Words.Platform != "VMware" {
		title = "Host details"
	}
	d.heading(1, "environment", title)
	d.kv([][2]string{{"Server", dxText(inv.Server)}, {"Product", dxText(inv.Product)}, {"Build", dxText(inv.Build)}, {"API version", dxText(inv.APIVersion)}, {"Instance UUID", dxText(inv.InstanceUUID)}, {"Datacenters", dxText(strings.Join(inv.Datacenters, ", "))}})
	if model.Words.Platform == "VMware" {
		d.heading(2, "licenses", "Licenses")
		rows := [][]vmDocxCell{}
		for _, item := range inv.Licenses {
			total := "unlimited"
			fill := ""
			if item.Total > 0 {
				total = strconv.Itoa(item.Total)
				fill = vmDocxMeterFill(vmReportPercent(int64(item.Used), int64(item.Total)))
			}
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Name)}, {XML: dxText(item.Key)}, {XML: dxText(fmt.Sprintf("%d / %s %s", item.Used, total, item.Unit)), Fill: fill}, {XML: dxText(vmNonEmpty(item.Expires, "Never"))}, {XML: dxText(strings.Join(item.AssignedTo, ", "))}})
		}
		if len(rows) == 0 {
			d.para("", dxMuted("Licenses were not read (inventories saved by older versions do not have them)."))
		} else {
			d.table([]int{22, 22, 14, 12, 30}, []string{"License", "Key", "Used / total", "Expires", "Assigned to"}, rows)
		}
		d.heading(2, "switches", "Distributed switches")
		rows = nil
		for _, item := range inv.Switches {
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Name)}, {XML: dxText(item.Version)}, {XML: dxText(strconv.Itoa(item.MTU))}, {XML: dxText(item.Discovery)}, {XML: dxText(strings.Join(item.Hosts, ", "))}})
		}
		if len(rows) == 0 {
			d.para("", dxMuted("None."))
		} else {
			d.table([]int{20, 12, 8, 14, 46}, []string{"Switch", "Version", "MTU", "Discovery", "Hosts"}, rows)
		}
		d.heading(2, "pools", "Resource pools and vApps")
		rows = nil
		for _, item := range inv.ResourcePools {
			cpuLimit, memLimit := "Unlimited", "Unlimited"
			if item.CPULimitMHz >= 0 {
				cpuLimit = fmt.Sprintf("%d MHz", item.CPULimitMHz)
			}
			if item.MemLimitMB >= 0 {
				memLimit = fmt.Sprintf("%d MB", item.MemLimitMB)
			}
			rows = append(rows, []vmDocxCell{{XML: dxText(item.Name)}, {XML: dxText(item.Owner)}, {XML: dxText(fmt.Sprintf("%d MHz / %s", item.CPUReservationMHz, cpuLimit))}, {XML: dxText(fmt.Sprintf("%d MB / %s", item.MemReservationMB, memLimit))}, {XML: dxText(strconv.Itoa(item.VMCount))}})
		}
		if len(rows) == 0 {
			d.para("", dxMuted("None."))
		} else {
			d.table([]int{22, 22, 22, 22, 12}, []string{"Pool", "Cluster or host", "CPU reservation / limit", "Memory reservation / limit", "VMs"}, rows)
		}
	}
	d.heading(2, "warnings", "Collection warnings")
	if len(inv.Warnings) == 0 {
		d.para("", dxMuted("Everything was read without warnings."))
	}
	for _, warning := range inv.Warnings {
		d.para("ListItem", dxChip(vmSeverityWarning, "Warning")+dxText(" "+warning))
	}
}
