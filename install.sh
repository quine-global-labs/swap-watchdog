#!/usr/bin/env bash
# Builds swap-watchdog / force-quit-gui and installs them as systemd user
# services. See README.md "Install" for the manual steps this automates,
# and uninstall.sh to remove them again.
set -eu

cd "$(dirname "${BASH_SOURCE[0]}")"

if [[ "$HOME/Code/swap-watchdog" != "$(pwd)" ]]; then
	echo "Warning: this repo is at $(pwd), but the unit files' ExecStart=" >&2
	echo "expects %h/Code/swap-watchdog (i.e. $HOME/Code/swap-watchdog)." >&2
	echo "Edit systemd/*.service before continuing, or the services won't" >&2
	echo "find the binaries." >&2
fi

if ! pkg-config --exists gtk+-3.0 2>/dev/null; then
	echo "Warning: gtk+-3.0 dev files not found (need gtk3-devel) --" >&2
	echo "building force-quit-gui will likely fail." >&2
fi

if ! command -v kdotool >/dev/null 2>&1; then
	echo "Warning: kdotool not found -- swap-watchdog needs it at runtime" >&2
	echo "to reveal the Force Quit Monitor window." >&2
fi

echo "Building binaries..."
go build -o bin/swap-watchdog ./cmd/swap-watchdog
go build -o bin/force-quit-gui ./cmd/force-quit-gui

echo "Installing unit files..."
mkdir -p "$HOME/.config/systemd/user"
cp systemd/*.service "$HOME/.config/systemd/user/"

systemctl --user daemon-reload

echo "Enabling and starting services..."
systemctl --user enable --now force-quit-gui.service swap-watchdog.service

echo "Done. Logs at ~/.local/state/swap-watchdog/watchdog.log"
