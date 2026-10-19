# 004: Public Host

- **Status:** Accepted
- **Date:** 2026-10-18
- **Owner:** Dodi Rusmana
- **Deciders:** Dodi Rusmana
- **Supersedes:** the `app-tiktok.kentangtech.com` host used since `docs/DECISIONS/001`

## Context

The v1 build named the public host after one integration, TikTok, at
`app-tiktok.kentangtech.com`. The service posts through TikTok, but it also
composes media and serves an operator console, so the host reads narrower than
the product. The host is not a display string: it is the RFC 9457 `type` URI
base, the Nginx `server_name` and TLS path, and the base URL in
`docs/SPEC/003`. The TikTok Developer Portal and the Google OAuth client also
register it as a callback and redirect.

## Decision

The public host is `app-post.kentangtech.com`. The rename lands in
`app-serve/internal/apierror/catalog.go` (`ProblemBase`), the Nginx config
(`deployment/nginx/app-post.conf`), `docs/SPEC/001`, `docs/SPEC/002`,
`docs/SPEC/003`, `README.md`, `deployment/README.md`, and `SYSTEM_MAP.md`.

## Consequences

**Positive**

- The host names the product, not one integration, so adding an integration
  does not force another rename.
- The Nginx file name matches the host it serves.
- The RFC 9457 `type` URIs, the Nginx `server_name`, and the SPEC base URL
  agree on one host again.

**Negative and constraints**

- The RFC 9457 `type` URIs move with the host. They are identifiers, not
  documents: no route serves `/problems/`, so nothing dereferences the old
  value either way.
- The TikTok Developer Portal callback URL and the Google OAuth client
  redirect URI must be re-registered against the new host before the next
  deploy. Both arrive through `TIKTOK_REDIRECT_URI` and `GOOGLE_REDIRECT_URI`.
- The DNS record and the TLS certificate must cover the new host before it
  serves traffic. The Nginx config points at
  `/etc/letsencrypt/live/app-post.kentangtech.com`.
- An existing deployment `.env` that referenced the old host in
  `TIKTOK_REDIRECT_URI`, `GOOGLE_REDIRECT_URI`, or `MEDIA_PUBLIC_BASE_URL`
  must move those values too.
