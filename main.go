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

const version = "0.2.3"

// The help text follows clig.dev: purpose first, then usage, flags, keys
// and probes; the shared colour legend; then environment, exit status and
// where to find the source.
const usage = `gotnet shows, layer by layer, whether this Mac can reach the internet and
how stable the connection is.

Usage:
  gotnet [flags]

Flags:
  -h, --help     show this help
  -v, --version  show the version

Keys:
  q   quit and print a session summary
  r   reset history
  h   show this legend in the app (also ?)

Probes, once a second (no reply within 900 ms, or 2 s for Web, counts as lost):
  Router    ping to your default gateway
  Internet  ping to 1.1.1.1
  DNS       lookup sent directly to your configured DNS server
  Web       HTTP request to cp.cloudflare.com, to detect captive portals

`

const usageFooter = `
Environment:
  NO_COLOR   set to any value to turn colour off

Exit status: 0 success, 1 error, 2 usage error.
Source and issues: https://github.com/davegarvey/gotnet
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
		fmt.Print(usage + legend() + usageFooter)
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
	header := "no outages"
	if count, down, _ := m.sessionOutages(); count > 0 {
		plural := "s"
		if count == 1 {
			plural = ""
		}
		header = fmt.Sprintf("%d outage%s, %s down", count, plural, fmtDur(down))
	}
	fmt.Fprintf(&b, "gotnet · %s · %s\n", time.Since(m.start).Round(time.Second), header)
	for t := range numTargets {
		s := m.series[t]
		if s.sent == 0 {
			continue
		}
		rtts := orZero(s.sessionRTTs)
		line := fmt.Sprintf("  %-9s p50 %-6s p95 %-6s jitter %-6s loss %.1f%%",
			targetNames[t], fmtMs(median(rtts)), fmtMs(percentile(rtts, 95)),
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
