// Package basicauth is the sidecar's one credential check: the Basic
// pair wwt injects for KasmVNC (the password is the kasmweb VNC_PW).
// wwt has already validated the connection JWT and the policy grant
// before any request gets here.
package basicauth

import (
	"crypto/subtle"
	"net/http"
)

// Require answers 401 unless the request carries the password. No
// WWW-Authenticate challenge: the response is relayed by wwt to a browser
// fetch, and a Basic challenge there would pop the browser's own login
// dialog.
func Require(password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pw, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(password)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
