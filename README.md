# GenerativeAutoPost

GenerativeAutoPost is a backend and admin UI for generating and scheduling automated posts. The Go service in `app-serve/` exposes the API and handles webhooks and background work, while `app-ui/` is the Bun and Svelte admin interface. Specifications, decisions, and the implementation plan live in `docs/`.

## Layout

- `scripts/`: repository helper scripts and quality gates
- `migrations/`: PostgreSQL schema migrations (up and down pairs)
- `docs/`: specs, decisions, rules, and plans
- `deployment/`: deployment configuration
- `app-serve/`: Go backend service
- `app-ui/`: Bun and Svelte admin UI

## Documentation

- Specifications: [`docs/SPEC/`](docs/SPEC/)
- Architecture decisions: [`docs/DECISIONS/`](docs/DECISIONS/)
- Changelog: [`docs/CHANGELOG/`](docs/CHANGELOG/)
- Design direction brief (awaiting owner input): [`DESIGN.md`](DESIGN.md)

## Quality gates

Run all gates from the repository root with:

```bash
scripts/gates/all.sh
```

The gates cover `go vet`, `gofmt`, `staticcheck`, `golangci-lint`, the 250 line
source limit, the AGENTS.md file header, comment hygiene, `go test -race`, and a
gitleaks pass. The integration suite is skipped unless `GENAUTO_TEST_POSTGRES_DSN`
is set.

## Local stack

```bash
cp app-serve/.env.example app-serve/.env
# Fill GENAUTO_DB_PASSWORD and the TikTok credentials in app-serve/.env.
GENAUTO_DB_PASSWORD=local-only-secret docker-compose -f deployment/docker-compose.yml up --build
curl http://localhost:8080/healthz
```

See [`deployment/README.md`](deployment/README.md) for the nginx edge config.

## Webhooks

Webhook callbacks are served from `https://app-post.kentangtech.com/webhooks/tiktok`,
outside the `/api/v1` version prefix. Replace the host in
`deployment/nginx/app-post.conf` if it moves.
