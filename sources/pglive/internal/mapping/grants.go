package mapping

import (
	"fmt"
	"slices"
	"strings"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	"github.com/thec1oud/dql/sources/pglive/internal/introspect"
)

// privileges maps aclexplode's privilege names to ACL letters.
var privileges = map[string]byte{
	"INSERT": 'a', "SELECT": 'r', "UPDATE": 'w', "DELETE": 'd', "TRUNCATE": 'D',
	"REFERENCES": 'x', "TRIGGER": 't', "EXECUTE": 'X', "USAGE": 'U', "CREATE": 'C',
	"TEMPORARY": 'T', "CONNECT": 'c', "SET": 's', "ALTER SYSTEM": 'A', "MAINTAIN": 'm',
}

// aclOrder is the order in which Postgres prints ACL letters.
const aclOrder = "arwdDxtXUCTcsAm"

// grants turns the Snapshot's one-privilege grants, sorted by grantee and
// grantor, into one ACL item per grantee and grantor.
func (m *mapper) grants(gs []introspect.Grant) []*pgextv1.Grant {
	var out []*pgextv1.Grant
	var held, grantable []byte
	for i, g := range gs {
		letter, ok := privileges[g.Privilege]
		if !ok {
			m.fail(fmt.Errorf("mapping: unknown privilege %q granted to %s", g.Privilege, g.Grantee))
		}
		held = append(held, letter)
		if g.Grantable {
			grantable = append(grantable, letter)
		}
		if i+1 == len(gs) || gs[i+1].Grantee != g.Grantee || gs[i+1].Grantor != g.Grantor {
			out = append(out, &pgextv1.Grant{Grantee: g.Grantee, Grantor: g.Grantor,
				Privileges: aclString(held), Grantable: aclString(grantable)})
			held, grantable = nil, nil
		}
	}
	return out
}

// aclString orders letters as Postgres prints them.
func aclString(letters []byte) string {
	slices.SortFunc(letters, func(a, b byte) int {
		return strings.IndexByte(aclOrder, a) - strings.IndexByte(aclOrder, b)
	})
	return string(letters)
}
