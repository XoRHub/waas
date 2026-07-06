package handler

import (
	"net/http"
	"reflect"

	"github.com/go-chi/chi/v5"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/scaffold"
	"github.com/xorhub/waas/api-server/internal/service"
)

// scaffoldTypes maps a scaffold kind to the exact PUT payload type the
// governance editors submit. Deriving the skeleton from these Go types
// (the same types the CRDs are generated from) means the scaffold can
// never drift from the API — no hand-maintained template.
var scaffoldTypes = map[string]reflect.Type{
	"workspacepolicy": reflect.TypeOf(service.UpsertPolicyInput{}),
	"workspaceimage":  reflect.TypeOf(service.UpsertImageInput{}),
}

// Scaffold handles GET /api/v1/meta/scaffold/{kind}: a YAML skeleton with
// EVERY field of the object, so admins discover the whole schema in the
// editor instead of going back to the docs. The skeleton is valid as
// generated (see internal/scaffold); examples live in comments.
func (h *MetaHandler) Scaffold(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	t, known := scaffoldTypes[kind]
	if !known {
		fail(w, r, apierror.NotFound("unknown scaffold kind "+kind))
		return
	}
	out, err := scaffold.YAML(t)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	ok(w, map[string]string{"scaffold": out})
}
