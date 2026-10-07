// force-quit-gui is a minimal, always-running process killer. It starts
// minimized at login and is revealed by swap-watchdog when memory pressure
// gets critical. Deliberately plain GTK3 (via gotk3) rather than anything
// heavier, so it has the best chance of already being alive and responsive
// exactly when the system is least able to start something new.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const refreshIntervalMs = 2000

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

type memStatus struct {
	ramUsedKB, ramTotalKB   int64
	swapUsedKB, swapTotalKB int64
}

// readMemStatus reports system-wide RAM and swap usage for the accounting
// line under the warning banner, as distinct from readProcesses' per-process
// breakdown.
func readMemStatus() memStatus {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return memStatus{}
	}
	defer f.Close()

	var memTotal, memAvailable, swapTotal, swapFree int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			memTotal = val
		case "MemAvailable:":
			memAvailable = val
		case "SwapTotal:":
			swapTotal = val
		case "SwapFree:":
			swapFree = val
		}
	}
	return memStatus{
		ramUsedKB:   memTotal - memAvailable,
		ramTotalKB:  memTotal,
		swapUsedKB:  swapTotal - swapFree,
		swapTotalKB: swapTotal,
	}
}

func gb(kb int64) float64 {
	return float64(kb) / (1024 * 1024)
}

func accountingText(m memStatus) string {
	var swapPct float64
	if m.swapTotalKB > 0 {
		swapPct = float64(m.swapUsedKB) / float64(m.swapTotalKB) * 100
	}
	return fmt.Sprintf(
		"Swap: %.2f GB used / %.2f GB total (%.0f%% full)    RAM: %.2f GB used / %.2f GB total",
		gb(m.swapUsedKB), gb(m.swapTotalKB), swapPct,
		gb(m.ramUsedKB), gb(m.ramTotalKB),
	)
}

func main() {
	gtk.Init(nil)

	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatalf("could not create window: %v", err)
	}
	win.SetTitle("Force Quit Monitor")
	win.SetDefaultSize(700, 450)

	// Closing the window just hides it — this process must keep running so
	// the watchdog can reveal it again later without spawning anything new.
	win.Connect("delete-event", func() bool {
		win.Iconify()
		return true
	})

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 8)
	box.SetBorderWidth(8)
	win.Add(box)

	warningLabel, _ := gtk.LabelNew("")
	warningLabel.SetMarkup(`<span foreground="red" weight="bold" size="x-large">⚠ SWAP IS FULL</span>`)
	warningLabel.SetHAlign(gtk.ALIGN_START)
	box.PackStart(warningLabel, false, false, 0)

	accountingLabel, _ := gtk.LabelNew(accountingText(readMemStatus()))
	accountingLabel.SetHAlign(gtk.ALIGN_START)
	box.PackStart(accountingLabel, false, false, 0)

	label, _ := gtk.LabelNew("Sorted by memory + swap used, highest first. Select a process and force quit it.")
	label.SetHAlign(gtk.ALIGN_START)
	box.PackStart(label, false, false, 0)

	// Columns: PID, Name, RSS (MB), Swap (MB), Total (MB)
	store, _ := gtk.ListStoreNew(glib.TYPE_INT, glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_STRING)

	treeView, _ := gtk.TreeViewNewWithModel(store)
	addColumn := func(title string, idx int) {
		renderer, _ := gtk.CellRendererTextNew()
		col, _ := gtk.TreeViewColumnNewWithAttribute(title, renderer, "text", idx)
		col.SetSortColumnID(idx)
		treeView.AppendColumn(col)
	}
	addColumn("PID", 0)
	addColumn("Name", 1)
	addColumn("RSS (MB)", 2)
	addColumn("Swap (MB)", 3)
	addColumn("Total (MB)", 4)

	scrolled, _ := gtk.ScrolledWindowNew(nil, nil)
	scrolled.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	scrolled.Add(treeView)
	box.PackStart(scrolled, true, true, 0)

	statusLabel, _ := gtk.LabelNew("")
	statusLabel.SetHAlign(gtk.ALIGN_START)
	box.PackStart(statusLabel, false, false, 0)

	killButton, _ := gtk.ButtonNewWithLabel("Force Quit Selected")
	box.PackStart(killButton, false, false, 0)

	// selectedPID reads the PID column of the current selection, if any.
	// Used both to act on the selected row and, in refresh, to find the
	// matching row again after the store is rebuilt — GtkTreeSelection
	// tracks a TreeIter, which store.Clear() invalidates, so without this
	// every refresh would silently drop the highlight even though nothing
	// the user cares about changed.
	selectedPID := func() (int, bool) {
		sel, err := treeView.GetSelection()
		if err != nil {
			return 0, false
		}
		model, iter, ok := sel.GetSelected()
		if !ok {
			return 0, false
		}
		val, err := model.(*gtk.TreeModel).GetValue(iter, 0)
		if err != nil {
			return 0, false
		}
		pidVal, err := val.GoValue()
		if err != nil {
			return 0, false
		}
		pid, ok := pidVal.(int)
		return pid, ok
	}

	refresh := func() {
		accountingLabel.SetText(accountingText(readMemStatus()))

		prevPID, hadSelection := selectedPID()
		vadj := scrolled.GetVAdjustment()
		scrollPos := vadj.GetValue()

		store.Clear()
		var reselect *gtk.TreeIter
		for _, p := range readProcesses() {
			iter := store.Append()
			store.Set(iter,
				[]int{0, 1, 2, 3, 4},
				[]interface{}{
					p.pid,
					p.name,
					fmt.Sprintf("%.1f", float64(p.rssKB)/1024),
					fmt.Sprintf("%.1f", float64(p.swapKB)/1024),
					fmt.Sprintf("%.1f", float64(p.totalKB)/1024),
				},
			)
			if hadSelection && p.pid == prevPID {
				reselect = iter
			}
		}
		if reselect != nil {
			if sel, err := treeView.GetSelection(); err == nil {
				sel.SelectIter(reselect)
			}
		}

		// store.Clear() resets the scrollbar to the top immediately, but
		// GTK doesn't recompute the adjustment's new upper bound until its
		// next layout pass — setting the value back right here would just
		// get clamped against the stale (just-emptied) bound. Deferring to
		// an idle callback runs this after that layout pass instead.
		glib.IdleAdd(func() {
			vadj.SetValue(scrollPos)
		})
	}
	refresh()

	glib.TimeoutAdd(refreshIntervalMs, func() bool {
		refresh()
		return true // keep repeating
	})

	killButton.Connect("clicked", func() {
		pid, ok := selectedPID()
		if !ok {
			statusLabel.SetText("No process selected.")
			return
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			statusLabel.SetText(fmt.Sprintf("Failed to kill PID %d: %v", pid, err))
		} else {
			statusLabel.SetText(fmt.Sprintf("Killed PID %d.", pid))
		}
		refresh()
	})

	win.ShowAll()
	// Deferred so the window has actually been mapped before we ask the
	// compositor to minimize it, rather than racing the initial show.
	glib.TimeoutAdd(200, func() bool {
		win.Iconify()
		return false // run once
	})

	gtk.Main()
}
