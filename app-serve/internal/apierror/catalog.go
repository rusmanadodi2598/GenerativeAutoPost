// Package apierror catalog holds the stable error code table.
//
// @file      internal/apierror/catalog.go
// @for       Code constants and their HTTP status mapping.
// @uses      nothing beyond the Go standard library
// @reason    One table maps TikTok and app failures to wire codes.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package apierror

// Code is a stable machine-parseable error identifier.
type Code string

// Client facing codes. Detail text may change, codes never do.
const (
	CodeValidationFailed       Code = "VALIDATION_FAILED"
	CodeUnauthorized           Code = "UNAUTHORIZED"
	CodeForbidden              Code = "FORBIDDEN"
	CodeNotFound               Code = "NOT_FOUND"
	CodeConflict               Code = "CONFLICT"
	CodeMediaNotStored         Code = "MEDIA_NOT_STORED"
	CodeIdempotencyKeyReused   Code = "IDEMPOTENCY_KEY_REUSED"
	CodeRateLimited            Code = "RATE_LIMITED"
	CodeInternal               Code = "INTERNAL_ERROR"
	CodeBadGateway             Code = "BAD_GATEWAY"
	CodeServiceUnavailable     Code = "SERVICE_UNAVAILABLE"
	CodeTikTokInvalidParam     Code = "TIKTOK_INVALID_PARAM"
	CodeTikTokReauthRequired   Code = "TIKTOK_REAUTH_REQUIRED"
	CodeTikTokScopeMissing     Code = "TIKTOK_SCOPE_MISSING"
	CodeTikTokDailyPostLimit   Code = "TIKTOK_DAILY_POST_LIMIT"
	CodeTikTokPostingBlocked   Code = "TIKTOK_POSTING_BLOCKED"
	CodeTikTokAppQuotaReached  Code = "TIKTOK_APP_QUOTA_REACHED"
	CodeTikTokUnauditedPrivate Code = "TIKTOK_UNAUDITED_PRIVATE_ONLY"
	CodeTikTokURLNotVerified   Code = "TIKTOK_URL_NOT_VERIFIED"
	CodePublishPrivacyMismatch Code = "PUBLISH_PRIVACY_LEVEL_MISMATCH"
	CodeTikTokRateLimited      Code = "TIKTOK_RATE_LIMITED"
	CodeTikTokUnavailable      Code = "TIKTOK_UNAVAILABLE"
	CodeTikTokUnknown          Code = "TIKTOK_UNKNOWN_ERROR"
	CodeTikTokBadUpstream      Code = "TIKTOK_BAD_UPSTREAM"
)

// ProblemBase hosts stable type URIs under our own domain.
const ProblemBase = "https://app-tiktok.kentangtech.com/problems/"

// catalogEntry maps one code to its HTTP status, title, and type slug.
type catalogEntry struct {
	status int
	title  string
	slug   string
}

// catalog maps each code to its HTTP status, title, and type slug.
var catalog = map[Code]catalogEntry{
	CodeValidationFailed:       {422, "Validation failed", "validation-failed"},
	CodeUnauthorized:           {401, "Unauthorized", "unauthorized"},
	CodeForbidden:              {403, "Forbidden", "forbidden"},
	CodeNotFound:               {404, "Not found", "not-found"},
	CodeConflict:               {409, "Conflict", "conflict"},
	CodeMediaNotStored:         {500, "Media not stored", "media-not-stored"},
	CodeIdempotencyKeyReused:   {409, "Idempotency key reused", "idempotency-key-reused"},
	CodeRateLimited:            {429, "Rate limited", "rate-limited"},
	CodeInternal:               {500, "Internal error", "internal-error"},
	CodeBadGateway:             {502, "Bad gateway", "bad-gateway"},
	CodeServiceUnavailable:     {503, "Service unavailable", "service-unavailable"},
	CodeTikTokInvalidParam:     {400, "Invalid TikTok request", "tiktok-invalid-param"},
	CodeTikTokReauthRequired:   {401, "TikTok reconnection needed", "tiktok-reauth-required"},
	CodeTikTokScopeMissing:     {403, "TikTok permission missing", "tiktok-scope-missing"},
	CodeTikTokDailyPostLimit:   {429, "TikTok posting limit reached", "tiktok-daily-post-limit"},
	CodeTikTokPostingBlocked:   {403, "TikTok posting blocked", "tiktok-posting-blocked"},
	CodeTikTokAppQuotaReached:  {429, "TikTok quota reached", "tiktok-app-quota-reached"},
	CodeTikTokUnauditedPrivate: {403, "Private posts only", "tiktok-unaudited-private-only"},
	CodeTikTokURLNotVerified:   {403, "URL not verified", "tiktok-url-not-verified"},
	CodePublishPrivacyMismatch: {422, "Privacy level rejected", "publish-privacy-mismatch"},
	CodeTikTokRateLimited:      {429, "TikTok rate limited", "tiktok-rate-limited"},
	CodeTikTokUnavailable:      {503, "TikTok unavailable", "tiktok-unavailable"},
	CodeTikTokUnknown:          {502, "TikTok error", "tiktok-unknown-error"},
	CodeTikTokBadUpstream:      {502, "TikTok response unreadable", "tiktok-bad-upstream"},
}
