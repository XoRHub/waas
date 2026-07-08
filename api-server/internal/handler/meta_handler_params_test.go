package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// The params field is an API contract: ALWAYS an array. A nil Go slice
// serialized as null once crashed every frontend param form the moment a
// kasmvnc template was selected (kasmvnc has no registry entries).
func TestMetaProtocolsParamsNeverNull(t *testing.T) {
	h := NewMetaHandler()
	rec := httptest.NewRecorder()
	h.Protocols(rec, httptest.NewRequest("GET", "/api/v1/meta/protocols", nil))

	if strings.Contains(rec.Body.String(), `"params":null`) {
		t.Fatalf("params must never be null: %s", rec.Body.String())
	}
	var payload struct {
		Data []struct {
			Name   string            `json:"name"`
			Params []json.RawMessage `json:"params"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range payload.Data {
		seen[entry.Name] = true
		if entry.Params == nil {
			t.Fatalf("protocol %q has null params", entry.Name)
		}
	}
	for _, proto := range []string{"vnc", "rdp", "ssh", "kasmvnc"} {
		if !seen[proto] {
			t.Fatalf("protocol %q missing from meta", proto)
		}
	}
}
