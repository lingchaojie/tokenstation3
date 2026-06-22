package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WebChatAttachment holds the schema definition for the WebChatAttachment entity.
type WebChatAttachment struct {
	ent.Schema
}

func (WebChatAttachment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "web_chat_attachments"},
	}
}

func (WebChatAttachment) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("message_id").
			Optional().
			Nillable(),
		field.Int64("conversation_id").
			Optional().
			Nillable(),
		field.Int64("user_id"),
		field.String("kind").
			MaxLen(20),
		field.String("filename").
			MaxLen(255),
		field.String("content_type").
			MaxLen(120),
		field.Int64("size_bytes"),
		field.String("storage_key").
			MaxLen(500),
		field.String("sha256").
			MaxLen(64),
		field.Text("text_preview").
			Optional().
			Nillable(),
		field.String("status").
			MaxLen(20).
			Default("uploaded"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (WebChatAttachment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at").
			StorageKey("idx_web_chat_attachments_user_created"),
		index.Fields("message_id").
			StorageKey("idx_web_chat_attachments_message").
			Annotations(entsql.IndexWhere("message_id IS NOT NULL")),
		index.Fields("conversation_id").
			StorageKey("idx_web_chat_attachments_conversation").
			Annotations(entsql.IndexWhere("conversation_id IS NOT NULL")),
	}
}
