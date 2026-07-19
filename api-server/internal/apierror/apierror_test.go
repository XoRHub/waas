package apierror

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConstructorsCarryStatusSlugAndDetail(t *testing.T) {
	cases := []struct {
		p      *Problem
		status int
		slug   string
	}{
		{BadRequest("d"), http.StatusBadRequest, "bad-request"},
		{Unauthorized("d"), http.StatusUnauthorized, "unauthorized"},
		{Forbidden("d"), http.StatusForbidden, "forbidden"},
		{NotFound("d"), http.StatusNotFound, "not-found"},
		{Conflict("d"), http.StatusConflict, "conflict"},
		{Internal("d"), http.StatusInternalServerError, "internal"},
		{Unavailable("d"), http.StatusServiceUnavailable, "unavailable"},
		{BadGateway("d"), http.StatusBadGateway, "upstream-error"},
	}
	for _, c := range cases {
		if c.p.Status != c.status {
			t.Errorf("%s: status = %d, want %d", c.slug, c.p.Status, c.status)
		}
		if want := "https://waas.xorhub.io/problems/" + c.slug; c.p.Type != want {
			t.Errorf("type = %q, want %q", c.p.Type, want)
		}
		if c.p.Detail != "d" {
			t.Errorf("%s: detail lost: %q", c.slug, c.p.Detail)
		}
	}
}

func TestErrorMessageJoinsTitleAndDetail(t *testing.T) {
	if got := NotFound("workspace w1").Error(); got != "Not Found: workspace w1" {
		t.Fatalf("Error() = %q", got)
	}
}

// The Is* helpers must see through wrapping — services annotate Problems
// with fmt.Errorf("%w") before returning them.
func TestIsHelpersUnwrap(t *testing.T) {
	wrapped := fmt.Errorf("loading template: %w", NotFound("gone"))
	if !IsNotFound(wrapped) {
		t.Error("IsNotFound must unwrap")
	}
	if !IsBadRequest(BadRequest("x")) || !IsForbidden(Forbidden("x")) {
		t.Error("direct Problems must match their helper")
	}
	if IsNotFound(fmt.Errorf("plain")) || IsForbidden(nil) {
		t.Error("non-Problem errors match nothing")
	}
}

func TestWriteRendersProblemJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, Conflict("name taken"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q", ct)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if p.Title != "Conflict" || p.Detail != "name taken" {
		t.Fatalf("payload = %+v", p)
	}
}

// A non-Problem error must never leak its message to the client.
func TestWriteOpaques500ForPlainErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, fmt.Errorf("pq: connection refused on 10.0.0.3"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if p.Detail != "an unexpected error occurred" {
		t.Fatalf("internals leaked: %q", p.Detail)
	}
}
