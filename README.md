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

## Quality gates

Run all gates from the repository root with:

```bash
scripts/gates/all.sh
```

The gates script is added in a later task of the implementation plan.

## Webhooks

Webhook callbacks are served from `<webhook-host>`. Replace this placeholder with the public host once it is provisioned.
