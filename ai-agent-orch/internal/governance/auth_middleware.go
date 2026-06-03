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
			if strings.HasPrefix(path, "/v1/admin/") {
				if !svc.RequireAdminRequest(w, r) {
					return
				}
				next.ServeHTTP(w, r)
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
