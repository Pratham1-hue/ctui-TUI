# ⚡ ctui

A tiny, keyboard-driven container dashboard for Windows Terminal.

View and manage Docker or Podman containers, images and volumes — live logs, live CPU/memory, and one-key lifecycle actions — from a single 4 MB executable with no runtime dependencies.

```
╭──────────────────────────────────────────────────────────────────────╮
│  ⚡ CTUI  docker v27.1.1  │  ● 2 running  ‖ 1 paused  ○ 1 stopped │
│  1 Containers 4   2 Images 2   3 Volumes 2                       1/4 │
│ ──────────────────────────────────────────────────────────────────── │
│   STATE         NAME               IMAGE                CPU  STATUS   │
│ ▌ ● running     web-1              nginx:latest        2.5%  Up 2h  │
│   ● running     api-server         ghcr.io/acme/api…   91.0%  Up 5m  │
│   ‖ paused      cache              redis:7                 -  Up 1h  │
│   ○ exited      old-job            busybox                 -  3 days │
│ ──────────────────────────────────────────────────────────────────── │
│ web-1 · abc123def456 · nginx:latest · ⇄ 8080→80/tcp · mem 12.4MiB │
│ ↑↓ move • enter logs • s stop • r restart • i shell • ? help       │
╰──────────────────────────────────────────────────────────────────────╯
```

## Why

Inspired by lazygit and Vessel, ctui exists so you never have to type `docker ps -a` or
scroll through uncoloured `docker logs` output again. It shells out to the container CLI
already on your machine, so there is no SDK, no daemon client library, and nothing to
install besides the binary.

- **Zero bloat** — one static `.exe`, ~4 MB, well under 20 MB RAM
- **Keyboard-centric** — every action is a single key; no typing container IDs
- **Works with Docker and Podman** — auto-detected, or forced with `-runtime podman`
- **Degrades gracefully** — 256/16-colour terminals, narrow windows, and an offline engine are all handled

## Install

1. Grab `dist\ctui.exe` from the [latest release]() (or build it yourself — see below).
2. Put it somewhere on your `PATH`, e.g. `C:\Users\<you>\bin`.
3. Run it inside Windows Terminal or PowerShell:

```powershell
ctui
```

Requirements: Docker Desktop **or** Podman on `PATH`. Nothing else.

## Keyboard reference

### Everywhere

| Key | Action |
| --- | --- |
| `↑` `↓` `k` `j` | move the selection |
| `PgUp` `PgDn` `g` `G` | page / jump to top / bottom |
| `Tab` `←` `→` or `1` `2` `3` | switch tab |
| `/` | filter the current list |
| `esc` | clear filter · close dialog · go back |
| `Ctrl+R` | refresh now |
| `?` | help screen (scrollable) |
| `q` / `Ctrl+C` | quit |
| Mouse | wheel to scroll, click to select a row or tab |

### Containers tab

| Key | Action |
| --- | --- |
| `Enter` / `l` | follow logs |
| `d` | inspect details |
| `s` | start / stop / resume (context-aware) |
| `r` | restart |
| `p` | pause / resume |
| `i` / `e` | interactive shell (bash, sh, or cmd on Windows containers) |
| `x` | remove (asks first) |
| `y` | copy container ID |
| `a` | toggle all / running only |
| `o` | cycle sort: state → name → cpu → memory |

### Logs screen

| Key | Action |
| --- | --- |
| `↑` `↓` `PgUp` `PgDn` `g` `G` | scroll; scrolling pauses the follow |
| `f` | follow on/off |
| `w` | wrap long lines |
| `t` | timestamps |
| `/` | search (matches highlighted) |
| `c` | clear the buffer |
| `r` | reconnect the stream |
| `d` / `i` | inspect the container / shell into it |
| `Esc` / `q` | back to the list |

### Images & Volumes tabs

| Key | Action |
| --- | --- |
| `x` | remove (asks first) |
| `y` | copy reference / name |

### Dialogs

`y` confirms, `n`/`Esc` cancels. Nothing destructive happens before you press `y`.

## Command line

```
Usage of ctui:
  -no-mouse        disable mouse support (keeps native text selection)
  -no-stats        don't sample live CPU / memory usage
  -refresh duration  how often to refresh the container list (default 2s, min 500ms)
  -runtime string    container CLI to use: docker or podman (default: auto-detect)
  -tail int          number of log lines to load when opening logs (default 200)
  -version           print version and exit
```

The runtime can also be set with the `CTUI_RUNTIME` environment variable:

```powershell
$env:CTUI_RUNTIME = "podman"
ctui -refresh 5s -tail 500
```

## Tips

- **`s` is context-aware.** It starts a stopped container, stops a running one, and
  resumes a paused one — one key covers all three states.
- **Scrolling in logs pauses the stream.** Press `G` to jump back to the live tail,
  or `f` to resume following.
- **Filter then act.** Press `/`, type part of a name, and `s`/`x`/`Enter` apply to
  whatever is highlighted.
- **Narrow window?** Columns hide themselves (ID, then ports, then image) instead of
  wrapping. Below 50×12 ctui asks for a bigger window.
- **No Docker daemon running?** ctui shows an offline banner and reconnects
  automatically once you start Docker Desktop (`podman machine start` for Podman).

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `no container runtime found on PATH` | Install Docker Desktop or Podman, or pass `-runtime` |
| Red border + "Can't reach the docker engine" | Start Docker Desktop (or `podman machine start`); ctui retries every refresh |
| `i` says "isn't running" | Start the container first (`s`); shells only exist in running containers |
| Mouse selection doesn't work as expected | Native selection is paused while ctui captures the mouse; use `-no-mouse` to disable capture |
| Copy (`y`) fails | Requires `clip.exe` (built into Windows) |

## Build

```powershell
.\build.ps1               # dev build
.\build.ps1 -Version 1.0.0
```

Requires Go 1.22+. The build is static (`CGO_ENABLED=0`), stripped, and
trimpath'ed. Tests: `go test ./...`.

## Layout

```
ctui/
├── main.go               # flags, engine detection, program setup
├── internal/
│   ├── engine/           # thin docker/podman CLI wrapper (list, stats, images,
│   │                     # volumes, inspect, lifecycle, log streaming)
│   └── ui/               # bubbletea model, commands, rendering, tables, styles
├── build.ps1
└── go.mod / go.sum
```

Stack: Go + [Bubble Tea](https://charm.sh/bubbletea) (reactive TUI),
[Lipgloss](https://github.com/charmbracelet/lipgloss) (borders, colours, layout),
go-runewidth + reflow (ANSI-aware text fitting). No Docker SDK — the CLI is the API,
which keeps the binary tiny and compatible with any runtime that speaks the Docker
CLI dialect.

## License

MIT
