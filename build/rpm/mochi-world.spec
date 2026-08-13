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
# The world-status socket's group: mochi-server chowns <data>/run/world.sock
# to it, so it must exist before that server starts. The shipped unit runs as
# the mochi user and reaches the socket by ownership; the group is what lets
# an operator run the world server under its own account instead.
if ! getent group mochi-world >/dev/null; then
    groupadd --system mochi-world
fi
# The server's own account joins it: the socket is created by mochi-server as
# the mochi user, and Linux only lets you chgrp to a group you are IN, so
# without this the chown fails EPERM and the group is decorative. Groups are
# read at process start, so it takes effect on the next mochi-server restart;
# until then mochi-world reaches the socket by ownership as before.
if getent passwd mochi >/dev/null && ! id -nG mochi 2>/dev/null | tr ' ' '\n' | grep -qx mochi-world; then
    usermod -a -G mochi-world mochi >/dev/null 2>&1 || true
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
