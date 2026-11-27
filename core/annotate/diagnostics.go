package annotate

// Diagnostic codes. They are stable: tools and tests match on them
// (implementation guide §7, rule 13).
const (
	// CodeKeyInvalid: a key is neither core.<key> nor x.<plugin>.<key> (§7.2).
	CodeKeyInvalid = "annotation-key-invalid"
	// CodeUnknown: a core. key the vocabulary does not define (§7.3).
	CodeUnknown = "annotation-unknown"
	// CodeWrongTarget: a key on an object §7.2 does not allow it on (§7.3).
	CodeWrongTarget = "annotation-wrong-target"
	// CodeValueInvalid: a value of the wrong form, or outside its allowed set.
	CodeValueInvalid = "annotation-value-invalid"
	// CodeValueMalformed: a value written as text, as a comment writes it,
	// that cannot be read as its key's list or struct form (§7.4).
	CodeValueMalformed = "annotation-value-malformed"
	// CodeEnumNotMembership: enum on a check that is not a single-column
	// membership test (§7.3, §8.4).
	CodeEnumNotMembership = "enum-not-membership-test"
	// CodeLogicalKeyNullable: logical-key over a nullable column (§7.3, §11.1).
	CodeLogicalKeyNullable = "logical-key-nullable"
	// CodeBoundWithVersion: bound and version on one column (§7.3).
	CodeBoundWithVersion = "bound-with-version"
	// CodeRedundant: an annotation that changes nothing (§7.3). A warning.
	CodeRedundant = "annotation-redundant"
	// CodeDuplicate: one source gave a key twice with the same value. A warning.
	CodeDuplicate = "annotation-duplicate"
	// CodeConflict: one source gave a key twice with different values.
	CodeConflict = "annotation-conflict"
	// CodeOriginMissing: an annotation that does not say where it was read
	// (§7.1: every annotation records where it came from).
	CodeOriginMissing = "annotation-origin-missing"
)
