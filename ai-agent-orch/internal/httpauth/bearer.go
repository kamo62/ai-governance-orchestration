package httpauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireBearerToken protects local internal endpoints with a configured bearer token.
func RequireBearerToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if token == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"bearer token not configured"}` + "\n"))
			return
		}
		if !AuthorizedBearer(r.Header.Get("Authorization"), token) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func AuthorizedBearer(header string, token string) bool {
	const prefix = "Bearer "
	if token == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(token)) == 1
}
