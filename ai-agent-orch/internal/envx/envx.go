// Package envx holds the shared environment-variable helpers.
package envx

import "os"

// OrDefault returns the value of the environment variable key, or fallback
// when the variable is unset or empty.
func OrDefault(key string, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
