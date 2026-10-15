# Deployment

Local and server wiring for the GenerativeAutoPost stack.

## Files

- `docker-compose.yml`: postgres 17, redis 7, and app-serve. app-ui joins as
  a second build once the Svelte shell exists.
- `nginx/app-tiktok.conf`: TLS edge config for
  `app-tiktok.kentangtech.com`. Includes `server`, `location`, and `upstream`
  blocks; paste the `server` blocks into the host's `nginx.conf` `http` block.
- `../app-serve/Dockerfile`: multi-stage build, static Go binary on Alpine.

## Run locally

```bash
cp ../app-serve/.env.example ../app-serve/.env
# Fill GENAUTO_DB_PASSWORD and the TikTok credentials in ../app-serve/.env.
GENAUTO_DB_PASSWORD=local-only-secret docker-compose config
GENAUTO_DB_PASSWORD=local-only-secret docker-compose up --build
curl http://localhost:8080/healthz
```

Compose reads `../app-serve/.env` into the app-serve container and derives
`DATABASE_URL` and `REDIS_URL` from the postgres and redis services, so the
only host-side variable it needs is `GENAUTO_DB_PASSWORD`.

## Nginx

```bash
sudo cp nginx/app-tiktok.conf /etc/nginx/sites-available/app-tiktok.conf
sudo ln -s /etc/nginx/sites-available/app-tiktok.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

TLS certificates live at `/etc/letsencrypt/live/app-tiktok.kentangtech.com/`.
The `app-ui` upstream points at port 3000; it 502s until the UI service is
added to the compose file. Expected and harmless during the backend phase.
