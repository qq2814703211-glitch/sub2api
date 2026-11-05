package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// APIKeyGroupRoute stores ordered fallback candidates; group_id remains the primary.
type APIKeyGroupRoute struct{ ent.Schema }

func (APIKeyGroupRoute) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "api_key_group_routes"}}
}
func (APIKeyGroupRoute) Fields() []ent.Field {
	return []ent.Field{field.Int64("api_key_id").Positive(), field.Int64("group_id").Positive(), field.Int("position").Positive()}
}
func (APIKeyGroupRoute) Indexes() []ent.Index {
	return []ent.Index{index.Fields("api_key_id", "group_id").Unique(), index.Fields("api_key_id", "position").Unique(), index.Fields("group_id")}
}
