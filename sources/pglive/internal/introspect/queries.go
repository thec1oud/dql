package introspect

import "fmt"

// The queries read pg_catalog only, never information_schema, which hides
// objects from roles without privileges on them and leaves out most of what
// §6.2 carries as extensions. Each query returns one JSON document per object,
// built with json_build_object and json_agg, so a whole object with its
// members arrives in one row and Go only decodes it. The one parameter, $1,
// is the list of schema names; nothing is ever interpolated into the text
// (implementation guide §7.3). The fragments below are SQL structure fixed at
// build time.
//
// Every list has a deterministic ORDER BY (§3.2): database order for the
// lists of §3.2 rule 1, and COLLATE "C" on name keys elsewhere, so the order
// never depends on the database's collation or the process locale.

// inSchemas restricts a query to the schemas asked for.
const inSchemas = `n.nspname = ANY($1::text[])`

// typeRef renders a TypeRef for the type oid with the given typmod and
// declared array dimensions. A NULL typmod (arguments, results) is reported as
// -1 and formatted as format_type does for function signatures.
func typeRef(oid, typmod, dims string) string {
	return fmt.Sprintf(`(SELECT json_build_object(
    'schema', rn.nspname, 'name', rt.typname, 'kind', rt.typtype, 'category', rt.typcategory,
    'typmod', COALESCE(%[2]s, -1), 'formatted', format_type(rt.oid, %[2]s), 'dimensions', %[3]s,
    'element', (SELECT json_build_object('schema', ren.nspname, 'name', re.typname, 'kind', re.typtype)
      FROM pg_type re JOIN pg_namespace ren ON ren.oid = re.typnamespace
      WHERE re.oid = rt.typelem AND rt.typsubscript = 'array_subscript_handler'::regproc))
  FROM pg_type rt JOIN pg_namespace rn ON rn.oid = rt.typnamespace WHERE rt.oid = %[1]s)`, oid, typmod, dims)
}

// collationFields renders the CollationRef fields of pg_collation row co in
// namespace cn.
func collationFields(co, cn string) string {
	return fmt.Sprintf(`'schema', %[2]s.nspname, 'name', %[1]s.collname, 'provider', %[1]s.collprovider,
    'deterministic', %[1]s.collisdeterministic, 'collate', %[1]s.collcollate, 'ctype', %[1]s.collctype,
    'icuLocale', `+icuLocale(co, "coll", "collprovider")+`, 'icuRules', %[1]s.collicurules`, co, cn)
}

// icuLocale renders the ICU locale of pg_database or pg_collation row r, whose
// columns start with prefix and whose provider is in column provider. Postgres
// 17 renamed <prefix>iculocale to <prefix>locale and uses it for the builtin
// provider too, so the column is read by name from the row as JSON, which
// works on either side of the rename, and only for the ICU provider.
func icuLocale(r, prefix, provider string) string {
	return fmt.Sprintf(`CASE WHEN %[1]s.%[3]s = 'i' THEN COALESCE(
      to_jsonb(%[1]s) ->> '%[2]slocale', to_jsonb(%[1]s) ->> '%[2]siculocale') END`, r, prefix, provider)
}

// collationRef renders a CollationRef, or NULL for collation oid 0.
func collationRef(oid string) string {
	return fmt.Sprintf(`(SELECT json_build_object(%s)
  FROM pg_collation rc JOIN pg_namespace rcn ON rcn.oid = rc.collnamespace WHERE rc.oid = %s)`,
		collationFields("rc", "rcn"), oid)
}

// grants renders the explicit ACL items of acl, one Grant per privilege.
func grants(acl string) string {
	return fmt.Sprintf(`(SELECT json_agg(json_build_object(
      'grantee', g.grantee, 'privilege', g.privilege, 'grantor', g.grantor, 'grantable', g.grantable)
    ORDER BY g.grantee COLLATE "C", g.privilege COLLATE "C", g.grantor COLLATE "C")
  FROM (SELECT CASE WHEN x.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(x.grantee)::text END,
               x.privilege_type::text, pg_get_userbyid(x.grantor)::text, x.is_grantable
        FROM aclexplode(%s) x) g(grantee, privilege, grantor, grantable))`, acl)
}

// columnNames renders the names of the columns numbered in the int2 array
// attnums of relation rel, in array order (§3.2 rule 1).
func columnNames(rel, attnums string) string {
	return fmt.Sprintf(`(SELECT json_agg(ca.attname ORDER BY ck.ord)
  FROM unnest(%[2]s) WITH ORDINALITY ck(attnum, ord)
  JOIN pg_attribute ca ON ca.attrelid = %[1]s AND ca.attnum = ck.attnum)`, rel, attnums)
}

// objectName renders an ObjectName for the pg_class row oid.
func objectName(oid string) string {
	return fmt.Sprintf(`(SELECT json_build_object('schema', on_.nspname, 'name', oc.relname)
  FROM pg_class oc JOIN pg_namespace on_ ON on_.oid = oc.relnamespace WHERE oc.oid = %s)`, oid)
}

// extensionOf renders the name of the extension the object belongs to.
func extensionOf(catalog, oid string) string {
	return fmt.Sprintf(`(SELECT x.extname FROM pg_depend d JOIN pg_extension x ON x.oid = d.refobjid
  WHERE d.classid = '%s'::regclass AND d.objid = %s
    AND d.refclassid = 'pg_extension'::regclass AND d.deptype = 'e')`, catalog, oid)
}

// tablespace renders a tablespace name, NULL for the default.
func tablespace(oid string) string {
	return fmt.Sprintf(`(SELECT spcname FROM pg_tablespace WHERE oid = %s)`, oid)
}

var databaseSQL = `SELECT json_build_object(
  'encoding', pg_encoding_to_char(d.encoding), 'localeProvider', d.datlocprovider,
  'collate', d.datcollate, 'ctype', d.datctype, 'icuLocale', ` + icuLocale("d", "dat", "datlocprovider") + `, 'icuRules', d.daticurules)
FROM pg_database d WHERE d.datname = current_database()`

const extensionsSQL = `SELECT json_build_object('name', x.extname, 'version', x.extversion, 'schema', n.nspname)
FROM pg_extension x JOIN pg_namespace n ON n.oid = x.extnamespace
ORDER BY x.extname COLLATE "C"`

var schemasSQL = `SELECT json_build_object(
  'name', n.nspname, 'owner', pg_get_userbyid(n.nspowner),
  'comment', obj_description(n.oid, 'pg_namespace'), 'grants', ` + grants("n.nspacl") + `)
FROM pg_namespace n WHERE ` + inSchemas + `
ORDER BY n.nspname COLLATE "C"`

var collationsSQL = `SELECT json_build_object(` + collationFields("co", "n") + `,
  'owner', pg_get_userbyid(co.collowner), 'comment', obj_description(co.oid, 'pg_collation'))
FROM pg_collation co JOIN pg_namespace n ON n.oid = co.collnamespace
WHERE ` + inSchemas + `
ORDER BY n.nspname COLLATE "C", co.collname COLLATE "C"`

// columnsOf renders the user columns of relation c in attnum order. A
// generated column's expression lives in pg_attrdef like a default; it is
// reported as Generation, not Default (§11.3).
var columnsOf = `(SELECT json_agg(json_build_object(
      'name', a.attname, 'ordinal', a.attnum,
      'type', ` + typeRef("a.atttypid", "a.atttypmod", "a.attndims") + `,
      'notNull', a.attnotnull,
      'default', CASE WHEN a.attgenerated = '' THEN pg_get_expr(ad.adbin, ad.adrelid) END,
      'identity', a.attidentity,
      'identitySequence', (SELECT s.relname FROM pg_depend d JOIN pg_class s ON s.oid = d.objid
        WHERE d.classid = 'pg_class'::regclass AND d.refclassid = 'pg_class'::regclass
          AND d.refobjid = a.attrelid AND d.refobjsubid = a.attnum AND d.deptype = 'i'),
      'generated', a.attgenerated,
      'generation', CASE WHEN a.attgenerated <> '' THEN pg_get_expr(ad.adbin, ad.adrelid) END,
      'collation', ` + collationRef("a.attcollation") + `,
      'comment', col_description(a.attrelid, a.attnum),
      'grants', ` + grants("a.attacl") + `)
    ORDER BY a.attnum)
  FROM pg_attribute a LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
  WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped)`

// constraintsOf renders the constraints of relation c. A foreign key's
// conindid is the referenced table's index, so Index is reported only for the
// kinds an index backs. Postgres 18 also stores each NOT NULL as a
// constraint of kind n; attnotnull already says it, so those are left out.
var constraintsOf = `(SELECT json_agg(json_build_object(
      'name', k.conname, 'kind', k.contype,
      'columns', ` + columnNames("k.conrelid", "k.conkey") + `,
      'definition', pg_get_constraintdef(k.oid),
      'references', (SELECT json_build_object(
          'schema', fn.nspname, 'table', f.relname,
          'columns', ` + columnNames("k.confrelid", "k.confkey") + `,
          'match', k.confmatchtype, 'onUpdate', k.confupdtype, 'onDelete', k.confdeltype,
          'onDeleteColumns', ` + columnNames("k.conrelid", "k.confdelsetcols") + `)
        FROM pg_class f JOIN pg_namespace fn ON fn.oid = f.relnamespace WHERE f.oid = k.confrelid),
      'deferrable', k.condeferrable, 'initiallyDeferred', k.condeferred,
      'notValid', NOT k.convalidated, 'noInherit', k.connoinherit,
      'index', CASE WHEN k.contype IN ('p', 'u', 'x') THEN
        (SELECT ki.relname FROM pg_class ki WHERE ki.oid = k.conindid) END,
      'nullsNotDistinct', CASE WHEN k.contype IN ('p', 'u') THEN
        (SELECT ix.indnullsnotdistinct FROM pg_index ix WHERE ix.indexrelid = k.conindid) END,
      'exclusionOperators', (SELECT json_agg(x.op::regoperator::text ORDER BY x.ord)
        FROM unnest(k.conexclop) WITH ORDINALITY x(op, ord)),
      'comment', obj_description(k.oid, 'pg_constraint'))
    ORDER BY k.conname COLLATE "C")
  FROM pg_constraint k WHERE k.conrelid = c.oid AND k.contype <> 'n')`

// indexesOf renders the indexes of relation c, each key as its column or its
// expression in index order.
var indexesOf = `(SELECT json_agg(json_build_object(
      'name', ic.relname, 'method', am.amname, 'unique', i.indisunique, 'primary', i.indisprimary,
      'keys', (SELECT json_agg(json_build_object(
            'column', ka.attname,
            'expression', CASE WHEN k.attnum = 0 THEN pg_get_indexdef(i.indexrelid, k.ord::int, true) END,
            'included', k.ord > i.indnkeyatts)
          ORDER BY k.ord)
        FROM unnest(i.indkey::int2[]) WITH ORDINALITY k(attnum, ord)
        LEFT JOIN pg_attribute ka ON ka.attrelid = i.indrelid AND ka.attnum = k.attnum AND k.attnum > 0),
      'predicate', pg_get_expr(i.indpred, i.indrelid),
      'nullsNotDistinct', i.indnullsnotdistinct,
      'constraint', (SELECT ik.conname FROM pg_constraint ik
        WHERE ik.conrelid = i.indrelid AND ik.conindid = i.indexrelid AND ik.contype IN ('p', 'u', 'x')),
      'definition', pg_get_indexdef(i.indexrelid),
      'options', ic.reloptions,
      'tablespace', ` + tablespace("ic.reltablespace") + `,
      'comment', obj_description(ic.oid, 'pg_class'))
    ORDER BY ic.relname COLLATE "C")
  FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid JOIN pg_am am ON am.oid = ic.relam
  WHERE i.indrelid = c.oid)`

// triggersOf renders the user triggers of relation c, decoding tgtype's bits
// (1 row, 2 before, 4 insert, 8 delete, 16 update, 32 truncate, 64 instead).
var triggersOf = `(SELECT json_agg(json_build_object(
      'name', t.tgname,
      'timing', CASE WHEN t.tgtype & 2 <> 0 THEN 'BEFORE' WHEN t.tgtype & 64 <> 0 THEN 'INSTEAD OF' ELSE 'AFTER' END,
      'events', (SELECT json_agg(e ORDER BY e COLLATE "C") FROM unnest(ARRAY[
          CASE WHEN t.tgtype & 4 <> 0 THEN 'INSERT' END, CASE WHEN t.tgtype & 8 <> 0 THEN 'DELETE' END,
          CASE WHEN t.tgtype & 16 <> 0 THEN 'UPDATE' END, CASE WHEN t.tgtype & 32 <> 0 THEN 'TRUNCATE' END]) e
        WHERE e IS NOT NULL),
      'level', CASE WHEN t.tgtype & 1 <> 0 THEN 'ROW' ELSE 'STATEMENT' END,
      'function', (SELECT json_build_object('schema', pn.nspname, 'name', p.proname)
        FROM pg_proc p JOIN pg_namespace pn ON pn.oid = p.pronamespace WHERE p.oid = t.tgfoid),
      'enabled', t.tgenabled, 'definition', pg_get_triggerdef(t.oid),
      'comment', obj_description(t.oid, 'pg_trigger'))
    ORDER BY t.tgname COLLATE "C")
  FROM pg_trigger t WHERE t.tgrelid = c.oid AND NOT t.tgisinternal)`

// policiesOf renders the row-level security policies of relation c.
var policiesOf = `(SELECT json_agg(json_build_object(
      'name', p.polname, 'command', p.polcmd, 'permissive', p.polpermissive,
      'roles', (SELECT json_agg(r.name ORDER BY r.name COLLATE "C")
        FROM (SELECT CASE WHEN x = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(x)::text END FROM unnest(p.polroles) x) r(name)),
      'using', pg_get_expr(p.polqual, p.polrelid), 'withCheck', pg_get_expr(p.polwithcheck, p.polrelid),
      'comment', obj_description(p.oid, 'pg_policy'))
    ORDER BY p.polname COLLATE "C")
  FROM pg_policy p WHERE p.polrelid = c.oid)`

// dependsOnOf renders the relations the rewrite rule of view c reads (§6.4).
const dependsOnOf = `(SELECT json_agg(json_build_object('schema', v.nspname, 'name', v.relname)
    ORDER BY v.nspname COLLATE "C", v.relname COLLATE "C")
  FROM (SELECT DISTINCT dn.nspname::text, dc.relname::text
        FROM pg_rewrite r
        JOIN pg_depend d ON d.classid = 'pg_rewrite'::regclass AND d.objid = r.oid
                        AND d.refclassid = 'pg_class'::regclass AND d.refobjid <> c.oid
        JOIN pg_class dc ON dc.oid = d.refobjid JOIN pg_namespace dn ON dn.oid = dc.relnamespace
        WHERE r.ev_class = c.oid) v(nspname, relname))`

var relationsSQL = `SELECT json_build_object(
  'schema', n.nspname, 'name', c.relname, 'kind', c.relkind, 'persistence', c.relpersistence,
  'owner', pg_get_userbyid(c.relowner), 'comment', obj_description(c.oid, 'pg_class'),
  'tablespace', ` + tablespace("c.reltablespace") + `,
  'options', c.reloptions,
  'partitionKey', CASE WHEN c.relkind = 'p' THEN pg_get_partkeydef(c.oid) END,
  'partitionOf', (SELECT ` + objectName("i.inhparent") + ` FROM pg_inherits i
    WHERE i.inhrelid = c.oid AND c.relispartition),
  'partitionBound', pg_get_expr(c.relpartbound, c.oid),
  'inherits', (SELECT json_agg(` + objectName("i.inhparent") + ` ORDER BY i.inhseqno) FROM pg_inherits i
    WHERE i.inhrelid = c.oid AND NOT c.relispartition),
  'rowSecurity', c.relrowsecurity, 'forceRowSecurity', c.relforcerowsecurity,
  'definition', CASE WHEN c.relkind IN ('v', 'm') THEN pg_get_viewdef(c.oid) END,
  'dependsOn', ` + dependsOnOf + `,
  'extension', ` + extensionOf("pg_class", "c.oid") + `,
  'columns', ` + columnsOf + `,
  'constraints', ` + constraintsOf + `,
  'indexes', ` + indexesOf + `,
  'triggers', ` + triggersOf + `,
  'policies', ` + policiesOf + `,
  'grants', ` + grants("c.relacl") + `)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE ` + inSchemas + ` AND c.relkind IN ('r', 'p', 'v', 'm')
ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C"`

// typeHeader renders the fields every named type shares, for pg_type row t.
var typeHeader = `'schema', n.nspname, 'name', t.typname, 'owner', pg_get_userbyid(t.typowner),
  'comment', obj_description(t.oid, 'pg_type'), 'extension', ` + extensionOf("pg_type", "t.oid") + `,
  'grants', ` + grants("t.typacl")

// typesFrom is the FROM clause of the type queries; kind restricts typtype.
func typesFrom(kind string) string {
	return `FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE ` + inSchemas + ` AND ` + kind + `
ORDER BY n.nspname COLLATE "C", t.typname COLLATE "C"`
}

var enumsSQL = `SELECT json_build_object(` + typeHeader + `,
  'labels', (SELECT json_agg(e.enumlabel ORDER BY e.enumsortorder) FROM pg_enum e WHERE e.enumtypid = t.oid))
` + typesFrom("t.typtype = 'e'")

var domainsSQL = `SELECT json_build_object(` + typeHeader + `,
  'base', ` + typeRef("t.typbasetype", "t.typtypmod", "t.typndims") + `,
  'notNull', t.typnotnull, 'default', pg_get_expr(t.typdefaultbin, 0),
  'collation', ` + collationRef("t.typcollation") + `,
  'checks', (SELECT json_agg(json_build_object(
        'name', k.conname, 'definition', pg_get_constraintdef(k.oid), 'notValid', NOT k.convalidated,
        'comment', obj_description(k.oid, 'pg_constraint'))
      ORDER BY k.conname COLLATE "C")
    FROM pg_constraint k WHERE k.contypid = t.oid AND k.contype = 'c'))
` + typesFrom("t.typtype = 'd'")

// compositesSQL reads stand-alone composite types; the row types of tables
// and views are their relations.
var compositesSQL = `SELECT json_build_object(` + typeHeader + `,
  'attributes', (SELECT json_agg(json_build_object(
        'name', a.attname, 'ordinal', a.attnum,
        'type', ` + typeRef("a.atttypid", "a.atttypmod", "a.attndims") + `,
        'collation', ` + collationRef("a.attcollation") + `,
        'comment', col_description(a.attrelid, a.attnum))
      ORDER BY a.attnum)
    FROM pg_attribute a WHERE a.attrelid = t.typrelid AND a.attnum > 0 AND NOT a.attisdropped))
` + typesFrom("t.typtype = 'c' AND (SELECT r.relkind FROM pg_class r WHERE r.oid = t.typrelid) = 'c'")

var rangesSQL = `SELECT json_build_object(` + typeHeader + `,
  'subtype', ` + typeRef("r.rngsubtype", "NULL", "0") + `,
  'multirange', (SELECT json_build_object('schema', mn.nspname, 'name', m.typname)
    FROM pg_type m JOIN pg_namespace mn ON mn.oid = m.typnamespace WHERE m.oid = r.rngmultitypid),
  'collation', ` + collationRef("r.rngcollation") + `,
  'canonical', NULLIF(r.rngcanonical, 0)::regproc::text,
  'subtypeDiff', NULLIF(r.rngsubdiff, 0)::regproc::text)
FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace JOIN pg_range r ON r.rngtypid = t.oid
WHERE ` + inSchemas + `
ORDER BY n.nspname COLLATE "C", t.typname COLLATE "C"`

// baseTypesSQL reads the remaining named types. Array types are left out:
// Postgres creates one for every type, and columns reach them through
// TypeRef.Element.
var baseTypesSQL = `SELECT json_build_object(` + typeHeader + `,
  'kind', t.typtype, 'category', t.typcategory)
` + typesFrom("t.typtype IN ('b', 'p') AND t.typsubscript <> 'array_subscript_handler'::regproc")

var sequencesSQL = `SELECT json_build_object(
  'schema', n.nspname, 'name', c.relname, 'owner', pg_get_userbyid(c.relowner),
  'comment', obj_description(c.oid, 'pg_class'),
  'dataType', ` + typeRef("s.seqtypid", "NULL", "0") + `,
  'start', s.seqstart, 'increment', s.seqincrement, 'min', s.seqmin, 'max', s.seqmax,
  'cache', s.seqcache, 'cycle', s.seqcycle,
  'ownedBy', (SELECT json_build_object('schema', tn.nspname, 'table', tc.relname, 'column', a.attname)
    FROM pg_depend d JOIN pg_class tc ON tc.oid = d.refobjid JOIN pg_namespace tn ON tn.oid = tc.relnamespace
    JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
    WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.refclassid = 'pg_class'::regclass
      AND d.refobjsubid > 0 AND d.deptype IN ('a', 'i')),
  'identity', EXISTS (SELECT FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid
      AND d.refclassid = 'pg_class'::regclass AND d.deptype = 'i'),
  'grants', ` + grants("c.relacl") + `)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_sequence s ON s.seqrelid = c.oid
WHERE ` + inSchemas + `
ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C"`

// routinesSQL reads every overload. Arguments come from proallargtypes when
// any argument is not IN, else from proargtypes, in declaration order (§3.2
// rule 1); pg_get_function_arg_default tells which have defaults (§6.5).
var routinesSQL = `SELECT json_build_object(
  'schema', n.nspname, 'name', p.proname, 'kind', p.prokind,
  'identityArguments', pg_get_function_identity_arguments(p.oid),
  'owner', pg_get_userbyid(p.proowner), 'comment', obj_description(p.oid, 'pg_proc'),
  'extension', ` + extensionOf("pg_proc", "p.oid") + `,
  'language', l.lanname,
  'arguments', (SELECT json_agg(json_build_object(
        'name', NULLIF(p.proargnames[a.ord::int], ''), 'mode', COALESCE(p.proargmodes[a.ord::int], 'i'),
        'type', ` + typeRef("a.typ", "NULL", "0") + `,
        'hasDefault', pg_get_function_arg_default(p.oid, a.ord::int) IS NOT NULL,
        'default', pg_get_function_arg_default(p.oid, a.ord::int))
      ORDER BY a.ord)
    FROM unnest(COALESCE(p.proallargtypes, p.proargtypes::oid[])) WITH ORDINALITY a(typ, ord)),
  'defaultCount', p.pronargdefaults,
  'returns', ` + typeRef("p.prorettype", "NULL", "0") + `,
  'result', pg_get_function_result(p.oid),
  'returnsSet', p.proretset, 'strict', p.proisstrict, 'volatility', p.provolatile,
  'parallel', p.proparallel, 'securityDefiner', p.prosecdef, 'leakproof', p.proleakproof,
  'source', NULLIF(p.prosrc, ''),
  'sqlBody', CASE WHEN p.prosqlbody IS NOT NULL THEN pg_get_function_sqlbody(p.oid) END,
  'grants', ` + grants("p.proacl") + `)
FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_language l ON l.oid = p.prolang
WHERE ` + inSchemas + `
ORDER BY n.nspname COLLATE "C", p.proname COLLATE "C", pg_get_function_identity_arguments(p.oid) COLLATE "C"`

var statisticsSQL = `SELECT json_build_object(
  'schema', n.nspname, 'name', s.stxname, 'owner', pg_get_userbyid(s.stxowner),
  'comment', obj_description(s.oid, 'pg_statistic_ext'),
  'table', ` + objectName("s.stxrelid") + `,
  'kinds', (SELECT json_agg(k::text ORDER BY k::text COLLATE "C") FROM unnest(s.stxkind) k),
  'columns', ` + columnNames("s.stxrelid", "s.stxkeys::int2[]") + `,
  'expressions', pg_get_statisticsobjdef_expressions(s.oid),
  'definition', pg_get_statisticsobjdef(s.oid))
FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid = s.stxnamespace
WHERE ` + inSchemas + `
ORDER BY n.nspname COLLATE "C", s.stxname COLLATE "C"`
