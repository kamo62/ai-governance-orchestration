package modelgateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeAuthCompositeKey(t *testing.T) {
	gateway := &Gateway{runtimeToken: "rt-secret"}
	cases := []struct {
		bearer string
		actor  string
		ok     bool
	}{
		{"rt-secret", "", true},
		{"rt-secret.kamogelo", "kamogelo", true},
		{"rt-secret.dev_user-1@org", "dev_user-1@org", true},
		{"rt-secret.", "", false},
		{"rt-secret.bad actor", "", false},
		{"wrong.kamogelo", "", false},
		{"rt-secre", "", false},
	}
	for _, tc := range cases {
		r := newAuthRequest(tc.bearer)
		actor, ok := gateway.runtimeAuth(r)
		if ok != tc.ok || actor != tc.actor {
			t.Fatalf("bearer %q: got actor=%q ok=%v, want actor=%q ok=%v", tc.bearer, actor, ok, tc.actor, tc.ok)
		}
	}
}

func newAuthRequest(bearer string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+bearer)
	return r
}
