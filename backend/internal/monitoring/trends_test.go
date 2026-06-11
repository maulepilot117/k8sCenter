package monitoring

import (
	"math"
	"testing"

	"github.com/prometheus/common/model"
)

func sample(v float64) model.SamplePair {
	return model.SamplePair{Timestamp: 0, Value: model.SampleValue(v)}
}

func TestParseMatrixSeries_Nil(t *testing.T) {
	if got := parseMatrixSeries(nil); got != nil {
		t.Fatalf("nil value: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_WrongType(t *testing.T) {
	// A range query that resolved to a Vector (instant) instead of a Matrix.
	if got := parseMatrixSeries(model.Vector{}); got != nil {
		t.Fatalf("vector value: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_EmptyMatrix(t *testing.T) {
	if got := parseMatrixSeries(model.Matrix{}); got != nil {
		t.Fatalf("empty matrix: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_EmptyValues(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{}}}
	if got := parseMatrixSeries(m); got != nil {
		t.Fatalf("series with no samples: want nil, got %v", got)
	}
}

func TestParseMatrixSeries_HappyPath(t *testing.T) {
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		sample(3), sample(5), sample(4),
	}}}
	got := parseMatrixSeries(m)
	want := []float64{3, 5, 4}
	if len(got) != len(want) {
		t.Fatalf("length: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: want %v, got %v", i, want[i], got[i])
		}
	}
}

func TestParseMatrixSeries_FirstSeriesOnly(t *testing.T) {
	// Scalar-aggregate queries return exactly one series; if a future query
	// returns multiple, we intentionally take only the first. Pin that so the
	// behavior is a conscious choice, not a silent surprise.
	m := model.Matrix{
		&model.SampleStream{Values: []model.SamplePair{sample(1), sample(2)}},
		&model.SampleStream{Values: []model.SamplePair{sample(9), sample(9)}},
	}
	got := parseMatrixSeries(m)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("want first series [1 2], got %v", got)
	}
}

func TestParseMatrixSeries_DropsNonFinite(t *testing.T) {
	// Prometheus can emit NaN/±Inf (div-by-zero, counter resets). These must be
	// dropped — encoding/json fails on them after the 200 header is sent, which
	// would blank every sparkline.
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		sample(10),
		sample(math.NaN()),
		sample(20),
		sample(math.Inf(1)),
		sample(math.Inf(-1)),
		sample(30),
	}}}
	got := parseMatrixSeries(m)
	want := []float64{10, 20, 30}
	if len(got) != len(want) {
		t.Fatalf("length: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: want %v, got %v", i, want[i], got[i])
		}
	}
}

func TestParseMatrixSeries_AllNonFinite(t *testing.T) {
	// A series of only non-finite samples collapses to nil — the frontend then
	// renders no sparkline rather than a broken one.
	m := model.Matrix{&model.SampleStream{Values: []model.SamplePair{
		sample(math.NaN()), sample(math.Inf(1)),
	}}}
	if got := parseMatrixSeries(m); got != nil {
		t.Fatalf("all-non-finite: want nil, got %v", got)
	}
}
