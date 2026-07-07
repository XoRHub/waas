package handler

import (
	"net/http"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/naming"
	"github.com/xorhub/waas/operator/pkg/params"
)

// MetaHandler serves platform metadata the frontend derives its forms
// from. The payload is the operator's parameter registry verbatim: the
// forms, the admission webhook and the docs all read the same table.
type MetaHandler struct{}

func NewMetaHandler() *MetaHandler { return &MetaHandler{} }

type protocolMeta struct {
	Name   string         `json:"name"`
	Params []params.Param `json:"params"`
}

// Protocols handles GET /api/v1/meta/protocols.
func (h *MetaHandler) Protocols(w http.ResponseWriter, _ *http.Request) {
	out := make([]protocolMeta, 0, 3)
	for _, proto := range params.Protocols() {
		out = append(out, protocolMeta{Name: proto, Params: params.ForProtocol(proto)})
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	ok(w, out)
}

// Placeholders handles GET /api/v1/meta/placeholders: the namespace
// pattern tokens with their sources, straight from the naming engine —
// the pattern editor's contextual help can never drift from the code.
func (h *MetaHandler) Placeholders(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "private, max-age=3600")
	ok(w, naming.Placeholders())
}

type overrideFieldMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// OverrideFields handles GET /api/v1/meta/override-fields: the canonical
// list of governable override fields with their semantics. The policy
// and template editors derive their choices and validation from it, so
// the frontend never carries its own copy of the list (the drift that
// once made "schedule" un-editable from the UI).
func (h *MetaHandler) OverrideFields(w http.ResponseWriter, _ *http.Request) {
	desc := waasv1alpha1.OverridableFieldDescriptions()
	out := make([]overrideFieldMeta, 0, len(waasv1alpha1.AllOverridableFields()))
	for _, f := range waasv1alpha1.AllOverridableFields() {
		out = append(out, overrideFieldMeta{Name: string(f), Description: desc[f]})
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	ok(w, out)
}
