#!/usr/bin/env bash
# Stops, disables, and removes the swap-watchdog / force-quit-gui systemd
# user services installed by this repo. Leaves the built binaries and the
# log at ~/.local/state/swap-watchdog/ in place.
set -u

echo "Stopping services..."
systemctl --user stop swap-watchdog.service force-quit-gui.service 2>/dev/null

echo "Disabling services..."
systemctl --user disable swap-watchdog.service force-quit-gui.service 2>/dev/null

echo "Removing unit files..."
rm -f "$HOME/.config/systemd/user/swap-watchdog.service"
rm -f "$HOME/.config/systemd/user/force-quit-gui.service"

systemctl --user daemon-reload

echo "Killing any still-running instances..."
pkill -f "$HOME/Code/swap-watchdog/bin/swap-watchdog" 2>/dev/null
pkill -f "$HOME/Code/swap-watchdog/bin/force-quit-gui" 2>/dev/null

echo "Done. Source and binaries are untouched; re-run install steps from README.md to re-enable."
