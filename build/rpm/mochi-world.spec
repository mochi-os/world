Name:           mochi-world
Version:        %{_version}
Release:        1%{?dist}
Summary:        Realtime game server for the Mochi ecosystem
License:        Proprietary
URL:            https://mochi-os.org

%description
Mochi is a distributed app platform. This package contains the world server,
a standalone realtime multiplayer game server.

%install
mkdir -p %{buildroot}/usr/sbin
mkdir -p %{buildroot}/etc/mochi
mkdir -p %{buildroot}/var/lib/mochi-world
mkdir -p %{buildroot}/usr/lib/systemd/system
mkdir -p %{buildroot}/usr/share/man/man8
cp %{_sourcedir}/mochi-world %{buildroot}/usr/sbin/
cp %{_sourcedir}/world.conf %{buildroot}/etc/mochi/
cp %{_sourcedir}/mochi-world.service %{buildroot}/usr/lib/systemd/system/
cp %{_sourcedir}/mochi-world.8 %{buildroot}/usr/share/man/man8/

%files
%attr(755, root, root) /usr/sbin/mochi-world
%config(noreplace) /etc/mochi/world.conf
%dir /var/lib/mochi-world
/usr/lib/systemd/system/mochi-world.service
/usr/share/man/man8/mochi-world.8*

%pre
if ! getent group mochi >/dev/null; then
    groupadd --system mochi
fi
if ! getent passwd mochi >/dev/null; then
    useradd --system --no-create-home --home-dir /var/lib/mochi --shell /usr/sbin/nologin --gid mochi --comment "Mochi server" mochi
fi

%post
chown -R mochi:mochi /var/lib/mochi-world
systemctl daemon-reload
systemctl enable mochi-world 2>/dev/null || true
systemctl start mochi-world 2>/dev/null || true

%preun
if [ $1 -eq 0 ]; then
    systemctl stop mochi-world 2>/dev/null || true
    systemctl disable mochi-world 2>/dev/null || true
fi

%postun
systemctl daemon-reload
