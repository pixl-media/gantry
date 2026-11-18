#!/bin/sh
# After installing or upgrading gantry-agent (.deb and .rpm).
#
# Arguments: Debian passes "configure" and the previous version (empty on a
# first installation); RPM passes the number of installed instances (1 on a
# first installation, 2 on an upgrade).
set -e

first_install=false
case "$1" in
configure) [ -z "$2" ] && first_install=true ;;
1) first_install=true ;;
esac

env_file=/etc/gantry/gantry.env
created_env=false
# Created once; upgrades and reinstallations never touch it.
if [ ! -e "$env_file" ]; then
	token() { head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
	install -d -m 0755 /etc/gantry
	tmp=$(mktemp /etc/gantry/.gantry.env.XXXXXX)
	sed -e "s/^GANTRY_TOKEN=\$/GANTRY_TOKEN=$(token)/" \
		-e "s/^GANTRY_UI_TOKEN=\$/GANTRY_UI_TOKEN=$(token)/" \
		/usr/share/gantry/gantry.env.example >"$tmp"
	chown root:gantry "$tmp"
	chmod 0640 "$tmp"
	mv "$tmp" "$env_file"
	created_env=true
fi

stacks=/opt/stacks
if [ ! -e "$stacks" ]; then
	install -d -m 0750 -o gantry -g gantry "$stacks"
elif [ "$(stat -c %U "$stacks" 2>/dev/null)" != gantry ]; then
	echo "gantry: $stacks exists and isn't owned by gantry. To manage its stacks, give gantry the directories, not their contents: sudo chown gantry $stacks $stacks/*/ (see \"Write access\" in the README)"
fi

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	if [ "$first_install" = true ]; then
		systemctl enable --now gantry-agent.service >/dev/null 2>&1 ||
			echo "gantry: gantry-agent didn't start (is Docker installed?); see: journalctl -u gantry-agent" >&2
	else
		# An upgrade: restart if it runs, and respect a disabled service.
		systemctl try-restart gantry-agent.service >/dev/null 2>&1 || true
		systemctl is-enabled --quiet gantry-agent.service 2>/dev/null ||
			echo "gantry: gantry-agent isn't enabled; to start it: sudo systemctl enable --now gantry-agent"
	fi
fi

if [ "$created_env" = true ]; then
	cat <<'EOF'
gantry: the agent listens on 127.0.0.1:7070 (this host only).
gantry: tokens are in /etc/gantry/gantry.env (sudo grep TOKEN /etc/gantry/gantry.env):
gantry:   GANTRY_UI_TOKEN for the web UI login, GANTRY_TOKEN for API clients.
gantry: to reach it from other hosts, read "Exposing gantry" in the README.
EOF
fi
