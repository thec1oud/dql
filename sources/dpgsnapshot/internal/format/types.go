// Code below mirrors dpg's committed snapshot format, version 0.2.x
// (github.com/thec1oud/dpg, core/internal/snapshot/types.go). Field names
// and JSON tags match dpg's exactly, pointer fields keep dpg's distinction
// between absent and empty, and nothing is dropped (design record §6.3).

package format

// NameMapEntry is one dpg NAME MAP entry: a naming rule (Rule) or a
// literal target name (Name) for one tool.
type NameMapEntry struct {
	Tool string `json:"tool"`
	Rule string `json:"rule,omitempty"`
	Name string `json:"name,omitempty"`
}

// Object is one entry of the snapshot's objects map: a union discriminated
// by Kind, with exactly the field for that kind set. Kinds stored in Opaque
// carry their own Kind too, equal to the outer one.
type Object struct {
	Kind                string               `json:"kind"`
	Table               *Table               `json:"table,omitempty"`
	View                *View                `json:"view,omitempty"`
	Function            *Function            `json:"function,omitempty"`
	Type                *Type                `json:"type,omitempty"`
	Schema              *Schema              `json:"schema,omitempty"`
	Extension           *Extension           `json:"extension,omitempty"`
	Sequence            *Sequence            `json:"sequence,omitempty"`
	Role                *Role                `json:"role,omitempty"`
	VirtualType         *VirtualType         `json:"virtual_type,omitempty"`
	DefaultPrivileges   *DefaultPrivileges   `json:"default_privileges,omitempty"`
	ParameterPrivileges *ParameterPrivileges `json:"parameter_privileges,omitempty"`
	Opaque              *Opaque              `json:"opaque,omitempty"`
}

// Opaque is dpg's record for the object kinds it compares by body: procedure,
// aggregate, tablespace, fdw, server, user_mapping, publication,
// subscription, event_trigger, collation, operator, operator_class,
// operator_family, cast, statistics, ts_config, ts_dict, ts_parser and
// ts_template. Each kind sets only its own prefixed fields.
type Opaque struct {
	Kind                           string           `json:"kind"`
	Schema                         string           `json:"schema,omitempty"`
	Name                           string           `json:"name"`
	Args                           string           `json:"args,omitempty"`
	Using                          string           `json:"using,omitempty"`
	BodyHash                       string           `json:"body_hash,omitempty"`
	Comment                        *string          `json:"comment,omitempty"`
	Deprecated                     *string          `json:"deprecated,omitempty"`
	Grants                         []Grant          `json:"grants,omitempty"`
	Revocations                    []Grant          `json:"revocations,omitempty"`
	SecurityLabels                 []SecurityLabel  `json:"security_labels,omitempty"`
	Mappings                       []TSMapping      `json:"mappings,omitempty"`
	NameMaps                       []NameMapEntry   `json:"name_maps,omitempty"`
	TablespaceLocation             string           `json:"tablespace_location,omitempty"`
	TablespaceOwner                *string          `json:"tablespace_owner,omitempty"`
	TablespaceStorageParams        string           `json:"tablespace_storage_params,omitempty"`
	CastMethod                     string           `json:"cast_method,omitempty"`
	CastContext                    string           `json:"cast_context,omitempty"`
	CastFunction                   string           `json:"cast_function,omitempty"`
	EventTriggerEvent              string           `json:"event_trigger_event,omitempty"`
	EventTriggerTags               []string         `json:"event_trigger_tags,omitempty"`
	EventTriggerFunction           string           `json:"event_trigger_function,omitempty"`
	EventTriggerOwner              *string          `json:"event_trigger_owner,omitempty"`
	EventTriggerEnableState        string           `json:"event_trigger_enable_state,omitempty"`
	TSDictOwner                    *string          `json:"ts_dict_owner,omitempty"`
	TSConfigOwner                  *string          `json:"ts_config_owner,omitempty"`
	TSDictOptionsStructured        bool             `json:"ts_dict_options_structured,omitempty"`
	TSDictOptions                  []OptionKV       `json:"ts_dict_options,omitempty"`
	TSDictTemplateName             string           `json:"ts_dict_template_name,omitempty"`
	ProcedureDependsOnExtensions   []string         `json:"procedure_depends_on_extensions,omitempty"`
	ProcedureOwner                 *string          `json:"procedure_owner,omitempty"`
	AggregateOwner                 *string          `json:"aggregate_owner,omitempty"`
	ProcedureTransforms            []string         `json:"procedure_transforms,omitempty"`
	ProcedureObjFile               *string          `json:"procedure_obj_file,omitempty"`
	ProcedureLinkSymbol            *string          `json:"procedure_link_symbol,omitempty"`
	ProcedureAtomicBody            bool             `json:"procedure_atomic_body,omitempty"`
	ProcedureSecurityDef           bool             `json:"procedure_security_def,omitempty"`
	ProcedureConfig                []FuncConfig     `json:"procedure_config,omitempty"`
	OptionsStructured              bool             `json:"options_structured,omitempty"`
	FDWHandler                     string           `json:"fdw_handler,omitempty"`
	FDWValidator                   string           `json:"fdw_validator,omitempty"`
	FDWOptions                     []OptionKV       `json:"fdw_options,omitempty"`
	FDWOwner                       *string          `json:"fdw_owner,omitempty"`
	ServerFDWName                  string           `json:"server_fdw_name,omitempty"`
	ServerType                     *string          `json:"server_type,omitempty"`
	ServerVersion                  *string          `json:"server_version,omitempty"`
	ServerOptions                  []OptionKV       `json:"server_options,omitempty"`
	ServerOwner                    *string          `json:"server_owner,omitempty"`
	UserMappingOptions             []OptionKV       `json:"user_mapping_options,omitempty"`
	PublicationOwner               *string          `json:"publication_owner,omitempty"`
	PublicationStructured          bool             `json:"publication_structured,omitempty"`
	PublicationAllTables           bool             `json:"publication_all_tables,omitempty"`
	PublicationTables              []string         `json:"publication_tables,omitempty"`
	PublicationInsert              bool             `json:"publication_insert,omitempty"`
	PublicationUpdate              bool             `json:"publication_update,omitempty"`
	PublicationDelete              bool             `json:"publication_delete,omitempty"`
	PublicationTruncate            bool             `json:"publication_truncate,omitempty"`
	PublicationHasFilteredTables   bool             `json:"publication_has_filtered_tables,omitempty"`
	CollationStructured            bool             `json:"collation_structured,omitempty"`
	CollationProvider              string           `json:"collation_provider,omitempty"`
	CollationCollate               *string          `json:"collation_collate,omitempty"`
	CollationCtype                 *string          `json:"collation_ctype,omitempty"`
	CollationICULocale             *string          `json:"collation_icu_locale,omitempty"`
	CollationDeterministic         bool             `json:"collation_deterministic,omitempty"`
	CollationOwner                 *string          `json:"collation_owner,omitempty"`
	CollationRules                 *string          `json:"collation_rules,omitempty"`
	CollationCopyFrom              *string          `json:"collation_copy_from,omitempty"`
	SubscriptionOwner              *string          `json:"subscription_owner,omitempty"`
	OperatorStructured             bool             `json:"operator_structured,omitempty"`
	OperatorRestrict               *string          `json:"operator_restrict,omitempty"`
	OperatorJoin                   *string          `json:"operator_join,omitempty"`
	OperatorCommutator             *string          `json:"operator_commutator,omitempty"`
	OperatorNegator                *string          `json:"operator_negator,omitempty"`
	OperatorHashes                 bool             `json:"operator_hashes,omitempty"`
	OperatorMerges                 bool             `json:"operator_merges,omitempty"`
	OperatorCoreHash               string           `json:"operator_core_hash,omitempty"`
	StatisticsStructured           bool             `json:"statistics_structured,omitempty"`
	StatisticsTable                string           `json:"statistics_table,omitempty"`
	StatisticsKinds                []string         `json:"statistics_kinds,omitempty"`
	StatisticsColumns              []string         `json:"statistics_columns,omitempty"`
	StatisticsTarget               *int             `json:"statistics_target,omitempty"`
	OpFamilyMembersStructured      bool             `json:"op_family_members_structured,omitempty"`
	OpFamilyMembers                []OpFamilyMember `json:"op_family_members,omitempty"`
	OperatorClassMembersStructured bool             `json:"operator_class_members_structured,omitempty"`
	OperatorClassMembers           []OpFamilyMember `json:"operator_class_members,omitempty"`
	OperatorClassStorageType       string           `json:"operator_class_storage_type,omitempty"`
	OperatorClassFamilySchema      string           `json:"operator_class_family_schema,omitempty"`
	OperatorClassFamilyName        string           `json:"operator_class_family_name,omitempty"`
	OperatorClassFamilyImplicit    bool             `json:"operator_class_family_implicit,omitempty"`
	AggregateOptionsStructured     bool             `json:"aggregate_options_structured,omitempty"`
	AggregateOptions               []OptionKV       `json:"aggregate_options,omitempty"`
}

// OpFamilyMember is one operator or support function of an operator family
// or operator class.
type OpFamilyMember struct {
	IsFunction       bool     `json:"is_function,omitempty"`
	Number           int      `json:"number"`
	NameSchema       string   `json:"name_schema,omitempty"`
	Name             string   `json:"name"`
	LeftType         string   `json:"left_type"`
	RightType        string   `json:"right_type"`
	FuncArgs         []string `json:"func_args,omitempty"`
	OrderBy          bool     `json:"order_by,omitempty"`
	SortFamilySchema string   `json:"sort_family_schema,omitempty"`
	SortFamilyName   string   `json:"sort_family_name,omitempty"`
}

// OptionKV is one OPTIONS (...) entry.
type OptionKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// TSMapping is one MAPPING FOR entry of a text search configuration;
// Dictionaries is tried in order.
type TSMapping struct {
	TokenTypes   []string `json:"token_types"`
	Dictionaries []string `json:"dictionaries"`
}

// Schema is a SCHEMA record.
type Schema struct {
	Name           string          `json:"name"`
	Owner          *string         `json:"owner,omitempty"`
	Comment        *string         `json:"comment,omitempty"`
	RenamedFrom    *string         `json:"renamed_from,omitempty"`
	Grants         []Grant         `json:"grants,omitempty"`
	Revocations    []Grant         `json:"revocations,omitempty"`
	SecurityLabels []SecurityLabel `json:"security_labels,omitempty"`
	NameMaps       []NameMapEntry  `json:"name_maps,omitempty"`
}

// Extension is an EXTENSION record.
type Extension struct {
	Name     string         `json:"name"`
	Schema   *string        `json:"schema,omitempty"`
	Version  *string        `json:"version,omitempty"`
	Comment  *string        `json:"comment,omitempty"`
	NameMaps []NameMapEntry `json:"name_maps,omitempty"`
}

// Table is a TABLE record, including unlogged, foreign and partitioned
// tables. Columns, Constraints, Indexes, Policies and Triggers keep source
// order.
type Table struct {
	Schema               string          `json:"schema"`
	Name                 string          `json:"name"`
	Unlogged             bool            `json:"unlogged,omitempty"`
	Foreign              bool            `json:"foreign,omitempty"`
	ForeignServer        *string         `json:"foreign_server,omitempty"`
	ForeignOptions       string          `json:"foreign_options,omitempty"`
	Owner                *string         `json:"owner,omitempty"`
	Tablespace           *string         `json:"tablespace,omitempty"`
	Comment              *string         `json:"comment,omitempty"`
	OfType               string          `json:"of_type,omitempty"`
	AccessMethod         string          `json:"access_method,omitempty"`
	StorageParams        string          `json:"storage_params,omitempty"`
	RenamedFrom          *string         `json:"renamed_from,omitempty"`
	RenamedFromSchema    *string         `json:"renamed_from_schema,omitempty"`
	Deprecated           *string         `json:"deprecated,omitempty"`
	Protected            bool            `json:"protected,omitempty"`
	DropCascade          bool            `json:"drop_cascade,omitempty"`
	RLSEnabled           bool            `json:"rls_enabled,omitempty"`
	RLSForced            bool            `json:"rls_forced,omitempty"`
	ReplicaIdentityMode  string          `json:"replica_identity_mode,omitempty"`
	ReplicaIdentityIndex string          `json:"replica_identity_index,omitempty"`
	ClusterOn            *string         `json:"cluster_on,omitempty"`
	Inherits             []string        `json:"inherits,omitempty"`
	PartitionBy          string          `json:"partition_by,omitempty"`
	Partitions           []Partition     `json:"partitions,omitempty"`
	Columns              []Column        `json:"columns,omitempty"`
	Constraints          []Constraint    `json:"constraints,omitempty"`
	Indexes              []Index         `json:"indexes,omitempty"`
	Policies             []Policy        `json:"policies,omitempty"`
	Triggers             []Trigger       `json:"triggers,omitempty"`
	Grants               []Grant         `json:"grants,omitempty"`
	Revocations          []Grant         `json:"revocations,omitempty"`
	SecurityLabels       []SecurityLabel `json:"security_labels,omitempty"`
	NameMaps             []NameMapEntry  `json:"name_maps,omitempty"`
}

// Partition is one partition of a partitioned table, with its own
// sub-partitions when PartitionBy is set. Bound is the raw FOR VALUES text.
type Partition struct {
	Schema         string       `json:"schema,omitempty"`
	Name           string       `json:"name"`
	Bound          string       `json:"bound"`
	PartitionBy    string       `json:"partition_by,omitempty"`
	Partitions     []Partition  `json:"partitions,omitempty"`
	Foreign        bool         `json:"foreign,omitempty"`
	ForeignServer  *string      `json:"foreign_server,omitempty"`
	ForeignOptions string       `json:"foreign_options,omitempty"`
	Constraints    []Constraint `json:"constraints,omitempty"`
}

// Column is a table column or a composite-type attribute. Type is the
// formatted type text, such as "character varying(80)" or "app.post_status";
// Default and Generated are expression text.
type Column struct {
	Name                string          `json:"name"`
	Type                string          `json:"type"`
	VirtualType         string          `json:"virtual_type,omitempty"`
	NotNull             bool            `json:"not_null,omitempty"`
	Default             *string         `json:"default,omitempty"`
	Identity            *string         `json:"identity,omitempty"`
	IdentityIncrementBy *int64          `json:"identity_increment_by,omitempty"`
	IdentityMinValue    *int64          `json:"identity_min_value,omitempty"`
	IdentityMaxValue    *int64          `json:"identity_max_value,omitempty"`
	IdentityNoMinValue  bool            `json:"identity_no_min_value,omitempty"`
	IdentityNoMaxValue  bool            `json:"identity_no_max_value,omitempty"`
	IdentityStartValue  *int64          `json:"identity_start_value,omitempty"`
	IdentityCache       *int64          `json:"identity_cache,omitempty"`
	IdentityCycle       *bool           `json:"identity_cycle,omitempty"`
	Serial              *string         `json:"serial,omitempty"`
	Generated           *string         `json:"generated,omitempty"`
	GeneratedVirtual    bool            `json:"generated_virtual,omitempty"`
	Comment             *string         `json:"comment,omitempty"`
	Statistics          *int            `json:"statistics,omitempty"`
	Compression         *string         `json:"compression,omitempty"`
	Storage             *string         `json:"storage,omitempty"`
	Deprecated          *string         `json:"deprecated,omitempty"`
	RenamedFrom         *string         `json:"renamed_from,omitempty"`
	Collation           string          `json:"collation,omitempty"`
	Grants              []Grant         `json:"grants,omitempty"`
	Revocations         []Grant         `json:"revocations,omitempty"`
	SecurityLabels      []SecurityLabel `json:"security_labels,omitempty"`
	NameMaps            []NameMapEntry  `json:"name_maps,omitempty"`
}

// Constraint is a table, partition or domain constraint. Type is "PRIMARY
// KEY", "UNIQUE", "CHECK", "FOREIGN KEY", "EXCLUDE" or "NOT NULL", and
// Expr is the whole constraint definition as text, such as
// `FOREIGN KEY ("author_id") REFERENCES "app"."users" ("id")`. Name is
// empty when the source left the constraint unnamed.
type Constraint struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	Expr              string `json:"expr,omitempty"`
	NotValid          bool   `json:"not_valid,omitempty"`
	Deferrable        bool   `json:"deferrable,omitempty"`
	Comment           string `json:"comment,omitempty"`
	NoInherit         bool   `json:"no_inherit,omitempty"`
	NotEnforced       bool   `json:"not_enforced,omitempty"`
	InitiallyDeferred bool   `json:"initially_deferred,omitempty"`
}

// Index is an index record. Columns, Include and With are comma-separated
// text; an expression column is parenthesized.
type Index struct {
	Name             string `json:"name"`
	Unique           bool   `json:"unique,omitempty"`
	Method           string `json:"method"`
	Columns          string `json:"columns"`
	Where            string `json:"where,omitempty"`
	Include          string `json:"include,omitempty"`
	NullsNotDistinct bool   `json:"nulls_not_distinct,omitempty"`
	With             string `json:"with,omitempty"`
	Tablespace       string `json:"tablespace,omitempty"`
	Comment          string `json:"comment,omitempty"`
}

// Policy is a row-level security policy.
type Policy struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Permissive bool     `json:"permissive"`
	Using      string   `json:"using,omitempty"`
	WithCheck  string   `json:"with_check,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	Comment    string   `json:"comment,omitempty"`
}

// Trigger is a table trigger. Events and UpdateOfColumns are
// comma-separated.
type Trigger struct {
	Name                string   `json:"name"`
	When                string   `json:"when"`
	Events              string   `json:"events"`
	ForEach             string   `json:"for_each"`
	UpdateOfColumns     string   `json:"update_of_columns,omitempty"`
	OldTransitionName   string   `json:"old_transition_name,omitempty"`
	NewTransitionName   string   `json:"new_transition_name,omitempty"`
	Function            string   `json:"function"`
	Condition           string   `json:"condition,omitempty"`
	Comment             string   `json:"comment,omitempty"`
	EnableState         string   `json:"enable_state,omitempty"`
	DependsOnExtensions []string `json:"depends_on_extensions,omitempty"`
	Constraint          bool     `json:"constraint,omitempty"`
	ConstraintFrom      string   `json:"constraint_from,omitempty"`
	Deferrable          bool     `json:"deferrable,omitempty"`
	InitiallyDeferred   bool     `json:"initially_deferred,omitempty"`
}

// Grant is a grant or revocation. Privileges is nil for ALL.
type Grant struct {
	Privileges []string `json:"privileges,omitempty"`
	Roles      []string `json:"roles"`
	WithGrant  bool     `json:"with_grant,omitempty"`
	GrantedBy  *string  `json:"granted_by,omitempty"`
}

// SecurityLabel is one SECURITY LABEL entry.
type SecurityLabel struct {
	Provider string `json:"provider,omitempty"`
	Label    string `json:"label"`
}

// View is a VIEW, MATERIALIZED VIEW or RECURSIVE VIEW record. Query is the
// view's query text.
type View struct {
	Schema         string          `json:"schema"`
	Name           string          `json:"name"`
	Query          string          `json:"query"`
	Owner          *string         `json:"owner,omitempty"`
	Comment        *string         `json:"comment,omitempty"`
	Deprecated     *string         `json:"deprecated,omitempty"`
	Recursive      bool            `json:"recursive,omitempty"`
	WithNoData     bool            `json:"with_no_data,omitempty"`
	Tablespace     *string         `json:"tablespace,omitempty"`
	StorageParams  string          `json:"storage_params,omitempty"`
	Grants         []Grant         `json:"grants,omitempty"`
	Revocations    []Grant         `json:"revocations,omitempty"`
	Indexes        []Index         `json:"indexes,omitempty"`
	SecurityLabels []SecurityLabel `json:"security_labels,omitempty"`
	NameMaps       []NameMapEntry  `json:"name_maps,omitempty"`
	Columns        []string        `json:"columns,omitempty"`
	Reconstructed  bool            `json:"reconstructed,omitempty"`
}

// Function is a FUNCTION record. Args is the type-only signature key
// (IN, INOUT and VARIADIC argument types; no names, modes or defaults), and
// the body is stored only as BodyHash.
type Function struct {
	Schema              string          `json:"schema"`
	Name                string          `json:"name"`
	Args                string          `json:"args"`
	ReturnType          string          `json:"return_type"`
	ReturnsSet          bool            `json:"returns_set,omitempty"`
	ReturnTable         string          `json:"return_table,omitempty"`
	Language            string          `json:"language"`
	Volatility          string          `json:"volatility"`
	Parallel            string          `json:"parallel,omitempty"`
	Cost                *float64        `json:"cost,omitempty"`
	Rows                *float64        `json:"rows,omitempty"`
	BodyHash            string          `json:"body_hash"`
	Comment             *string         `json:"comment,omitempty"`
	Deprecated          *string         `json:"deprecated,omitempty"`
	Grants              []Grant         `json:"grants,omitempty"`
	Revocations         []Grant         `json:"revocations,omitempty"`
	SecurityLabels      []SecurityLabel `json:"security_labels,omitempty"`
	NameMaps            []NameMapEntry  `json:"name_maps,omitempty"`
	DependsOnExtensions []string        `json:"depends_on_extensions,omitempty"`
	Leakproof           bool            `json:"leakproof,omitempty"`
	Transforms          []string        `json:"transforms,omitempty"`
	ObjFile             *string         `json:"obj_file,omitempty"`
	LinkSymbol          *string         `json:"link_symbol,omitempty"`
	AtomicBody          bool            `json:"atomic_body,omitempty"`
	Owner               *string         `json:"owner,omitempty"`
	Strict              bool            `json:"strict,omitempty"`
	SecurityDef         bool            `json:"security_def,omitempty"`
	Config              []FuncConfig    `json:"config,omitempty"`
}

// FuncConfig is one SET clause of a function or procedure.
type FuncConfig struct {
	Param       string  `json:"param"`
	Value       *string `json:"value,omitempty"`
	FromCurrent bool    `json:"from_current,omitempty"`
}

// Type is a TYPE or DOMAIN record. Variant is ENUM, COMPOSITE, RANGE,
// DOMAIN or BASE, and only that variant's fields are set.
type Type struct {
	Schema            string          `json:"schema"`
	Name              string          `json:"name"`
	Variant           string          `json:"variant"`
	Values            []string        `json:"values,omitempty"`
	CompositeAttrs    []Column        `json:"composite_attrs,omitempty"`
	BodyHash          string          `json:"body_hash,omitempty"`
	BaseStructured    bool            `json:"base_structured,omitempty"`
	BaseImmutableHash string          `json:"base_immutable_hash,omitempty"`
	BaseReceive       *string         `json:"base_receive,omitempty"`
	BaseSend          *string         `json:"base_send,omitempty"`
	BaseTypmodIn      *string         `json:"base_typmod_in,omitempty"`
	BaseTypmodOut     *string         `json:"base_typmod_out,omitempty"`
	BaseAnalyze       *string         `json:"base_analyze,omitempty"`
	BaseSubscript     *string         `json:"base_subscript,omitempty"`
	BaseStorage       *string         `json:"base_storage,omitempty"`
	MultirangeName    string          `json:"multirange_name,omitempty"`
	DomainBaseType    string          `json:"domain_base_type,omitempty"`
	DomainCollation   string          `json:"domain_collation,omitempty"`
	DomainDefault     *string         `json:"domain_default,omitempty"`
	DomainNotNull     bool            `json:"domain_not_null,omitempty"`
	DomainConstraints []Constraint    `json:"domain_constraints,omitempty"`
	Comment           *string         `json:"comment,omitempty"`
	Owner             *string         `json:"owner,omitempty"`
	Grants            []Grant         `json:"grants,omitempty"`
	Revocations       []Grant         `json:"revocations,omitempty"`
	SecurityLabels    []SecurityLabel `json:"security_labels,omitempty"`
	NameMaps          []NameMapEntry  `json:"name_maps,omitempty"`
}

// Sequence is a SEQUENCE record.
type Sequence struct {
	Schema         string          `json:"schema"`
	Name           string          `json:"name"`
	Unlogged       bool            `json:"unlogged,omitempty"`
	Owner          *string         `json:"owner,omitempty"`
	Comment        *string         `json:"comment,omitempty"`
	Grants         []Grant         `json:"grants,omitempty"`
	Revocations    []Grant         `json:"revocations,omitempty"`
	IncrementBy    *int64          `json:"increment_by,omitempty"`
	MinValue       *int64          `json:"min_value,omitempty"`
	MaxValue       *int64          `json:"max_value,omitempty"`
	NoMinValue     bool            `json:"no_min_value,omitempty"`
	NoMaxValue     bool            `json:"no_max_value,omitempty"`
	StartValue     *int64          `json:"start_value,omitempty"`
	Cache          *int64          `json:"cache,omitempty"`
	Cycle          bool            `json:"cycle,omitempty"`
	AsType         string          `json:"as_type,omitempty"`
	OwnedBy        *string         `json:"owned_by,omitempty"`
	SecurityLabels []SecurityLabel `json:"security_labels,omitempty"`
	NameMaps       []NameMapEntry  `json:"name_maps,omitempty"`
}

// Role is a ROLE record, stored in a cluster-level snapshot. PasswordHash
// is a hash of the declared password text, never the password.
type Role struct {
	Name            string           `json:"name"`
	CanLogin        *bool            `json:"can_login,omitempty"`
	Superuser       *bool            `json:"superuser,omitempty"`
	CreateDB        *bool            `json:"create_db,omitempty"`
	CreateRole      *bool            `json:"create_role,omitempty"`
	Inherit         *bool            `json:"inherit,omitempty"`
	IsReplication   *bool            `json:"is_replication,omitempty"`
	BypassRLS       *bool            `json:"bypass_rls,omitempty"`
	ConnectionLimit *int             `json:"connection_limit,omitempty"`
	PasswordHash    string           `json:"password_hash,omitempty"`
	ValidUntil      *string          `json:"valid_until,omitempty"`
	Comment         *string          `json:"comment,omitempty"`
	Memberships     []RoleMembership `json:"memberships,omitempty"`
	Configs         []RoleConfig     `json:"configs,omitempty"`
	SecurityLabels  []SecurityLabel  `json:"security_labels,omitempty"`
	NameMaps        []NameMapEntry   `json:"name_maps,omitempty"`
}

// RoleMembership is one role membership of a role.
type RoleMembership struct {
	Role      string `json:"role"`
	Direction string `json:"direction"`
	Admin     bool   `json:"admin,omitempty"`
	Inherit   *bool  `json:"inherit,omitempty"`
	Set       *bool  `json:"set,omitempty"`
}

// RoleConfig is one role configuration setting.
type RoleConfig struct {
	Param       string  `json:"param,omitempty"`
	Value       *string `json:"value,omitempty"`
	FromCurrent bool    `json:"from_current,omitempty"`
	Reset       bool    `json:"reset,omitempty"`
	ResetAll    bool    `json:"reset_all,omitempty"`
	InDatabase  *string `json:"in_database,omitempty"`
}

// VtypeBody is a dpg VIRTUAL TYPE body: a union discriminated by Kind,
// which is "type_ref", "composite" or "union".
type VtypeBody struct {
	Kind    string       `json:"kind"`
	Schema  string       `json:"schema,omitempty"`
	Name    string       `json:"name,omitempty"`
	IsArray bool         `json:"array,omitempty"`
	Virtual bool         `json:"virtual,omitempty"`
	Fields  []VtypeField `json:"fields,omitempty"`
	Members []VtypeBody  `json:"members,omitempty"`
}

// VtypeField is one field of a composite virtual type body.
type VtypeField struct {
	Name string    `json:"name"`
	Type VtypeBody `json:"type"`
}

// VirtualType is a dpg VIRTUAL TYPE: a structured description of a json or
// jsonb value. It exists only in dpg; the database holds the json or jsonb
// type.
type VirtualType struct {
	Schema     string         `json:"schema,omitempty"`
	Name       string         `json:"name"`
	Body       VtypeBody      `json:"body"`
	JsonFormat string         `json:"json_format,omitempty"`
	Comment    *string        `json:"comment,omitempty"`
	NameMaps   []NameMapEntry `json:"name_maps,omitempty"`
}

// DefaultPrivileges is a DEFAULT PRIVILEGES record for one role, schema
// and object type.
type DefaultPrivileges struct {
	ForRole     *string `json:"for_role,omitempty"`
	InSchema    *string `json:"in_schema,omitempty"`
	ObjectType  string  `json:"object_type"`
	Grants      []Grant `json:"grants,omitempty"`
	Revocations []Grant `json:"revocations,omitempty"`
}

// ParameterPrivileges is the PARAMETER PRIVILEGES record.
type ParameterPrivileges struct {
	Grants      []ParamGrant `json:"grants,omitempty"`
	Revocations []ParamGrant `json:"revocations,omitempty"`
}

// ParamGrant is one grant or revocation of PARAMETER PRIVILEGES.
type ParamGrant struct {
	Privileges []string `json:"privileges,omitempty"`
	Parameters []string `json:"parameters"`
	Roles      []string `json:"roles"`
	WithGrant  bool     `json:"with_grant,omitempty"`
	GrantedBy  *string  `json:"granted_by,omitempty"`
}
