// gotnet shows, layer by layer, whether this Mac can reach the internet and
// how stable the connection is.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.0"

const help = `gotnet: is it me or them, and is it stable?

Usage:
  gotnet            start monitoring (q quits, r resets)
  gotnet --help     show this help
  gotnet --version  show the version

Probes, once a second:
  Router    ping to your default gateway
  Internet  ping to 1.1.1.1
  DNS       lookup sent directly to your configured DNS server
  Web       HTTP request to cp.cloudflare.com (detects captive portals)
A probe with no reply within 900 ms (2 s for Web) counts as lost.

Chain (top line):
  ●  green   healthy
  ◐  amber   degraded: recent loss, 2%+ loss, or Internet jitter over 30 ms
  ✕  red     down: the last three probes failed; the problem starts here
  ✕  faint   down because of an earlier failure
  ○  grey    no result yet, or not applicable
The Wi-Fi node turns amber below -75 dBm.

Heatmap (one cell per second, relative to that row's best time):
  █  green   close to normal
  █  amber   noticeably slower
  █  orange  spike
  ×  red     lost
  ·  faint   no data yet

p50, jitter and loss cover the seconds shown in the heatmap.
On exit, a summary of the whole session is printed.
Set NO_COLOR=1 to turn colour off.
`

func main() {
	fs := flag.NewFlagSet("gotnet", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are reported below, in one line
	var showVersion bool
	fs.BoolVar(&showVersion, "v", false, "")
	fs.BoolVar(&showVersion, "version", false, "")
	err := fs.Parse(os.Args[1:])
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Print(help)
		return
	case err != nil:
		fail(err.Error())
	case fs.NArg() > 0:
		fail("unexpected argument " + fs.Arg(0))
	case showVersion:
		fmt.Println("gotnet", version)
		return
	}

	p := newProber()
	n := readNetInfo()
	p.setNet(n)

	m := newModel(p, n)
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gotnet:", err)
		os.Exit(1)
	}
	fmt.Print(summary(final.(*model)))
}

// summary is printed on exit so the outcome of a long run survives in the
// terminal's scrollback after the alternate screen is gone.
func summary(m *model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gotnet · %s\n", time.Since(m.start).Round(time.Second))
	for t := range numTargets {
		s := m.series[t]
		if s.sent == 0 {
			continue
		}
		line := fmt.Sprintf("  %-9s p50 %-6s jitter %-6s loss %.1f%%",
			targetNames[t], fmtMs(median(orZero(s.sessionRTTs))),
			fmtMs(s.jitterSum/float64(max(1, s.jitterN))),
			100*float64(s.lost)/float64(s.sent))
		if s.outages > 0 {
			plural := "s"
			if s.outages == 1 {
				plural = ""
			}
			line += fmt.Sprintf("   %d outage%s, %s", s.outages, plural, fmtDur(s.outageTotal))
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

func fail(msg string) {
	fmt.Fprintf(os.Stderr, "gotnet: %s\nRun 'gotnet --help' for usage.\n", msg)
	os.Exit(2)
}

func orZero(xs []float64) []float64 {
	if len(xs) == 0 {
		return []float64{0}
	}
	return xs
}
