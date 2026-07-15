package confidence

import "testing"

func TestToBandBoundaries(t *testing.T) {
	tests := []struct {
		value float64
		want  Band
	}{
		{1, High},
		{0.9, High},
		{0.899999, Medium},
		{0.7, Medium},
		{0.699999, Low},
		{0.4, Low},
		{0.399999, InsufficientEvidence},
		{0, InsufficientEvidence},
	}
	for _, test := range tests {
		if got := ToBand(test.value); got != test.want {
			t.Errorf("ToBand(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}
