// Package tiktokclient maps TikTok wire failures to typed client errors.
//
// @file      internal/tiktokclient/errors.go
// @for       TikTok wire error code table and lookup helpers.
// @uses      internal/apierror
// @reason    Central place where upstream TikTok codes become our client codes.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-12
package tiktokclient

import "github.com/kentangtech/GenerativeAutoPost/app-serve/internal/apierror"

// wireError maps one TikTok code to our stable code, client message, and retry hint.
type wireError struct {
	code    apierror.Code
	message string
	retry   bool
}

// wireErrors holds every TikTok code we can classify.
var wireErrors = map[string]wireError{
	"invalid_param": {
		code:    apierror.CodeTikTokInvalidParam,
		message: "The request to TikTok was invalid. Check the input.",
		retry:   false,
	},
	"access_token_invalid": {
		code:    apierror.CodeTikTokReauthRequired,
		message: "The TikTok connection needs to be renewed.",
		retry:   false,
	},
	"scope_not_authorized": {
		code:    apierror.CodeTikTokScopeMissing,
		message: "This action needs permission you have not granted.",
		retry:   false,
	},
	"spam_risk_too_many_posts": {
		code:    apierror.CodeTikTokDailyPostLimit,
		message: "You have reached the posting limit. Try again later.",
		retry:   false,
	},
	"spam_risk_user_banned_from_posting": {
		code:    apierror.CodeTikTokPostingBlocked,
		message: "This TikTok account cannot post right now.",
		retry:   false,
	},
	"reached_active_user_cap": {
		code:    apierror.CodeTikTokAppQuotaReached,
		message: "Posting is temporarily unavailable. Try again later.",
		retry:   false,
	},
	"unaudited_client_can_only_post_to_private_accounts": {
		code:    apierror.CodeTikTokUnauditedPrivate,
		message: "Posts are limited to private until the app passes TikTok's audit.",
		retry:   false,
	},
	"url_ownership_unverified": {
		code:    apierror.CodeTikTokURLNotVerified,
		message: "The video URL domain is not verified for posting.",
		retry:   false,
	},
	"privacy_level_option_mismatch": {
		code:    apierror.CodePublishPrivacyMismatch,
		message: "The selected privacy level is not available for this account.",
		retry:   false,
	},
	"rate_limit_exceeded": {
		code:    apierror.CodeTikTokRateLimited,
		message: "Too many requests. Try again shortly.",
		retry:   true,
	},
}

// mapWireError turns a TikTok error body into a typed client error.
func mapWireError(body apiErrorBody) *apierror.Error {
	entry, ok := wireErrors[body.Code]
	if !ok {
		return apierror.New(apierror.CodeTikTokUnknown, 0, "TikTok returned an error we could not classify.").WithLogID(body.LogID)
	}
	return apierror.New(entry.code, 0, entry.message).WithLogID(body.LogID)
}

// badUpstream builds the error for an unreadable upstream body.
func badUpstream(err error) *apierror.Error {
	return apierror.New(apierror.CodeTikTokBadUpstream, 0, "TikTok returned a response we could not read.").Wrap(err)
}

// retryableWireCode reports whether the wire code is worth retrying.
func retryableWireCode(code string) bool {
	entry, ok := wireErrors[code]
	if !ok {
		return false
	}
	return entry.retry
}
