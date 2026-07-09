package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/xorhub/waas/api-server/internal/metrics"
)

// Metrics observes every request into the Prometheus counters/histogram.
// The route label is the chi PATTERN ("/api/v1/workspaces/{id}"), read
// after the handler ran so nested mounts are fully resolved; raw paths
// never become label values.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		status := ww.Status()
		if status == 0 {
			// Nothing explicitly written: net/http answers 200.
			status = http.StatusOK
		}
		metrics.HTTPRequests.WithLabelValues(route, r.Method, strconv.Itoa(status)).Inc()
		metrics.HTTPDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}
