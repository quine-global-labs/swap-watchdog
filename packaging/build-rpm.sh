#!/usr/bin/env bash
# Builds swap-watchdog and force-quit-gui and packages them as an RPM
# (packaging/swap-watchdog.spec). Runs in a Fedora container
# (.forgejo/workflows/rpm.yml) with this repo at /src, a cache that outlives
# the job at /cache (Go's toolchain and modules), and the RPMs going to /out.
# kdotool isn't built here: Fedora packages it, and the RPM requires it.
set -euo pipefail

dnf install -y --setopt=install_weak_deps=False golang gtk3-devel git rpm-build

# go.mod asks for a newer Go than Fedora's; GOTOOLCHAIN=auto fetches it (into
# the module cache, so once).
export GOTOOLCHAIN=auto GOMODCACHE=/cache/go/mod GOCACHE=/cache/go/build GOFLAGS=-buildvcs=false
git config --global --add safe.directory '*'

cd /src
mkdir -p bin
go build -trimpath -o bin/ ./cmd/swap-watchdog ./cmd/force-quit-gui

version=0
release=$(date -u +%Y%m%d%H%M).git$(git rev-parse --short=7 HEAD)
rpmbuild -bb packaging/swap-watchdog.spec \
  --define "_topdir /tmp/rpmbuild" \
  --define "version_ $version" --define "release_ $release" --define "srcdir /src"
cp -v /tmp/rpmbuild/RPMS/*/*.rpm /out/
