package postgres

type RelationContract struct {
	Schema  string
	Name    string
	Relkind string
}

type ColumnContract struct {
	Schema   string
	Table    string
	Column   string
	UDTName  string
	Nullable bool
}

type Contract struct {
	Relations []RelationContract
	Columns   []ColumnContract
}

func CurrentContract() Contract {
	return Contract{
		Relations: []RelationContract{
			{Schema: "public", Name: "api_keys", Relkind: "r"},
			{Schema: "public", Name: "groups", Relkind: "r"},
			{Schema: "public", Name: "usage_logs", Relkind: "r"},
		},
		Columns: []ColumnContract{
			{Schema: "public", Table: "api_keys", Column: "id", UDTName: "int8"},
			{Schema: "public", Table: "api_keys", Column: "key", UDTName: "varchar"},
			{Schema: "public", Table: "api_keys", Column: "name", UDTName: "varchar"},
			{Schema: "public", Table: "api_keys", Column: "group_id", UDTName: "int8", Nullable: true},
			{Schema: "public", Table: "api_keys", Column: "quota", UDTName: "numeric"},
			{Schema: "public", Table: "api_keys", Column: "quota_used", UDTName: "numeric"},
			{Schema: "public", Table: "api_keys", Column: "last_used_at", UDTName: "timestamptz", Nullable: true},
			{Schema: "public", Table: "api_keys", Column: "expires_at", UDTName: "timestamptz", Nullable: true},
			{Schema: "public", Table: "api_keys", Column: "status", UDTName: "varchar"},
			{Schema: "public", Table: "api_keys", Column: "created_at", UDTName: "timestamptz"},
			{Schema: "public", Table: "api_keys", Column: "deleted_at", UDTName: "timestamptz", Nullable: true},

			{Schema: "public", Table: "groups", Column: "id", UDTName: "int8"},
			{Schema: "public", Table: "groups", Column: "name", UDTName: "varchar"},

			{Schema: "public", Table: "usage_logs", Column: "id", UDTName: "int8"},
			{Schema: "public", Table: "usage_logs", Column: "api_key_id", UDTName: "int8", Nullable: true},
			{Schema: "public", Table: "usage_logs", Column: "actual_cost", UDTName: "numeric"},
			{Schema: "public", Table: "usage_logs", Column: "created_at", UDTName: "timestamptz"},
		},
	}
}
