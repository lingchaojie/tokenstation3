package schema

import (
	"fmt"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/domain"
)

type UserAPIKeyRoute struct {
	ent.Schema
}

func (UserAPIKeyRoute) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (UserAPIKeyRoute) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.String("key_type").
			MaxLen(20).
			Validate(func(v string) error {
				switch v {
				case domain.PlatformAnthropic, domain.PlatformOpenAI:
					return nil
				default:
					return fmt.Errorf("invalid API key route key type %q", v)
				}
			}),
		field.Int64("group_id"),
	}
}

func (UserAPIKeyRoute) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("api_key_routes").
			Field("user_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("group", Group.Type).
			Ref("api_key_routes").
			Field("group_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (UserAPIKeyRoute) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "key_type").Unique(),
		index.Fields("group_id"),
	}
}
