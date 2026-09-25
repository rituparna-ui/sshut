package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rituu/sshut/internal/filesystem"
)

// Pane is a keyboard-driven view of one filesystem.
type Pane struct {
	Title    string
	Path     string
	Entries  []filesystem.Entry
	Cursor   int
	Offset   int
	Loading  bool
	Err      error
	Selected map[string]struct{}
}

// NewPane creates an empty filesystem pane.
func NewPane(title string) Pane {
	return Pane{Title: title, Selected: make(map[string]struct{})}
}

// SetEntries replaces the visible directory and clamps the cursor.
func (p *Pane) SetEntries(entries []filesystem.Entry) {
	p.Entries = entries
	if p.Selected == nil {
		p.Selected = make(map[string]struct{})
	}
	if p.Cursor >= len(p.Entries) || p.Cursor < 0 {
		p.Cursor = max(0, len(p.Entries)-1)
	}
	p.clampOffset(0)
}

// Current returns the entry under the cursor.
func (p Pane) Current() (filesystem.Entry, bool) {
	if p.Cursor < 0 || p.Cursor >= len(p.Entries) {
		return filesystem.Entry{}, false
	}
	return p.Entries[p.Cursor], true
}

// Move changes the cursor by delta and returns the new absolute position.
func (p *Pane) Move(delta int) int {
	if len(p.Entries) == 0 {
		p.Cursor, p.Offset = 0, 0
		return 0
	}
	p.Cursor = min(max(0, p.Cursor+delta), len(p.Entries)-1)
	return p.Cursor
}

// ToggleSelected toggles the current entry. The parent sentinel cannot be
// selected.
func (p *Pane) ToggleSelected() {
	entry, ok := p.Current()
	if !ok || entry.Name == ".." {
		return
	}
	if _, selected := p.Selected[entry.Path]; selected {
		delete(p.Selected, entry.Path)
	} else {
		p.Selected[entry.Path] = struct{}{}
	}
}

// Selection returns selected entries in display order, or the current entry
// when no explicit selection exists.
func (p Pane) Selection() []filesystem.Entry {
	if len(p.Selected) == 0 {
		if entry, ok := p.Current(); ok && entry.Name != ".." {
			return []filesystem.Entry{entry}
		}
		return nil
	}

	entries := make([]filesystem.Entry, 0, len(p.Selected))
	for _, entry := range p.Entries {
		if _, ok := p.Selected[entry.Path]; ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

// ClearSelection removes all explicit selections.
func (p *Pane) ClearSelection() {
	clear(p.Selected)
}

// View renders a fixed-size pane. Rows remain one line high.
func (p Pane) View(styles Styles, width, height int, active bool) string {
	width, height = max(width, 4), max(height, 3)
	innerWidth := width - 2
	innerHeight := height - 2

	header := p.header(styles, innerWidth)
	// View receives a value because Bubble Tea models are values. Recompute the
	// window for this render rather than relying on a mutation that would be
	// discarded after View returns.
	p.clampOffset(innerHeight - 2)
	lines := make([]string, 0, innerHeight)
	lines = append(lines, header, "")
	lines = append(lines, p.visibleRows(styles, innerWidth, innerHeight-2)...)

	for len(lines) < innerHeight {
		lines = append(lines, "")
	}
	lines = lines[:innerHeight]
	content := strings.Join(lines, "\n")

	border := styles.Border
	if active {
		border = styles.Active
	}
	return border.Width(innerWidth).Height(innerHeight).Render(content)
}

func (p Pane) header(styles Styles, width int) string {
	title := p.Title
	if p.Path != "" {
		title += "  " + p.Path
	}
	if p.Loading {
		title += " (loading)"
	}
	return fit(title, width, styles.Title)
}

func (p Pane) visibleRows(styles Styles, width, height int) []string {
	if p.Err != nil {
		return []string{fit("Error: "+p.Err.Error(), width, styles.Error)}
	}
	if len(p.Entries) == 0 {
		return []string{fit("(empty directory)", width, styles.Muted)}
	}

	showMetadata := width >= 42
	nameWidth := width
	if showMetadata {
		nameWidth = width - 21
	}
	p.clampOffset(height)
	rows := make([]string, 0, height)
	for index := p.Offset; index < len(p.Entries) && len(rows) < height; index++ {
		entry := p.Entries[index]
		rows = append(rows, p.renderRow(styles, index, entry, width, nameWidth, showMetadata))
	}
	return rows
}

func (p Pane) renderRow(styles Styles, index int, entry filesystem.Entry, width, nameWidth int, showMetadata bool) string {
	_, selected := p.Selected[entry.Path]
	marker := "  "
	if index == p.Cursor {
		marker = "> "
	} else if selected {
		marker = "× "
	}

	nameStyle := entryStyle(styles, entry.Kind)
	nameText := marker + entryIcon(entry) + entry.Name
	name := nameStyle.Render(nameText)
	if index == p.Cursor {
		name = styles.Cursor.Render(nameText)
	} else if selected {
		name = styles.Selected.Render(nameText)
	}
	name = fit(name, nameWidth, lipgloss.NewStyle())
	if !showMetadata {
		return padRight(name, width)
	}

	size := "-"
	if entry.Kind == filesystem.KindFile {
		size = formatSize(entry.Size)
	}
	modified := "-"
	if !entry.ModTime.IsZero() {
		modified = entry.ModTime.Format("Jan 02 15:04")
	}
	metadata := fmt.Sprintf("%9s  %15s", size, modified)
	return name + metadata
}

func entryStyle(styles Styles, kind filesystem.Kind) lipgloss.Style {
	switch kind {
	case filesystem.KindDirectory:
		return styles.Directory
	case filesystem.KindSymlink:
		return styles.Symlink
	case filesystem.KindOther:
		return styles.Special
	default:
		return styles.Row
	}
}

func entryIcon(entry filesystem.Entry) string {
	switch entry.Kind {
	case filesystem.KindDirectory:
		return "/ "
	case filesystem.KindSymlink:
		return "@ "
	case filesystem.KindOther:
		return "? "
	default:
		return "  "
	}
}

func (p *Pane) clampOffset(height int) {
	if height < 1 {
		height = 1
	}
	if p.Cursor < p.Offset {
		p.Offset = p.Cursor
	}
	if p.Cursor >= p.Offset+height {
		p.Offset = p.Cursor - height + 1
	}
	maxOffset := max(0, len(p.Entries)-height)
	p.Offset = min(max(0, p.Offset), maxOffset)
}

func fit(value string, width int, base lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	value = base.MaxWidth(width).Render(value)
	return padRight(value, width)
}

func padRight(value string, width int) string {
	padding := width - lipgloss.Width(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	divisor, exponent := int64(unit), 0
	for value := size / unit; value >= unit && exponent < 4; value /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(divisor), "KMGTP"[exponent])
}
