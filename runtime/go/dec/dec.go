// Package dec holds exact decimal numbers (design record §8.1, §8.7).
//
// A Decimal is an arbitrary-precision integer coefficient times a power of
// ten. It never passes through binary floating point (§8.7), and it keeps its
// scale: 1.50 and 1.5 are equal numbers with different text, as in a database
// column of scale 2.
//
// Values are immutable; every operation returns a new Decimal. The zero value
// is 0.
package dec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Decimal is coefficient × 10^exponent. Compare values with Cmp or Equal: ==
// compares representations, not numbers.
type Decimal struct {
	coef *big.Int // nil is 0; never mutated once set
	exp  int32
}

// MaxParseExponent bounds the exponent Parse accepts, in both directions. It
// is beyond the range of every database's decimal type (Postgres numeric
// holds 131072 digits before the point and 16383 after), and it keeps
// untrusted text from forcing huge allocations: the size of a value's plain
// text, and of the work to align it with another, grows with its exponent.
const MaxParseExponent = 1 << 18

var (
	bigZero = new(big.Int)
	bigTen  = big.NewInt(10)
)

// New returns coef × 10^exp.
func New(coef int64, exp int32) Decimal {
	return Decimal{big.NewInt(coef), exp}
}

// NewFromBigInt returns coef × 10^exp. It copies coef.
func NewFromBigInt(coef *big.Int, exp int32) Decimal {
	return Decimal{new(big.Int).Set(coef), exp}
}

// Parse parses a decimal in plain or exponent notation: an optional sign,
// digits with an optional point (at least one digit on either side), and an
// optional exponent, such as -12.50, .5, 5. or 1.2e-3. The scale is kept:
// 1.50 has exponent -2.
func Parse(s string) (Decimal, error) {
	d, ok := parse(s)
	if !ok {
		return Decimal{}, fmt.Errorf("dec: invalid decimal %q", s)
	}
	return d, nil
}

// MustParse is Parse that panics on an error, for constants in code.
func MustParse(s string) Decimal {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func parse(s string) (Decimal, bool) {
	mant, expText, hasExp := strings.Cut(strings.ToLower(s), "e")
	neg := false
	if mant != "" && (mant[0] == '+' || mant[0] == '-') {
		neg = mant[0] == '-'
		mant = mant[1:]
	}
	intPart, frac, _ := strings.Cut(mant, ".")
	digits := intPart + frac
	if digits == "" || !allDigits(digits) {
		return Decimal{}, false
	}
	exp := -int64(len(frac))
	if hasExp {
		e, err := strconv.ParseInt(expText, 10, 32)
		if err != nil {
			return Decimal{}, false
		}
		exp += e
	}
	if exp < -MaxParseExponent || exp > MaxParseExponent {
		return Decimal{}, false
	}
	coef, _ := new(big.Int).SetString(digits, 10)
	if neg {
		coef.Neg(coef)
	}
	return Decimal{coef, int32(exp)}, true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// String returns d in plain notation, never with an exponent: its scale
// digits after the point when the exponent is negative, and zeros appended
// when it is positive.
func (d Decimal) String() string {
	c := d.c()
	digits := new(big.Int).Abs(c).String()
	var b strings.Builder
	if c.Sign() < 0 {
		b.WriteByte('-')
	}
	switch {
	case d.exp >= 0:
		b.WriteString(digits)
		if c.Sign() != 0 {
			b.WriteString(strings.Repeat("0", int(d.exp)))
		}
	default:
		scale := int(-int64(d.exp))
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		point := len(digits) - scale
		b.WriteString(digits[:point])
		b.WriteByte('.')
		b.WriteString(digits[point:])
	}
	return b.String()
}

func (d Decimal) c() *big.Int {
	if d.coef == nil {
		return bigZero
	}
	return d.coef
}

// Coefficient returns a copy of d's coefficient.
func (d Decimal) Coefficient() *big.Int {
	return new(big.Int).Set(d.c())
}

// Exponent returns d's exponent.
func (d Decimal) Exponent() int32 {
	return d.exp
}

// Sign returns -1, 0 or +1 as d is negative, zero or positive.
func (d Decimal) Sign() int {
	return d.c().Sign()
}

// IsZero reports whether d is 0, at any scale.
func (d Decimal) IsZero() bool {
	return d.Sign() == 0
}

// Neg returns -d.
func (d Decimal) Neg() Decimal {
	return Decimal{new(big.Int).Neg(d.c()), d.exp}
}

// Abs returns |d|.
func (d Decimal) Abs() Decimal {
	return Decimal{new(big.Int).Abs(d.c()), d.exp}
}

// Add returns d + o, at the larger of the two scales.
func (d Decimal) Add(o Decimal) Decimal {
	a, b, exp := align(d, o)
	return Decimal{a.Add(a, b), exp}
}

// Sub returns d - o, at the larger of the two scales.
func (d Decimal) Sub(o Decimal) Decimal {
	a, b, exp := align(d, o)
	return Decimal{a.Sub(a, b), exp}
}

// Mul returns d × o, at the sum of the two scales. It panics when the
// exponent leaves the range of int32.
func (d Decimal) Mul(o Decimal) Decimal {
	return Decimal{new(big.Int).Mul(d.c(), o.c()), exponent(int64(d.exp) + int64(o.exp))}
}

// Cmp returns -1, 0 or +1 as d is less than, equal to or greater than o.
func (d Decimal) Cmp(o Decimal) int {
	a, b, _ := align(d, o)
	return a.Cmp(b)
}

// Equal reports whether d and o are the same number, at any scale.
func (d Decimal) Equal(o Decimal) bool {
	return d.Cmp(o) == 0
}

// align returns new coefficients of d and o at their smaller exponent.
func align(d, o Decimal) (*big.Int, *big.Int, int32) {
	a, b := new(big.Int).Set(d.c()), new(big.Int).Set(o.c())
	switch {
	case d.exp > o.exp:
		a.Mul(a, pow10(int64(d.exp)-int64(o.exp)))
		return a, b, o.exp
	case o.exp > d.exp:
		b.Mul(b, pow10(int64(o.exp)-int64(d.exp)))
	}
	return a, b, d.exp
}

func pow10(n int64) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(n), nil)
}

func exponent(e int64) int32 {
	if e < math.MinInt32 || e > math.MaxInt32 {
		panic("dec: exponent out of range")
	}
	return int32(e)
}

// Round returns d rounded to scale digits after the point, half away from
// zero, as Postgres, MySQL and SQL Server round decimals. The result has
// exactly that scale: Round(1.5, 3) is 1.500. A negative scale rounds to a
// power of ten: Round(1250, -2) is 1300.
func (d Decimal) Round(scale int32) Decimal {
	return d.rescale(scale, true)
}

// Truncate returns d cut to scale digits after the point, toward zero, with
// exactly that scale.
func (d Decimal) Truncate(scale int32) Decimal {
	return d.rescale(scale, false)
}

func (d Decimal) rescale(scale int32, round bool) Decimal {
	exp := exponent(-int64(scale))
	if exp <= d.exp {
		c := new(big.Int).Mul(d.c(), pow10(int64(d.exp)-int64(exp)))
		return Decimal{c, exp}
	}
	div := pow10(int64(exp) - int64(d.exp))
	q, r := new(big.Int).QuoRem(d.c(), div, new(big.Int))
	if round && r.Abs(r).Lsh(r, 1).Cmp(div) >= 0 {
		q.Add(q, big.NewInt(int64(d.Sign())))
	}
	return Decimal{q, exp}
}

// Fits reports whether d fits decimal(precision, scale): whether, rounded to
// scale as the database rounds it (see Round), it has at most precision
// significant digits. A database accepts exactly such values, so this is the
// digits constraint of §10.2. A negative scale, or one above the precision,
// is allowed, as Postgres allows it.
func (d Decimal) Fits(precision, scale int32) bool {
	c := d.Round(scale).c()
	return new(big.Int).Abs(c).Cmp(pow10(int64(precision))) < 0
}

// MarshalText encodes d as String does. encoding/json therefore writes a
// Decimal as a JSON string, which every reader takes without losing digits.
func (d Decimal) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText decodes d as Parse does.
func (d *Decimal) UnmarshalText(b []byte) error {
	v, err := Parse(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

var errNull = errors.New("dec: null for a non-null decimal")

// UnmarshalJSON decodes a JSON string as Parse does, and also a JSON number,
// from its digits rather than through float64. null is an error: a nullable
// decimal is a *Decimal, which encoding/json sets to nil itself.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		return errNull
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		b = []byte(s)
	}
	return d.UnmarshalText(b)
}
