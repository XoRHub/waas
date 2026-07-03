// Package response implements the API envelope: every success is wrapped in
// {"data": …}, lists additionally carry {"meta": {total, page, page_size}}.
package response

import (
	"encoding/json"
	"net/http"
)

// Meta is the pagination block attached to list responses.
type Meta struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type envelope struct {
	Data any   `json:"data"`
	Meta *Meta `json:"meta,omitempty"`
}

// JSON writes a single-resource success response.
func JSON(w http.ResponseWriter, status int, data any) {
	write(w, status, envelope{Data: data})
}

// List writes a collection response with pagination metadata.
func List(w http.ResponseWriter, status int, data any, meta Meta) {
	write(w, status, envelope{Data: data, Meta: &meta})
}

func write(w http.ResponseWriter, status int, body envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
