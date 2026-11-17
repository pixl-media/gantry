#!/bin/sh
# Before installing or upgrading gantry-agent (.deb and .rpm): the gantry
# system user and group.
set -e

if ! getent group gantry >/dev/null; then
	groupadd --system gantry
fi
if ! getent passwd gantry >/dev/null; then
	nologin=$(command -v nologin || echo /bin/false)
	# The home is for the Docker CLI's settings, e.g. registry logins.
	useradd --system --gid gantry --home-dir /var/lib/gantry --create-home \
		--shell "$nologin" --comment "gantry agent" gantry
fi
