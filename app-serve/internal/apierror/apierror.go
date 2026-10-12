// Package apierror serializes one RFC 9457 error shape for clients.
//
// @file      internal/apierror/apierror.go
// @for       Stable code catalog and environment gated error writer.
// @uses      net/http, encoding/json, os, strings
// @reason    Single place where failures cross the wire to clients.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package apierror

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// FieldError carries one validator failure with the JSON field name.
type FieldError struct {
	Field string `json:"field"`
	Tag   string `json:"tag"`
	Param string `json:"param,omitempty"`
}

// Debug holds development only detail, never sent in production.
type Debug struct {
	Chain    string         `json:"chain,omitempty"`
	Upstream UpstreamDetail `json:"upstream,omitempty"`
	Stack    string         `json:"stack,omitempty"`
}

// UpstreamDetail carries the TikTok failure identifiers for debugging.
type UpstreamDetail struct {
	HTTPStatus int    `json:"http_status,omitempty"`
	Code       string `json:"code,omitempty"`
	LogID      string `json:"log_id,omitempty"`
}

// Writer renders errors with debug detail gated by an explicit flag.
type Writer struct {
	Development bool
}

// problemBody is the RFC 9457 shape with optional members omitted.
type problemBody struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail"`
	Instance string       `json:"instance"`
	Code     string       `json:"code"`
	LogID    string       `json:"log_id,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
	Debug    *debugBody   `json:"debug,omitempty"`
}

// debugBody carries development only chain and upstream identifiers.
type debugBody struct {
	Chain    string          `json:"chain,omitempty"`
	Upstream *UpstreamDetail `json:"upstream,omitempty"`
	Stack    string          `json:"stack,omitempty"`
}

// Error is the only shape handlers write to client response bodies.
type Error struct {
	Code   Code
	Status int
	Title  string
	Detail string
	LogID  string
	Errors []FieldError
	Debug  *Debug
	cause  error
}

// New builds an Error from the catalog, defaulting status and title.
func New(code Code, status int, detail string) *Error {
	entry, ok := catalog[code]
	if !ok {
		entry.status = status
		entry.title = string(code)
		entry.slug = "unknown"
	}
	if status == 0 {
		status = entry.status
	}
	return &Error{Code: code, Status: status, Title: entry.title, Detail: detail}
}

// WithLogID attaches the upstream TikTok log id for support.
func (e *Error) WithLogID(id string) *Error {
	e.LogID = id
	return e
}

// WithFields attaches validator field errors to a 422 response.
func (e *Error) WithFields(fields []FieldError) *Error {
	e.Errors = fields
	return e
}

// WithDebug attaches development only detail.
func (e *Error) WithDebug(d *Debug) *Error {
	e.Debug = d
	return e
}

// Wrap records the internal cause for development debug output.
func (e *Error) Wrap(err error) *Error {
	e.cause = err
	return e
}

// Error satisfies the error interface with the client safe message.
func (e *Error) Error() string { return e.Detail }

// redactPair matches JSON "key":"value" pairs case-insensitively.
var redactPair = regexp.MustCompile(`(?i)"(client_secret|refresh_token|access_token|password|authorization)"\s*:\s*"[^"]*"`)

// redactBare matches unquoted key=value or key: value secrets in free text.
var redactBare = regexp.MustCompile(`(?i)(client_secret|refresh_token|access_token|password|authorization)\s*[:=]\s*[^\s,"'}]+`)

// redactKey matches one secret key name at the start of a match.
var redactKey = regexp.MustCompile(`(?i)^(client_secret|refresh_token|access_token|password|authorization)`)

// redact replaces secret values in the serialized body before writing.
func redact(raw []byte) []byte {
	out := redactPair.ReplaceAll(raw, []byte(`"$1":"[REDACTED]"`))
	return redactBare.ReplaceAllFunc(out, func(m []byte) []byte {
		sep := byte('=')
		for _, b := range m {
			if b == ':' || b == '=' {
				sep = b
				break
			}
		}
		next := append([]byte{}, redactKey.Find(m)...)
		next = append(next, sep)
		return append(next, []byte("[REDACTED]")...)
	})
}

// Write serializes err as production safe output for the request path.
func Write(w http.ResponseWriter, r *http.Request, err *Error) {
	(&Writer{}).Write(w, r, err)
}

// Write serializes err with this writer's development flag.
func (wr *Writer) Write(w http.ResponseWriter, r *http.Request, err *Error) {
	if err.Status == 0 {
		err.Status = http.StatusInternalServerError
	}
	entry, ok := catalog[err.Code]
	typeURI := ProblemBase + "unknown"
	title := err.Title
	if ok {
		typeURI = ProblemBase + entry.slug
		if title == "" {
			title = entry.title
		}
	}
	instance := "/"
	if r != nil && r.URL != nil {
		instance = r.URL.Path
		if instance == "" {
			instance = "/"
		}
	}
	body := problemBody{
		Type:     typeURI,
		Title:    title,
		Status:   err.Status,
		Detail:   err.Detail,
		Instance: instance,
		Code:     string(err.Code),
		LogID:    err.LogID,
		Errors:   err.Errors,
	}
	if wr != nil && wr.Development {
		dbg := &debugBody{}
		if err.Debug != nil {
			dbg.Chain = err.Debug.Chain
			if err.Debug.Upstream.HTTPStatus != 0 || err.Debug.Upstream.Code != "" {
				up := err.Debug.Upstream
				dbg.Upstream = &up
			}
			dbg.Stack = err.Debug.Stack
		}
		if err.cause != nil {
			dbg.Chain = err.cause.Error()
		}
		body.Debug = dbg
	}
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		http.Error(w, "error serialization failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(err.Status)
	_, _ = w.Write(redact(raw))
}
