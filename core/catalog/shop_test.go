package catalog

import (
	"google.golang.org/protobuf/proto"

	pgextv1 "github.com/thec1oud/dql/proto/dql/ext/postgres/v1"
	irv1 "github.com/thec1oud/dql/proto/dql/ir/v1"
)

// This file builds the running example (§1.3) as pg-live produces it: the
// catalog with stored names only, the annotations it read from comments, the
// config annotations of dql.yaml keyed by qualified name, and provenance.
// Following §8.3 it also has users.nickname citext, the opaque column that
// makes the bundle require opaque-types (§3.1).

func app(object string) *irv1.QualifiedName { return qn("shop", "app", object) }

func onColumn(table, column string) string { return "COMMENT ON COLUMN app." + table + "." + column }

func shopCatalog() *irv1.Catalog {
	tenants := table("tenants",
		&irv1.Column{Name: ident("id"), Type: tUUID(), NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE,
			Default: &irv1.Default{Class: irv1.DefaultClass_DEFAULT_EXPRESSION, Native: "gen_random_uuid()"}},
		col("name", tText()))
	tenants.Uniqueness = []*irv1.Uniqueness{pk("tenants_pkey", "id")}

	displayName := col("display_name?", &irv1.Type{
		Kind:   &irv1.Type_Varchar{Varchar: &irv1.Varchar{Length: 80, Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}},
		Native: native("varchar", "character varying(80)", 80),
	})
	passwordHash := col("password_hash", tText())
	passwordHash.Annotations = []*irv1.Annotation{
		comment("write-only", onColumn("users", "password_hash"), nil),
		comment("sensitive", onColumn("users", "password_hash"), nil),
	}
	// §7.5: the raw comment text is kept as an extension.
	passwordHash.Extensions = append(passwordHash.Extensions, mustAny(&pgextv1.Comment{Text: "@write-only @sensitive"}))
	users := table("users",
		&irv1.Column{Name: ident("id"), Type: tInt64(), NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE,
			Default: &irv1.Default{Class: irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS, Sequence: app("users_id_seq")}},
		annotated(col("tenant_id", tUUID()), comment("bound", onColumn("users", "tenant_id"), nil)),
		col("email", tDomain(app("email_address"))),
		displayName,
		passwordHash,
		annotated(defaulted(col("created_at", tInstant()), irv1.DefaultClass_DEFAULT_EXPRESSION, "now()", nil),
			comment("immutable", onColumn("users", "created_at"), nil)),
		annotated(defaulted(col("updated_at", tInstant()), irv1.DefaultClass_DEFAULT_EXPRESSION, "now()", nil),
			comment("db-updated", onColumn("users", "updated_at"), nil)),
		annotated(defaulted(col("version", tInt32()), irv1.DefaultClass_DEFAULT_CONSTANT, "1", &irv1.Value{Kind: &irv1.Value_Int{Int: 1}}),
			comment("version", onColumn("users", "version"), nil)),
		col("nickname?", tOpaque("public", "citext")))
	users.Uniqueness = []*irv1.Uniqueness{unique("users_tenant_email_key", "tenant_id", "email"), pk("users_pkey", "id")}
	users.ForeignKeys = []*irv1.ForeignKey{fk("users_tenant_id_fkey", []string{"tenant_id"}, app("tenants"), "id")}
	// §6.2: a trigger travels as a typed extension.
	users.Extensions = []*anyMessage{mustAny(&pgextv1.Trigger{
		Name: "users_touch_updated_at", Timing: "BEFORE", Events: []string{"UPDATE"}, Level: "ROW",
		Function: app("touch_updated_at"), Enabled: "ORIGIN",
	})}

	profiles := table("profiles", col("user_id", tInt64()), col("bio?", tText()), col("birthday?", tDate()))
	profiles.Uniqueness = []*irv1.Uniqueness{pk("profiles_pkey", "user_id")}
	profileFK := fk("profiles_user_id_fkey", []string{"user_id"}, app("users"), "id")
	// §7.6: the comment's name is superseded by config.
	profileFK.Annotations = []*irv1.Annotation{comment("inverse-name",
		"COMMENT ON CONSTRAINT profiles_user_id_fkey ON app.profiles", stringValue("user_profile"))}
	profiles.ForeignKeys = []*irv1.ForeignKey{profileFK}

	slug := col("slug?", tText())
	slug.Generation = &irv1.Generation{Kind: irv1.GenerationKind_GENERATION_STORED, Native: "lower(replace(title, ' '::text, '-'::text))"}
	labels := col("labels?", tArray(tText()))
	labels.Type.Native = &irv1.NativeType{Name: &irv1.QualifiedName{Namespace: "pg_catalog", Object: "_text"}, Raw: "text[]"}
	posts := table("posts",
		&irv1.Column{Name: ident("id"), Type: tInt64(), NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE,
			Default: &irv1.Default{Class: irv1.DefaultClass_DEFAULT_IDENTITY_BY_DEFAULT, Sequence: app("posts_id_seq")}},
		col("author_id", tInt64()),
		col("editor_id?", tInt64()),
		col("title", tText()),
		slug,
		defaulted(col("status", tEnum(app("post_status"))), irv1.DefaultClass_DEFAULT_CONSTANT, "'draft'::app.post_status",
			&irv1.Value{Kind: &irv1.Value_String_{String_: "draft"}}),
		col("price?", tComposite(app("money_amount"))),
		labels,
		col("published_at?", tInstant()))
	posts.Uniqueness = []*irv1.Uniqueness{pk("posts_pkey", "id")}
	posts.ForeignKeys = []*irv1.ForeignKey{
		fk("posts_editor_id_fkey", []string{"editor_id"}, app("users"), "id"),
		fk("posts_author_id_fkey", []string{"author_id"}, app("users"), "id"),
	}
	posts.Constraints = []*irv1.Constraint{
		opaqueCheck("posts_published_check", []string{"published_at", "status"}, "((published_at IS NULL) OR (status <> 'draft'::app.post_status))"),
		lengthCheck("posts_title_check", []string{"title"}, proto.Uint32(1), proto.Uint32(200)),
	}

	tags := table("tags",
		&irv1.Column{Name: ident("id"), Type: tInt64(), NullabilitySource: irv1.NullabilitySource_NULLABILITY_SOURCE_DATABASE,
			Default: &irv1.Default{Class: irv1.DefaultClass_DEFAULT_IDENTITY_ALWAYS, Sequence: app("tags_id_seq")}},
		col("name", tDomain(app("slug_text"))))
	tags.Uniqueness = []*irv1.Uniqueness{pk("tags_pkey", "id"), unique("tags_name_key", "name")}

	postTags := table("post_tags", col("post_id", tInt64()), col("tag_id", tInt64()))
	postTags.Uniqueness = []*irv1.Uniqueness{pk("post_tags_pkey", "post_id", "tag_id")}
	postTags.ForeignKeys = []*irv1.ForeignKey{
		fk("post_tags_tag_id_fkey", []string{"tag_id"}, app("tags"), "id"),
		fk("post_tags_post_id_fkey", []string{"post_id"}, app("posts"), "id"),
	}

	// §6.4: Postgres reports every view column as nullable.
	published := view("published_posts",
		annotated(col("id?", tInt64()), comment("nonnull", onColumn("published_posts", "id"), nil)),
		annotated(col("title?", tText()), comment("nonnull", onColumn("published_posts", "title"), nil)),
		col("author_id?", tInt64()),
		col("published_at?", tInstant()))
	published.Annotations = []*irv1.Annotation{comment("logical-key", "COMMENT ON VIEW app.published_posts", listValue("id"))}
	published.DependsOn = []*irv1.QualifiedName{app("posts")}

	postScore := &irv1.Routine{Name: ident("post_score"), Overloads: []*irv1.Overload{
		overload("COMMENT ON FUNCTION app.post_score(bigint, integer)",
			&irv1.Argument{Name: ident("p_id"), Type: tInt64(), Mode: irv1.ArgMode_ARG_MODE_IN},
			&irv1.Argument{Name: ident("window_days"), Type: tInt32(), Mode: irv1.ArgMode_ARG_MODE_IN}),
		overload("COMMENT ON FUNCTION app.post_score(bigint)",
			&irv1.Argument{Name: ident("p_id"), Type: tInt64(), Mode: irv1.ArgMode_ARG_MODE_IN}),
	}}

	emailAddress := &irv1.Domain{Name: ident("email_address"), Base: tText(), Nullable: true,
		Constraints: []*irv1.Constraint{lengthCheck("email_address_check", nil, nil, proto.Uint32(320))}}
	slugText := &irv1.Domain{Name: ident("slug_text"), Base: tText(), Nullable: true,
		Constraints: []*irv1.Constraint{{Name: "slug_text_check", Kind: irv1.ConstraintKind_CONSTRAINT_KIND_CHECK, ClientValidated: true,
			Form: portable(&irv1.PortableConstraint{Kind: &irv1.PortableConstraint_Pattern{Pattern: &irv1.PatternConstraint{Regex: "^[a-z0-9]+(-[a-z0-9]+)*$"}}})}}}

	// §8.5: Postgres cannot declare composite attributes NOT NULL.
	moneyAmount := &irv1.Composite{Name: ident("money_amount"), Attributes: numbered(
		col("amount?", &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{Precision: proto.Uint32(12), Scale: proto.Uint32(2)}}}),
		col("currency?", &irv1.Type{Kind: &irv1.Type_Char{Char: &irv1.Char{Length: 3, Unit: irv1.LengthUnit_LENGTH_UNIT_CHARACTERS}}}),
	)}

	sequence := func(name, table string) *irv1.Sequence {
		return &irv1.Sequence{Name: ident(name), Type: tInt64(), Start: 1, Increment: 1, Min: 1, Max: 9223372036854775807,
			OwnedBy: &irv1.QualifiedName{Catalog: "shop", Namespace: "app", Object: table, Member: "id"}}
	}

	return &irv1.Catalog{
		Name: ident("shop"),
		Provenance: &irv1.Provenance{
			Source: "pg-live", SourceVersion: "1.0.0", InputFingerprint: "sha256:9f2c…",
			Fidelity: irv1.SourceFidelity_SOURCE_FIDELITY_FULL,
		},
		DefaultNamespace: "app",
		Namespaces: []*irv1.Namespace{{
			Name:   ident("app"),
			Tables: []*irv1.Table{users, posts, tenants, tags, profiles, published, postTags},
			Enums: []*irv1.Enum{{Name: ident("post_status"), Labels: []string{"draft", "published", "archived"},
				Origin: irv1.EnumOrigin_ENUM_ORIGIN_NATIVE}},
			Domains:    []*irv1.Domain{slugText, emailAddress},
			Composites: []*irv1.Composite{moneyAmount},
			Sequences:  []*irv1.Sequence{sequence("users_id_seq", "users"), sequence("tags_id_seq", "tags"), sequence("posts_id_seq", "posts")},
			Routines:   []*irv1.Routine{postScore},
		}},
	}
}

// shopConfig is the annotations section of dql.yaml (§1.3), with the
// nonnull of §8.5 and the plugin annotation of §7.7, as pg-live reads it:
// names as written, so the core splits them into words.
func shopConfig() map[string][]*irv1.Annotation {
	cfg := map[string][]*irv1.Annotation{}
	add := func(key, annotation string, v *irv1.AnnotationValue) {
		cfg[key] = append(cfg[key], configured(key, annotation, v))
	}
	for table, name := range map[string]string{
		"tenants": "tenant", "users": "user", "profiles": "profile", "posts": "post",
		"tags": "tag", "post_tags": "post_tag", "published_posts": "published_post",
	} {
		add("shop.app."+table, "name", stringValue(name))
	}
	add("shop.app.profiles.profiles_user_id_fkey", "inverse-name", stringValue("profile"))
	add("shop.app.posts.posts_author_id_fkey", "collection-policy", stringValue("replace"))
	add("shop.app.posts.posts_author_id_fkey", "removal", stringValue("delete"))
	add("shop.app.money_amount.amount", "nonnull", nil)
	add("shop.app.posts.published_at", "x.ts.time-type", stringValue("date"))
	return cfg
}

func shopSource() SourceOutput {
	return SourceOutput{Dialect: irv1.Dialect_DIALECT_POSTGRES, Catalogs: []*irv1.Catalog{shopCatalog()}, Config: shopConfig()}
}

// shopOptions maps citext, as the Go emitter's types config does (§8.3).
var shopOptions = Options{OpaqueMapped: []*irv1.QualifiedName{{Namespace: "public", Object: "citext"}}}

func annotated(c *irv1.Column, anns ...*irv1.Annotation) *irv1.Column {
	c.Annotations = append(c.Annotations, anns...)
	return c
}

func defaulted(c *irv1.Column, class irv1.DefaultClass, nativeText string, v *irv1.Value) *irv1.Column {
	c.Default = &irv1.Default{Class: class, Native: nativeText, Value: v}
	return c
}

func overload(location string, args ...*irv1.Argument) *irv1.Overload {
	return &irv1.Overload{
		Kind:      irv1.RoutineKind_ROUTINE_KIND_FUNCTION,
		Arguments: args,
		Returns: &irv1.Returns{Kind: &irv1.Returns_Scalar{Scalar: &irv1.ReturnValue{
			Type: &irv1.Type{Kind: &irv1.Type_Decimal{Decimal: &irv1.Decimal{}}}, Nullable: true}}},
		NullBehavior: irv1.NullBehavior_NULL_BEHAVIOR_STRICT,
		Volatility:   irv1.Volatility_VOLATILITY_STABLE,
		Annotations:  []*irv1.Annotation{comment("public", location, nil)},
		Extensions:   []*anyMessage{mustAny(&pgextv1.RoutineSource{Language: "sql", Body: "..."})},
	}
}
