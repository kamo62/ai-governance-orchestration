package governance

import (
	"net/http"
	"strings"
)

// AuthMiddleware centralizes authentication at the HTTP layer.
// Public paths (healthz, readyz, metrics) skip auth.
// Admin paths require the admin token.
// All other paths require the ordinary dev/OIDC token.
func AuthMiddleware(svc *SessionService, publicPrefixes []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			for _, prefix := range publicPrefixes {
				if strings.HasPrefix(path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if path == "/v1/admin" || strings.HasPrefix(path, "/v1/admin/") {
				if !svc.RequireAdminRequest(w, r) {
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			// Backend control mutations are admin actions: the handler checks
			// the admin token on the same Authorization header, so the
			// middleware must not consume it as a dev credential first.
			if path == "/v1/backends" && r.Method != http.MethodGet {
				if !svc.RequireAdminRequest(w, r) {
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			// The admin token is a superset credential: a governance operator
			// holding it can read every actor-scoped surface as the
			// admin-operator subject without also carrying a dev token.
			if subject, ok := svc.AdminBearerSubject(r.Header.Get("Authorization")); ok {
				next.ServeHTTP(w, r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "admin"})))
				return
			}
			authReq, ok := svc.RequireAuthorizedRequest(w, r)
			if !ok {
				return
			}
			next.ServeHTTP(w, authReq)
		})
	}
}
