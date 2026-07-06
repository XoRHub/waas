// Package apierror implements RFC 7807 problem responses.
package apierror

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Problem is an RFC 7807 "problem details" payload.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Error implements the error interface so Problems can flow through service
// layers and be unwrapped at the handler boundary.
func (p *Problem) Error() string { return p.Title + ": " + p.Detail }

func newProblem(status int, slug, title, detail string) *Problem {
	return &Problem{
		Type:   "https://waas.xorhub.io/problems/" + slug,
		Title:  title,
		Status: status,
		Detail: detail,
	}
}

func BadRequest(detail string) *Problem {
	return newProblem(http.StatusBadRequest, "bad-request", "Bad Request", detail)
}

func Unauthorized(detail string) *Problem {
	return newProblem(http.StatusUnauthorized, "unauthorized", "Unauthorized", detail)
}

func Forbidden(detail string) *Problem {
	return newProblem(http.StatusForbidden, "forbidden", "Forbidden", detail)
}

func NotFound(detail string) *Problem {
	return newProblem(http.StatusNotFound, "not-found", "Not Found", detail)
}

func Conflict(detail string) *Problem {
	return newProblem(http.StatusConflict, "conflict", "Conflict", detail)
}

func Internal(detail string) *Problem {
	return newProblem(http.StatusInternalServerError, "internal", "Internal Server Error", detail)
}

func Unavailable(detail string) *Problem {
	return newProblem(http.StatusServiceUnavailable, "unavailable", "Service Unavailable", detail)
}

// statusOf unwraps the Problem status, 0 for non-Problem errors.
func statusOf(err error) int {
	var p *Problem
	if errors.As(err, &p) {
		return p.Status
	}
	return 0
}

func IsBadRequest(err error) bool { return statusOf(err) == http.StatusBadRequest }
func IsForbidden(err error) bool  { return statusOf(err) == http.StatusForbidden }
func IsNotFound(err error) bool   { return statusOf(err) == http.StatusNotFound }

// Write renders err as an RFC 7807 response. Non-Problem errors become an
// opaque 500 so internals never leak to clients.
func Write(w http.ResponseWriter, err error) {
	var p *Problem
	if !errors.As(err, &p) {
		p = Internal("an unexpected error occurred")
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
