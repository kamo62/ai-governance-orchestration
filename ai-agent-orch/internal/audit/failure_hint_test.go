package audit

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFailureHint(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil",
			err:  nil,
			want: "",
		},
		{
			name: "sqlite busy",
			err:  errors.New("seed audit chain head: database is locked"),
			want: "SQLite is temporarily locked",
		},
		{
			name: "chain conflict",
			err:  errors.New("chain append: concurrent append conflict"),
			want: "Audit hash chain conflict",
		},
		{
			name: "prev hash mismatch",
			err:  fmt.Errorf("update audit chain head: %w", errors.New("prev hash mismatch")),
			want: "Audit hash chain conflict",
		},
		{
			name: "generic",
			err:  errors.New("insert audit event: disk full"),
			want: "Audit persistence failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureHint(tc.err)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("expected empty hint, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected hint containing %q, got %q", tc.want, got)
			}
		})
	}
}
