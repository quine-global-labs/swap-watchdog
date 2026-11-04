# Built by quine-global/app-build (kind go), which compiles the binaries first; not a
# from-source spec (no %prep/%build).
%global debug_package %{nil}

Name:           swap-watchdog
Version:        %{version_}
Release:        %{release_}%{?dist}
Summary:        Reveals a Force Quit window when swap or memory pressure turns critical
License:        Apache-2.0
URL:            https://forge.in.quineglobal.com/quine-global-labs/swap-watchdog
ExclusiveArch:  x86_64 aarch64

Requires:       gtk3
Requires:       kdotool

%description
swap-watchdog polls swap and memory pressure, and when either turns critical
reveals the Force Quit Monitor (force-quit-gui), which runs minimized from
login so it's there without starting anything new under memory pressure.
Both run as systemd user services.

%install
# %%license and %%doc read from the build directory, not the checkout.
cp -p %{srcdir}/LICENSE %{srcdir}/README.md .
install -D -m 0755 %{srcdir}/bin/swap-watchdog %{buildroot}%{_bindir}/swap-watchdog
install -D -m 0755 %{srcdir}/bin/force-quit-gui %{buildroot}%{_bindir}/force-quit-gui
mkdir -p %{buildroot}%{_userunitdir}
for u in swap-watchdog force-quit-gui; do
  sed -e "s|^ExecStart=%%h/Code/swap-watchdog/bin/|ExecStart=%{_bindir}/|" \
      -e "s|^Environment=PATH=%%h/.local/bin:|Environment=PATH=|" \
      %{srcdir}/systemd/$u.service > %{buildroot}%{_userunitdir}/$u.service
  grep -q "^ExecStart=%{_bindir}/$u$" %{buildroot}%{_userunitdir}/$u.service
done
install -D -m 0644 /dev/stdin %{buildroot}%{_userpresetdir}/80-swap-watchdog.preset <<'PRESET'
enable force-quit-gui.service
enable swap-watchdog.service
PRESET

%post
%systemd_user_post force-quit-gui.service swap-watchdog.service

%preun
%systemd_user_preun force-quit-gui.service swap-watchdog.service

%files
%license LICENSE
%doc README.md
%{_bindir}/swap-watchdog
%{_bindir}/force-quit-gui
%{_userunitdir}/swap-watchdog.service
%{_userunitdir}/force-quit-gui.service
%{_userpresetdir}/80-swap-watchdog.preset
