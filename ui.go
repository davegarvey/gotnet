package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	netRefresh = 2 * time.Second
	maxEvents  = 5

	// Wi-Fi signal thresholds in dBm, with hysteresis so the node does not
	// flicker around the boundary.
	weakSignal      = -75
	recoveredSignal = -72
)

var (
	cText   = lipgloss.AdaptiveColor{Light: "#444441", Dark: "#D3D1C7"}
	cBright = lipgloss.AdaptiveColor{Light: "#2C2C2A", Dark: "#F1EFE8"}
	cDim    = lipgloss.AdaptiveColor{Light: "#888780", Dark: "#888780"}
	cFaint  = lipgloss.AdaptiveColor{Light: "#D3D1C7", Dark: "#444441"}
	cGreen  = lipgloss.AdaptiveColor{Light: "#1D9E75", Dark: "#5DCAA5"}
	cAmber  = lipgloss.AdaptiveColor{Light: "#BA7517", Dark: "#EF9F27"}
	cOrange = lipgloss.AdaptiveColor{Light: "#D85A30", Dark: "#D85A30"}
	cRed    = lipgloss.AdaptiveColor{Light: "#E24B4A", Dark: "#E24B4A"}

	sText   = lipgloss.NewStyle().Foreground(cText)
	sBright = lipgloss.NewStyle().Foreground(cBright)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sFaint  = lipgloss.NewStyle().Foreground(cFaint)
	sGreen  = lipgloss.NewStyle().Foreground(cGreen)
	sAmber  = lipgloss.NewStyle().Foreground(cAmber)
	sOrange = lipgloss.NewStyle().Foreground(cOrange)
	sRed    = lipgloss.NewStyle().Foreground(cRed)
)

type event struct {
	at     time.Time
	mark   string // already styled
	target string
	text   string
}

type model struct {
	prober *prober
	net    netInfo
	series [numTargets]*series
	start  time.Time

	events    []*event // newest first
	openEvent [numTargets]*event // outage still in progress
	// Session totals count outages as the log does: root causes only, so a
	// Web outage during an Internet outage is not counted twice.
	outages  int
	downtime time.Duration
	weak     bool
	captive   bool

	width, height int
	showHelp      bool
}

type tickMsg time.Time
type netMsg netInfo
type netTickMsg struct{}
type resultMsg struct {
	t   target
	seq int
	rtt time.Duration
	err error
}

func newModel(p *prober, n netInfo) *model {
	m := &model{prober: p, net: n, start: time.Now(), width: 80}
	for i := range m.series {
		m.series[i] = &series{}
	}
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.probeAll(time.Now()), tick(), netTick())
}

func tick() tea.Cmd {
	return tea.Every(probeInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func netTick() tea.Cmd {
	return tea.Tick(netRefresh, func(time.Time) tea.Msg { return netTickMsg{} })
}

func (m *model) probeAll(now time.Time) tea.Cmd {
	cmds := make([]tea.Cmd, 0, numTargets)
	for t := range numTargets {
		seq := m.series[t].add(now)
		cmds = append(cmds, func() tea.Msg {
			rtt, err := m.prober.probe(t)
			return resultMsg{t: t, seq: seq, rtt: rtt, err: err}
		})
	}
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		// While help is open, any key returns to the display. Probes keep
		// running underneath, so no history is lost.
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		switch key {
		case "q", "esc":
			return m, tea.Quit
		case "r":
			m.reset()
		case "h", "?":
			m.showHelp = true
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(m.probeAll(time.Time(msg)), tick())
	case resultMsg:
		m.handleResult(msg)
	case netTickMsg:
		return m, func() tea.Msg { return netMsg(readNetInfo()) }
	case netMsg:
		m.handleNet(netInfo(msg))
		return m, netTick()
	}
	return m, nil
}

func (m *model) reset() {
	for i := range m.series {
		m.series[i] = &series{}
	}
	m.events = nil
	m.openEvent = [numTargets]*event{}
	m.outages, m.downtime = 0, 0
	m.start = time.Now()
}

func (m *model) handleResult(r resultMsg) {
	s := m.series[r.t]
	edge, dur := s.complete(r.seq, r.rtt, r.err)
	name := targetNames[r.t]

	if r.t == targetWeb {
		isCaptive := errors.Is(r.err, errCaptive)
		if isCaptive && !m.captive {
			m.log(event{at: time.Now(), mark: sAmber.Render("!"), target: name, text: "intercepted (captive portal?)"})
		}
		m.captive = isCaptive
	}

	switch edge {
	case edgeStarted:
		// An outage downstream of one already in progress is a consequence,
		// not news, so it is not logged.
		for up := range r.t {
			if m.openEvent[up] != nil {
				return
			}
		}
		m.openEvent[r.t] = m.log(event{at: s.failStart, mark: sRed.Render("✕"), target: name, text: "down"})
	case edgeEnded:
		if e := m.openEvent[r.t]; e != nil {
			e.text = fmtDur(dur) + " outage"
			m.openEvent[r.t] = nil
			m.outages++
			m.downtime += dur
		}
	}
}

// sessionOutages returns root-cause outages so far, including any still in
// progress, and their total duration.
func (m *model) sessionOutages() (count int, down time.Duration, ongoing bool) {
	count, down = m.outages, m.downtime
	for _, e := range m.openEvent {
		if e != nil {
			count++
			down += time.Since(e.at)
			ongoing = true
		}
	}
	return count, down, ongoing
}

// viewSession summarises the whole session in one line, dropping detail
// from the end until it fits.
func (m *model) viewSession(width int) string {
	count, down, ongoing := m.sessionOutages()

	countPart := sDim.Render("no outages")
	downPart := ""
	if count > 0 {
		plural := "s"
		if count == 1 {
			plural = ""
		}
		countPart = sRed.Render(fmt.Sprintf("%d outage%s", count, plural))
		downPart = sText.Render(fmtDur(down) + " down")
	}
	lossPart := ""
	if s := m.series[targetInternet]; s.sent > 0 {
		style := sDim
		if s.lost > 0 {
			style = sAmber
		}
		lossPart = style.Render(fmt.Sprintf("Internet loss %.1f%%", 100*float64(s.lost)/float64(s.sent)))
	}
	nowPart := ""
	if ongoing {
		nowPart = sRed.Render("down now")
	}

	// Narrow terminals lose the loss figure first, then the downtime.
	var line string
	for _, keep := range [][]string{
		{countPart, downPart, lossPart, nowPart},
		{countPart, downPart, nowPart},
		{countPart, nowPart},
	} {
		var parts []string
		for _, p := range keep {
			if p != "" {
				parts = append(parts, p)
			}
		}
		line = sDim.Render(padR("Session", labelW)) + strings.Join(parts, sDim.Render(" · "))
		if lipgloss.Width(line) <= width {
			break
		}
	}
	return line
}

func (m *model) handleNet(n netInfo) {
	prev := m.net
	now := time.Now()
	switch {
	case prev.iface != "" && n.iface == "":
		m.log(event{at: now, mark: sRed.Render("✕"), target: prev.linkLabel(), text: "no network route"})
	case prev.iface == "" && n.iface != "":
		m.log(event{at: now, mark: sGreen.Render("✓"), target: n.linkLabel(), text: "connected (" + n.iface + ")"})
	case prev.iface != n.iface || !prev.gateway.Equal(n.gateway):
		m.log(event{at: now, mark: sDim.Render("↺"), target: n.linkLabel(), text: "network changed (" + prev.iface + " → " + n.iface + ")"})
	}

	if n.onWiFi() && n.rssi != 0 {
		if !m.weak && n.rssi < weakSignal {
			m.weak = true
			m.log(event{at: now, mark: sAmber.Render("!"), target: "Wi-Fi", text: fmt.Sprintf("weak signal (%d dBm)", n.rssi)})
		} else if m.weak && n.rssi > recoveredSignal {
			m.weak = false
			m.log(event{at: now, mark: sGreen.Render("✓"), target: "Wi-Fi", text: fmt.Sprintf("signal recovered (%d dBm)", n.rssi)})
		}
	} else {
		m.weak = false
	}

	m.net = n
	m.prober.setNet(n)
}

// log prepends an event and returns it so an in-progress outage can be
// updated when it ends.
func (m *model) log(e event) *event {
	m.events = append([]*event{&e}, m.events...)
	if len(m.events) > maxEvents {
		m.events = m.events[:maxEvents]
	}
	return &e
}

// ---- view ----

const (
	labelW = 10
	numW   = 8

	// The heatmap is one minute wide at most. In narrower terminals it
	// shrinks to minHeat, then the jitter column goes, then it shrinks
	// again down to floorHeat. Below that, lines wrap.
	minHeat   = 30
	floorHeat = 20
	defaultW  = labelW + maxSamples + 3*numW // before the first resize
)

// layout is sized to the content, not the terminal: a wide terminal gets
// the same 94-column display as a standard one.
type layout struct {
	heatW  int
	jitter bool
	width  int
}

func (m *model) layout() layout {
	w := m.width
	if w == 0 {
		w = defaultW
	}
	l := layout{jitter: true}
	cols := 3
	if w-labelW-cols*numW < minHeat {
		l.jitter = false
		cols = 2
	}
	l.heatW = min(maxSamples, max(floorHeat, w-labelW-cols*numW))
	l.width = labelW + l.heatW + cols*numW
	return l
}

func (m *model) header(title string, width int) string {
	t := sBright.Render(title)
	elapsed := sDim.Render(fmtElapsed(time.Since(m.start)))
	gap := max(1, width-lipgloss.Width(t)-lipgloss.Width(elapsed))
	return t + strings.Repeat(" ", gap) + elapsed + "\n\n"
}

func (m *model) View() string {
	var b strings.Builder
	l := m.layout()

	if m.showHelp {
		b.WriteString(m.header("gotnet · help", defaultW))
		b.WriteString(legend())
		b.WriteString("\n" + sDim.Render("any key to return · monitoring continues"))
		return b.String()
	}

	termW := m.width
	if termW == 0 {
		termW = defaultW
	}
	chain := m.viewChain(termW)
	width := max(l.width, lipgloss.Width(chain))

	b.WriteString(m.header("gotnet", width))
	b.WriteString(chain + "\n\n")

	cols := padL("p50", numW)
	if l.jitter {
		cols += padL("jitter", numW)
	}
	cols += padL("loss", numW)
	b.WriteString(strings.Repeat(" ", labelW+l.heatW) + sDim.Render(cols) + "\n")
	for t := range numTargets {
		b.WriteString(m.viewRow(t, l) + "\n")
	}

	b.WriteString("\n" + m.viewSession(max(l.width, termW)) + "\n")
	if len(m.events) == 0 {
		b.WriteString(sFaint.Render("No events yet") + "\n")
	}
	for _, e := range m.events {
		fmt.Fprintf(&b, "%s  %s %s %s\n",
			sDim.Render(e.at.Format("15:04:05")), e.mark,
			sText.Render(padR(e.target, 9)), sText.Render(e.text))
	}

	b.WriteString("\n" + sDim.Render("q quit · r reset · h help"))
	return b.String()
}

type node struct {
	label string
	state nodeState
}

func (m *model) nodes() []node {
	mac := stateOK
	if !m.net.hasIP {
		mac = stateDown
	}
	link := stateOK
	switch {
	case m.net.iface == "":
		link = stateDown
	case m.weak:
		link = stateDegraded
	}
	ns := []node{{"Mac", mac}, {m.net.linkLabel(), link}}
	for t := range numTargets {
		st := m.series[t].state(t, m.layout().heatW)
		if t == targetRouter && m.net.gateway == nil && m.net.iface != "" {
			st = statePending // no gateway to probe (e.g. full-tunnel VPN)
		}
		ns = append(ns, node{targetNames[t], st})
	}
	return ns
}

// shortLabels are used when the chain will not otherwise fit.
var shortLabels = map[string]string{"Router": "Rtr", "Internet": "Net", "Ethernet": "Eth"}

// viewChain renders the chain at full size if it fits in maxW, otherwise
// with short connectors, otherwise with short labels too.
func (m *model) viewChain(maxW int) string {
	ns := m.nodes()
	var s string
	for _, v := range []struct{ compact, short bool }{{false, false}, {true, false}, {true, true}} {
		s = renderChain(ns, v.compact, v.short)
		if lipgloss.Width(s) <= maxW {
			break
		}
	}
	return s
}

func renderChain(ns []node, compact, short bool) string {
	var parts []string
	seenDown := false
	for i, n := range ns {
		if short && shortLabels[n.label] != "" {
			n.label = shortLabels[n.label]
		}
		// Only the first failure is the cause; later ones are consequences
		// and are shown faint so the eye goes to the right place.
		consequence := n.state == stateDown && seenDown
		if n.state == stateDown {
			seenDown = true
		}
		mark, line, style := "●", "━━━", sGreen
		switch {
		case consequence:
			mark, line, style = "✕", "╌╌╌", sFaint
		case n.state == statePending:
			mark, line, style = "○", "───", sDim
		case n.state == stateDegraded:
			mark, line, style = "◐", "╍╍╍", sAmber
		case n.state == stateDown:
			mark, line, style = "✕", "╌╌╌", sRed
		}
		label := sText.Render(n.label)
		if consequence {
			label = sFaint.Render(n.label)
		}
		if compact {
			line = line[:len(line)/3] // each box-drawing rune is 3 bytes
		}
		if i > 0 {
			parts = append(parts, style.Render(line))
		}
		parts = append(parts, style.Render(mark)+" "+label)
	}
	return strings.Join(parts, " ")
}

func (m *model) viewRow(t target, l layout) string {
	s := m.series[t]
	heatW := l.heatW
	ws := s.window(heatW)

	var heat strings.Builder
	n := len(s.samples)
	if n < heatW {
		heat.WriteString(sFaint.Render(strings.Repeat("·", heatW-n)))
	}
	for _, sm := range s.samples[max(0, n-heatW):] {
		heat.WriteString(cell(sm, ws.base))
	}

	p50, jit, loss := "–", "–", "–"
	lossStyle := sDim
	if ws.n > 0 {
		loss = fmt.Sprintf("%.0f%%", ws.loss*100)
		if ws.loss > 0 && ws.loss < 0.01 {
			loss = fmt.Sprintf("%.1f%%", ws.loss*100)
		}
		if ws.loss > 0 {
			lossStyle = sAmber
		}
		if ws.loss >= 0.5 {
			lossStyle = sRed
		}
	}
	if ws.base > 0 {
		p50, jit = fmtMs(ws.p50), fmtMs(ws.jitter)
	}
	jitStyle := sText
	if t == targetInternet && ws.jitter > jitterLimit {
		jitStyle = sAmber
	}

	row := sDim.Render(padR(targetNames[t], labelW)) + heat.String() + sText.Render(padL(p50, numW))
	if l.jitter {
		row += jitStyle.Render(padL(jit, numW))
	}
	return row + lossStyle.Render(padL(loss, numW))
}

// cell colours one second of history relative to the target's own baseline,
// so 20 ms reads as fine for the internet but poor for the router.
func cell(sm sample, base float64) string {
	switch {
	case !sm.done, sm.skipped:
		return sFaint.Render("·")
	case sm.err != nil:
		return sRed.Render("×")
	}
	ms := float64(sm.rtt) / float64(time.Millisecond)
	excess := ms - base
	switch {
	case excess < max(base*0.6, 10):
		return sGreen.Render("█")
	case excess < max(base*2, 50):
		return sAmber.Render("█")
	default:
		return sOrange.Render("█")
	}
}

func fmtMs(ms float64) string {
	if ms < 1 {
		return "<1ms"
	}
	return fmt.Sprintf("%.0fms", ms)
}

func fmtDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func fmtElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

func padL(s string, w int) string { return strings.Repeat(" ", max(0, w-lipgloss.Width(s))) + s }
func padR(s string, w int) string { return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s))) }
