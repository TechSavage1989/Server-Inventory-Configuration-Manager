package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

// The PDF discovery and readiness report (rule 44): the same content as the
// HTML and Word reports, written directly (no browser, printer or other
// program runs). It has an outline (bookmarks pane), a contents page with
// page numbers, links between every host, VM, datastore, network and issue,
// colour-coded readiness, summary tiles and bar charts. Text uses the
// Windows Segoe UI font when it is there (embedded as a subset), otherwise
// the PDF standard Helvetica.

type vmPDF struct {
	f      *fpdf.Fpdf
	family string
	utf8   bool
	enc    func(string) string
	links  map[string]int
	known  map[string]bool // anchors that exist (from the first pass)
	placed map[string]bool
	pages  map[string]int // page of each contents entry
	toc    []vmDocxTOC
	tocAt  map[string]int // page numbers from the first pass
	model  *vmReportModel
	width  float64 // usable width of the current page
	footer string
}

const (
	vmPDFMargin = 15.0
	vmPDFFont   = "simm"
)

func vmPDFColor(hex string) (int, int, int) {
	value, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return int(value >> 16 & 255), int(value >> 8 & 255), int(value & 255)
}

func (p *vmPDF) text(hex string)   { p.f.SetTextColor(vmPDFColor(hex)) }
func (p *vmPDF) fill(hex string)   { p.f.SetFillColor(vmPDFColor(hex)) }
func (p *vmPDF) stroke(hex string) { p.f.SetDrawColor(vmPDFColor(hex)) }

func (p *vmPDF) font(bold bool, size float64) {
	style := ""
	if bold {
		style = "B"
	}
	p.f.SetFont(p.family, style, size)
}

// clean keeps text the font can measure: no control characters and nothing
// outside the Basic Multilingual Plane.
func (p *vmPDF) clean(text string) string {
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return ' '
		case r < 0x20 || r == 0x7F:
			return -1
		case r > 0xFFFF:
			return '?'
		}
		return r
	}, text)
	return p.enc(text)
}

// split wraps text (already cleaned) to width.
func (p *vmPDF) split(text string, width float64) []string {
	if text == "" {
		return []string{""}
	}
	if p.utf8 {
		lines := p.f.SplitText(text, width)
		if len(lines) == 0 {
			return []string{""}
		}
		return lines
	}
	lines := []string{}
	for _, line := range p.f.SplitLines([]byte(text), width) {
		lines = append(lines, string(line))
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// link returns the fpdf link for an anchor, or 0 when it has no target.
func (p *vmPDF) link(anchor string) int {
	if anchor == "" || !p.known[anchor] {
		return 0
	}
	if id, ok := p.links[anchor]; ok {
		return id
	}
	id := p.f.AddLink()
	p.links[anchor] = id
	return id
}

// place makes an anchor point here.
func (p *vmPDF) place(anchor string) {
	if anchor == "" {
		return
	}
	p.placed[anchor] = true
	if p.known[anchor] {
		p.f.SetLink(p.link(anchor), p.f.GetY()-2, -1)
	}
}

func (p *vmPDF) pageHeight() float64 {
	_, h := p.f.GetPageSize()
	return h
}

// need starts a new page unless height fits on this one.
func (p *vmPDF) need(height float64) {
	if p.f.GetY()+height > p.pageHeight()-vmPDFMargin-8 {
		p.addPage(p.width > 200)
	}
}

func (p *vmPDF) addPage(landscape bool) {
	if landscape {
		p.f.AddPageFormat("L", fpdf.SizeType{Wd: 215.9, Ht: 279.4})
	} else {
		p.f.AddPageFormat("P", fpdf.SizeType{Wd: 215.9, Ht: 279.4})
	}
	w, _ := p.f.GetPageSize()
	p.width = w - 2*vmPDFMargin
}

func (p *vmPDF) heading(level int, anchor, text string) {
	key := anchor
	if key == "" {
		key = fmt.Sprintf("h%d-%s", len(p.toc), text)
	}
	switch level {
	case 1:
		p.f.SetY(vmPDFMargin + 6)
	case 2:
		p.need(22)
		p.f.Ln(4)
	default:
		p.need(28)
		p.f.Ln(3)
	}
	p.place(anchor)
	if level <= 3 {
		p.f.Bookmark(p.clean(text), level-1, -1)
	}
	if level <= 2 {
		p.toc = append(p.toc, vmDocxTOC{Level: level, Text: text, Mark: key})
		p.pages[key] = p.f.PageNo()
	}
	x, y := p.f.GetX(), p.f.GetY()
	switch level {
	case 1:
		p.font(true, 20)
		p.text("14274A")
		p.f.CellFormat(p.width, 10, p.clean(text), "", 1, "L", false, 0, "")
		p.stroke("2563EB")
		p.f.SetLineWidth(0.7)
		p.f.Line(x, p.f.GetY()+1, x+p.width, p.f.GetY()+1)
		p.f.Ln(5)
	case 2:
		p.font(true, 14)
		p.text("1F3A68")
		p.f.CellFormat(p.width, 8, p.clean(text), "", 1, "L", false, 0, "")
		p.f.Ln(1)
	case 3:
		p.stroke("DBE3ED")
		p.f.SetLineWidth(0.3)
		p.f.Line(x, y, x+p.width, y)
		p.f.Ln(2)
		p.font(true, 12.5)
		p.text("172033")
		p.f.CellFormat(p.width, 7, p.clean(text), "", 1, "L", false, 0, "")
	default:
		p.font(true, 10)
		p.text("334156")
		p.f.CellFormat(p.width, 6, p.clean(text), "", 1, "L", false, 0, "")
	}
	p.f.SetLineWidth(0.2)
}

// para writes wrapped text.
func (p *vmPDF) para(text string, color string, size float64, bold bool) {
	p.font(bold, size)
	p.text(color)
	lineHeight := size * 0.5
	for _, line := range p.split(p.clean(text), p.width) {
		p.need(lineHeight)
		p.f.CellFormat(p.width, lineHeight, line, "", 1, "L", false, 0, "")
	}
	p.f.Ln(1.5)
}

// note writes a shaded note with a blue bar.
func (p *vmPDF) note(text string) {
	p.font(false, 9)
	lines := p.split(p.clean(text), p.width-8)
	height := float64(len(lines))*4.6 + 4
	p.need(height)
	x, y := p.f.GetX(), p.f.GetY()
	p.fill("F3F8FF")
	p.f.Rect(x, y, p.width, height, "F")
	p.fill("2563EB")
	p.f.Rect(x, y, 1.2, height, "F")
	p.text("172033")
	p.f.SetY(y + 2)
	for _, line := range lines {
		p.f.SetX(x + 5)
		p.f.CellFormat(p.width-8, 4.6, line, "", 1, "L", false, 0, "")
	}
	p.f.SetY(y + height + 3)
}

// chip draws a small coloured label at the current position and moves past it.
func (p *vmPDF) chip(kind, label string, height float64) {
	colors := vmDocxStatus[kind]
	p.font(true, 7)
	text := p.clean(label)
	width := p.f.GetStringWidth(text) + 4
	x, y := p.f.GetX(), p.f.GetY()
	p.fill(colors[0])
	p.f.RoundedRect(x, y+(height-4)/2, width, 4, 1.5, "1234", "F")
	p.text(colors[1])
	p.f.SetXY(x, y)
	p.f.CellFormat(width, height, text, "", 0, "C", false, 0, "")
}

// flow writes runs of text and links that wrap at the right margin.
type vmPDFRun struct {
	Text   string
	Anchor string
	Color  string
	Bold   bool
	Chip   string
}

func (p *vmPDF) flow(runs []vmPDFRun, size float64) {
	height := size * 0.5
	p.need(height)
	left := p.f.GetX()
	for _, run := range runs {
		if run.Chip != "" {
			p.font(true, 7)
			if p.f.GetX()+p.f.GetStringWidth(p.clean(run.Text))+6 > left+p.width {
				p.f.Ln(height)
				p.need(height)
			}
			p.chip(run.Chip, run.Text, height)
			p.f.SetX(p.f.GetX() + 1.5)
			continue
		}
		p.font(run.Bold, size)
		link := p.link(run.Anchor)
		switch {
		case link != 0:
			p.text("1D4ED8")
		case run.Color != "":
			p.text(run.Color)
		default:
			p.text("172033")
		}
		text := p.clean(run.Text)
		if link != 0 {
			p.f.WriteLinkID(height, text, link)
		} else {
			p.f.Write(height, text)
		}
		if p.f.GetY()+height > p.pageHeight()-vmPDFMargin-8 {
			p.f.Ln(height)
			p.need(height)
		}
	}
	p.f.Ln(height + 1.5)
}

// vmPDFCell is one table cell.
type vmPDFCell struct {
	Text   string
	Anchor string
	Fill   string
	Color  string
	Bold   bool
	Sub    string // a second, muted line
}

func (p *vmPDF) table(weights []float64, header []string, rows [][]vmPDFCell) {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	widths := make([]float64, len(weights))
	for i, w := range weights {
		widths[i] = p.width * w / total
	}
	const pad, lineHeight = 1.4, 3.7
	drawHeader := func() {
		if header == nil {
			return
		}
		p.font(true, 7.5)
		height := 0.0
		lines := make([][]string, len(header))
		for i, text := range header {
			lines[i] = p.split(p.clean(text), widths[i]-2*pad+p.f.GetCellMargin()*2)
			if h := float64(len(lines[i]))*lineHeight + 2*pad; h > height {
				height = h
			}
		}
		x, y := vmPDFMargin, p.f.GetY()
		p.fill("E9EEF5")
		p.f.Rect(x, y, p.width, height, "F")
		p.text("334156")
		for i := range header {
			for n, line := range lines[i] {
				p.f.SetXY(x+pad, y+pad+float64(n)*lineHeight)
				p.f.CellFormat(widths[i]-2*pad, lineHeight, line, "", 0, "L", false, 0, "")
			}
			x += widths[i]
		}
		p.f.SetY(y + height)
	}
	p.need(14)
	drawHeader()
	for _, row := range rows {
		cells := make([][]string, len(weights))
		subs := make([][]string, len(weights))
		height := 0.0
		for i := range weights {
			if i >= len(row) {
				continue
			}
			p.font(row[i].Bold, 8)
			cells[i] = p.split(p.clean(row[i].Text), widths[i]-2*pad+p.f.GetCellMargin()*2)
			h := float64(len(cells[i])) * lineHeight
			if row[i].Sub != "" {
				p.font(false, 7)
				subs[i] = p.split(p.clean(row[i].Sub), widths[i]-2*pad+p.f.GetCellMargin()*2)
				h += float64(len(subs[i])) * 3.3
			}
			if h+2*pad > height {
				height = h + 2*pad
			}
		}
		if p.f.GetY()+height > p.pageHeight()-vmPDFMargin-8 {
			p.addPage(p.width > 200)
			p.f.SetY(vmPDFMargin + 6)
			drawHeader()
		}
		x, y := vmPDFMargin, p.f.GetY()
		for i := range weights {
			if i < len(row) {
				cell := row[i]
				if cell.Fill != "" {
					p.fill(cell.Fill)
					p.f.Rect(x, y, widths[i], height, "F")
				}
				link := p.link(cell.Anchor)
				p.font(cell.Bold, 8)
				switch {
				case link != 0:
					p.text("1D4ED8")
				case cell.Color != "":
					p.text(cell.Color)
				default:
					p.text("172033")
				}
				for n, line := range cells[i] {
					p.f.SetXY(x+pad, y+pad+float64(n)*lineHeight)
					p.f.CellFormat(widths[i]-2*pad, lineHeight, line, "", 0, "L", false, 0, "")
				}
				if link != 0 {
					p.f.Link(x, y, widths[i], height, link)
				}
				if len(subs[i]) > 0 {
					p.font(false, 7)
					p.text("52647C")
					for n, line := range subs[i] {
						p.f.SetXY(x+pad, y+pad+float64(len(cells[i]))*lineHeight+float64(n)*3.3)
						p.f.CellFormat(widths[i]-2*pad, 3.3, line, "", 0, "L", false, 0, "")
					}
				}
			}
			x += widths[i]
		}
		p.stroke("E6EBF2")
		p.f.Line(vmPDFMargin, y+height, vmPDFMargin+p.width, y+height)
		p.f.SetY(y + height)
	}
	p.f.Ln(4)
}

// kv writes label and value pairs, two to a row.
func (p *vmPDF) kv(pairs [][2]string) {
	rows := [][]vmPDFCell{}
	for i := 0; i < len(pairs); i += 2 {
		row := []vmPDFCell{{Text: pairs[i][0], Bold: true, Color: "52647C", Fill: "F4F6FA"}, {Text: pairs[i][1]}}
		if i+1 < len(pairs) {
			row = append(row, vmPDFCell{Text: pairs[i+1][0], Bold: true, Color: "52647C", Fill: "F4F6FA"}, vmPDFCell{Text: pairs[i+1][1]})
		}
		rows = append(rows, row)
	}
	p.table([]float64{18, 32, 18, 32}, nil, rows)
}

// tiles draws summary figures, four to a row; each opens what it counts.
func (p *vmPDF) tiles(tiles []vmReportTile, anchorFor func(vmReportTile) string) {
	const perRow, gap, height = 4, 3.0, 21.0
	width := (p.width - gap*(perRow-1)) / perRow
	for start := 0; start < len(tiles); start += perRow {
		p.need(height + gap)
		y := p.f.GetY()
		for i := start; i < start+perRow && i < len(tiles); i++ {
			tile := tiles[i]
			x := vmPDFMargin + float64(i-start)*(width+gap)
			colors := vmDocxStatus[tile.Tone]
			bar := vmDocxBar[tile.Tone]
			if tile.Tone == "" || tile.Tone == "info" {
				colors, bar = [2]string{"F7F9FC", "172033"}, "C7D3E3"
			}
			p.fill(colors[0])
			p.stroke("DBE3ED")
			p.f.RoundedRect(x, y, width, height, 2, "1234", "FD")
			p.fill(bar)
			p.f.Rect(x+0.2, y+1.5, 1.4, height-3, "F")
			p.font(true, 17)
			p.text(colors[1])
			p.f.SetXY(x+4, y+2)
			p.f.CellFormat(width-6, 8, p.clean(tile.Value), "", 0, "L", false, 0, "")
			p.font(true, 8.5)
			p.text("172033")
			p.f.SetXY(x+4, y+10.5)
			p.f.CellFormat(width-6, 4, p.clean(tile.Label), "", 0, "L", false, 0, "")
			if tile.Detail != "" {
				p.font(false, 7.5)
				p.text("52647C")
				p.f.SetXY(x+4, y+15)
				p.f.CellFormat(width-6, 4, p.clean(tile.Detail), "", 0, "L", false, 0, "")
			}
			if link := p.link(anchorFor(tile)); link != 0 {
				p.f.Link(x, y, width, height, link)
			}
		}
		p.f.SetY(y + height + gap)
	}
	p.f.Ln(2)
}

// bars draws one stacked bar per target; each part links to that target's issues.
func (p *vmPDF) bars(targets []vmReportTarget) {
	label, numbers := 42.0, 56.0
	barWidth := p.width - label - numbers - 4
	for _, target := range targets {
		p.need(8)
		x, y := vmPDFMargin, p.f.GetY()
		p.font(true, 8.5)
		p.text("1D4ED8")
		p.f.SetXY(x, y)
		p.f.CellFormat(label, 6, p.clean(target.Label), "", 0, "L", false, p.link("issues-"+target.ID), "")
		bx := x + label + 2
		p.fill("EDF1F7")
		p.f.Rect(bx, y+0.8, barWidth, 4.4, "F")
		for _, part := range []vmDocxBarPart{{vmStatusReady, target.Ready}, {vmStatusReview, target.Review}, {vmStatusBlocked, target.Blocked}, {vmStatusSkip, target.Skip}} {
			if part.Value == 0 || target.Total == 0 {
				continue
			}
			w := barWidth * float64(part.Value) / float64(target.Total)
			p.fill(vmDocxBar[part.Status])
			p.f.Rect(bx, y+0.8, w, 4.4, "F")
			if w > 6 {
				p.font(true, 7)
				p.text("FFFFFF")
				p.f.SetXY(bx, y+0.8)
				p.f.CellFormat(w, 4.4, strconv.Itoa(part.Value), "", 0, "C", false, 0, "")
			}
			bx += w
		}
		p.font(false, 7.5)
		p.text("52647C")
		p.f.SetXY(x+label+barWidth+4, y)
		p.f.CellFormat(numbers, 6, fmt.Sprintf("%d ready · %d review · %d blocked · %d not moved", target.Ready, target.Review, target.Blocked, target.Skip), "", 0, "L", false, 0, "")
		p.f.SetY(y + 7.5)
	}
	p.f.Ln(1)
	p.f.SetX(vmPDFMargin)
	p.flow([]vmPDFRun{{Chip: vmStatusReady, Text: "Ready"}, {Text: "nothing to fix first   ", Color: "52647C"}, {Chip: vmStatusReview, Text: "Review"}, {Text: "warnings to check   ", Color: "52647C"}, {Chip: vmStatusBlocked, Text: "Blocked"}, {Text: "must be fixed to move   ", Color: "52647C"}, {Chip: vmStatusSkip, Text: "Not moved"}, {Text: p.model.Words.Platform + " system VMs", Color: "52647C"}}, 8)
}

func vmPDFStatus(status string) vmPDFCell {
	colors := vmDocxStatus[status]
	return vmPDFCell{Text: vmReportStatusLabel(status), Fill: colors[0], Color: colors[1], Bold: true}
}

// vmReportFontFiles lists the regular and bold fonts to embed, best first.
func vmReportFontFiles() [][2]string {
	candidates := [][2]string{}
	if windows := os.Getenv("WINDIR"); windows != "" {
		fonts := filepath.Join(windows, "Fonts")
		candidates = append(candidates, [2]string{filepath.Join(fonts, "segoeui.ttf"), filepath.Join(fonts, "segoeuib.ttf")}, [2]string{filepath.Join(fonts, "arial.ttf"), filepath.Join(fonts, "arialbd.ttf")})
	}
	if extra := os.Getenv("SIMM_REPORT_FONTS"); extra != "" {
		if regular, bold, ok := strings.Cut(extra, string(os.PathListSeparator)); ok {
			candidates = append([][2]string{{regular, bold}}, candidates...)
		}
	}
	return candidates
}

// newVMPDF starts a document with the best font available.
func newVMPDF(model *vmReportModel, useFonts bool) (p *vmPDF) {
	f := fpdf.NewCustom(&fpdf.InitType{OrientationStr: "P", UnitStr: "mm", Size: fpdf.SizeType{Wd: 215.9, Ht: 279.4}})
	f.SetMargins(vmPDFMargin, vmPDFMargin, vmPDFMargin)
	f.SetAutoPageBreak(false, vmPDFMargin)
	f.SetCreator(strings.TrimPrefix(vmReportGeneratorLine, "Generated by "), true)
	f.SetTitle(model.Title+" - "+model.Inventory.Server, true)
	f.SetCompression(true)
	p = &vmPDF{f: f, family: "helvetica", enc: func(s string) string { return s }, links: map[string]int{}, known: map[string]bool{}, placed: map[string]bool{}, pages: map[string]int{}, model: model}
	if useFonts {
		for _, pair := range vmReportFontFiles() {
			regular, errRegular := os.ReadFile(pair[0])
			bold, errBold := os.ReadFile(pair[1])
			if errRegular != nil || errBold != nil {
				continue
			}
			f.AddUTF8FontFromBytes(vmPDFFont, "", regular)
			f.AddUTF8FontFromBytes(vmPDFFont, "B", bold)
			if f.Err() {
				return nil
			}
			p.utf8, p.family = true, vmPDFFont
			return p
		}
	}
	// The standard Helvetica needs no embedding; text outside Windows-1252
	// prints as question marks.
	p.enc = f.UnicodeTranslatorFromDescriptor("")
	return p
}

// writeVMReportPDF writes the PDF report.
func writeVMReportPDF(path string, model *vmReportModel) error {
	data, err := vmReportPDFBytes(model, true)
	if err != nil {
		// A font that cannot be embedded must not cost the report.
		data, err = vmReportPDFBytes(model, false)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func vmReportPDFBytes(model *vmReportModel, useFonts bool) (data []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			data, err = nil, fmt.Errorf("PDF report: %v", recovered)
		}
	}()
	// The first pass finds every anchor and the page of each contents
	// entry; the second draws the links and page numbers.
	first := newVMPDF(model, useFonts)
	if first == nil {
		return nil, fmt.Errorf("the font could not be embedded")
	}
	first.render()
	if first.f.Err() {
		return nil, first.f.Error()
	}
	second := newVMPDF(model, useFonts)
	second.known, second.tocAt = first.placed, first.pages
	second.render()
	if second.f.Err() {
		return nil, second.f.Error()
	}
	var out strings.Builder
	if err := second.f.Output(&out); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

func (p *vmPDF) render() {
	model, inv := p.model, p.model.Inventory
	p.footer = strings.TrimPrefix(vmReportGeneratorLine, "Generated by ") + " " + inv.AppVersion + " · " + inv.Server
	p.f.SetHeaderFunc(func() {
		if p.f.PageNo() == 1 {
			return
		}
		p.font(false, 7)
		p.text("6B7C91")
		pageWidth, _ := p.f.GetPageSize()
		p.f.SetXY(vmPDFMargin, 7)
		p.f.CellFormat(pageWidth-2*vmPDFMargin, 4, p.clean(model.Title+" · "+inv.Server), "", 0, "R", false, 0, "")
		p.f.SetY(vmPDFMargin + 6)
	})
	p.f.SetFooterFunc(func() {
		p.font(false, 7)
		p.text("6B7C91")
		h := p.pageHeight()
		p.stroke("DBE3ED")
		p.f.Line(vmPDFMargin, h-12, vmPDFMargin+p.width, h-12)
		p.f.SetXY(vmPDFMargin, h-11)
		p.f.CellFormat(p.width/2, 4, p.clean(p.footer), "", 0, "L", false, 0, "")
		p.f.CellFormat(p.width/2, 4, fmt.Sprintf("Page %d of {nb}", p.f.PageNo()), "", 0, "R", false, 0, "")
	})
	p.f.AliasNbPages("{nb}")

	// Cover.
	p.addPage(false)
	p.fill("14274A")
	p.f.Rect(0, 0, 215.9, 70, "F")
	p.font(true, 26)
	p.text("FFFFFF")
	p.f.SetXY(vmPDFMargin, 30)
	p.f.CellFormat(p.width, 12, p.clean(model.Title), "", 1, "L", false, 0, "")
	p.font(false, 13)
	p.text("C9D6EE")
	p.f.SetX(vmPDFMargin)
	p.f.CellFormat(p.width, 8, p.clean(inv.ServerType+" "+inv.Server), "", 1, "L", false, 0, "")
	p.f.SetY(82)
	p.kv([][2]string{
		{"Source", strings.TrimSpace(inv.ServerType + " " + inv.Server)}, {"Product", inv.Product},
		{"Read", strings.TrimSpace(vmReportDate(inv.CollectedAt) + " by " + inv.CollectedBy)}, {"Written", vmReportDate(model.Generated)},
		{"Contents", fmt.Sprintf("%d VMs, %d hosts, %d %s, %d %s", len(model.VMs), len(inv.Hosts), len(inv.Datastores), strings.ToLower(model.Words.Storage), len(inv.Networks), strings.ToLower(model.Words.Networks))}, {"By", strings.TrimPrefix(vmReportGeneratorLine, "Generated by ") + " " + inv.AppVersion},
	})
	p.note("Read-only: nothing in " + model.Words.Platform + " was changed to write this report. Readiness is a guide based on the configuration " + model.Words.Platform + " reports; confirm against your target's supported guest list and test a migration before moving production VMs.")
	p.tiles(model.Tiles, func(tile vmReportTile) string {
		if tile.Status != "" {
			return "issues-" + tile.Target
		}
		return "vms"
	})

	// Contents, with page numbers from the first pass.
	p.addPage(false)
	p.f.SetY(vmPDFMargin + 6)
	p.font(true, 20)
	p.text("14274A")
	p.f.CellFormat(p.width, 10, "Contents", "", 1, "L", false, 0, "")
	p.para("Select any entry, name, figure or bar to go to it. The bookmarks pane of your PDF reader lists every host, VM, datastore and network.", "52647C", 9, false)
	// The first pass draws the same lines without page numbers, so both
	// passes take the same room.
	for _, entry := range vmPDFContents(model) {
		p.need(7)
		indent, size, bold := 6.0, 9.0, false
		if entry.Level == 1 {
			p.f.Ln(1.5)
			indent, size, bold = 0, 10.5, true
		}
		page, link := "", 0
		if p.tocAt != nil {
			page, link = strconv.Itoa(p.tocAt[entry.Mark]), p.link(entry.Mark)
		}
		p.font(bold, size)
		p.text("1D4ED8")
		p.f.SetX(vmPDFMargin + indent)
		p.f.CellFormat(p.width-indent-14, 5.5, p.clean(entry.Text), "", 0, "L", false, link, "")
		p.text("52647C")
		p.f.CellFormat(14, 5.5, page, "", 1, "R", false, link, "")
	}

	vmPDFOverview(p)
	p.addPage(true)
	vmPDFVMTable(p)
	p.addPage(false)
	vmPDFIssues(p)
	vmPDFHosts(p)
	vmPDFStorage(p)
	vmPDFNetworks(p)
	vmPDFVMs(p)
	vmPDFEnvironment(p)
}

// vmPDFContents is the contents list: the sections and their main parts.
func vmPDFContents(model *vmReportModel) []vmDocxTOC {
	words := model.Words
	entries := []vmDocxTOC{
		{1, "Overview", "overview"}, {2, "Readiness by target", "overview-targets"}, {2, "Environment", "overview-env"}, {2, "Top issues and needs attention", "overview-issues"},
		{1, "VMs and readiness", "vms"},
		{1, "Issues and fixes", "issues"},
	}
	for _, target := range model.Targets {
		entries = append(entries, vmDocxTOC{2, target.Label, "issues-" + target.ID})
	}
	entries = append(entries, vmDocxTOC{1, words.Hosts + " and clusters", "hosts"})
	for _, cluster := range model.Relations.Clusters {
		anchor := "hosts-standalone"
		if !vmReportStandalone(cluster) {
			anchor = vmReportClusterID(cluster.Datacenter, cluster.Name)
		}
		entries = append(entries, vmDocxTOC{2, cluster.Name, anchor})
	}
	entries = append(entries, vmDocxTOC{1, words.Storage + " and their disks", "datastores"}, vmDocxTOC{1, words.Networks + " and their VMs", "networks"}, vmDocxTOC{1, "Virtual machines", "vm-pages"})
	title := "vCenter, licenses and more"
	if words.Platform != "VMware" {
		title = "Host details"
	}
	entries = append(entries, vmDocxTOC{1, title, "environment"})
	return entries
}

func vmPDFOverview(p *vmPDF) {
	model := p.model
	p.addPage(false)
	p.heading(1, "overview", "Overview")
	p.para(fmt.Sprintf("%d virtual machines on %d hosts, with their readiness to move to each target. Select any figure, bar label or name to go to it.", len(model.VMs), len(model.Inventory.Hosts)), "52647C", 9.5, false)
	p.heading(2, "overview-focus", "Readiness for "+model.Focus.Label)
	p.tiles(model.Tiles, func(tile vmReportTile) string {
		if tile.Status != "" {
			return "issues-" + tile.Target
		}
		return "vms"
	})
	p.heading(2, "overview-targets", "Readiness by target")
	p.bars(model.Targets)
	p.heading(2, "overview-env", "Environment")
	p.tiles(model.EnvTiles, func(tile vmReportTile) string { return strings.TrimPrefix(tile.Href, "#") })
	p.heading(2, "overview-issues", "Top issues for "+model.Focus.Label)
	issues := model.IssuesFor(model.Focus.ID)
	if len(issues) == 0 {
		p.para("No warnings or blockers for "+model.Focus.Label+".", "52647C", 9, false)
	}
	for i, issue := range issues {
		if i == 8 {
			break
		}
		p.f.SetX(vmPDFMargin)
		p.flow([]vmPDFRun{{Chip: issue.Severity, Text: vmReportSeverityLabel(issue.Severity)}, {Text: issue.Message, Anchor: issue.Anchor}, {Text: fmt.Sprintf("  (%d VM%s)", len(issue.VMs), vmPluralS(len(issue.VMs))), Color: "52647C"}}, 9)
	}
	p.heading(2, "attention", "Needs attention in the environment")
	if len(model.Watch) == 0 {
		p.para("Nothing found: no expired or over-allocated licenses, host alarms, disconnected hosts or hosts in maintenance mode.", "52647C", 9, false)
	}
	for _, note := range model.Watch {
		p.f.SetX(vmPDFMargin)
		p.flow([]vmPDFRun{{Chip: vmStatusReview, Text: "Check"}, {Text: note}}, 9)
	}
}

func vmPDFVMTable(p *vmPDF) {
	model := p.model
	p.heading(1, "vms", "VMs and readiness")
	p.para("Every VM with its readiness for each target. Select a VM for its findings, disks and network adapters, or a host to open it.", "52647C", 9.5, false)
	header := []string{"VM", "Power", "Guest OS", "vCPU", "Memory", "Used", "Host"}
	weights := []float64{14, 5, 17, 4.5, 6.5, 6.5, 14}
	for _, profile := range vmTargetProfiles {
		header = append(header, profile.Short)
		weights = append(weights, 7)
	}
	rows := [][]vmPDFCell{}
	for _, vm := range model.VMs {
		row := []vmPDFCell{{Text: vm.VM.Name, Anchor: vm.Anchor, Bold: true, Sub: vm.VM.IPAddress}, {Text: vmReportPower(vm.VM.PowerState)}, {Text: vmReportGuest(vm.VM)}, {Text: strconv.Itoa(vm.VM.CPUs)}, {Text: vmFormatBytes(vm.VM.MemoryMB << 20)}, {Text: vmFormatBytes(vm.VM.UsedBytes)}, {Text: vm.VM.Host, Anchor: vm.HostAnchor, Sub: vm.VM.Cluster}}
		for _, status := range vm.Statuses {
			row = append(row, vmPDFStatus(status.Status))
		}
		rows = append(rows, row)
	}
	p.table(weights, header, rows)
	if len(model.Templates) > 0 {
		p.heading(2, "", "Templates")
		rows = nil
		for _, vm := range model.Templates {
			rows = append(rows, []vmPDFCell{{Text: vm.VM.Name, Anchor: vm.Anchor}, {Text: vmReportGuest(vm.VM)}, {Text: vmFormatBytes(vm.VM.ProvisionedBytes)}, {Text: vm.VM.Host}})
		}
		p.table([]float64{25, 40, 15, 20}, []string{"Template", "Guest OS", "Provisioned", "Host"}, rows)
	}
}

func vmPDFIssues(p *vmPDF) {
	model := p.model
	p.heading(1, "issues", "Issues and fixes")
	p.para("Every warning and blocker for each target, grouped by issue, with the fix and the VMs it applies to. Information-only notes are with each VM.", "52647C", 9.5, false)
	for _, group := range model.Issues {
		target := group.Target
		p.heading(2, "issues-"+target.ID, target.Label)
		p.f.SetX(vmPDFMargin)
		p.flow([]vmPDFRun{{Chip: vmStatusReady, Text: fmt.Sprintf("%d ready", target.Ready)}, {Chip: vmStatusReview, Text: fmt.Sprintf("%d review", target.Review)}, {Chip: vmStatusBlocked, Text: fmt.Sprintf("%d blocked", target.Blocked)}, {Chip: vmStatusSkip, Text: fmt.Sprintf("%d not moved", target.Skip)}}, 9)
		if len(group.Issues) == 0 {
			p.para("No warnings or blockers for "+target.Label+".", "52647C", 9, false)
		}
		for _, issue := range group.Issues {
			p.need(22)
			p.f.Ln(2)
			top := p.f.GetY()
			p.place(issue.Anchor)
			p.f.SetX(vmPDFMargin + 4)
			p.chip(issue.Severity, vmReportSeverityLabel(issue.Severity), 5)
			p.font(true, 9.5)
			p.text("172033")
			left := p.f.GetX() + 1.5
			lines := p.split(p.clean(issue.Message), vmPDFMargin+p.width-left)
			for n, line := range lines {
				p.f.SetXY(left, top+float64(n)*4.8)
				p.f.CellFormat(vmPDFMargin+p.width-left, 5, line, "", 0, "L", false, 0, "")
			}
			p.f.SetY(top + float64(len(lines))*4.8 + 1.5)
			if issue.Action != "" {
				saved := p.width
				p.f.SetX(vmPDFMargin + 4)
				p.width = saved - 4
				p.noteFix(issue.Action)
				p.width = saved
			}
			bar := vmDocxBar[vmStatusReview]
			if issue.Severity == vmSeverityBlocker {
				bar = vmDocxBar[vmStatusBlocked]
			}
			if issue.Varies {
				rows := [][]vmPDFCell{}
				for _, vm := range issue.VMs {
					rows = append(rows, []vmPDFCell{{Text: vm.Name, Anchor: vm.Anchor}, {Text: vmNonEmpty(vm.Message, issue.Message)}})
				}
				p.drawIssueBar(top, bar)
				p.table([]float64{20, 80}, []string{"VM", "Detail"}, rows)
				continue
			}
			runs := []vmPDFRun{{Text: fmt.Sprintf("%d VM%s: ", len(issue.VMs), vmPluralS(len(issue.VMs))), Color: "52647C"}}
			for i, vm := range issue.VMs {
				if i > 0 {
					runs = append(runs, vmPDFRun{Text: ", "})
				}
				runs = append(runs, vmPDFRun{Text: vm.Name, Anchor: vm.Anchor})
			}
			p.f.SetX(vmPDFMargin + 4)
			p.flow(runs, 8.5)
			p.drawIssueBar(top, bar)
		}
	}
}

// drawIssueBar draws the coloured bar beside an issue that fits on one page.
func (p *vmPDF) drawIssueBar(top float64, color string) {
	bottom := p.f.GetY() - 1
	if bottom <= top {
		return
	}
	p.fill(color)
	p.f.Rect(vmPDFMargin, top, 1.2, bottom-top, "F")
}

// noteFix writes a shaded "Fix:" box.
func (p *vmPDF) noteFix(text string) {
	p.font(false, 8.5)
	x := p.f.GetX()
	lines := p.split(p.clean("Fix: "+text), p.width-6)
	height := float64(len(lines))*4.2 + 3
	p.need(height)
	y := p.f.GetY()
	p.fill("F3F8FF")
	p.f.Rect(x, y, p.width, height, "F")
	p.text("1F3A68")
	for n, line := range lines {
		p.f.SetXY(x+3, y+1.5+float64(n)*4.2)
		p.f.CellFormat(p.width-6, 4.2, line, "", 0, "L", false, 0, "")
	}
	p.f.SetXY(vmPDFMargin, y+height+1.5)
}

func vmPDFHosts(p *vmPDF) {
	model, inv, words := p.model, p.model.Inventory, p.model.Words
	p.addPage(false)
	p.heading(1, "hosts", words.Hosts+" and clusters")
	p.para(fmt.Sprintf("%d hosts with %d physical cores and %s memory.", len(inv.Hosts), model.Cores, vmFormatBytes(model.MemoryBytes)), "52647C", 9.5, false)
	if len(inv.Clusters) > 0 {
		rows := [][]vmPDFCell{}
		for _, c := range inv.Clusters {
			rows = append(rows, []vmPDFCell{{Text: c.Name, Anchor: vmReportClusterID(c.Datacenter, c.Name)}, {Text: c.Datacenter}, {Text: strconv.Itoa(c.Hosts)}, {Text: strconv.Itoa(c.CPUCores)}, {Text: vmFormatBytes(c.MemoryBytes)}, {Text: vmYesNo(c.HA)}, {Text: vmYesNo(c.DRS)}, {Text: vmNonEmpty(c.EVCMode, "Off")}})
		}
		p.table([]float64{20, 16, 8, 8, 12, 8, 8, 20}, []string{"Cluster", "Datacenter", "Hosts", "Cores", "Memory", "HA", "DRS", "EVC mode"}, rows)
	}
	for _, cluster := range model.Relations.Clusters {
		anchor := "hosts-standalone"
		if !vmReportStandalone(cluster) {
			anchor = vmReportClusterID(cluster.Datacenter, cluster.Name)
		}
		p.heading(2, anchor, cluster.Name)
		if len(cluster.Hosts) == 0 {
			p.para("No hosts were read in this cluster.", "52647C", 9, false)
			continue
		}
		rows := [][]vmPDFCell{}
		for _, host := range cluster.Hosts {
			mp := vmReportPercent(host.MemoryMB<<20, host.Host.MemoryBytes)
			state := vmStatusReady
			if !strings.EqualFold(host.Host.ConnectionState, "connected") || host.Host.Maintenance {
				state = vmStatusReview
			}
			rows = append(rows, []vmPDFCell{{Text: host.Host.Name, Anchor: host.Anchor}, {Text: strings.TrimSpace(host.Host.Vendor + " " + host.Host.Model)}, {Text: fmt.Sprintf("%d cores", host.Host.Cores)}, {Text: vmFormatBytes(host.Host.MemoryBytes)}, {Text: host.Host.Version}, {Text: host.Host.ConnectionState, Fill: vmDocxStatus[state][0]}, {Text: strconv.Itoa(len(host.VMs))}, {Text: fmt.Sprintf("%d%%", mp), Fill: vmDocxMeterFill(mp)}})
		}
		p.table([]float64{24, 18, 9, 10, 9, 11, 7, 12}, []string{"Host", "Model", "CPU", "Memory", words.Version, "State", "VMs", "Memory allocated"}, rows)
	}
	for _, host := range model.Relations.Hosts {
		vmPDFHost(p, host)
	}
}

func vmPDFHost(p *vmPDF, rel *vmRelHost) {
	model, words, h := p.model, p.model.Words, rel.Host
	p.heading(3, rel.Anchor, h.Name)
	runs := []vmPDFRun{{Text: words.Hosts, Anchor: "hosts"}}
	if h.Cluster != "" {
		runs = append(runs, vmPDFRun{Text: " / ", Color: "52647C"}, vmPDFRun{Text: h.Cluster, Anchor: vmReportClusterID(h.Datacenter, h.Cluster)})
	}
	runs = append(runs, vmPDFRun{Text: fmt.Sprintf("  ·  %d VMs, %d powered on", len(rel.VMs), rel.PoweredOn), Color: "52647C"})
	p.f.SetX(vmPDFMargin)
	p.flow(runs, 8.5)
	mp := vmReportPercent(rel.MemoryMB<<20, h.MemoryBytes)
	pairs := [][2]string{
		{"Hardware", strings.TrimSpace(h.Vendor + " " + h.Model)}, {"CPU", fmt.Sprintf("%d × %s (%d cores, %d threads)", h.Sockets, h.CPUModel, h.Cores, h.Threads)},
		{"Memory", vmFormatBytes(h.MemoryBytes)}, {words.Version, strings.TrimSpace(h.Version + " " + h.Build)},
		{"State", h.ConnectionState + map[bool]string{true: ", maintenance mode"}[h.Maintenance]}, {"Allocated", fmt.Sprintf("%d vCPU, %s memory (%d%%)", rel.VCPUs, vmFormatBytes(rel.MemoryMB<<20), mp)},
	}
	if h.License != nil {
		pairs = append(pairs, [2]string{"License", h.License.Name + " (" + h.License.Key + ")"})
	}
	if d := h.Details; d != nil {
		pairs = append(pairs, [][2]string{{"Serial number", d.SerialNumber}, {"BIOS", strings.TrimSpace(d.BIOSVendor + " " + d.BIOSVersion + " " + d.BIOSDate)}, {"Management IP", d.ManagementIP}, {"DNS servers", strings.Join(d.DNSServers, ", ")}, {"Default gateway", d.DefaultGateway}, {"NTP", strings.Join(d.NTPServers, ", ")}, {"Usage now", fmt.Sprintf("CPU %d%%, memory %d%%", d.CPUUsagePercent, d.MemUsagePercent)}, {"Certificate expires", d.CertificateExpiry}}...)
	}
	names := []string{}
	for _, ds := range rel.Datastores {
		names = append(names, ds.Name)
	}
	pairs = append(pairs, [2]string{words.Storage, vmNonEmpty(strings.Join(names, ", "), "Not reported")})
	p.kv(pairs)
	if d := h.Details; d != nil {
		for _, alarm := range d.Alarms {
			p.f.SetX(vmPDFMargin)
			p.flow([]vmPDFRun{{Chip: vmSeverityWarning, Text: "Alarm"}, {Text: alarm}}, 9)
		}
		for _, issue := range d.ConfigIssues {
			p.f.SetX(vmPDFMargin)
			p.flow([]vmPDFRun{{Chip: vmSeverityWarning, Text: "Issue"}, {Text: issue}}, 9)
		}
	}
	p.heading(4, "", fmt.Sprintf("Virtual machines on this host (%d)", len(rel.VMs)))
	rows := [][]vmPDFCell{}
	for _, vm := range rel.VMs {
		disks := []string{}
		for _, disk := range vm.Disks {
			disks = append(disks, disk.Disk.Label+" "+vmFormatBytes(disk.Disk.CapacityBytes)+" on "+vmNonEmpty(disk.Disk.Datastore, "RDM"))
		}
		rows = append(rows, []vmPDFCell{{Text: vm.VM.Name, Anchor: vm.Anchor}, {Text: vmReportPower(vm.VM.PowerState)}, {Text: vmReportGuest(vm.VM)}, {Text: fmt.Sprintf("%d vCPU, %s", vm.VM.CPUs, vmFormatBytes(vm.VM.MemoryMB<<20))}, {Text: strings.Join(disks, "; ")}, vmPDFStatus(vmReportStatusOf(vm.VM, model.Focus.ID))})
	}
	if len(rows) == 0 {
		p.para("No VMs on this host.", "52647C", 9, false)
	} else {
		p.table([]float64{18, 7, 24, 15, 26, 10}, []string{"VM", "Power", "Guest OS", "Size", "Disks", model.Focus.Short}, rows)
	}
	d := h.Details
	if d == nil {
		return
	}
	if len(d.PhysicalNICs) > 0 {
		p.heading(4, "", "Physical adapters")
		rows = nil
		for _, nic := range d.PhysicalNICs {
			speed, fill := "Down", vmDocxStatus[vmStatusBlocked][0]
			if nic.SpeedMb > 0 {
				speed, fill = fmt.Sprintf("%d Mb/s", nic.SpeedMb), ""
			}
			rows = append(rows, []vmPDFCell{{Text: nic.Device}, {Text: speed, Fill: fill}, {Text: nic.MAC}, {Text: strings.TrimSpace(nic.Driver + " " + nic.Firmware)}, {Text: nic.Switch}})
		}
		p.table([]float64{14, 14, 24, 28, 20}, []string{"Adapter", "Speed", "MAC", "Driver / firmware", "Switch"}, rows)
	}
	if len(d.Switches) > 0 {
		p.heading(4, "", "Virtual switches")
		rows = nil
		for _, item := range d.Switches {
			rows = append(rows, []vmPDFCell{{Text: item.Name}, {Text: item.Kind}, {Text: strconv.Itoa(item.MTU)}, {Text: strings.Join(item.Uplinks, ", ")}, {Text: strings.Join(item.Portgroups, ", ")}})
		}
		p.table([]float64{18, 14, 8, 20, 40}, []string{"Switch", "Kind", "MTU", "Uplinks", "Port groups"}, rows)
	}
	if len(d.VMkernel) > 0 {
		p.heading(4, "", "VMkernel ports")
		rows = nil
		for _, item := range d.VMkernel {
			rows = append(rows, []vmPDFCell{{Text: item.Device}, {Text: item.Portgroup}, {Text: item.IP + " / " + item.Mask}, {Text: strconv.Itoa(item.MTU)}, {Text: strings.Join(item.Services, ", ")}})
		}
		p.table([]float64{12, 24, 28, 8, 28}, []string{"Port", "Port group", "Address", "MTU", "Services"}, rows)
	}
	if len(d.StorageAdapters)+len(d.LUNs) > 0 {
		p.heading(4, "", "Storage adapters and devices")
		rows = nil
		for _, item := range d.StorageAdapters {
			rows = append(rows, []vmPDFCell{{Text: item.Device}, {Text: item.Kind}, {Text: item.Model}, {Text: item.Driver}, {Text: item.Status}})
		}
		p.table([]float64{14, 16, 34, 18, 18}, []string{"Adapter", "Kind", "Model", "Driver", "Status"}, rows)
		rows = nil
		for _, item := range d.LUNs {
			rows = append(rows, []vmPDFCell{{Text: item.DisplayName, Sub: item.CanonicalName}, {Text: strings.TrimSpace(item.Vendor + " " + item.Model)}, {Text: vmFormatBytes(item.CapacityBytes)}, {Text: vmYesNo(item.SSD)}, {Text: vmYesNo(item.Local)}, {Text: item.State}})
		}
		p.table([]float64{38, 20, 12, 8, 8, 14}, []string{"Device", "Vendor / model", "Capacity", "SSD", "Local", "State"}, rows)
	}
	if len(d.Services) > 0 {
		p.heading(4, "", "Services")
		rows = nil
		for _, item := range d.Services {
			state := vmStatusSkip
			if item.Running {
				state = vmStatusReady
			}
			rows = append(rows, []vmPDFCell{{Text: item.Label}, {Text: item.Key}, {Text: map[bool]string{true: "Running", false: "Stopped"}[item.Running], Fill: vmDocxStatus[state][0]}, {Text: item.Policy}})
		}
		p.table([]float64{40, 25, 15, 20}, []string{"Service", "Key", "State", "Start policy"}, rows)
	}
	if len(d.Packages) > 0 {
		p.heading(4, "", fmt.Sprintf("Installed software packages (%d)", len(d.Packages)))
		rows = nil
		for _, item := range d.Packages {
			rows = append(rows, []vmPDFCell{{Text: item.Name}, {Text: item.Version}, {Text: item.Vendor}, {Text: item.Acceptance}})
		}
		p.table([]float64{28, 36, 14, 22}, []string{"Package", "Version", "Vendor", "Acceptance"}, rows)
	}
}

func vmPDFStorage(p *vmPDF) {
	model, words := p.model, p.model.Words
	p.addPage(false)
	p.heading(1, "datastores", words.Storage+" and their disks")
	p.para(fmt.Sprintf("%s used of %s.", vmFormatBytes(model.StorageCapacity-model.StorageFree), vmFormatBytes(model.StorageCapacity)), "52647C", 9.5, false)
	rows := [][]vmPDFCell{}
	for _, item := range model.Relations.Datastores {
		ds := item.Datastore
		pc := vmReportPercent(ds.CapacityBytes-ds.FreeBytes, ds.CapacityBytes)
		rows = append(rows, []vmPDFCell{{Text: ds.Name, Anchor: item.Anchor}, {Text: ds.Type}, {Text: vmFormatBytes(ds.CapacityBytes)}, {Text: vmFormatBytes(ds.FreeBytes)}, {Text: fmt.Sprintf("%d%%", pc), Fill: vmDocxMeterFill(pc)}, {Text: strconv.Itoa(item.VMs)}, {Text: strconv.Itoa(len(item.Disks))}})
	}
	p.table([]float64{28, 10, 14, 14, 12, 10, 12}, []string{words.Datastore, "Type", "Capacity", "Free", "Used", "VMs", "Disks"}, rows)
	for _, item := range model.Relations.Datastores {
		ds := item.Datastore
		p.heading(3, item.Anchor, ds.Name)
		hosts := []string{}
		for _, host := range item.Hosts {
			hosts = append(hosts, host.Name)
		}
		pc := vmReportPercent(ds.CapacityBytes-ds.FreeBytes, ds.CapacityBytes)
		p.kv([][2]string{{"Type", ds.Type}, {"Used", fmt.Sprintf("%d%%: %s free of %s", pc, vmFormatBytes(ds.FreeBytes), vmFormatBytes(ds.CapacityBytes))}, {"Provisioned", vmFormatBytes(ds.ProvisionedBytes)}, {"Shared", vmYesNo(ds.Shared)}, {"Mounted on", vmNonEmpty(strings.Join(hosts, ", "), "Not reported")}, {"VMs", fmt.Sprintf("%d (%d disks, %s)", item.VMs, len(item.Disks), vmFormatBytes(item.DiskBytes))}})
		if len(item.Disks) == 0 {
			continue
		}
		rows = nil
		for _, disk := range item.Disks {
			rows = append(rows, []vmPDFCell{{Text: disk.VM, Anchor: disk.VMAnchor}, {Text: disk.Disk.Label}, {Text: vmFormatBytes(disk.Disk.CapacityBytes)}, {Text: disk.Disk.Provisioning}, {Text: disk.Disk.Mode}})
		}
		p.table([]float64{26, 20, 14, 20, 20}, []string{"VM", "Disk", "Size", "Provisioning", "Mode"}, rows)
	}
}

func vmPDFNetworks(p *vmPDF) {
	model, words := p.model, p.model.Words
	p.addPage(false)
	p.heading(1, "networks", words.Networks+" and their VMs")
	rows := [][]vmPDFCell{}
	for _, item := range model.Relations.Networks {
		n := item.Network
		rows = append(rows, []vmPDFCell{{Text: n.Name, Anchor: item.Anchor}, {Text: n.Kind}, {Text: n.Switch}, {Text: n.VLAN}, {Text: strconv.Itoa(item.VMs)}, {Text: strconv.Itoa(len(item.Adapters))}})
	}
	p.table([]float64{30, 15, 20, 11, 12, 12}, []string{words.Network, "Kind", "Switch", "VLAN", "VMs", "Adapters"}, rows)
	for _, item := range model.Relations.Networks {
		p.heading(3, item.Anchor, item.Network.Name)
		p.para(fmt.Sprintf("%s · switch %s · VLAN %s · %d VMs", item.Network.Kind, vmNonEmpty(item.Network.Switch, "none"), vmNonEmpty(item.Network.VLAN, "none"), item.VMs), "52647C", 9, false)
		if len(item.Adapters) == 0 {
			continue
		}
		rows = nil
		for _, nic := range item.Adapters {
			rows = append(rows, []vmPDFCell{{Text: nic.VM, Anchor: nic.VMAnchor}, {Text: nic.NIC.Label}, {Text: nic.NIC.MAC}, {Text: vmYesNo(nic.NIC.Connected)}, {Text: strings.Join(ipMaskList(nic.NIC.IPAddresses), ", ") + map[bool]string{true: " (static)"}[nic.NIC.Static]}})
		}
		p.table([]float64{22, 18, 20, 10, 30}, []string{"VM", "Adapter", "MAC", "Connected", "Guest addresses"}, rows)
	}
}

func vmPDFVMs(p *vmPDF) {
	model, words := p.model, p.model.Words
	p.addPage(false)
	p.heading(1, "vm-pages", "Virtual machines")
	p.para("Each VM's readiness for every target with what to fix, and its disks and network adapters.", "52647C", 9.5, false)
	all := append(append([]vmReportVM{}, model.VMs...), model.Templates...)
	for _, vm := range all {
		m := vm.VM
		p.need(60)
		p.heading(3, vm.Anchor, m.Name)
		p.f.SetX(vmPDFMargin)
		p.flow([]vmPDFRun{{Text: "VMs", Anchor: "vms"}, {Text: " / ", Color: "52647C"}, {Text: vmNonEmpty(m.Host, "host not read"), Anchor: vm.HostAnchor}, {Text: fmt.Sprintf("  ·  %s · %s · %d vCPU · %s · %s used", vmReportPower(m.PowerState), vmReportGuest(m), m.CPUs, vmFormatBytes(m.MemoryMB<<20), vmFormatBytes(m.UsedBytes)), Color: "52647C"}}, 8.5)
		p.kv([][2]string{{"Firmware", vmReportFirmware(m)}, {words.Tools, vmReportTools(m)}, {"Hardware version", vmNonEmpty(m.ConfigVersion, m.HardwareVersion)}, {"IP address", m.IPAddress}, {"Guest name", m.GuestHostName}, {"Snapshots", strconv.Itoa(len(m.Snapshots))}})
		rows := [][]vmPDFCell{}
		for _, status := range vm.Statuses {
			lines := []string{}
			for _, finding := range status.Findings {
				line := vmReportSeverityLabel(finding.Severity) + ": " + finding.Message
				if finding.Action != "" {
					line += " Fix: " + finding.Action
				}
				lines = append(lines, line)
			}
			text := strings.Join(lines, "   ")
			if text == "" {
				text = "Nothing to fix first."
			}
			label := vmReportStatusLabel(status.Status)
			if status.Generation > 0 {
				label += fmt.Sprintf(", Gen %d", status.Generation)
			}
			cell := vmPDFStatus(status.Status)
			cell.Text = label
			rows = append(rows, []vmPDFCell{{Text: status.Label, Bold: true}, cell, {Text: text}})
		}
		p.table([]float64{16, 12, 72}, []string{"Target", "Readiness", "Findings and fixes"}, rows)
		if len(vm.Checks) > 0 {
			p.para("Domain controller and SQL Server checks", "1E293B", 9.5, true)
			p.kv(vm.Checks)
		}
		if len(vm.Disks) > 0 {
			rows = nil
			for _, disk := range vm.Disks {
				rows = append(rows, []vmPDFCell{{Text: disk.Disk.Label}, {Text: vmFormatBytes(disk.Disk.CapacityBytes)}, {Text: disk.Disk.Provisioning + map[bool]string{true: " (" + disk.Disk.RDM + " RDM)"}[disk.Disk.RDM != ""]}, {Text: fmt.Sprintf("%s %d:%d", disk.Disk.Controller, disk.Disk.Bus, disk.Disk.Unit)}, {Text: disk.Disk.Datastore, Anchor: disk.DatastoreAnchor}})
			}
			p.table([]float64{18, 12, 22, 20, 28}, []string{"Disk", "Size", "Type", "Controller", words.Datastore}, rows)
		}
		if len(vm.NICs) > 0 {
			rows = nil
			for _, nic := range vm.NICs {
				rows = append(rows, []vmPDFCell{{Text: nic.NIC.Label}, {Text: nic.NIC.AdapterType}, {Text: nic.NIC.Network, Anchor: nic.NetworkAnchor}, {Text: nic.NIC.MAC}, {Text: strings.Join(ipMaskList(nic.NIC.IPAddresses), ", ") + map[bool]string{true: " (static)"}[nic.NIC.Static]}})
			}
			p.table([]float64{18, 12, 24, 20, 26}, []string{"Adapter", "Type", words.Network, "MAC", "Guest addresses"}, rows)
		}
	}
}

func vmPDFEnvironment(p *vmPDF) {
	model, inv := p.model, p.model.Inventory
	title := "vCenter, licenses and more"
	if model.Words.Platform != "VMware" {
		title = "Host details"
	}
	p.addPage(false)
	p.heading(1, "environment", title)
	p.kv([][2]string{{"Server", inv.Server}, {"Product", inv.Product}, {"Build", inv.Build}, {"API version", inv.APIVersion}, {"Instance UUID", inv.InstanceUUID}, {"Datacenters", strings.Join(inv.Datacenters, ", ")}})
	if model.Words.Platform == "VMware" {
		p.heading(2, "licenses", "Licenses")
		rows := [][]vmPDFCell{}
		for _, item := range inv.Licenses {
			total, fill := "unlimited", ""
			if item.Total > 0 {
				total = strconv.Itoa(item.Total)
				fill = vmDocxMeterFill(vmReportPercent(int64(item.Used), int64(item.Total)))
			}
			rows = append(rows, []vmPDFCell{{Text: item.Name}, {Text: item.Key}, {Text: fmt.Sprintf("%d / %s %s", item.Used, total, item.Unit), Fill: fill}, {Text: vmNonEmpty(item.Expires, "Never")}, {Text: strings.Join(item.AssignedTo, ", ")}})
		}
		if len(rows) == 0 {
			p.para("Licenses were not read (inventories saved by older versions do not have them).", "52647C", 9, false)
		} else {
			p.table([]float64{22, 22, 14, 12, 30}, []string{"License", "Key", "Used / total", "Expires", "Assigned to"}, rows)
		}
		p.heading(2, "switches", "Distributed switches")
		rows = nil
		for _, item := range inv.Switches {
			rows = append(rows, []vmPDFCell{{Text: item.Name}, {Text: item.Version}, {Text: strconv.Itoa(item.MTU)}, {Text: item.Discovery}, {Text: strings.Join(item.Hosts, ", ")}})
		}
		if len(rows) == 0 {
			p.para("None.", "52647C", 9, false)
		} else {
			p.table([]float64{20, 12, 8, 14, 46}, []string{"Switch", "Version", "MTU", "Discovery", "Hosts"}, rows)
		}
		p.heading(2, "pools", "Resource pools and vApps")
		rows = nil
		for _, item := range inv.ResourcePools {
			cpuLimit, memLimit := "Unlimited", "Unlimited"
			if item.CPULimitMHz >= 0 {
				cpuLimit = fmt.Sprintf("%d MHz", item.CPULimitMHz)
			}
			if item.MemLimitMB >= 0 {
				memLimit = fmt.Sprintf("%d MB", item.MemLimitMB)
			}
			rows = append(rows, []vmPDFCell{{Text: item.Name}, {Text: item.Owner}, {Text: fmt.Sprintf("%d MHz / %s", item.CPUReservationMHz, cpuLimit)}, {Text: fmt.Sprintf("%d MB / %s", item.MemReservationMB, memLimit)}, {Text: strconv.Itoa(item.VMCount)}})
		}
		if len(rows) == 0 {
			p.para("None.", "52647C", 9, false)
		} else {
			p.table([]float64{22, 22, 22, 22, 12}, []string{"Pool", "Cluster or host", "CPU reservation / limit", "Memory reservation / limit", "VMs"}, rows)
		}
	}
	p.heading(2, "warnings", "Collection warnings")
	if len(inv.Warnings) == 0 {
		p.para("Everything was read without warnings.", "52647C", 9, false)
	}
	for _, warning := range inv.Warnings {
		p.f.SetX(vmPDFMargin)
		p.flow([]vmPDFRun{{Chip: vmSeverityWarning, Text: "Warning"}, {Text: warning}}, 9)
	}
}
