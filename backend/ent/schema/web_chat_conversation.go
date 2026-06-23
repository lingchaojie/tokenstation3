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

// WebChatConversation holds the schema definition for the WebChatConversation entity.
type WebChatConversation struct {
	ent.Schema
}

func (WebChatConversation) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "web_chat_conversations"},
	}
}

func (WebChatConversation) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (WebChatConversation) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.String("title").
			MaxLen(200).
			Default(""),
		field.String("default_model").
			MaxLen(100).
			Default(""),
		field.String("default_provider").
			MaxLen(50).
			Default(""),
		field.String("last_model").
			MaxLen(100).
			Default(""),
		field.String("last_provider").
			MaxLen(50).
			Default(""),
		field.String("status").
			MaxLen(20).
			Default("active"),
		field.Int("message_count").
			Default(0),
		field.Time("last_message_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (WebChatConversation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "updated_at").
			StorageKey("idx_web_chat_conversations_user_updated").
			Annotations(entsql.IndexWhere("status <> 'deleted'")),
	}
}
