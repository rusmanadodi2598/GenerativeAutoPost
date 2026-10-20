# Cloudflare Tunnel (local development)

The development PC has no public IP. Cloudflare Tunnel gives the stack a stable
HTTPS hostname, so the Google and TikTok OAuth redirect URIs and the TikTok
webhook URL stay identical when the service moves to a server later.

`scripts/dev/cloudflare-tunnel.sh` drives the Cloudflare API with a scoped API
token (a PAT). The tunnel itself runs with a separate tunnel token, so the PAT
is only needed while creating or changing the route.

## Two tokens

| Token | Used by | Stored in |
|---|---|---|
| API token (PAT) | `verify`, `setup`, `status`, `teardown` | `~/.config/genauto/cloudflare.token` |
| Tunnel token | `cloudflared tunnel run` | `~/.config/genauto/tunnel.env` |

Both files are mode 600. Neither value is printed by the script or committed.

## Create the API token

Cloudflare dashboard, My Profile, API Tokens, Create Token, Custom token.

Permissions:

- Account, Cloudflare Tunnel, Edit
- Account, Access: Apps and Policies, Edit
- Account, Access: Organizations, Identity Providers, and Groups, Edit
  (only needed the first time Zero Trust is set up)
- Zone, DNS, Edit
- Zone, Zone, Read

Resources:

- Account resources: include the account that owns `kentang.ai.id`
- Zone resources: include `kentang.ai.id`

Store it without putting it in shell history:

```bash
mkdir -p ~/.config/genauto && chmod 700 ~/.config/genauto
read -rsp 'Cloudflare API token: ' CF_PAT; echo
printf '%s' "$CF_PAT" > ~/.config/genauto/cloudflare.token
chmod 600 ~/.config/genauto/cloudflare.token
unset CF_PAT
```

## Verify, then create

```bash
scripts/dev/cloudflare-tunnel.sh verify
scripts/dev/cloudflare-tunnel.sh setup
```

`verify` checks the token, resolves the account and zone, and proves the tunnel
and DNS permissions. `setup` is idempotent: it reuses an existing
`genauto-dev` tunnel, refreshes the ingress, and only changes DNS when the
record does not already point at the tunnel.

`app-post.kentang.ai.id` has no DNS record yet, so the first run creates it.
If a record exists but does not point at this tunnel, `setup` stops and prints
the old value. Rerun with `FORCE=1` to replace it; the old record is saved to
`~/.config/genauto/dns-app-post.kentang.ai.id.json` first.

```bash
FORCE=1 scripts/dev/cloudflare-tunnel.sh setup
```

What `setup` creates:

- tunnel `genauto-dev`, remotely managed
- ingress `app-post.kentang.ai.id` to `http://localhost:8080`
- proxied CNAME `app-post.kentang.ai.id` to `<tunnel-id>.cfargotunnel.com`
- optionally, Zero Trust applications (see below)

## Zero Trust Access

Set `ACCESS_EMAIL` to enable it:

```bash
ACCESS_EMAIL=you@example.com scripts/dev/cloudflare-tunnel.sh setup
```

That creates two Access applications:

- `app-post.kentang.ai.id`: allow the email, one-time PIN login
- `app-post.kentang.ai.id/webhooks/tiktok`: bypass for everyone

The bypass is required. TikTok posts server to server and cannot complete an
Access login. The webhook handler still verifies the `TikTok-Signature` HMAC
before trusting the body, so the bypass does not weaken that route.

Any other server-to-server caller needs the same treatment. If media is later
served from this hostname for `PULL_FROM_URL`, add a bypass for that path or
move media to R2.

Without `ACCESS_EMAIL`, the tunnel is public on the hostname. That is fine
while testing with curl, but add Access before sharing the URL.

## After the first request

cloudflared connects to app-serve from loopback. Set
`TRUSTED_PROXIES=127.0.0.1/32` in `app-serve/.env` so `ClientIP` reads the
Cloudflare client address from `X-Forwarded-For` instead of `127.0.0.1`.
Without it every caller shares one rate limit bucket on the auth routes.
Restart app-serve after changing the value.

The OAuth redirect URIs in `app-serve/.env` already point at
`https://app-post.kentang.ai.id`. If you pick a different
`TUNNEL_HOSTNAME`, update `GOOGLE_REDIRECT_URI`, `TIKTOK_REDIRECT_URI`, and
`MEDIA_PUBLIC_BASE_URL`, then update the Google and TikTok developer consoles
to match.

## Run the tunnel

Foreground, for a quick test:

```bash
scripts/dev/cloudflare-tunnel.sh run
```

As a user service, so it survives logout and reboot:

```bash
mkdir -p ~/.config/systemd/user
cp deployment/cloudflare/genauto-tunnel.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now genauto-tunnel.service
loginctl enable-linger "$USER"
```

Check it:

```bash
scripts/dev/cloudflare-tunnel.sh status
systemctl --user status genauto-tunnel.service
curl -i https://app-post.kentang.ai.id/healthz
```

With Access enabled, `/healthz` returns a redirect to the Access login. That is
the expected result; a 200 without a login means the Access application did not
attach.

The webhook bypass can be checked without a valid signature:

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  https://app-post.kentang.ai.id/webhooks/tiktok
```

A JSON 400 or 401 from app-serve is correct. A 302 to
`<team>.cloudflareaccess.com` means the bypass did not attach.

## Relay when the local network blocks the edge

Some networks reset connections to `198.41.192.0/24` and `198.41.200.0/24` on
port 7844, the Cloudflare Tunnel edge. The log shows repeated
`TLS handshake with edge error: EOF` or `timeout: no recent network activity`,
while ordinary HTTPS to Cloudflare still works.

On a network like that, run the connector on a small server with a public IP
and forward the local origin to it over SSH:

- local PC: `genauto-reverse-ssh.service` binds `127.0.0.1:18080` on the relay
  and forwards it to the local `127.0.0.1:8080`
- relay: `relay-server-setup.sh` installs cloudflared and runs the tunnel with
  ingress `http://127.0.0.1:18080`

The relay host is not in this repo. The unit reads it from
`~/.config/genauto/relay.env`, which holds one line, `RELAY_TARGET=user@host`,
and is never committed.

```bash
# 1. point the tunnel ingress at the reverse SSH port
TUNNEL_ORIGIN=http://127.0.0.1:18080 scripts/dev/cloudflare-tunnel.sh setup

# 2. keep the relay target out of the repo, one line, mode 600
mkdir -p ~/.config/genauto
printf 'RELAY_TARGET=%s\n' 'you@relay.example.com' > ~/.config/genauto/relay.env
chmod 600 ~/.config/genauto/relay.env
source ~/.config/genauto/relay.env

# 3. copy the tunnel token to the relay over SSH stdin, not the command line
ssh "$RELAY_TARGET" \
  'mkdir -p ~/.config/genauto && umask 077 && cat > ~/.config/genauto/tunnel.env' \
  < ~/.config/genauto/tunnel.env

# 4. install and start cloudflared on the relay
ssh "$RELAY_TARGET" 'bash -s' \
  < deployment/cloudflare/relay-server-setup.sh

# 5. keep the reverse tunnel up on the local PC
cp deployment/cloudflare/genauto-reverse-ssh.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now genauto-reverse-ssh.service
```

The reverse tunnel binds port 18080 on the relay's loopback only, so the relay
does not expose the SSH forward to the internet. cloudflared is the only
process that should reach that port.

The direct `scripts/dev/cloudflare-tunnel.sh run` remains the simpler path on
networks that allow the edge.

## Moving to production

The script is not development-specific. On the server, run the same commands
with the server origin and a separate tunnel:

```bash
TUNNEL_NAME=genauto-prod \
TUNNEL_ORIGIN=http://127.0.0.1:8080 \
FORCE=1 scripts/dev/cloudflare-tunnel.sh setup
```

Then run `cloudflared` under a system unit with
`EnvironmentFile=/etc/genauto/tunnel.env`. Keep the API token off the server
after setup, or scope a second token to tunnel edit only.

The nginx config in `deployment/nginx/app-post.conf` stays as the origin-side
TLS edge only if the tunnel points at nginx. If the tunnel points straight at
`app-serve`, nginx is not in the path.

## Troubleshooting

- `526` from Cloudflare: the DNS record still points at an origin with an
  invalid certificate. Re-run `setup` and confirm the CNAME target is
  `<tunnel-id>.cfargotunnel.com`.
- `1033`: the tunnel is registered but the connector is down. Run
  `scripts/dev/cloudflare-tunnel.sh status` and check the user service.
- `502` from the tunnel: cloudflared is connected but nothing listens on
  `TUNNEL_ORIGIN`. Check `curl http://localhost:8080/healthz`.
- Repeated QUIC timeouts in the journal: the network blocks UDP 7844. Use
  `cloudflared tunnel run --protocol http2` in the service unit.
- `TLS handshake with edge error: EOF` on both protocols: the network blocks
  the edge TCP ranges too. Use the relay described above.
