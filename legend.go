package main

import "strings"

// legend explains every colour and symbol, using the same styles as the
// live display. It is shared by --help and the in-app help screen so the two
// cannot drift apart. Lip Gloss drops the colour when output is not a
// terminal or NO_COLOR is set, so colour names are always spelt out too.
func legend() string {
	g, a, o, r, f, d := sGreen.Render, sAmber.Render, sOrange.Render, sRed.Render, sFaint.Render, sDim.Render
	t := sText.Render
	blocks := func(render func(...string) string, n int) string { return render(strings.Repeat("█", n)) }
	row := func(sample string, w int, text string) string {
		return "  " + padR(sample, w) + t(text) + "\n"
	}

	var b strings.Builder
	b.WriteString(sBright.Render("Chain") + "\n")
	b.WriteString(row(g("● green"), 10, "healthy"))
	b.WriteString(row(a("◐ amber"), 10, "degraded: loss in the last 15 s, 2%+ loss, or Internet jitter over 30 ms"))
	b.WriteString(row(r("✕ red"), 10, "down: the last two probes failed; the problem starts here"))
	b.WriteString(row(f("✕ faint"), 10, "also down, because of an earlier failure"))
	b.WriteString(row(d("○ grey"), 10, "no result yet, or not applicable"))
	b.WriteString("  " + t("The Wi-Fi node turns amber when the signal is below -75 dBm.") + "\n")

	b.WriteString("\n" + sBright.Render("Heatmap") + d(" (one cell per second, coloured against that row's best time)") + "\n")
	b.WriteString("  " + g("█ green") + " " + t("normal") + "   " +
		a("█ amber") + " " + t("slow") + "   " +
		o("█ orange") + " " + t("very slow") + "   " +
		r("×") + " " + t("lost") + "   " +
		f("·") + " " + t("no data yet") + "\n")
	example := blocks(sGreen.Render, 10) + blocks(sAmber.Render, 3) + blocks(sOrange.Render, 2) +
		blocks(sGreen.Render, 2) + r("×××") + blocks(sGreen.Render, 8)
	b.WriteString("  " + example + "  " + t("slowing, very slow, a 3 s outage, then recovery") + "\n")

	b.WriteString("\n" + sBright.Render("Numbers") + d(" (covering the seconds shown in the heatmap)") + "\n")
	b.WriteString(row(d("p50"), 9, "median latency"))
	b.WriteString(row(d("jitter"), 9, "average change between consecutive probes; "+a("amber")+" above 30 ms on Internet"))
	b.WriteString(row(d("loss"), 9, "share of probes lost; "+a("amber")+" above 0%, "+r("red")+" at 50% or more"))

	b.WriteString("\n" + sBright.Render("Session line and log") + d(" (since start or reset)") + "\n")
	b.WriteString("  " + r("✕") + " " + t("outage") + "   " +
		a("!") + " " + t("warning") + "   " +
		g("✓") + " " + t("recovered") + "   " +
		d("↺") + " " + t("network changed") + "\n")
	b.WriteString("  " + t("An outage is two or more consecutive lost probes. One caused by an") + "\n")
	b.WriteString("  " + t("earlier outage isn't counted or logged again.") + "\n")
	return b.String()
}
