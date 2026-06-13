package envx

import "testing"

func TestOrDefault(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		t.Setenv("ENVX_TEST_KEY", "configured")
		if got := OrDefault("ENVX_TEST_KEY", "fallback"); got != "configured" {
			t.Fatalf("OrDefault = %q, want %q", got, "configured")
		}
	})

	t.Run("returns fallback when unset", func(t *testing.T) {
		if got := OrDefault("ENVX_TEST_KEY_UNSET", "fallback"); got != "fallback" {
			t.Fatalf("OrDefault = %q, want %q", got, "fallback")
		}
	})

	t.Run("returns fallback when empty", func(t *testing.T) {
		t.Setenv("ENVX_TEST_KEY", "")
		if got := OrDefault("ENVX_TEST_KEY", "fallback"); got != "fallback" {
			t.Fatalf("OrDefault = %q, want %q", got, "fallback")
		}
	})
}
