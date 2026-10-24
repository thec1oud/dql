package dql

// Change is what one element of a nested write does to a related row
// (§14.5, §14.6): the variant a generated reference or change type holds.
// Its values are the same in every host language.
type Change string

// The changes of §14.5 and §14.6.
const (
	// ChangeLink points at an existing row by key.
	ChangeLink Change = "link"
	// ChangeCreate inserts a new row.
	ChangeCreate Change = "create"
	// ChangeUpdate patches an existing row.
	ChangeUpdate Change = "update"
	// ChangeDelete removes a child according to the relation's removal.
	ChangeDelete Change = "delete"
	// ChangeUnlink sets the foreign key to NULL, or removes a junction row.
	ChangeUnlink Change = "unlink"
)
