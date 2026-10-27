#!/bin/sh
# Make a systemd image boot cleanly inside Docker: mask units that need real hardware or a tty.
set -e
for u in systemd-udevd.service systemd-udevd-kernel.socket systemd-udevd-control.socket getty.target \
  console-getty.service getty@tty1.service systemd-logind.service sys-kernel-debug.mount sys-kernel-tracing.mount \
  systemd-remount-fs.service systemd-firstboot.service systemd-modules-load.service systemd-networkd-wait-online.service \
  apt-daily.timer apt-daily-upgrade.timer e2scrub_all.timer fstrim.timer motd-news.timer systemd-timesyncd.service \
  dnf-makecache.timer unattended-upgrades.service; do
  systemctl mask "$u" >/dev/null 2>&1 || true
done
mkdir -p /etc/phosphor-lab
