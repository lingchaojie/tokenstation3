package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WebChatMessage holds the schema definition for the WebChatMessage entity.
type WebChatMessage struct {
	ent.Schema
}

func (WebChatMessage) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "web_chat_messages"},
	}
}

func (WebChatMessage) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (WebChatMessage) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("conversation_id"),
		field.Int64("user_id"),
		field.String("role").
			MaxLen(20),
		field.String("model").
			MaxLen(100).
			Default(""),
		field.String("provider").
			MaxLen(50).
			Default(""),
		field.Text("content_text").
			Default(""),
		field.JSON("content_json", []map[string]any{}).
			Default([]map[string]any{}).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.String("status").
			MaxLen(20).
			Default("completed"),
		field.String("error_code").
			MaxLen(80).
			Optional().
			Nillable(),
		field.Text("error_message").
			Optional().
			Nillable(),
		field.Int64("usage_log_id").
			Optional().
			Nillable(),
	}
}

func (WebChatMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("conversation_id", "created_at").
			StorageKey("idx_web_chat_messages_conversation_created"),
		index.Fields("user_id", "created_at").
			StorageKey("idx_web_chat_messages_user_created"),
		index.Fields("usage_log_id").
			StorageKey("idx_web_chat_messages_usage_log_id").
			Annotations(entsql.IndexWhere("usage_log_id IS NOT NULL")),
	}
}
