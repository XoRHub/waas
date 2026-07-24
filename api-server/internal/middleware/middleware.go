// Package middleware provides the HTTP middlewares of the API server.
// The JWT middleware guards every /api/v1 route except login — there are no
// bypass routes.
package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/shared/auth"
)

type contextKey int

const claimsKey contextKey = iota

// Auth validates the Bearer access token on every request and stores the
// claims in the request context. The Authorization header is the ONLY
// accepted transport: query-string credentials end up in proxy access logs,
// browser history and Referer headers, so they are never read here. The SSE
// stream, which cannot set headers, has its own middleware (StreamAuth) with
// its own token audience.
func Auth(signer *auth.Signer, issuer string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || token == "" {
				apierror.Write(w, apierror.Unauthorized("missing bearer token"))
				return
			}
			claims, err := auth.VerifyAccessToken(token, issuer, signer.Public())
			if err != nil {
				apierror.Write(w, apierror.Unauthorized("invalid or expired token"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
		})
	}
}

// StreamAuth authenticates the SSE stream ONLY. EventSource cannot set
// headers, so the short-lived waas-stream token travels as the access_token
// query parameter — a deliberate second auth path with its own audience,
// sealed off in both directions: an API bearer in the query string and a
// stream token in an Authorization header are both rejected. The claims
// land in the context in the same shape, so Actor works unchanged.
func StreamAuth(signer *auth.Signer, issuer string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get("access_token")
			if token == "" {
				apierror.Write(w, apierror.Unauthorized("missing stream token"))
				return
			}
			claims, err := auth.VerifyStreamToken(token, issuer, signer.Public())
			if err != nil {
				apierror.Write(w, apierror.Unauthorized("invalid or expired stream token"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
		})
	}
}

// RequireAdmin rejects non-admin callers. Must run after Auth.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(claimsKey).(*auth.AccessClaims)
		if !ok || claims.Role != auth.RoleAdmin {
			apierror.Write(w, apierror.Forbidden("admin role required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Actor extracts the authenticated actor from the request. The username is
// not carried in the token; handlers that need it enrich the actor from the
// user service when relevant.
func Actor(r *http.Request) service.Actor {
	actor := service.Actor{ClientIP: chimiddleware.GetClientIP(r.Context())}
	if claims, ok := r.Context().Value(claimsKey).(*auth.AccessClaims); ok {
		actor.ID = claims.Subject
		actor.Role = string(claims.Role)
	}
	return actor
}

// Internal authenticates service-to-service calls (WebSocket proxy) with a
// shared token, compared in constant time.
func Internal(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get("X-Internal-Token")
			if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				apierror.Write(w, apierror.Unauthorized("invalid internal token"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS applies a permissive-for-listed-origins policy; only used in dev
// where the Vite server runs on another port.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, origin := range allowedOrigins {
		allowed[strings.TrimSpace(origin)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
