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

// Auth validates the access token on every request and stores the claims
// in the request context. Two transports, in this order:
//
//   - the Authorization header — every non-browser client, unchanged;
//   - the session cookie — browsers, which must not hold a credential
//     JavaScript can read.
//
// Query-string credentials are never accepted here: they end up in proxy
// access logs, browser history and Referer headers. The SSE stream, which
// cannot set headers, has its own middleware (StreamAuth) with its own
// token audience.
func Auth(signer *auth.Signer, issuer string, users UserSource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, fromCookie := accessToken(r)
			if token == "" {
				apierror.Write(w, apierror.Unauthorized("missing bearer token"))
				return
			}
			// A cookie rides along automatically, so it is the transport a
			// cross-site page could abuse; the header is not, since an
			// attacker's page cannot set it. Hence the check applies to the
			// cookie only.
			if fromCookie && !sameOriginRequest(r) {
				// Written directly, NOT through denySession: expiring the
				// cookie here would hand any cross-site page a one-request
				// way to sign the user out.
				apierror.Write(w, apierror.Unauthorized("cross-site request rejected"))
				return
			}
			claims, err := auth.VerifyAccessToken(token, issuer, signer.Public())
			if err != nil {
				denySession(w, r, fromCookie, apierror.Unauthorized("invalid or expired token"))
				return
			}
			if err := vetBearer(r.Context(), users, claims); err != nil {
				denySession(w, r, fromCookie, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
		})
	}
}

// accessToken pulls the token from the header first, then the cookie, and
// reports which one answered.
func accessToken(r *http.Request) (token string, fromCookie bool) {
	if header, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && header != "" {
		return header, false
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
}

// sameOriginRequest is the CSRF guard on the cookie transport. Fetch
// Metadata rather than a synchronized token: every current browser sends
// Sec-Fetch-Site, and the header cannot be forged from a page (it is
// forbidden to scripts). Requests without it are not browsers — they use
// the Authorization transport — and SameSite=Strict on the cookie is the
// backstop that stops it from being attached cross-site in the first
// place. "same-origin" only: the platform is served from one origin, so
// "same-site" would needlessly admit a sibling subdomain.
func sameOriginRequest(r *http.Request) bool {
	switch site := r.Header.Get("Sec-Fetch-Site"); site {
	case "":
		return true // not a browser; SameSite already governed the cookie
	case "same-origin":
		return true
	default: // same-site, cross-site, none
		return false
	}
}

// SameOrigin applies that same guard to the routes that MINT the session
// cookie. Auth covers the routes that consume it, but login runs before
// any credential exists, so SameSite is no protection there: it governs
// whether a cookie is SENT, not whether one may be SET, and a top-level
// form POST lands in a first-party context. Ungated, a cross-site form
// submitting the attacker's own credentials silently signs the victim's
// browser into the attacker's account (login CSRF / session fixation) —
// and since the SPA now derives identity solely from the cookie, the
// victim's workspaces and clipboard land in that account.
//
// The "header absent" branch keeps non-browser clients working: they read
// the token from the JSON body and never rely on the cookie. The OIDC
// callback also mints the cookie but must NOT be gated — it arrives as a
// cross-site navigation from the IdP, and the one-shot state cookie is
// what authenticates it.
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginRequest(r) {
			apierror.Write(w, apierror.Forbidden("cross-site request rejected"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// denySession writes an authentication failure, expiring the session
// cookie when that cookie is what just failed. Without this a browser
// whose token expired or was revoked keeps sending a dead credential on
// every request until its Expires date — including the logout call meant
// to get rid of it, which never reaches its handler because this
// middleware rejects it first. Only 401 clears: a 503 means the server
// could not tell, and throwing the cookie away over a database hiccup
// would sign the fleet out.
func denySession(w http.ResponseWriter, r *http.Request, fromCookie bool, err error) {
	if fromCookie && apierror.IsUnauthorized(err) {
		ClearSessionCookie(w, r)
	}
	apierror.Write(w, err)
}

// vetBearer re-checks the token bearer against CURRENT user state — this
// is what makes deactivation, demotion and logout effective immediately
// instead of at token expiry (security audit 2026-07-20, finding #2). One
// primary-key read per request, deliberately uncached: a cache would
// reopen the revocation window this exists to close. Returns nil when the
// bearer may pass, and the problem to answer with otherwise — the caller
// writes it, since only the caller knows which transport carried the
// token and therefore whether a cookie needs expiring.
func vetBearer(ctx context.Context, users UserSource, claims *auth.AccessClaims) error {
	user, err := users.FindByID(ctx, claims.Subject)
	switch {
	case errors.Is(err, repository.ErrUserNotFound):
		return apierror.Unauthorized("account no longer exists")
	case err != nil:
		// NOT a 401: the frontend drops its auth state on every 401, so a
		// database hiccup must read as a server failure, never as a
		// revocation — or an outage logs out the whole fleet.
		slog.ErrorContext(ctx, "auth user lookup failed", "user", claims.Subject, "error", err)
		return apierror.Unavailable("user state is temporarily unavailable")
	case !user.Active:
		return apierror.Unauthorized("account is disabled")
	case user.Role != claims.Role:
		// A role that diverged from the claims rejects instead of silently
		// serving the database role: honoring the token with a substituted
		// role would split the request between two sources of truth.
		// Re-login mints claims matching the new role.
		return apierror.Unauthorized("role has changed — sign in again")
	case revoked(user, claims):
		return apierror.Unauthorized("token has been revoked")
	}
	return nil
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
			if err := vetBearer(r.Context(), users, claims); err != nil {
				// No cookie on this path — the stream token travels in the
				// query string — so nothing to expire.
				apierror.Write(w, err)
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
				// Non-safelisted response headers are hidden from JS on a
				// cross-origin answer. The portal never gets here — it
				// fetches relative paths, and its SameSite=Strict cookie
				// would not travel cross-site anyway — but a browser client
				// served from another origin would otherwise lose the
				// session-ended announcement silently, which is worse than
				// not having it.
				w.Header().Set("Access-Control-Expose-Headers", SessionEndedHeader)
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
