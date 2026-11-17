#!/bin/sh
# Before removing or upgrading gantry-agent (.deb and .rpm). Debian passes
# "remove" (or "upgrade"); RPM passes 0 on removal, 1 on an upgrade.
set -e

case "$1" in
remove | 0)
	if [ -d /run/systemd/system ]; then
		systemctl disable --now gantry-agent.service >/dev/null 2>&1 || true
	fi
	;;
esac
