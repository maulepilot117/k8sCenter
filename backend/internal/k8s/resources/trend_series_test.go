package resources

import (
	"encoding/json"
	"math"
	"testing"
)

func TestTrendSeries_MarshalJSON(t *testing.T) {
	cases := []struct {
		name string
		in   TrendSeries
		want string
	}{
		{"nil is null", nil, `null`},
		{"empty is an empty array", TrendSeries{}, `[]`},
		{"finite values", TrendSeries{1, 2.5, 0}, `[1,2.5,0]`},
		{"gaps keep their slot", TrendSeries{1, math.NaN(), math.Inf(1), math.Inf(-1), 5}, `[1,null,null,null,5]`},
		{"float formatting matches encoding/json", TrendSeries{1e21, 1e-7}, `[1e+21,1e-7]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != c.want {
				t.Fatalf("want %s, got %s", c.want, got)
			}
		})
	}
}

func TestTrendSeries_NaNDoesNotFailThePayload(t *testing.T) {
	// The handler encodes the whole payload after sending the 200 header; a
	// NaN anywhere must encode, not truncate the body. A zero End is omitted.
	body, err := json.Marshal(DashboardTrends{CPU: TrendSeries{math.NaN(), 3}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"nodes":null,"pods":null,"services":null,"cpu":[null,3],"memory":null,"networkRx":null,"networkTx":null,"window":"","step":""}`
	if string(body) != want {
		t.Fatalf("want %s\n got %s", want, body)
	}
}
