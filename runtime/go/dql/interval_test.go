package dql_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dql"
)

func TestIntervalText(t *testing.T) {
	tests := []struct {
		i    dql.Interval
		text string
	}{
		{dql.Interval{}, "PT0S"},
		{dql.Interval{Months: 14, Days: 3, Microseconds: 4*3600e6 + 5*60e6 + 6.5e6}, "P1Y2M3DT4H5M6.5S"},
		{dql.Interval{Months: 1, Days: -1}, "P1M-1D"},
		{dql.Interval{Months: -13}, "P-1Y-1M"},
		{dql.Interval{Days: 7}, "P7D"},
		{dql.Interval{Microseconds: -(3600e6 + 30*60e6)}, "PT-1H-30M"},
		{dql.Interval{Microseconds: -500000}, "PT-0.5S"},
		{dql.Interval{Microseconds: 1}, "PT0.000001S"},
		{dql.Interval{Microseconds: 60e6}, "PT1M"},
		// MySQL TIME's range ends at 838:59:59.
		{dql.Interval{Microseconds: -(838*3600e6 + 59*60e6 + 59e6)}, "PT-838H-59M-59S"},
		{dql.Interval{Months: math.MaxInt32, Days: math.MinInt32, Microseconds: math.MaxInt64},
			"P178956970Y7M-2147483648DT2562047788H54.775807S"},
		{dql.Interval{Months: math.MinInt32, Microseconds: math.MinInt64},
			"P-178956970Y-8MT-2562047788H-54.775808S"},
	}
	for _, tt := range tests {
		if got := tt.i.String(); got != tt.text {
			t.Errorf("String() = %q, want %q", got, tt.text)
		}
		got, err := dql.ParseInterval(tt.text)
		if err != nil || got != tt.i {
			t.Errorf("ParseInterval(%q) = %+v, %v", tt.text, got, err)
		}
	}
}

func TestParseIntervalVariants(t *testing.T) {
	tests := []struct {
		text string
		want dql.Interval
	}{
		{"P0D", dql.Interval{}},
		{"PT90M", dql.Interval{Microseconds: 90 * 60e6}},
		{"P1Y-2M", dql.Interval{Months: 10}},
		{"PT1.25S", dql.Interval{Microseconds: 1.25e6}},
	}
	for _, tt := range tests {
		if got, err := dql.ParseInterval(tt.text); err != nil || got != tt.want {
			t.Errorf("ParseInterval(%q) = %+v, %v", tt.text, got, err)
		}
	}
}

func TestParseIntervalErrors(t *testing.T) {
	for _, s := range []string{
		"", "P", "PT", "1D", "P1", "P1W", "P1M1Y", "P1D1M", "PT1S1M", "P1DT", "P1DX",
		"P1.5D", "PT1.S", "PT1.1234567S", "PT1.x1S", "P--1D", "PxD", "PD", "P1D T1H",
		"P2147483648D", "P-2147483649D", "P178956971Y", "P9223372036854775808D",
		"PT2562047789H", "PT2562047788H1M", "PT9223372036854.775808S", "P1YT1H1Y",
	} {
		if got, err := dql.ParseInterval(s); err == nil {
			t.Errorf("ParseInterval(%q) = %+v", s, got)
		}
	}
}

func TestIntervalJSON(t *testing.T) {
	var v struct{ I dql.Interval }
	if err := json.Unmarshal([]byte(`{"I":"P1M-1D"}`), &v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil || string(out) != `{"I":"P1M-1D"}` {
		t.Fatalf("round trip = %s, %v", out, err)
	}
	if err := json.Unmarshal([]byte(`{"I":"x"}`), &v); err == nil {
		t.Error("x decoded")
	}
}
