package resources

import (
	"encoding/json"
	"math"
)

// TrendSeries is one dashboard trend series on a fixed time grid, oldest
// first. A non-finite entry (NaN or ±Inf) is a step with no usable sample and
// encodes as JSON null, so the step keeps its slot: the reader places entry k
// at its own time instead of shifting every older sample later.
//
// The encoder writes null rather than failing on a non-finite value.
// encoding/json rejects NaN, and because writeJSON has already sent the 200
// header by the time it encodes, a rejected value truncates the body and
// blanks every sparkline at once.
type TrendSeries []float64

// MarshalJSON encodes a nil series as null and each non-finite entry as null.
func (s TrendSeries) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	out := make([]*float64, len(s))
	for i := range s {
		if math.IsNaN(s[i]) || math.IsInf(s[i], 0) {
			continue
		}
		out[i] = &s[i]
	}
	return json.Marshal(out)
}
