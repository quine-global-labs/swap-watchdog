// Builds swap-watchdog's binaries (including kdotool, which isn't packaged
// for Fedora) without installing any build-time dev headers on the host.
//
// gtk3-devel and dbus-devel (needed to compile force-quit-gui and kdotool,
// respectively) are fetched inside an ephemeral Nix-provisioned container,
// never on the host. This only works because the corresponding *runtime*
// shared libraries (libgtk-3, libdbus-1, ...) are already present on any
// normal Linux desktop -- so the binaries built here link against the
// host's own runtime libs the moment they're copied out. Nothing is
// bundled or vendored.
//
// The Go toolchain itself is passed in from the host (see install.sh) rather
// than pulled from nixpkgs, so the build always uses the exact same `go`
// version the host's go.mod requires, with no risk of nixpkgs happening to
// carry an older/newer one.
package main

import (
	"context"

	"dagger/swap-watchdog/internal/dagger"
)

type SwapWatchdog struct{}

// BuildLinux compiles swap-watchdog, force-quit-gui, and kdotool and returns
// a Directory containing all three, ready to export into bin/.
func (m *SwapWatchdog) BuildLinux(ctx context.Context,
	// +defaultPath="."
	src *dagger.Directory,
	// Host Go toolchain root (i.e. `go env GOROOT`), mounted in so the
	// container builds with the exact same `go` version go.mod requires.
	goToolchain *dagger.Directory,
) *dagger.Directory {
	nixPkgs := "gtk3 dbus pkg-config gcc cargo rustc git patchelf"

	// nixpkgs' glibc uses a non-FHS dynamic linker path
	// (/nix/store/.../ld-linux-x86-64.so.2), which doesn't exist outside the
	// build container. The runtime libs themselves resolve fine via the
	// host's normal SONAME search path (ldd confirms this against the
	// host's own libgtk-3/libdbus-1/...) -- only the ELF interpreter is
	// wrong, so patchelf is enough; nothing needs bundling.
	const hostInterp = "/lib64/ld-linux-x86-64.so.2"

	base := dag.Container().
		From("nixos/nix:latest").
		WithDirectory("/usr/local/go", goToolchain).
		WithEnvVariable("PATH", "/usr/local/go/bin:/root/.nix-profile/bin:/nix/var/nix/profiles/default/bin:/nix/var/nix/profiles/default/sbin:/usr/bin:/bin").
		WithEnvVariable("GOROOT", "/usr/local/go").
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("swap-watchdog-go-build")).
		WithMountedCache("/root/.cargo/registry", dag.CacheVolume("swap-watchdog-cargo-registry"))

	goBuilt := base.
		WithDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"sh", "-c", `set -e
nix-shell -p ` + nixPkgs + ` --run '
set -e
mkdir -p /out
CGO_ENABLED=1 go build -o /out/swap-watchdog ./cmd/swap-watchdog
CGO_ENABLED=1 go build -o /out/force-quit-gui ./cmd/force-quit-gui
patchelf --set-interpreter ` + hostInterp + ` /out/force-quit-gui
'`})

	kdotoolBuilt := base.
		WithExec([]string{"sh", "-c", `set -e
nix-shell -p ` + nixPkgs + ` --run '
set -e
git clone --depth 1 https://github.com/jinliu/kdotool /tmp/kdotool
cd /tmp/kdotool
cargo build --release
mkdir -p /out
cp target/release/kdotool /out/kdotool
patchelf --set-interpreter ` + hostInterp + ` /out/kdotool
'`})

	return dag.Directory().
		WithFile("swap-watchdog", goBuilt.File("/out/swap-watchdog")).
		WithFile("force-quit-gui", goBuilt.File("/out/force-quit-gui")).
		WithFile("kdotool", kdotoolBuilt.File("/out/kdotool"))
}
