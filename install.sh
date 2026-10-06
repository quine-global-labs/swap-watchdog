#!/usr/bin/env bash
# Builds swap-watchdog / force-quit-gui / kdotool and installs them as
# systemd user services. See README.md "Install" for what this automates,
# and uninstall.sh to remove them again.
#
# The build runs in an ephemeral Nix-provisioned container via Dagger (see
# .dagger/main.go) instead of requiring gtk3-devel, dbus-devel, or a Rust
# toolchain on the host -- those are only needed at build time, and the
# resulting binaries link against the host's own GTK3/D-Bus runtime libs,
# which any normal Linux desktop already has.
set -eu

cd "$(dirname "${BASH_SOURCE[0]}")"

if [[ "$HOME/Code/swap-watchdog" != "$(pwd)" ]]; then
	echo "Warning: this repo is at $(pwd), but the unit files' ExecStart=" >&2
	echo "expects %h/Code/swap-watchdog (i.e. $HOME/Code/swap-watchdog)." >&2
	echo "Edit systemd/*.service before continuing, or the services won't" >&2
	echo "find the binaries." >&2
fi

if ! command -v dagger >/dev/null 2>&1; then
	echo "Error: dagger CLI not found. Install it (e.g. 'brew install dagger/tap/dagger')" >&2
	echo "and a container runtime (podman or docker) before running this script." >&2
	exit 1
fi

echo "Building swap-watchdog, force-quit-gui, and kdotool via Dagger+Nix..."
dagger call build-linux --src=. --go-toolchain="$(go env GOROOT)" export --path=./bin

chmod +x bin/swap-watchdog bin/force-quit-gui bin/kdotool

echo "Installing kdotool to ~/.local/bin (so the systemd service can find it)..."
mkdir -p "$HOME/.local/bin"
cp bin/kdotool "$HOME/.local/bin/kdotool"

echo "Installing unit files..."
mkdir -p "$HOME/.config/systemd/user"
cp systemd/*.service "$HOME/.config/systemd/user/"

systemctl --user daemon-reload

echo "Enabling and starting services..."
systemctl --user enable --now force-quit-gui.service swap-watchdog.service

echo "Done. Logs at ~/.local/state/swap-watchdog/watchdog.log"
