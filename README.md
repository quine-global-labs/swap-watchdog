# swap-watchdog

A background watchdog + always-ready "Force Quit" GUI for Linux desktops,
for when swap fills up and the whole desktop grinds to a halt. Built after
a real Firefox flatpak process ran away to 3.3GB RSS + 5.8GB swap, swap hit
100% full, and the desktop stayed unresponsive for a long stretch before
`systemd-oomd` eventually killed it on its own.

## What it does

- **`cmd/swap-watchdog`** polls every 3s for real memory distress:
  - `SwapFree/SwapTotal` from `/proc/meminfo` — critical if under 1%.
  - `full avg10` from `/proc/pressure/memory` (kernel PSI — the same signal
    `systemd-oomd` itself uses) — critical if over 10.0.
  - On the rising edge into "critical" (not on every poll while it stays
    critical, so it doesn't keep yanking the window back if you minimize it
    again while still investigating), it reveals the Force Quit Monitor
    window.
  - Both thresholds are overridable via `SWAP_PCT_THRESHOLD` /
    `PSI_THRESHOLD` env vars, specifically so this can be tested without
    rebuilding.
  - Logs every trigger to `~/.local/state/swap-watchdog/watchdog.log`.

- **`cmd/force-quit-gui`** is a plain GTK3 window (via `gotk3`) titled
  "Force Quit Monitor", listing processes sorted by RSS+Swap descending,
  refreshed every 2s, with a **Force Quit Selected** button
  (`SIGKILL`s the selected PID directly — no `pkexec`, since it only needs
  to kill the current user's own processes).

## The cold-start problem, and how this avoids it

The whole point is to still work when the system is least able to do
anything — including launch a new process. So neither binary is spawned
fresh at crisis time:

- The GUI starts once (at login, via its systemd unit) and stays running,
  minimized the rest of the time. The watchdog's job at trigger time is
  just to *reveal* it — `kdotool search -t "Force Quit Monitor"` →
  `kdotool windowstate --remove MINIMIZED --add ABOVE` →
  `kdotool windowactivate`. No process spawn happens at the moment it's
  needed.
- Closing the GUI's window (the X button) just re-minimizes it rather than
  exiting — it has to keep running to be revealed again later.
- Both services get `OOMScoreAdjust=-1000` in their systemd units, so
  neither is itself a candidate for the kernel OOM killer or
  `systemd-oomd` exactly when memory is critical.
- Polling two small `/proc` pseudo-files every few seconds is negligible
  cost regardless of system load.

Scope note: this only keeps things workable within the existing KWin/Wayland
session (always-on-top + activate). It does not fall back to a virtual
terminal if the compositor itself is too starved to render — that's a
deliberately separate, bigger problem, not handled here.

## Requirements

- `dagger` CLI + a container runtime (podman or docker) — the build runs
  in an ephemeral Nix-provisioned container (see `.dagger/main.go`), so
  `gtk3-devel`, `dbus-devel`, and a Rust toolchain (needed to build
  `kdotool`, which isn't packaged for Fedora) never have to be installed on
  the host. Only the resulting binaries' *runtime* libraries matter, and
  any normal desktop — GTK-based or not — already has `libgtk-3`,
  `libdbus-1`, etc., since KDE/GNOME portals and GTK-using apps pull them
  in regardless of your actual desktop toolkit.
- Built and tested against `gotk3 v0.6.3` — v0.6.4 fails to compile its
  `gdk` package against this system's GTK3 (3.24.52) with an `undefined:
  callback` error in `gdk_since_3_22.go`'s `Seat.Grab`, an unrelated
  function this project never calls but which still blocks the build since
  Go compiles the whole package.

## `force-quit-gui-nucular`: evaluating a GTK-free rebuild

`cmd/force-quit-gui-nucular` is a prototype that reimplements
`force-quit-gui` on top of [nucular](https://github.com/aarzilli/nucular)
instead of GTK3, to see whether the cgo/GTK3 dependency above (`gotk3`,
`libgtk-3`, `libdbus-1`, the version-pinning pain) can be dropped in favor of
a pure-Go GUI stack. It's evaluation-only — not wired into `install.sh` or
the Dagger pipeline — build and run it by hand:

```
go build -tags nucular_shiny -o bin/force-quit-gui-nucular ./cmd/force-quit-gui-nucular
```

**Why `-tags nucular_shiny` specifically:** nucular's default backend
(without the tag) is [gio](https://gioui.org), and gio's own Linux backend
turns out not to be pure Go either — it cgo-links `libEGL` directly
(`#cgo linux,!android pkg-config: egl` in `gioui.org/internal/egl`), plus,
depending on the windowing path it picks, `libX11`, `libxkbcommon`,
`libxkbcommon-x11`, `libX11-xcb`, `libXcursor`, `libXfixes` (X11) or
`wayland-egl` (Wayland). That's a different native dependency chain than
GTK3, not a smaller one — building the default way would just trade one cgo
toolkit for another and defeat the point of the prototype. The
`nucular_shiny` tag switches nucular to `golang.org/x/exp/shiny`'s
`x11driver` backend instead, which speaks the X11 protocol directly in pure
Go — verified by checking shiny's driver source for `import "C"`: there
isn't one, outside an unrelated macOS-only tool elsewhere in `x/exp`. So the
original reasoning holds: we *do* actually need the tag, for as long as
"no cgo" stays the actual goal of this prototype.

One asterisk: `go.mod`/`go.sum` still list `gioui.org` and its whole
dependency tree (`go-gl/glfw`, `go-text/typesetting`, `x/mobile`, `x/image`,
etc.) as indirect requirements regardless — nucular's own `go.mod` requires
gio unconditionally, since Go's module graph resolution doesn't know about
build tags. None of that code is ever compiled or linked into the binary
when `nucular_shiny` is set; it just rides along in the dependency graph
without costing anything at link time.

The trade-off for going pure-Go: `x11driver` has no HiDPI awareness of its
own (worked around in `main.go`'s `uiScale()`, which reads `Xft.dpi` off the
`RESOURCE_MANAGER` property directly) and exposes no iconify/withdraw call
(worked around by the close-reopens-a-fresh-window respawn loop documented
at the top of `main.go`).

Confirmed by actually building it (`go build -tags nucular_shiny`) and
running `ldd` on the result: `linux-vdso.so.1`, `libresolv.so.2`,
`libc.so.6`, `ld-linux-x86-64.so.2` — nothing else. No X11, no EGL, no
xkbcommon, no GTK.

## Install

```
./install.sh
```

Builds `swap-watchdog`, `force-quit-gui`, and `kdotool` via Dagger into
`bin/`, copies `kdotool` to `~/.local/bin`, installs the systemd user units,
and enables + starts both services.

The unit files' `ExecStart=` point at `%h/Code/swap-watchdog/bin/...` — if
you clone this somewhere other than `~/Code/swap-watchdog`, edit the unit
files to match before running `install.sh`.

## Uninstall

```
./uninstall.sh
```

Stops and disables both services and removes the unit files. Leaves the
built binaries and the log in place.

## Testing without a real crisis

```
SWAP_PCT_THRESHOLD=100 PSI_THRESHOLD=-1 ./bin/swap-watchdog
```

Forces an immediate trigger regardless of actual system state (thresholds
set so the very first poll counts as critical) — confirms the reveal path
end-to-end against the real running GUI instance.
