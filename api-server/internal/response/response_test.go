package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJSONWrapsInDataEnvelopeWithoutMeta(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusCreated, map[string]string{"id": "w1"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if string(body["data"]) != `{"id":"w1"}` {
		t.Fatalf("data = %s", body["data"])
	}
	if _, ok := body["meta"]; ok {
		t.Fatal("single-resource responses must not carry meta")
	}
}

func TestListAttachesPaginationMeta(t *testing.T) {
	rec := httptest.NewRecorder()
	List(rec, http.StatusOK, []string{"a", "b"}, Meta{Total: 12, Page: 2, PageSize: 2})

	var body struct {
		Data []string `json:"data"`
		Meta *Meta    `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(body.Data) != 2 || body.Meta == nil {
		t.Fatalf("body = %+v", body)
	}
	if body.Meta.Total != 12 || body.Meta.Page != 2 || body.Meta.PageSize != 2 {
		t.Fatalf("meta = %+v", body.Meta)
	}
}
