package confidence

// Band is the shared vocabulary for numeric evidence confidence.
type Band string

const (
	High                 Band = "high"
	Medium               Band = "medium"
	Low                  Band = "low"
	InsufficientEvidence Band = "insufficient_evidence"
)

// ToBand maps a numeric confidence to its reporting band.
func ToBand(v float64) Band {
	switch {
	case v >= 0.9:
		return High
	case v >= 0.7:
		return Medium
	case v >= 0.4:
		return Low
	default:
		return InsufficientEvidence
	}
}
