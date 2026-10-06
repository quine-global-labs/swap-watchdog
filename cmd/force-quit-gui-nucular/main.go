// force-quit-gui-nucular is a prototype rebuild of force-quit-gui on top of
// nucular instead of GTK3, to evaluate dropping the GTK runtime dependency.
//
// Must be built with `-tags nucular_shiny`. Without that tag nucular
// defaults to a Gio-based backend that links cgo + EGL/GLES, which defeats
// the point of the switch; with it, nucular runs on x/exp/shiny's x11driver,
// which is pure Go with no cgo at all.
//
// KNOWN GAP vs. the GTK version: x11driver exposes no iconify/withdraw call,
// and nucular's OnClose only fires as a cleanup hook after the window
// manager's close request has already begun tearing the window down — there
// is no way to intercept delete-and-cancel like GTK's delete-event handler.
// So, unlike force-quit-gui, clicking the window's close button here exits
// the process instead of hiding it. Whatever reveals this window (currently
// swap-watchdog, expecting an always-resident process) would need to relaunch
// it rather than un-hide it. See TODO below for the planned fix.
//
// TODO: investigate extending nucular (or vendoring a thin wrapper around
// x11driver) to support withdrawing/remapping the X11 window directly via
// its xgb connection, so close-to-hide can work without pulling in cgo.
package main

import (
	"bufio"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aarzilli/nucular"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// uiScale reads Xft.dpi from the X RESOURCE_MANAGER property (the same value
// GTK/Qt use to scale HiDPI desktops) and converts it to a nucular style
// scale factor. nucular's x11driver backend has no HiDPI awareness of its
// own — without this, text renders at a fixed size regardless of the
// desktop's configured scale (e.g. KDE's 144 DPI == 1.5x on this machine),
// which is why the first prototype build looked too small.
func uiScale() float64 {
	const defaultDPI = 96.0

	conn, err := xgb.NewConn()
	if err != nil {
		return 1.0
	}
	defer conn.Close()

	root := xproto.Setup(conn).DefaultScreen(conn).Root
	reply, err := xproto.GetProperty(conn, false, root, xproto.AtomResourceManager, xproto.AtomString, 0, (1<<32)-1).Reply()
	if err != nil || reply == nil {
		return 1.0
	}

	for _, line := range strings.Split(string(reply.Value), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "Xft.dpi" {
			if dpi, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && dpi > 0 {
				return dpi / defaultDPI
			}
		}
	}
	return 1.0
}

const refreshInterval = 2 * time.Second

type procInfo struct {
	pid     int
	name    string
	rssKB   int64
	swapKB  int64
	totalKB int64
}

func readProcesses() []procInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var procs []procInfo
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		status, err := os.Open(filepath.Join("/proc", e.Name(), "status"))
		if err != nil {
			continue
		}
		var rss, swap int64
		scanner := bufio.NewScanner(status)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "VmRSS:":
				rss, _ = strconv.ParseInt(fields[1], 10, 64)
			case "VmSwap:":
				swap, _ = strconv.ParseInt(fields[1], 10, 64)
			}
		}
		status.Close()

		if rss == 0 && swap == 0 {
			continue
		}

		name := e.Name()
		if commBytes, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm")); err == nil {
			name = strings.TrimSpace(string(commBytes))
		}

		procs = append(procs, procInfo{pid: pid, name: name, rssKB: rss, swapKB: swap, totalKB: rss + swap})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].totalKB > procs[j].totalKB })
	return procs
}

type appState struct {
	mu         sync.Mutex
	procs      []procInfo
	selected   int // pid, -1 for none
	statusText string
}

func (s *appState) refresh() {
	procs := readProcesses()
	s.mu.Lock()
	s.procs = procs
	s.mu.Unlock()
}

func main() {
	state := &appState{selected: -1}
	state.refresh()

	var mw nucular.MasterWindow

	update := func(w *nucular.Window) {
		state.mu.Lock()
		procs := state.procs
		selected := state.selected
		statusText := state.statusText
		state.mu.Unlock()

		w.Row(20).Dynamic(1)
		w.Label("Sorted by memory + swap used, highest first. Select a process and force quit it.", "LC")

		// Static() scales its width slice in place, so a shared slice must
		// never be passed via `widths...` to more than one row — each call
		// would compound the previous call's scaling. Pass fresh literals.
		w.Row(22).Static(70, 260, 90, 90, 90)
		w.Label("PID", "LC")
		w.Label("Name", "LC")
		w.Label("RSS (MB)", "LC")
		w.Label("Swap (MB)", "LC")
		w.Label("Total (MB)", "LC")

		for _, p := range procs {
			w.Row(22).Static(70, 260, 90, 90, 90)
			isSelected := p.pid == selected
			wasSelected := isSelected
			w.SelectableLabel(strconv.Itoa(p.pid), "LC", &isSelected)
			if isSelected && !wasSelected {
				state.mu.Lock()
				state.selected = p.pid
				state.mu.Unlock()
			}
			w.Label(p.name, "LC")
			w.Label(fmt.Sprintf("%.1f", float64(p.rssKB)/1024), "LC")
			w.Label(fmt.Sprintf("%.1f", float64(p.swapKB)/1024), "LC")
			w.Label(fmt.Sprintf("%.1f", float64(p.totalKB)/1024), "LC")
		}

		w.Row(20).Dynamic(1)
		w.Label(statusText, "LC")

		w.Row(28).Dynamic(1)
		if w.ButtonText("Force Quit Selected") {
			state.mu.Lock()
			pid := state.selected
			state.mu.Unlock()

			var msg string
			if pid < 0 {
				msg = "No process selected."
			} else if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
				msg = fmt.Sprintf("Failed to kill PID %d: %v", pid, err)
			} else {
				msg = fmt.Sprintf("Killed PID %d.", pid)
			}

			state.mu.Lock()
			state.statusText = msg
			state.mu.Unlock()
			state.refresh()
		}
	}

	mw = nucular.NewMasterWindowOptions(0, nucular.NewWindowOptions{
		Title: "Force Quit Monitor",
		Size:  image.Point{X: 700, Y: 450},
	}, update)
	mw.Style().Scale(uiScale())

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for range ticker.C {
			state.refresh()
			mw.Changed()
		}
	}()

	mw.Main()
	log.Println("force-quit-gui-nucular exiting")
}
