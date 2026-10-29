# AGENTS.md

gotnet is a macOS terminal tool that shows, layer by layer, whether the
machine can reach the internet and how stable the connection is. It is
written in Go with Bubble Tea and Lip Gloss.

## Build and run

```
go build -o gotnet .   # build in place
./gotnet               # run
go install .           # install to ~/go/bin
```

macOS only: `netinfo_darwin.go` uses cgo and CoreWLAN, so the Xcode command
line tools are required.

## Versioning

The version is the `version` constant in `main.go`, printed by
`gotnet --version`. It is maintained by hand and does not come from git tags
or build flags.

- Use semantic versioning: `MAJOR.MINOR.PATCH`.
- Bump PATCH for fixes, MINOR for new features or flags, MAJOR for changes
  that break documented behaviour (flags, exit codes, summary format).
- Bump the constant in the same change as the work it describes, not
  separately afterwards.
- If the project later adopts git tags, keep the tag and the constant in step.

## Code notes

- macOS delivers every ICMP echo reply to every unprivileged ICMP socket.
  `pinger` must keep matching replies on ID, per-process token and peer
  address, or other processes' pings will be counted as successes.
- The display is deliberately fixed at 94 columns, with a 60-second heatmap.
  p50, jitter and loss must always cover exactly the seconds the heatmap
  shows.
- Keep the UI restrained: colour only for state, and every state also has a
  symbol. The colour legend lives in `--help`, not on screen.
