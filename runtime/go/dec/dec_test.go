package dec_test

import (
	"encoding/json"
	"math"
	"math/big"
	"testing"

	"github.com/thec1oud/dql/runtime/go/dec"
)

func TestParseString(t *testing.T) {
	tests := []struct {
		in, out string
		exp     int32
	}{
		{"0", "0", 0},
		{"-0", "0", 0},
		{"0.00", "0.00", -2},
		{"12", "12", 0},
		{"+12", "12", 0},
		{"-12.50", "-12.50", -2},
		{"1.5", "1.5", -1},
		{".5", "0.5", -1},
		{"-.05", "-0.05", -2},
		{"5.", "5", 0},
		{"1e3", "1000", 3},
		{"1E+3", "1000", 3},
		{"0e3", "0", 3},
		{"1.2e-3", "0.0012", -4},
		{"-123.456e1", "-1234.56", -2},
		{"12345678901234567890123456789.0123456789", "12345678901234567890123456789.0123456789", -10},
		{"1e262144", "1" + zeros(262144), 262144},
		{"1e-262144", "0." + zeros(262143) + "1", -262144},
	}
	for _, tt := range tests {
		d, err := dec.Parse(tt.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tt.in, err)
			continue
		}
		if got := d.String(); got != tt.out {
			t.Errorf("Parse(%q).String() = %.40q, want %.40q", tt.in, got, tt.out)
		}
		if d.Exponent() != tt.exp {
			t.Errorf("Parse(%q).Exponent() = %d, want %d", tt.in, d.Exponent(), tt.exp)
		}
	}
}

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{
		"", "+", "-", ".", "+.", "1.2.3", "1,5", " 1", "1 ", "1_000", "0x10", "1e", "1e+",
		"1e1.5", "1e2e3", "e3", "NaN", "Infinity", "-inf", "1e262145", "1e-262145",
		"0.1e-262144", "1e99999999999", "--1", "+-1",
	} {
		if d, err := dec.Parse(s); err == nil {
			t.Errorf("Parse(%q) = %v", s, d)
		}
	}
}

func TestMustParse(t *testing.T) {
	if got := dec.MustParse("1.50").String(); got != "1.50" {
		t.Fatalf("got %s", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustParse(x) did not panic")
		}
	}()
	dec.MustParse("x")
}

func TestConstructors(t *testing.T) {
	if got := dec.New(-1250, -2).String(); got != "-12.50" {
		t.Errorf("New = %s", got)
	}
	c := big.NewInt(7)
	d := dec.NewFromBigInt(c, 1)
	c.SetInt64(8) // NewFromBigInt copies
	if got := d.String(); got != "70" {
		t.Errorf("NewFromBigInt = %s", got)
	}
	got := d.Coefficient()
	got.SetInt64(9) // Coefficient copies
	if d.Coefficient().Int64() != 7 {
		t.Error("Coefficient shares its value")
	}

	var zero dec.Decimal
	if zero.String() != "0" || !zero.IsZero() || zero.Sign() != 0 || zero.Exponent() != 0 ||
		zero.Coefficient().Sign() != 0 {
		t.Errorf("zero Decimal = %s", zero)
	}
}

func TestArithmetic(t *testing.T) {
	p := dec.MustParse
	tests := []struct {
		got  dec.Decimal
		want string
	}{
		{p("1.5").Add(p("2.25")), "3.75"},
		{p("2.25").Add(p("1.5")), "3.75"},
		{p("1.50").Add(p("-1.5")), "0.00"},
		{p("1e2").Add(p("1")), "101"},
		{p("1.5").Sub(p("2.25")), "-0.75"},
		{p("0.1").Sub(p("0.3")), "-0.2"},
		{p("1.5").Mul(p("-2.25")), "-3.375"},
		{p("1e2").Mul(p("1.5")), "150"},
		{p("-12.50").Neg(), "12.50"},
		{p("12.50").Neg(), "-12.50"},
		{p("-12.50").Abs(), "12.50"},
		{dec.Decimal{}.Add(p("1.5")), "1.5"},
		{dec.Decimal{}.Neg(), "0"},
	}
	for i, tt := range tests {
		if got := tt.got.String(); got != tt.want {
			t.Errorf("%d: got %s, want %s", i, got, tt.want)
		}
	}
	// 0.1 + 0.2 is exactly 0.3: nothing passes through binary floating point.
	if !p("0.1").Add(p("0.2")).Equal(p("0.3")) {
		t.Error("0.1 + 0.2 != 0.3")
	}
}

func TestMulOverflow(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("exponent overflow did not panic")
		}
	}()
	dec.New(1, math.MaxInt32).Mul(dec.New(1, 1))
}

func TestCompare(t *testing.T) {
	p := dec.MustParse
	tests := []struct {
		a, b string
		want int
	}{
		{"1.5", "1.50", 0},
		{"1.5", "1.49", 1},
		{"-1.5", "1.5", -1},
		{"1e3", "999.9", 1},
		{"0", "0.000", 0},
		{"-0.001", "0", -1},
	}
	for _, tt := range tests {
		a, b := p(tt.a), p(tt.b)
		if got := a.Cmp(b); got != tt.want {
			t.Errorf("Cmp(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		if got := b.Cmp(a); got != -tt.want {
			t.Errorf("Cmp(%s, %s) = %d, want %d", tt.b, tt.a, got, -tt.want)
		}
		if got := a.Equal(b); got != (tt.want == 0) {
			t.Errorf("Equal(%s, %s) = %v", tt.a, tt.b, got)
		}
	}
	if p("-2").Sign() != -1 || p("2").Sign() != 1 || !p("0.00").IsZero() {
		t.Error("Sign")
	}
}

func TestRound(t *testing.T) {
	p := dec.MustParse
	tests := []struct {
		in    string
		scale int32
		round string
		trunc string
	}{
		{"1.5", 3, "1.500", "1.500"},
		{"1.25", 1, "1.3", "1.2"},
		{"1.24", 1, "1.2", "1.2"},
		{"-1.25", 1, "-1.3", "-1.2"},
		{"-1.24", 1, "-1.2", "-1.2"},
		{"0.5", 0, "1", "0"},
		{"-0.5", 0, "-1", "0"},
		{"0.49", 0, "0", "0"},
		{"99.95", 1, "100.0", "99.9"},
		{"1250", -2, "1300", "1200"},
		{"1249", -2, "1200", "1200"},
		{"1e3", 0, "1000", "1000"},
		{"0", 2, "0.00", "0.00"},
	}
	for _, tt := range tests {
		d := p(tt.in)
		if got := d.Round(tt.scale).String(); got != tt.round {
			t.Errorf("Round(%s, %d) = %s, want %s", tt.in, tt.scale, got, tt.round)
		}
		if got := d.Truncate(tt.scale).String(); got != tt.trunc {
			t.Errorf("Truncate(%s, %d) = %s, want %s", tt.in, tt.scale, got, tt.trunc)
		}
	}
}

func TestRoundOverflow(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("scale overflow did not panic")
		}
	}()
	dec.New(1, 0).Round(math.MinInt32)
}

func TestFits(t *testing.T) {
	tests := []struct {
		in               string
		precision, scale int32
		want             bool
	}{
		{"999.99", 5, 2, true},
		{"-999.99", 5, 2, true},
		{"1000.00", 5, 2, false},
		{"999.994", 5, 2, true},  // rounds to 999.99
		{"999.995", 5, 2, false}, // rounds to 1000.00
		{"0.99", 2, 2, true},
		{"1", 2, 2, false},
		{"0.00012", 2, 5, true}, // scale above precision: |x| < 0.001
		{"0.001", 2, 5, false},
		{"12345", 2, -3, true}, // negative scale: rounds to 12000
		{"99500", 2, -3, false},
		{"0", 1, 0, true},
	}
	for _, tt := range tests {
		if got := dec.MustParse(tt.in).Fits(tt.precision, tt.scale); got != tt.want {
			t.Errorf("Fits(%s, %d, %d) = %v, want %v", tt.in, tt.precision, tt.scale, got, tt.want)
		}
	}
}

func TestJSON(t *testing.T) {
	type row struct {
		Total dec.Decimal
		Tax   *dec.Decimal
	}
	b, err := json.Marshal(row{Total: dec.MustParse("12.50")})
	if err != nil || string(b) != `{"Total":"12.50","Tax":null}` {
		t.Fatalf("Marshal = %s, %v", b, err)
	}

	var r row
	if err := json.Unmarshal([]byte(`{"Total":"12.50","Tax":null}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Total.String() != "12.50" || r.Tax != nil {
		t.Fatalf("got %+v", r)
	}
	// A JSON number is read from its digits, not through float64.
	if err := json.Unmarshal([]byte(`{"Total":0.10000000000000000001,"Tax":1e2}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Total.String() != "0.10000000000000000001" || r.Tax.String() != "100" {
		t.Fatalf("got %s, %s", r.Total, r.Tax)
	}

	for _, in := range []string{`{"Total":null}`, `{"Total":"x"}`, `{"Total":true}`} {
		if err := json.Unmarshal([]byte(in), &r); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", in)
		}
	}
	var d dec.Decimal
	if err := d.UnmarshalJSON([]byte(`"1.5`)); err == nil {
		t.Error("an unterminated string decoded")
	}
	if err := d.UnmarshalText([]byte("1.5")); err != nil || d.String() != "1.5" {
		t.Errorf("UnmarshalText = %s, %v", d, err)
	}
}
