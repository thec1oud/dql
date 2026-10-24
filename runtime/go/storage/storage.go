// Package storage holds raw values of a dialect's storage classes: the raw
// side of a weak column (design record §8.10).
//
// The classes are those of the IR's StorageClass: integer, real, text and
// blob, which are SQLite's. A raw value is never NULL; nullability is a
// separate axis, carried by a pointer around the weak value.
package storage

import "fmt"

// Class is a storage class. Its values match the IR's StorageClass.
type Class int

// The storage classes.
const (
	ClassInteger Class = iota + 1
	ClassReal
	ClassText
	ClassBlob
)

// String returns the class's name, such as "integer".
func (c Class) String() string {
	switch c {
	case ClassInteger:
		return "integer"
	case ClassReal:
		return "real"
	case ClassText:
		return "text"
	case ClassBlob:
		return "blob"
	}
	return fmt.Sprintf("Class(%d)", int(c))
}

// Value is a raw value of one storage class: Integer, Real, Text or Blob.
// No other type implements it.
type Value interface {
	// Class returns the value's storage class.
	Class() Class
	sealed()
}

// Integer is a value of the integer class.
type Integer int64

// Real is a value of the real class.
type Real float64

// Text is a value of the text class.
type Text string

// Blob is a value of the blob class.
type Blob []byte

// Class returns ClassInteger.
func (Integer) Class() Class { return ClassInteger }

// Class returns ClassReal.
func (Real) Class() Class { return ClassReal }

// Class returns ClassText.
func (Text) Class() Class { return ClassText }

// Class returns ClassBlob.
func (Blob) Class() Class { return ClassBlob }

func (Integer) sealed() {}
func (Real) sealed()    {}
func (Text) sealed()    {}
func (Blob) sealed()    {}
