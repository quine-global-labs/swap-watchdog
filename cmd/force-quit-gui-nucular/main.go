// force-quit-gui-nucular is a prototype rebuild of force-quit-gui on top of
// nucular instead of GTK3, to evaluate dropping the GTK runtime dependency.
//
// Must be built with `-tags nucular_shiny`. Without that tag nucular
// defaults to a Gio-based backend that links cgo + EGL/GLES, which defeats
// the point of the switch; with it, nucular runs on x/exp/shiny's x11driver,
// which is pure Go with no cgo at all.
//
// WINDOW LIFECYCLE vs. the GTK version: x11driver exposes no iconify or
// withdraw call, and nucular's OnClose only fires as a cleanup hook after
// the window manager's close request has already begun tearing the window
// down — there's no intercept-and-cancel like GTK's delete-event handler.
// So clicking the close button always ends the current window.
//
// That turns out not to matter much here. swap-watchdog never talks to this
// process directly — it reveals the window purely externally, by searching
// for its title with kdotool and then un-minimizing/activating it (see
// cmd/swap-watchdog/main.go's revealForceQuitWindow). kdotool is already a
// hard runtime dependency of this project for that reason. So instead of
// patching nucular/x11driver to support a native hide, main() just loops:
// each time the window closes, it opens a fresh one in the same process and
// re-minimizes it with the same kdotool call swap-watchdog itself depends
// on. The process — and its warm Go runtime, the thing that actually matters
// for staying responsive under memory pressure — never exits; only the X
// window is briefly destroyed and recreated. The one cosmetic cost is a
// flicker on manual close, which should be rare since this window is meant
// to stay hidden until the watchdog reveals it.
package main

import (
	"bufio"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
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

const windowTitle = "Force Quit Monitor"

// minimizeSelf shells out to kdotool to minimize this process's own window,
// mirroring what swap-watchdog does in reverse to reveal it. Best-effort: if
// kdotool isn't installed or the window can't be found yet, the window just
// stays visible rather than the app failing to start.
func minimizeSelf() {
	out, err := exec.Command("kdotool", "search", "-t", windowTitle).Output()
	if err != nil {
		log.Printf("kdotool search failed, leaving window visible: %v", err)
		return
	}
	id := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if id == "" {
		log.Printf("kdotool search found no window titled %q", windowTitle)
		return
	}
	if err := exec.Command("kdotool", "windowminimize", id).Run(); err != nil {
		log.Printf("kdotool windowminimize failed: %v", err)
	}
}

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
	scale := uiScale()

	for {
		runWindow(state, scale)
	}
}

// runWindow opens a window, blocks until it's closed (by the user or the
// window manager), and returns. Called in a loop from main so the process
// stays alive across closes — see the file doc comment.
func runWindow(state *appState, scale float64) {
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
		Title: windowTitle,
		Size:  image.Point{X: 700, Y: 450},
	}, update)
	mw.Style().Scale(scale)

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				state.refresh()
				mw.Changed()
			case <-stop:
				return
			}
		}
	}()

	// Deferred so the window has actually been mapped before kdotool goes
	// looking for it, rather than racing the initial show.
	go func() {
		time.Sleep(200 * time.Millisecond)
		minimizeSelf()
	}()

	mw.Main()
	close(stop)
}
