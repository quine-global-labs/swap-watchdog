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
