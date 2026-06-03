package main

import "testing"

func TestValidateSmokeResponseRejectsBlankContent(t *testing.T) {
	if err := validateSmokeResponse("  ", "smoke-ok"); err == nil {
		t.Fatal("expected blank content to fail smoke validation")
	}
}

func TestValidateSmokeResponseRequiresExpectedContent(t *testing.T) {
	if err := validateSmokeResponse("smoke", "smoke-ok"); err == nil {
		t.Fatal("expected mismatched content to fail smoke validation")
	}
}

func TestValidateSmokeResponseAllowsExpectedContent(t *testing.T) {
	if err := validateSmokeResponse(" smoke-ok\n", "smoke-ok"); err != nil {
		t.Fatalf("expected matching content to pass: %v", err)
	}
}
