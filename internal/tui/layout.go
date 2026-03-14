package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
)

// Layout constants.
//
// lipgloss Width() sets inner size (content + padding), border is added on top:
//
//	panelStyle   has Padding(0,1) + RoundedBorder → rendered = Width() + 2 (border)
//	appStyle     has Padding(0,1), no border       → rendered = Width()
//
// So to fit a panel inside inner width W:  panel.Width(W - 2)
// Inner width of appStyle.Width(appW):     appW - 2  (horizontal padding eats 2)
const (
	// appPadH is the horizontal padding of appStyle (Padding(0,1) = left+right = 2).
	appPadH = 2

	// inputContentRows: title(1) + hint(1) + blank(1) + input-field(1) = 4
	inputContentRows = 4
	// baseInputPanelRows: inputContentRows + rounded border overhead
	baseInputPanelRows = inputContentRows + 2
	// footerRows: one line of key hints
	footerRows = 1
)

// layoutDims holds all pre-computed dimensions so both resizeViewports and
// View use a single consistent source of truth.
type layoutDims struct {
	// appW is passed to appStyle().Width(); rendered = appW, inner = appW - appPadH.
	appW int

	mainW int
	mainH int

	// Input panel.
	inputPanelW int // passed to inputPanelStyle().Width(); same formula as logPanelW

	// Footer.
	footerW int // passed to footerHintStyle().Width(); no border/padding so = rendered width
}

// computeLayout derives all layout dimensions from the terminal size.
func computeLayout(w, h, inputPanelRows int) layoutDims {
	if inputPanelRows <= 0 {
		inputPanelRows = baseInputPanelRows
	}
	// appW fills the terminal; appStyle renders at exactly appW chars.
	appW := w
	// innerW is the usable content width inside appStyle's horizontal padding.
	innerW := appW - appPadH

	mainW := max(16, innerW)
	mainH := max(8, h-(inputPanelRows+footerRows))

	inputPanelW := max(20, innerW-2)

	return layoutDims{
		appW:        appW,
		mainW:       mainW,
		mainH:       mainH,
		inputPanelW: inputPanelW,
		footerW:     innerW,
	}
}

func (m model) layoutDims() layoutDims {
	return computeLayout(m.width, m.height, m.inputPanelRows())
}

func (m model) inputPanelRows() int {
	rows := baseInputPanelRows
	if m.input.Focused() && strings.HasPrefix(strings.TrimSpace(m.input.Value()), "/") {
		rows += min(6, len(filterCommandItems(m.commandQuery()))) + 1
	}
	return rows
}

func (m *model) resizeViewports() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	ld := m.layoutDims()
	m.detailViewport.Width = ld.mainW
	m.detailViewport.Height = ld.mainH
}

func (m *model) syncViewports() {
	m.resizeViewports()
	m.syncCommandList()
	content := m.renderMain()
	if content == m.mainContent && m.detailViewport.YOffset > 0 {
		// Content unchanged but user has scrolled - preserve position
		m.detailViewport.YOffset = clampOffset(m.detailViewport.YOffset, m.detailViewport.TotalLineCount(), m.detailViewport.Height)
		return
	}
	oldOffset := m.detailViewport.YOffset
	m.detailViewport.SetContent(content)
	m.mainContent = content

	// Only auto-scroll to bottom on new content if user was already near bottom
	oldTotal := m.detailViewport.TotalLineCount() + m.detailViewport.Height
	wasNearBottom := oldOffset >= oldTotal-5

	newTotal := m.detailViewport.TotalLineCount()
	newMaxOffset := max(0, newTotal-m.detailViewport.Height)

	if wasNearBottom && newMaxOffset > 0 {
		// User was near bottom, auto-scroll to show new content
		m.detailViewport.SetYOffset(newMaxOffset)
	} else {
		// Preserve scroll position or clamp to valid range
		m.detailViewport.SetYOffset(clampOffset(oldOffset, newTotal, m.detailViewport.Height))
	}
}

func clampOffset(offset, total, height int) int {
	if total <= height {
		return 0
	}
	maxOffset := total - height
	if offset > maxOffset {
		return maxOffset
	}
	if offset < 0 {
		return 0
	}
	return offset
}

func (m *model) syncJobList() {
	ld := m.layoutDims()
	titleMax := max(16, ld.mainW-8)
	descMax := max(24, ld.mainW-8)

	items := make([]list.Item, 0, len(m.queue))
	selectedIndex := 0
	for i, item := range m.queue {
		items = append(items, jobItem{
			id:          item.ID,
			title:       trimLine(item.ID, titleMax),
			description: trimLine(compactQueueSummary(item), descMax),
		})
		if item.ID == m.selectedJobID {
			selectedIndex = i
		}
	}
	m.jobList.SetItems(items)
	if len(items) == 0 {
		return
	}
	if selectedIndex >= len(items) {
		selectedIndex = 0
	}
	m.jobList.Select(selectedIndex)
}
