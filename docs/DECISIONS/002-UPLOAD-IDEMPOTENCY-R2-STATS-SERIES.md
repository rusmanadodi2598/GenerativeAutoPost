# 002: Upload Idempotency, R2 Gate, and Stats Range Series

- **Status:** Accepted
- **Date:** 2026-10-16
- **Owner:** Dodi Rusmana
- **Deciders:** Dodi Rusmana
- **Supersedes:** none

## Context

Three items from the SDD ledger stayed open because each needs a contract
decision before code is written:

1. `Idempotency-Key` on `POST /media/uploads` (contract 003 section 10).
   The body is streamed multipart up to 200 MB, so a body hash is only known
   after the stream is consumed, while the claim must happen before storage
   to stop concurrent duplicates.
2. The R2 object client, the last gate before `MEDIA_DRIVER=s3` opens.
3. The stats range series (contract 003 section 7.5 and open item 3).
   `GET /videos/{id}/stats` currently ignores `range` and returns one
   snapshot, while the contract promises a snapshot series.

## Decision 1: upload idempotency via `Checksum-SHA256`

**Adopted: option (b), with one tightening.** The client may send a
`Checksum-SHA256` header carrying the hex SHA-256 of the file bytes (not of
the multipart framing).

- Header present: the idempotency claim is `key + checksum` and happens
  before any byte is stored. A repeat replays the stored object, so no blob
  is written twice. The service recomputes the digest from the streamed
  bytes and refuses to store anything when it differs from the header, so a
  wrong claim never leaves an orphan behind.
- `Idempotency-Key` without `Checksum-SHA256` is a `422`. The header was
  first written as optional with a weaker promise for headerless clients,
  but the weaker promise is not honestly keepable: without the digest the
  claim has nothing to compare and would replay the first answer onto a
  different file, breaking the "different body is a 409" rule of section 10.
  One rule beats two promises of different strength.
- A failed attempt releases its claim, so the client may correct the file
  and retry under the same key instead of meeting a 409 that never clears.
- A repeat with the same key and a different checksum is `409` with
  `IDEMPOTENCY_KEY_REUSED`; a repeat while the first attempt is still
  running is `409` with `CONFLICT`.

The store moved to `internal/platform/idempotency` because publish and media
now both claim keys. Media declares a three method `UploadGuard` interface and
never imports publish.

Contract 003 section 10 carries the header definition, the rejection of a key
without a digest, and the `409` shape.

## Decision 2: R2 gate stays fail-fast

**Accepted as-is, no code change.** `compose.mediaStore` keeps returning an
error for `MEDIA_DRIVER=s3` until a real R2 `ObjectClient` is wired. A silent
disk fallback is rejected: it would split the `PULL_FROM_URL` truth between
two backends and hide a misconfigured deployment behind `200` responses.

The gate opens in the task that wires the R2 client from `S3_ENDPOINT`,
`S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, and `S3_REGION`. That task also
adds a boot-time connectivity check so a bad credential fails at startup,
not on the first upload.

**Resolved 2026-10-16.** The gate is open. `internal/platform/r2` implements
the S3 surface with an in-house AWS SigV4 signer over the standard library, so
no AWS SDK dependency is added, and `compose.mediaStore` proves the
credentials with a write probe before returning the store. The `ObjectClient`
put seam streams now (`io.Reader` plus a known size and the body SHA-256), so
the adapter no longer buffers an object to hash it. The upload service still
buffers the stream to verify the client checksum before any write, so the seam
removes the adapter's second copy, not the service's verification buffer.

## Decision 3: stats series with a `range` enum and refresh-on-read

**Adopted.** `GET /videos/{id}/stats` takes `range` as `7d | 30d | 90d`
(default `30d`); any other value is `422 VALIDATION_FAILED`. The response is
`data: { video_id, range, points: [...], truncated }` with points ascending by
`fetched_at`, capped at 500 points per response.

- Freshness applies to the latest point only, reusing the existing TTL rule:
  a stale latest point triggers one fetch-and-append before the series is
  read; an upstream failure falls back to the stored series. History is never
  refetched, only appended to.
- `POST /videos/{id}/stats/refresh` is unchanged: it always fetches and
  appends, so the series keeps one point per manual request.
- This needs one new repository method, `ListStats(videoID, since, limit)`.
  The required index (`video_stats(video_id, fetched_at DESC)`) already
  exists per 001 section 5.

Contract 003 section 7.5 is updated with the enum, the default, the cap, and
the refresh-on-read rule. Open item 3 in section 12 is closed by this ADR.

## Consequences

**Positive**

- Upload idempotency costs one contract header instead of buffering or a
  storage migration.
- The S3 gate cannot silently serve from the wrong backend.
- The stats endpoint matches its contract with one bounded query.

**Negative and constraints**

- Dedupe is a promise this route makes only to clients that send both
  headers; a key without a digest is refused rather than answered with a
  promise we cannot keep.
- A wrong checksum header fails the upload, so a client that computes its
  digest over the multipart envelope instead of the file bytes is rejected
  rather than silently stored.
- Series responses grow with retention; a retention or downsampling policy
  for `video_stats` becomes the next open question once real fetch volume
  is known.
- `POST /videos/{id}/stats` no longer returns a single snapshot object. That
  is safe today because app-ui has no code, and it becomes a breaking change
  the moment a client ships.

## Open Questions

1. Retention and downsampling policy for `video_stats` rows.
