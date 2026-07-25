package middleware

import (
	"net/http"
	"strings"
	"time"
)

// The session cookie has exactly one owner: this file. Name, path and
// attributes are read by Auth and written by the auth handler, and a
// cookie set with attributes that do not match the ones it is cleared
// with is a cookie the browser keeps — so both sides go through here
// rather than each spelling them out.

// SessionCookie is the browser transport for the access token: the same
// waas-api JWT the Authorization header carries, kept out of JavaScript's
// reach (security audit 2026-07-20, finding #13).
const SessionCookie = "waas_session"

// sessionCookiePath scopes the cookie to the API. wwt's /ws and /kasm sit
// outside it on purpose: they authenticate with connection tokens, and a
// platform session has no business being sent there.
const sessionCookiePath = "/api"

// SetSessionCookie writes the session cookie for a freshly authenticated
// browser. SameSite=Strict is affordable here even though it normally
// breaks cold external links: the SPA document is static and served
// without auth, and every authenticated call is a same-site XHR that
// carries the cookie anyway.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     sessionCookiePath,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   IsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie expires the cookie. Attributes must match the ones
// used when setting it, or the browser keeps the original.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Path:     sessionCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   IsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// SessionEndedHeader tells the SPA that the response it is reading ended
// the caller's own session, and why. A browser cannot see this any other
// way: the credential is an HttpOnly cookie, and `Set-Cookie` is a
// forbidden response header for JavaScript — so an endpoint that revokes
// its own caller has to say so out of band, or the UI keeps rendering a
// session that no longer exists until the next request happens to 401.
//
// Values are the frontend's SignedOutReason vocabulary; the SPA ignores
// anything it does not know.
const SessionEndedHeader = "X-Waas-Session-Ended"

// Session-ending reasons carried by SessionEndedHeader.
const (
	// SessionEndedPasswordChanged: the caller changed their own password.
	SessionEndedPasswordChanged = "password-changed"
	// SessionEndedRightsChanged: the caller demoted, deactivated or reset
	// the password of their own account through the admin API.
	SessionEndedRightsChanged = "rights-changed"
)

// EndSession closes the caller's browser session in the response being
// written: it expires the cookie and names the reason. THE single way to
// do it — a handler that only cleared the cookie would leave the SPA
// unable to tell why, and one that only set the header would leave a dead
// credential in the jar.
//
// It does not revoke anything: the caller has already done that (the
// revocation is what makes this necessary), and this cannot be inferred
// from the response body.
func EndSession(w http.ResponseWriter, r *http.Request, reason string) {
	ClearSessionCookie(w, r)
	w.Header().Set(SessionEndedHeader, reason)
}

// IsHTTPS reports whether the request reached the platform over TLS. The
// ingress terminates it, so the connection to this process is plain http
// and the forwarded header is the only truth available.
//
// The header is a comma-separated list when more than one proxy is in the
// path, client hop first — comparing the whole value would read
// "https, http" as not-https and hand out the session cookie without
// Secure on a perfectly ordinary chained-proxy deployment, leaving the
// browser free to send a bearer-equivalent credential in cleartext. The
// value is case-insensitive per RFC 9110.
func IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
