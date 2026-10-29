package main

import "strings"

// legend explains every colour and symbol, using the same styles as the
// live display. It is shared by --help and the in-app help screen so the two
// cannot drift apart. Lip Gloss drops the colour when output is not a
// terminal or NO_COLOR is set.
func legend() string {
	g, a, r, f, d := sGreen.Render, sAmber.Render, sRed.Render, sFaint.Render, sDim.Render
	block := func(render func(...string) string, n int) string { return render(strings.Repeat("█", n)) }
	row := func(sample string, w int, text string) string {
		return "  " + padR(sample, w) + "  " + sText.Render(text) + "\n"
	}

	var b strings.Builder
	b.WriteString(sBright.Render("Chain") + "\n")
	b.WriteString(row(g("●")+" Internet "+g("━━━"), 16, "healthy"))
	b.WriteString(row(a("◐")+" Internet "+a("╍╍╍"), 16, "degraded: recent loss, 2%+ loss, or Internet jitter over 30 ms"))
	b.WriteString(row(r("✕")+" Internet "+r("╌╌╌"), 16, "down: the last three probes failed; the problem starts here"))
	b.WriteString(row(f("✕ DNS"), 16, "down because of an earlier failure"))
	b.WriteString(row(d("○")+" Router", 16, "no result yet, or not applicable"))
	b.WriteString(row(a("◐")+" Wi-Fi", 16, "weak signal, below -75 dBm"))

	b.WriteString("\n" + sBright.Render("Heatmap") + d("  one cell per second, coloured against that row's best time") + "\n")
	// Colour names are spelt out because, without colour, the blocks look
	// identical.
	b.WriteString("  " + block(sGreen.Render, 4) + " " + sText.Render("normal") + d(" green") + "   " +
		block(sAmber.Render, 4) + " " + sText.Render("slower") + d(" amber") + "   " +
		block(sOrange.Render, 4) + " " + sText.Render("spike") + d(" orange") + "   " +
		r("××××") + " " + sText.Render("lost") + "   " +
		f("····") + " " + sText.Render("no data yet") + "\n")
	example := block(sGreen.Render, 10) + block(sAmber.Render, 3) + block(sOrange.Render, 2) +
		block(sGreen.Render, 2) + r("×××") + block(sGreen.Render, 8)
	b.WriteString(row(example, 28, "slowing, a spike, a 3 s outage, then recovery"))

	b.WriteString("\n" + sBright.Render("Numbers") + "\n")
	b.WriteString(row(d("loss"), 8, "0% grey · "+a("above 0% amber")+" · "+r("50%+ red")))
	b.WriteString(row(d("jitter"), 8, a("Internet over 30 ms amber")+": video calls start to stutter"))
	b.WriteString(row("", 8, "p50, jitter and loss cover the seconds shown in the heatmap"))

	b.WriteString("\n" + sBright.Render("Session and log") + d("  since start or reset; outages count root causes only") + "\n")
	b.WriteString("  " + r("✕") + " " + sText.Render("outage") + "   " +
		a("!") + " " + sText.Render("warning") + "   " +
		g("✓") + " " + sText.Render("recovered") + "   " +
		d("↺") + " " + sText.Render("network changed") + "\n")
	return b.String()
}
