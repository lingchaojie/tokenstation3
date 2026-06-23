package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	dbwebchatattachment "github.com/Wei-Shaw/sub2api/ent/webchatattachment"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newWebChatRepoTestClient(t *testing.T) (*dbent.Client, func()) {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", t.Name()))
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))

	return client, func() {
		_ = client.Close()
		_ = db.Close()
	}
}

func createWebChatRepoTestUser(t *testing.T, client *dbent.Client, email string) int64 {
	t.Helper()
	user, err := client.User.Create().
		SetEmail(email).
		SetPasswordHash("hash").
		Save(context.Background())
	require.NoError(t, err)
	return user.ID
}

func TestWebChatRepository_ConversationOwnership(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	ownerID := createWebChatRepoTestUser(t, client, "chat-owner@example.com")
	otherID := createWebChatRepoTestUser(t, client, "chat-other@example.com")

	conv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{
		UserID:          ownerID,
		Title:           "Owned conversation",
		DefaultModel:    "gpt-5",
		DefaultProvider: "openai",
	})
	require.NoError(t, err)

	_, err = repo.GetConversationForUser(ctx, otherID, conv.ID)
	require.ErrorIs(t, err, service.ErrWebChatConversationNotFound)

	got, err := repo.GetConversationForUser(ctx, ownerID, conv.ID)
	require.NoError(t, err)
	require.Equal(t, conv.ID, got.ID)
}

func TestWebChatRepository_ListConversationsExcludesDeleted(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	userID := createWebChatRepoTestUser(t, client, "chat-list@example.com")

	active, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{
		UserID: userID,
		Title:  "Active conversation",
	})
	require.NoError(t, err)
	deleted, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{
		UserID: userID,
		Title:  "Deleted conversation",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SoftDeleteConversation(ctx, userID, deleted.ID))

	conversations, page, err := repo.ListConversations(ctx, userID, pagination.PaginationParams{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Len(t, conversations, 1)
	require.Equal(t, active.ID, conversations[0].ID)
}

func TestWebChatRepository_AttachUploadedFilesToMessageRejectsForeignAttachment(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	ownerID := createWebChatRepoTestUser(t, client, "chat-attach-owner@example.com")
	otherID := createWebChatRepoTestUser(t, client, "chat-attach-other@example.com")

	conv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: ownerID})
	require.NoError(t, err)
	msg, err := repo.CreateMessage(ctx, service.CreateWebChatMessageInput{
		ConversationID: conv.ID,
		UserID:         ownerID,
		Role:           service.WebChatRoleUser,
		ContentText:    "hello",
		Status:         service.WebChatMessageStatusCompleted,
	})
	require.NoError(t, err)
	ownerAttachment, err := repo.CreateAttachment(ctx, service.CreateWebChatAttachmentInput{
		UserID:      ownerID,
		Kind:        service.WebChatAttachmentKindFile,
		Filename:    "owned.txt",
		ContentType: "text/plain",
		SizeBytes:   5,
		StorageKey:  "owner/owned.txt",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	require.NoError(t, err)
	foreignAttachment, err := repo.CreateAttachment(ctx, service.CreateWebChatAttachmentInput{
		UserID:      otherID,
		Kind:        service.WebChatAttachmentKindFile,
		Filename:    "foreign.txt",
		ContentType: "text/plain",
		SizeBytes:   7,
		StorageKey:  "other/foreign.txt",
		SHA256:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	require.NoError(t, err)

	_, err = repo.AttachUploadedFilesToMessage(ctx, ownerID, conv.ID, msg.ID, []int64{ownerAttachment.ID, foreignAttachment.ID})
	require.ErrorIs(t, err, service.ErrWebChatAttachmentNotFound)

	stored, err := client.WebChatAttachment.Get(ctx, ownerAttachment.ID)
	require.NoError(t, err)
	require.Nil(t, stored.MessageID)
	require.Nil(t, stored.ConversationID)
	foreignStored, err := client.WebChatAttachment.Query().
		Where(dbwebchatattachment.IDEQ(foreignAttachment.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, foreignStored.MessageID)
	require.Nil(t, foreignStored.ConversationID)
}

func TestWebChatRepository_CreateUserMessageUpdatesConversationStats(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	userID := createWebChatRepoTestUser(t, client, "chat-message-stats@example.com")

	conv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{
		UserID:          userID,
		DefaultModel:    "gpt-5",
		DefaultProvider: "openai",
	})
	require.NoError(t, err)

	msg, err := repo.CreateMessage(ctx, service.CreateWebChatMessageInput{
		ConversationID: conv.ID,
		UserID:         userID,
		Role:           service.WebChatRoleUser,
		Model:          "claude-sonnet-4.5",
		Provider:       "anthropic",
		ContentText:    "hello",
		Status:         service.WebChatMessageStatusCompleted,
	})
	require.NoError(t, err)
	require.NotZero(t, msg.ID)

	updated, err := repo.GetConversationForUser(ctx, userID, conv.ID)
	require.NoError(t, err)
	require.Equal(t, 1, updated.MessageCount)
	require.Equal(t, "claude-sonnet-4.5", updated.LastModel)
	require.Equal(t, "anthropic", updated.LastProvider)
	require.NotNil(t, updated.LastMessageAt)
}

func TestWebChatRepository_GetArtifactForUserRejectsNonOwner(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	ownerID := createWebChatRepoTestUser(t, client, "chat-artifact-owner@example.com")
	otherID := createWebChatRepoTestUser(t, client, "chat-artifact-other@example.com")

	conv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: ownerID})
	require.NoError(t, err)
	msg, err := repo.CreateMessage(ctx, service.CreateWebChatMessageInput{
		ConversationID: conv.ID,
		UserID:         ownerID,
		Role:           service.WebChatRoleAssistant,
		ContentText:    "created a file",
		Status:         service.WebChatMessageStatusCompleted,
	})
	require.NoError(t, err)
	artifact, err := repo.CreateArtifact(ctx, service.CreateWebChatArtifactInput{
		MessageID:      msg.ID,
		ConversationID: conv.ID,
		UserID:         ownerID,
		Filename:       "result.txt",
		ContentType:    "text/plain",
		SizeBytes:      6,
		StorageKey:     "owner/result.txt",
		SHA256:         "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Source:         service.WebChatArtifactSourceGeneratedFile,
	})
	require.NoError(t, err)

	_, err = repo.GetArtifactForUser(ctx, otherID, artifact.ID)
	require.ErrorIs(t, err, service.ErrWebChatArtifactNotFound)

	got, err := repo.GetArtifactForUser(ctx, ownerID, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, artifact.ID, got.ID)
}

func TestWebChatRepository_CreateArtifactRejectsForeignMessage(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	ownerID := createWebChatRepoTestUser(t, client, "chat-artifact-parent-owner@example.com")
	otherID := createWebChatRepoTestUser(t, client, "chat-artifact-parent-other@example.com")

	ownerConv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: ownerID})
	require.NoError(t, err)
	otherConv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: otherID})
	require.NoError(t, err)
	otherMsg, err := repo.CreateMessage(ctx, service.CreateWebChatMessageInput{
		ConversationID: otherConv.ID,
		UserID:         otherID,
		Role:           service.WebChatRoleAssistant,
		ContentText:    "foreign result",
		Status:         service.WebChatMessageStatusCompleted,
	})
	require.NoError(t, err)

	_, err = repo.CreateArtifact(ctx, service.CreateWebChatArtifactInput{
		MessageID:      otherMsg.ID,
		ConversationID: ownerConv.ID,
		UserID:         ownerID,
		Filename:       "foreign-result.txt",
		ContentType:    "text/plain",
		SizeBytes:      6,
		StorageKey:     "owner/foreign-result.txt",
		SHA256:         "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Source:         service.WebChatArtifactSourceGeneratedFile,
	})
	require.ErrorIs(t, err, service.ErrWebChatMessageNotFound)
}

func TestWebChatRepository_CreateAttachmentRejectsForeignMessage(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	ownerID := createWebChatRepoTestUser(t, client, "chat-attachment-parent-owner@example.com")
	otherID := createWebChatRepoTestUser(t, client, "chat-attachment-parent-other@example.com")

	ownerConv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: ownerID})
	require.NoError(t, err)
	otherConv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: otherID})
	require.NoError(t, err)
	otherMsg, err := repo.CreateMessage(ctx, service.CreateWebChatMessageInput{
		ConversationID: otherConv.ID,
		UserID:         otherID,
		Role:           service.WebChatRoleUser,
		ContentText:    "foreign upload",
		Status:         service.WebChatMessageStatusCompleted,
	})
	require.NoError(t, err)

	_, err = repo.CreateAttachment(ctx, service.CreateWebChatAttachmentInput{
		MessageID:      &otherMsg.ID,
		ConversationID: &ownerConv.ID,
		UserID:         ownerID,
		Kind:           service.WebChatAttachmentKindFile,
		Filename:       "foreign.txt",
		ContentType:    "text/plain",
		SizeBytes:      7,
		StorageKey:     "owner/foreign.txt",
		SHA256:         "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
	})
	require.ErrorIs(t, err, service.ErrWebChatMessageNotFound)
}

func TestWebChatRepository_GetAttachmentForUserRejectsDeletedConversation(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	userID := createWebChatRepoTestUser(t, client, "chat-attachment-deleted@example.com")

	conv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: userID})
	require.NoError(t, err)
	msg, err := repo.CreateMessage(ctx, service.CreateWebChatMessageInput{
		ConversationID: conv.ID,
		UserID:         userID,
		Role:           service.WebChatRoleUser,
		ContentText:    "with attachment",
		Status:         service.WebChatMessageStatusCompleted,
	})
	require.NoError(t, err)
	attachment, err := repo.CreateAttachment(ctx, service.CreateWebChatAttachmentInput{
		MessageID:      &msg.ID,
		ConversationID: &conv.ID,
		UserID:         userID,
		Kind:           service.WebChatAttachmentKindFile,
		Filename:       "hidden.txt",
		ContentType:    "text/plain",
		SizeBytes:      6,
		StorageKey:     "owner/hidden.txt",
		SHA256:         "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SoftDeleteConversation(ctx, userID, conv.ID))

	_, err = repo.GetAttachmentForUser(ctx, userID, attachment.ID)
	require.ErrorIs(t, err, service.ErrWebChatAttachmentNotFound)
}

func TestWebChatRepository_GetArtifactForUserRejectsDeletedConversation(t *testing.T) {
	client, cleanup := newWebChatRepoTestClient(t)
	defer cleanup()

	repo := NewWebChatRepository(client)
	ctx := context.Background()
	userID := createWebChatRepoTestUser(t, client, "chat-artifact-deleted@example.com")

	conv, err := repo.CreateConversation(ctx, service.CreateWebChatConversationInput{UserID: userID})
	require.NoError(t, err)
	msg, err := repo.CreateMessage(ctx, service.CreateWebChatMessageInput{
		ConversationID: conv.ID,
		UserID:         userID,
		Role:           service.WebChatRoleAssistant,
		ContentText:    "artifact",
		Status:         service.WebChatMessageStatusCompleted,
	})
	require.NoError(t, err)
	artifact, err := repo.CreateArtifact(ctx, service.CreateWebChatArtifactInput{
		MessageID:      msg.ID,
		ConversationID: conv.ID,
		UserID:         userID,
		Filename:       "hidden-result.txt",
		ContentType:    "text/plain",
		SizeBytes:      6,
		StorageKey:     "owner/hidden-result.txt",
		SHA256:         "9999999999999999999999999999999999999999999999999999999999999999",
		Source:         service.WebChatArtifactSourceGeneratedFile,
	})
	require.NoError(t, err)
	require.NoError(t, repo.SoftDeleteConversation(ctx, userID, conv.ID))

	_, err = repo.GetArtifactForUser(ctx, userID, artifact.ID)
	require.ErrorIs(t, err, service.ErrWebChatArtifactNotFound)
}
