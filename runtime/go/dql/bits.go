package dql

import (
	"bytes"
	"fmt"
)

// Bits is a bit string, the host type of the logical type bit (design record
// §8.1), such as a Postgres bit(n) or bit varying(n) value. The zero value is
// the empty bit string.
type Bits struct {
	data []byte // bits from the most significant bit of data[0]; padding is zero
	n    int
}

// NewBits returns the first n bits of data, from the most significant bit of
// data[0] on, as Postgres sends them. It copies data, and returns an error
// when data holds fewer than n bits or n is negative.
func NewBits(data []byte, n int) (Bits, error) {
	if n < 0 || n > 8*len(data) {
		return Bits{}, fmt.Errorf("dql: %d bits do not fit %d bytes", n, len(data))
	}
	b := Bits{data: make([]byte, (n+7)/8), n: n}
	copy(b.data, data)
	if r := n % 8; r != 0 {
		b.data[len(b.data)-1] &= 0xff << (8 - r)
	}
	return b, nil
}

// ParseBits parses a string of 0 and 1 characters.
func ParseBits(s string) (Bits, error) {
	b := Bits{data: make([]byte, (len(s)+7)/8), n: len(s)}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '1':
			b.data[i/8] |= 0x80 >> (i % 8)
		case '0':
		default:
			return Bits{}, fmt.Errorf("dql: invalid bit string %q", s)
		}
	}
	return b, nil
}

// Len returns the number of bits.
func (b Bits) Len() int { return b.n }

// Bit reports whether bit i, counted from 0 at the left, is set. It panics
// when i is out of range.
func (b Bits) Bit(i int) bool {
	if i < 0 || i >= b.n {
		panic(fmt.Sprintf("dql: bit %d of a %d-bit string", i, b.n))
	}
	return b.data[i/8]&(0x80>>(i%8)) != 0
}

// Bytes returns a copy of the bits packed from the most significant bit of
// the first byte, with the unused bits of the last byte zero.
func (b Bits) Bytes() []byte {
	return bytes.Clone(b.data)
}

// Equal reports whether b and o hold the same bits.
func (b Bits) Equal(o Bits) bool {
	return b.n == o.n && bytes.Equal(b.data, o.data)
}

// String returns the bits as 0 and 1 characters.
func (b Bits) String() string {
	s := make([]byte, b.n)
	for i := range s {
		s[i] = '0'
		if b.Bit(i) {
			s[i] = '1'
		}
	}
	return string(s)
}

// MarshalText encodes b as String does.
func (b Bits) MarshalText() ([]byte, error) {
	return []byte(b.String()), nil
}

// UnmarshalText decodes b as ParseBits does.
func (b *Bits) UnmarshalText(text []byte) error {
	v, err := ParseBits(string(text))
	if err != nil {
		return err
	}
	*b = v
	return nil
}
