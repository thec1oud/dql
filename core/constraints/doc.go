// Package constraints checks the value constraints of a catalog (design
// record §10): the portable constraint kinds every emitter validates
// identically, the portable regex subset, the length units of each dialect
// and the marking of opaque constraints.
//
// Source plugins recognize constraints; the core never parses SQL (§1.2, P5,
// §10.1). This package only checks the structured facts it is given, and
// compiles portable patterns for the emitters' runtimes.
package constraints
