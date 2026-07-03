// Package handler contains the HTTP handlers. Handlers are methods on
// injectable structs, call services only (never repositories), and speak the
// {"data": …} envelope with RFC 7807 errors.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/response"
)

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

func pagination(r *http.Request) (page, pageSize int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}

func decode(r *http.Request, into any) error {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		return apierror.BadRequest("invalid JSON body")
	}
	return nil
}

// fail logs unexpected errors once (at the HTTP entry point, per the
// no-double-logging rule) and renders the RFC 7807 response.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	var problem *apierror.Problem
	if !asProblem(err, &problem) || problem.Status >= 500 {
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	apierror.Write(w, err)
}

func asProblem(err error, target **apierror.Problem) bool {
	p, ok := err.(*apierror.Problem)
	if ok {
		*target = p
	}
	return ok
}

func ok(w http.ResponseWriter, data any) {
	response.JSON(w, http.StatusOK, data)
}

func created(w http.ResponseWriter, data any) {
	response.JSON(w, http.StatusCreated, data)
}

func noContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func list(w http.ResponseWriter, data any, total, page, pageSize int) {
	response.List(w, http.StatusOK, data, response.Meta{Total: total, Page: page, PageSize: pageSize})
}
