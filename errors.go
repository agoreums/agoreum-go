package agoreum

import (
	"errors"
	"fmt"
)

// APIError is returned for every request that reaches the server and fails. It
// mirrors the API error envelope:
//
//	{"error": {"code": "...", "message": "...", "details": {...}, "request_id": "..."}}
//
// Branch on it with errors.As, or use the Is* helpers (IsNotFound, IsRateLimited, …).
type APIError struct {
	// StatusCode is the HTTP status of the response.
	StatusCode int
	// Code is the machine-readable error code, e.g. "not_found" or "insufficient_scope".
	Code string
	// Message is the human-readable message from the API.
	Message string
	// Details carries any structured context (e.g. the scopes a key is missing).
	Details map[string]any
	// RequestID identifies the request in the API's logs, when present.
	RequestID string
	// RetryAfter is the seconds the API asked the caller to wait (429 only), else 0.
	RetryAfter float64
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", e.StatusCode)
	}
	if e.Code != "" {
		return fmt.Sprintf("agoreum: %s (code=%s status=%d)", msg, e.Code, e.StatusCode)
	}
	return fmt.Sprintf("agoreum: %s (status=%d)", msg, e.StatusCode)
}

// ConnectionError wraps a transport failure, the request never got a response
// (DNS, TCP, TLS, a dropped connection, or a timeout). The underlying cause is
// available via errors.Unwrap.
type ConnectionError struct {
	// Timeout is true when the failure was a deadline or context timeout.
	Timeout bool
	err     error
}

func (e *ConnectionError) Error() string {
	if e.Timeout {
		return fmt.Sprintf("agoreum: request timed out: %v", e.err)
	}
	return fmt.Sprintf("agoreum: could not reach Agoreum: %v", e.err)
}

func (e *ConnectionError) Unwrap() error { return e.err }

// asAPIError extracts an *APIError from err, if present.
func asAPIError(err error) (*APIError, bool) {
	var e *APIError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

func hasStatus(err error, status int) bool {
	if e, ok := asAPIError(err); ok {
		return e.StatusCode == status
	}
	return false
}

// IsAuthError reports whether err is a 401 (missing, invalid, expired, or revoked key).
func IsAuthError(err error) bool { return hasStatus(err, 401) }

// IsPermissionDenied reports whether err is a 403 (valid key, action not allowed).
func IsPermissionDenied(err error) bool { return hasStatus(err, 403) }

// IsInsufficientScope reports whether err is a 403 caused by a missing scope.
func IsInsufficientScope(err error) bool {
	if e, ok := asAPIError(err); ok {
		return e.StatusCode == 403 && e.Code == "insufficient_scope"
	}
	return false
}

// IsNotFound reports whether err is a 404.
func IsNotFound(err error) bool { return hasStatus(err, 404) }

// IsConflict reports whether err is a 409.
func IsConflict(err error) bool { return hasStatus(err, 409) }

// IsRateLimited reports whether err is a 429.
func IsRateLimited(err error) bool { return hasStatus(err, 429) }

// IsServerError reports whether err is a 5xx.
func IsServerError(err error) bool {
	if e, ok := asAPIError(err); ok {
		return e.StatusCode >= 500
	}
	return false
}
