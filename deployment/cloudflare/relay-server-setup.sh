#!/usr/bin/env bash
# Installs cloudflared on the relay server and runs the genauto-dev tunnel.
#
# Run this on the relay server as the user that owns
# ~/.config/genauto/tunnel.env. The token file is copied over SSH stdin, so the
# tunnel token never appears in a command line or shell history on either side.
#
# The tunnel ingress is http://127.0.0.1:18080, which the local PC exposes with
# genauto-reverse-ssh.service.

set -euo pipefail

CONFIG_DIR="$HOME/.config/genauto"
TOKEN_FILE="$CONFIG_DIR/tunnel.env"
UNIT_DIR="$HOME/.config/systemd/user"
UNIT="$UNIT_DIR/genauto-tunnel.service"

if [ ! -f "$TOKEN_FILE" ]; then
	printf 'missing %s\npipe the tunnel env first, for example:\n' "$TOKEN_FILE" >&2
	printf "  ssh %s@<host> 'umask 077; cat > ~/.config/genauto/tunnel.env' < ~/.config/genauto/tunnel.env\n" "$USER" >&2
	exit 1
fi

if ! command -v cloudflared >/dev/null 2>&1; then
	printf '==> installing cloudflared\n'
	sudo install -d -m 0755 /usr/share/keyrings
	curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg |
		sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
	echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' |
		sudo tee /etc/apt/sources.list.d/cloudflared.list >/dev/null
	sudo apt-get update
	sudo apt-get install -y cloudflared
fi

cloudflared_bin="$(command -v cloudflared)"
mkdir -p "$UNIT_DIR"
cat >"$UNIT" <<EOF
[Unit]
Description=Cloudflare Tunnel for GenerativeAutoPost (relay)
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
EnvironmentFile=%h/.config/genauto/tunnel.env
ExecStart=$cloudflared_bin --no-autoupdate tunnel run
Restart=on-failure
RestartSec=5s
TimeoutStartSec=30

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now genauto-tunnel.service
if ! loginctl enable-linger "$USER" 2>/dev/null; then
	printf 'enable-linger failed; run: sudo loginctl enable-linger %s\n' "$USER" >&2
fi
systemctl --user --no-pager status genauto-tunnel.service | head -12
