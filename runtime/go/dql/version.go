package dql

// EnforceVersion makes generated code fail to compile against a runtime that
// does not implement the generated-code version it was generated for (§26.5).
// A generated package for version N declares
//
//	const (
//		_ = dql.EnforceVersion(N - dql.MinVersion)
//		_ = dql.EnforceVersion(dql.MaxVersion - N)
//	)
//
// and a version outside [MinVersion, MaxVersion] makes one of the constants
// negative, which an unsigned type cannot hold.
type EnforceVersion uint

// The generated-code versions this runtime implements.
const (
	MinVersion = 1
	MaxVersion = 1
)
