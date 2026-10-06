// swap-watchdog polls system memory pressure and, on a rising edge into
// "critical", reveals an already-running Force Quit Monitor window via
// kdotool rather than spawning anything new at the exact moment the system
// is least able to launch a process.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	pollInterval      = 3 * time.Second
	defaultSwapPct    = 1.0  // percent SwapFree/SwapTotal below which swap counts as exhausted
	defaultPSI        = 10.0 // percent "full avg10" above which memory pressure counts as critical
	forceQuitWinTitle = "Force Quit Monitor"
)

func envFloat(name string, def float64) float64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// swapStatus reports whether swap is critically exhausted. ok is false if
// there's no swap configured at all (nothing to measure).
func swapStatus(pctThreshold float64) (critical bool, pct float64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return false, 0, false
	}
	defer f.Close()

	var total, free float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "SwapTotal:":
			total, _ = strconv.ParseFloat(fields[1], 64)
		case "SwapFree:":
			free, _ = strconv.ParseFloat(fields[1], 64)
		}
	}
	if total <= 0 {
		return false, 0, false
	}
	pct = (free / total) * 100
	return pct < pctThreshold, pct, true
}

// memoryPressure reads the kernel PSI "full avg10" figure — the same signal
// systemd-oomd uses — for /proc/pressure/memory.
func memoryPressure(psiThreshold float64) (critical bool, avg10 float64, ok bool) {
	f, err := os.Open("/proc/pressure/memory")
	if err != nil {
		return false, 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "full ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if val, found := strings.CutPrefix(field, "avg10="); found {
				avg10, _ = strconv.ParseFloat(val, 64)
				return avg10 > psiThreshold, avg10, true
			}
		}
	}
	return false, 0, false
}

func revealForceQuitWindow() error {
	out, err := exec.Command("kdotool", "search", "-t", forceQuitWinTitle).Output()
	if err != nil {
		return fmt.Errorf("kdotool search failed: %w", err)
	}
	id := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if id == "" {
		return fmt.Errorf("no window titled %q found", forceQuitWinTitle)
	}
	if err := exec.Command("kdotool", "windowstate", "--remove", "MINIMIZED", "--add", "ABOVE", id).Run(); err != nil {
		return fmt.Errorf("kdotool windowstate failed: %w", err)
	}
	if err := exec.Command("kdotool", "windowactivate", id).Run(); err != nil {
		return fmt.Errorf("kdotool windowactivate failed: %w", err)
	}
	return nil
}

func main() {
	stateDir := filepath.Join(os.Getenv("HOME"), ".local", "state", "swap-watchdog")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		log.Fatalf("could not create state dir: %v", err)
	}
	logFile, err := os.OpenFile(filepath.Join(stateDir, "watchdog.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("could not open log file: %v", err)
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags)

	swapPctThreshold := envFloat("SWAP_PCT_THRESHOLD", defaultSwapPct)
	psiThreshold := envFloat("PSI_THRESHOLD", defaultPSI)

	logger.Printf("started (swap threshold=%.2f%%, psi threshold=%.2f)", swapPctThreshold, psiThreshold)

	wasCritical := false
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		swapCrit, swapPct, swapOK := swapStatus(swapPctThreshold)
		psiCrit, psiAvg10, psiOK := memoryPressure(psiThreshold)

		critical := (swapOK && swapCrit) || (psiOK && psiCrit)

		if critical && !wasCritical {
			logger.Printf("TRIGGER: swap_free_pct=%.2f psi_full_avg10=%.2f", swapPct, psiAvg10)
			if err := revealForceQuitWindow(); err != nil {
				logger.Printf("failed to reveal force-quit window: %v", err)
			}
		}
		wasCritical = critical
	}
}
