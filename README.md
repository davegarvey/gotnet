# gotnet

A small macOS terminal tool for answering two questions when the internet feels
broken: is it me or them, and is it stable?

```
go build -o gotnet . && ./gotnet
```

The top line is a chain from this Mac to the web. Each node is green (`●`),
degraded (`◐`), down (`✕`) or waiting for its first result (`○`). Only the
first failure is coloured red; failures after it are consequences and are shown
faint.

Below the chain, one row per probe shows a heatmap (one cell per second,
coloured relative to that probe's own baseline, `×` for a lost probe) with p50,
jitter and loss over the last 60 seconds. The log lists outages, Wi-Fi signal
changes, network changes and captive portals. On exit, a session summary is
printed to the terminal.

| Probe    | Method                                                 |
|----------|--------------------------------------------------------|
| Router   | ICMP echo to the default gateway                       |
| Internet | ICMP echo to 1.1.1.1                                   |
| DNS      | A-record query sent directly to the configured server  |
| Web      | HTTP GET of Cloudflare's connectivity check (204)      |

ICMP uses unprivileged datagram sockets, so no sudo is needed. If ICMP is
unavailable it falls back to timing a TCP handshake. Probes run once a second
and a reply slower than 900 ms (2 s for Web) counts as lost.

Keys: `q` quit, `r` reset. Run `gotnet --help` for the colour legend; set `NO_COLOR=1` to turn colour off.
