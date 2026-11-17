#!/bin/sh
# After removing gantry-agent (.deb and .rpm). The settings with the tokens,
# the stacks and the gantry user stay; only a Debian purge removes the
# settings.
set -e

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi

if [ "$1" = purge ]; then
	rm -f /etc/gantry/gantry.env
	rmdir /etc/gantry 2>/dev/null || true
fi
