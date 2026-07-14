package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type DailyCheckInClaim struct {
	ent.Schema
}

func (DailyCheckInClaim) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "daily_check_in_claims"}}
}

func (DailyCheckInClaim) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.Time("activity_start_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("check_in_date").
			SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.Float("reward_amount").
			Positive().
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("balance_after").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Time("claimed_at").
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (DailyCheckInClaim) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("daily_check_in_claims").
			Field("user_id").
			Required().
			Unique(),
	}
}

func (DailyCheckInClaim) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "activity_start_at", "check_in_date").Unique(),
		index.Fields("claimed_at"),
	}
}
