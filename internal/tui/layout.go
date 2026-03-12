package tui

import "github.com/charmbracelet/bubbles/list"

// Layout constants.
//
// lipgloss Width() sets inner size (content + padding), border is added on top:
//   panelStyle   has Padding(0,1) + RoundedBorder → rendered = Width() + 2 (border)
//   appStyle     has Padding(0,1), no border       → rendered = Width()
//
// So to fit a panel inside inner width W:  panel.Width(W - 2)
// Inner width of appStyle.Width(appW):     appW - 2  (horizontal padding eats 2)
const (
	// panelBorderH is the horizontal border overhead added to Width() by panelStyle.
	panelBorderH = 2
	// panelBorderV is the vertical border overhead (top + bottom) added by panelStyle.
	panelBorderV = 2
	// panelPadH is the horizontal padding inside panelStyle (Padding(0,1) = left+right = 2).
	panelPadH = 2
	// appPadH is the horizontal padding of appStyle (Padding(0,1) = left+right = 2).
	appPadH = 2

	// headerRows: brand(1) + subtitle(1) + meta(1) + stats-chips(3) + bottom-pad(1) = 7
	headerRows = 7
	// inputContentRows: title(1) + hint(1) + blank(1) + input-field(1) = 4
	inputContentRows = 4
	// inputPanelRows: inputContentRows + panelBorderV (top+bottom border)
	inputPanelRows = inputContentRows + panelBorderV
	// footerRows: one line of key hints
	footerRows = 1
	// vertFixed is the total rows consumed by non-scrollable sections.
	vertFixed = headerRows + inputPanelRows + footerRows
)

// layoutDims holds all pre-computed dimensions so both resizeViewports and
// View use a single consistent source of truth.
type layoutDims struct {
	// appW is passed to appStyle().Width(); rendered = appW, inner = appW - appPadH.
	appW int

	// Body row: list (left) + detail panel (right).
	listW       int // list.SetSize width = exactly listW rendered chars
	bodyH       int // total height of the body row
	rightPanelW int // passed to panelStyle().Width(); rendered = rightPanelW + panelBorderH
	detailW     int // detailViewport.Width  (fits inside rightPanelW - panelPadH)
	detailH     int // detailViewport.Height (= bodyH - panelBorderV)

	// Log panel.
	logPanelW int // passed to panelStyle().Width(); rendered = logPanelW + panelBorderH
	logW      int // logViewport.Width
	logH      int // logViewport.Height

	// Input panel.
	inputPanelW int // passed to inputPanelStyle().Width(); same formula as logPanelW

	// Footer.
	footerW int // passed to footerHintStyle().Width(); no border/padding so = rendered width
}

// computeLayout derives all layout dimensions from the terminal size.
func computeLayout(w, h int) layoutDims {
	// appW fills the terminal; appStyle renders at exactly appW chars.
	appW := w
	// innerW is the usable content width inside appStyle's horizontal padding.
	innerW := appW - appPadH

	// Vertical split: remaining rows after fixed sections go to body + log.
	available := h - vertFixed
	if available < 6 {
		available = 6
	}
	bodyH := max(6, available*3/5)
	logAllocH := max(4, available-bodyH)

	// Horizontal split: list takes ~1/3, detail panel takes the rest.
	listW := max(28, innerW/3)

	// right panel rendered width = rightPanelW + panelBorderH.
	// Constraint: listW + 1 (gap) + rightPanelW + panelBorderH = innerW
	rightPanelW := max(20, innerW-listW-1-panelBorderH)
	// detailViewport fills the panel's inner content area (panel padding eats panelPadH).
	detailW := max(16, rightPanelW-panelPadH)
	detailH := max(4, bodyH-panelBorderV)

	// log/input panels span full inner width.
	// Constraint: logPanelW + panelBorderH = innerW
	logPanelW := max(20, innerW-panelBorderH)
	logW := max(16, logPanelW-panelPadH)
	logH := max(0, logAllocH-panelBorderV)

	return layoutDims{
		appW:        appW,
		listW:       listW,
		bodyH:       bodyH,
		rightPanelW: rightPanelW,
		detailW:     detailW,
		detailH:     detailH,
		logPanelW:   logPanelW,
		logW:        logW,
		logH:        logH,
		inputPanelW: logPanelW,
		footerW:     innerW,
	}
}

func (m *model) resizeViewports() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	ld := computeLayout(m.width, m.height)
	m.jobList.SetSize(ld.listW, ld.bodyH)
	m.detailViewport.Width = ld.detailW
	m.detailViewport.Height = ld.detailH
	m.logViewport.Width = ld.logW
	m.logViewport.Height = ld.logH
}

func (m *model) syncViewports() {
	m.resizeViewports()
	m.syncJobList()
	m.detailViewport.SetContent(m.renderDetail())
	m.logViewport.SetContent(m.renderMessages())
	m.logViewport.GotoBottom()
}

func (m *model) syncJobList() {
	ld := computeLayout(m.width, m.height)
	titleMax := max(16, ld.listW-4)
	descMax := max(24, ld.listW-4)

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
