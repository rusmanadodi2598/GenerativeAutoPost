# Deployment

Local and server wiring for the GenerativeAutoPost stack.

## Files

- `docker-compose.yml`: postgres 17, redis 7, and app-serve. app-ui joins as
  a second build once the Svelte shell exists.
- `nginx/app-post.conf`: TLS edge config for
  `app-post.kentangtech.com`. Includes `server`, `location`, and `upstream`
  blocks; paste the `server` blocks into the host's `nginx.conf` `http` block.
- `cloudflare/README.md`: API-token driven Cloudflare Tunnel setup for the
  development PC, plus the Zero Trust applications around it.
- `cloudflare/genauto-tunnel.service`: systemd user unit for the tunnel.
- `../app-serve/Dockerfile`: multi-stage build, static Go binary on Alpine.

## Required environment

`app-serve` refuses to boot without its required keys. Besides the database,
Redis, and TikTok values, a sign in needs `GOOGLE_CLIENT_ID`,
`GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`, and `OPERATOR_EMAIL`; an existing
`app-serve/.env` predating the Google login must add them before the next up.

## Behind the proxy

`nginx` sets `X-Forwarded-For`. Set `TRUSTED_PROXIES` to the address `nginx`
proxies from (the host gateway, for example `172.17.0.1`), or every request
reaches app-serve from one peer and all clients share one rate limit budget.
Leaving it empty is safe but coarse: `ClientIP` falls back to that peer, so a
single abusive caller can exhaust the shared auth throttle.

## Run locally

```bash
cp ../app-serve/.env.example ../app-serve/.env
# Fill GENAUTO_DB_PASSWORD, the TikTok credentials, and the four Google login
# keys (GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REDIRECT_URI,
# OPERATOR_EMAIL) in ../app-serve/.env.
GENAUTO_DB_PASSWORD=local-only-secret docker-compose config
GENAUTO_DB_PASSWORD=local-only-secret docker-compose up --build
curl http://localhost:8080/healthz
```

Compose reads `../app-serve/.env` into the app-serve container and derives
`DATABASE_URL` and `REDIS_URL` from the postgres and redis services, so the
only host-side variable it needs is `GENAUTO_DB_PASSWORD`.

## Nginx

```bash
sudo cp nginx/app-post.conf /etc/nginx/sites-available/app-post.conf
sudo ln -s /etc/nginx/sites-available/app-post.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

TLS certificates live at `/etc/letsencrypt/live/app-post.kentangtech.com/`.
The `app-ui` upstream points at port 3000; it 502s until the UI service is
added to the compose file. Expected and harmless during the backend phase.

## Cloudflare Tunnel (development)

The development PC has no public IP. `cloudflare/README.md` covers the
API-token driven tunnel that points `app-post.kentang.ai.id` at the local
`app-serve`, including the Zero Trust access policy and the TikTok webhook
bypass. Read it before replacing the current DNS record for that hostname.
