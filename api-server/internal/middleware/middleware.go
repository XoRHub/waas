// Package middleware provides the HTTP middlewares of the API server.
// The JWT middleware guards every /api/v1 route except login — there are no
// bypass routes.
package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/shared/auth"
)

type contextKey int

const claimsKey contextKey = iota

// UserSource is the middleware's window on user state: the single by-ID
// read Auth/StreamAuth performs after cryptographic validation.
// Deliberately one method — not the whole repository — so tests double it
// with a func.
type UserSource interface {
	FindByID(ctx context.Context, id string) (*model.User, error)
}

// Auth validates the Bearer access token on every request and stores the
// claims in the request context. The Authorization header is the ONLY
// accepted transport: query-string credentials end up in proxy access logs,
// browser history and Referer headers, so they are never read here. The SSE
// stream, which cannot set headers, has its own middleware (StreamAuth) with
// its own token audience.
func Auth(signer *auth.Signer, issuer string, users UserSource) func(http.Handler) http.Handler {
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
			if !vetBearer(w, r, users, claims) {
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
		})
	}
}

// vetBearer re-checks the token bearer against CURRENT user state — this
// is what makes deactivation, demotion and logout effective immediately
// instead of at token expiry (security audit 2026-07-20, finding #2). One
// primary-key read per request, deliberately uncached: a cache would
// reopen the revocation window this exists to close. Writes the response
// and returns false when the bearer must not pass.
func vetBearer(w http.ResponseWriter, r *http.Request, users UserSource, claims *auth.AccessClaims) bool {
	user, err := users.FindByID(r.Context(), claims.Subject)
	switch {
	case errors.Is(err, repository.ErrUserNotFound):
		apierror.Write(w, apierror.Unauthorized("account no longer exists"))
	case err != nil:
		// NOT a 401: the frontend drops its auth state on every 401, so a
		// database hiccup must read as a server failure, never as a
		// revocation — or an outage logs out the whole fleet.
		slog.ErrorContext(r.Context(), "auth user lookup failed", "user", claims.Subject, "error", err)
		apierror.Write(w, apierror.Unavailable("user state is temporarily unavailable"))
	case !user.Active:
		apierror.Write(w, apierror.Unauthorized("account is disabled"))
	case user.Role != claims.Role:
		// A role that diverged from the claims rejects instead of silently
		// serving the database role: honoring the token with a substituted
		// role would split the request between two sources of truth.
		// Re-login mints claims matching the new role.
		apierror.Write(w, apierror.Unauthorized("role has changed — sign in again"))
	case revoked(user, claims):
		apierror.Write(w, apierror.Unauthorized("token has been revoked"))
	default:
		return true
	}
	return false
}

// revoked reports whether the token was issued before the user's validity
// bound. The JWT iat is second-truncated (RFC 7519 NumericDate) while the
// bound keeps sub-second precision: comparing the truncated iat against
// the precise bound errs toward revocation, so a logout within the same
// second as the login still kills the token instead of leaving it alive
// until the next second boundary. A bound with a missing iat fails closed.
//
// Known flip side, accepted: a token minted in the SAME second as a
// revocation is rejected too, so signing back in immediately after a
// logout yields a token that 401s on its first call and bounces the user
// to the login page — a retry a second later works. At second
// granularity "issued just before" and "issued just after" are
// indistinguishable, so the tie has to fall one way; it falls toward
// revocation, because the opposite would let the token you just walked
// away from live out its remaining hours. Closing the gap for real would
// mean setting jwt.TimePrecision below a second, a package-global that
// would also reshape connection tokens and everything reading the JWKS —
// too much blast radius for a one-second window that heals itself.
func revoked(user *model.User, claims *auth.AccessClaims) bool {
	if user.TokensValidAfter == nil {
		return false
	}
	return claims.IssuedAt == nil || claims.IssuedAt.Before(*user.TokensValidAfter)
}

// StreamAuth authenticates the SSE stream ONLY. EventSource cannot set
// headers, so the short-lived waas-stream token travels as the access_token
// query parameter — a deliberate second auth path with its own audience,
// sealed off in both directions: an API bearer in the query string and a
// stream token in an Authorization header are both rejected. The claims
// land in the context in the same shape, so Actor works unchanged.
func StreamAuth(signer *auth.Signer, issuer string, users UserSource) func(http.Handler) http.Handler {
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
			// Same state re-check as Auth: an unexpired stream token must
			// not open a NEW stream after revocation. A stream already
			// open is never re-vetted — documented limitation.
			if !vetBearer(w, r, users, claims) {
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
