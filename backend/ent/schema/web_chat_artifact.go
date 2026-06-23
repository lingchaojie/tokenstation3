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

// WebChatArtifact holds the schema definition for the WebChatArtifact entity.
type WebChatArtifact struct {
	ent.Schema
}

func (WebChatArtifact) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "web_chat_artifacts"},
	}
}

func (WebChatArtifact) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("message_id"),
		field.Int64("conversation_id"),
		field.Int64("user_id"),
		field.String("filename").
			MaxLen(255),
		field.String("content_type").
			MaxLen(120),
		field.Int64("size_bytes"),
		field.String("storage_key").
			MaxLen(500),
		field.String("sha256").
			MaxLen(64),
		field.String("source").
			MaxLen(30),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (WebChatArtifact) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("message_id").
			StorageKey("idx_web_chat_artifacts_message"),
		index.Fields("user_id", "created_at").
			StorageKey("idx_web_chat_artifacts_user_created"),
	}
}
