package repository

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	dbwebchatartifact "github.com/Wei-Shaw/sub2api/ent/webchatartifact"
	dbwebchatattachment "github.com/Wei-Shaw/sub2api/ent/webchatattachment"
	dbwebchatconversation "github.com/Wei-Shaw/sub2api/ent/webchatconversation"
	dbwebchatmessage "github.com/Wei-Shaw/sub2api/ent/webchatmessage"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type webChatRepository struct {
	client *dbent.Client
}

func NewWebChatRepository(client *dbent.Client) service.WebChatRepository {
	return &webChatRepository{client: client}
}

func (r *webChatRepository) CreateConversation(ctx context.Context, in service.CreateWebChatConversationInput) (*service.WebChatConversation, error) {
	client := clientFromContext(ctx, r.client)
	created, err := client.WebChatConversation.Create().
		SetUserID(in.UserID).
		SetTitle(in.Title).
		SetDefaultModel(in.DefaultModel).
		SetDefaultProvider(in.DefaultProvider).
		SetLastModel(in.DefaultModel).
		SetLastProvider(in.DefaultProvider).
		SetStatus(service.WebChatConversationStatusActive).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return webChatConversationFromEnt(created), nil
}

func (r *webChatRepository) ListConversations(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.WebChatConversation, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := client.WebChatConversation.Query().
		Where(
			dbwebchatconversation.UserIDEQ(userID),
			dbwebchatconversation.StatusNEQ(service.WebChatConversationStatusDeleted),
		)

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	items, err := q.
		Order(dbent.Desc(dbwebchatconversation.FieldUpdatedAt), dbent.Desc(dbwebchatconversation.FieldID)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	return webChatConversationEntitiesToService(items), paginationResultFromTotal(int64(total), params), nil
}

func (r *webChatRepository) GetConversationForUser(ctx context.Context, userID, conversationID int64) (*service.WebChatConversation, error) {
	client := clientFromContext(ctx, r.client)
	conv, err := getWebChatConversationForUser(ctx, client, userID, conversationID)
	if err != nil {
		return nil, err
	}
	return webChatConversationFromEnt(conv), nil
}

func (r *webChatRepository) UpdateConversation(ctx context.Context, userID, conversationID int64, in service.UpdateWebChatConversationInput) (*service.WebChatConversation, error) {
	client := clientFromContext(ctx, r.client)
	builder := client.WebChatConversation.Update().
		Where(
			dbwebchatconversation.IDEQ(conversationID),
			dbwebchatconversation.UserIDEQ(userID),
			dbwebchatconversation.StatusNEQ(service.WebChatConversationStatusDeleted),
		).
		SetUpdatedAt(time.Now().UTC())
	if in.Title != nil {
		builder.SetTitle(*in.Title)
	}
	if in.DefaultModel != nil {
		builder.SetDefaultModel(*in.DefaultModel)
	}
	if in.DefaultProvider != nil {
		builder.SetDefaultProvider(*in.DefaultProvider)
	}
	if in.Status != nil {
		builder.SetStatus(*in.Status)
	}
	affected, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, service.ErrWebChatConversationNotFound
	}
	return r.GetConversationForUser(ctx, userID, conversationID)
}

func (r *webChatRepository) SoftDeleteConversation(ctx context.Context, userID, conversationID int64) error {
	client := clientFromContext(ctx, r.client)
	affected, err := client.WebChatConversation.Update().
		Where(
			dbwebchatconversation.IDEQ(conversationID),
			dbwebchatconversation.UserIDEQ(userID),
			dbwebchatconversation.StatusNEQ(service.WebChatConversationStatusDeleted),
		).
		SetStatus(service.WebChatConversationStatusDeleted).
		SetUpdatedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrWebChatConversationNotFound
	}
	return nil
}

func (r *webChatRepository) CreateMessage(ctx context.Context, in service.CreateWebChatMessageInput) (*service.WebChatMessage, error) {
	if r.client == nil {
		return nil, fmt.Errorf("web chat repository not ready")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	client := tx.Client()
	if _, err := getWebChatConversationForUser(ctx, client, in.UserID, in.ConversationID); err != nil {
		return nil, err
	}

	status := in.Status
	if status == "" {
		status = service.WebChatMessageStatusCompleted
	}
	builder := client.WebChatMessage.Create().
		SetConversationID(in.ConversationID).
		SetUserID(in.UserID).
		SetRole(in.Role).
		SetModel(in.Model).
		SetProvider(in.Provider).
		SetContentText(in.ContentText).
		SetStatus(status).
		SetNillableErrorCode(in.ErrorCode).
		SetNillableErrorMessage(in.ErrorMessage).
		SetNillableUsageLogID(in.UsageLogID)
	if in.ContentJSON != nil {
		builder.SetContentJSON(in.ContentJSON)
	}
	msg, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}

	var attachments []service.WebChatAttachment
	if len(in.AttachmentIDs) > 0 {
		attachments, err = attachWebChatFilesToMessage(ctx, client, in.UserID, in.ConversationID, msg.ID, in.AttachmentIDs)
		if err != nil {
			return nil, err
		}
	}

	if in.Role == service.WebChatRoleUser {
		now := time.Now().UTC()
		affected, err := client.WebChatConversation.Update().
			Where(
				dbwebchatconversation.IDEQ(in.ConversationID),
				dbwebchatconversation.UserIDEQ(in.UserID),
				dbwebchatconversation.StatusNEQ(service.WebChatConversationStatusDeleted),
			).
			AddMessageCount(1).
			SetLastMessageAt(now).
			SetLastModel(in.Model).
			SetLastProvider(in.Provider).
			SetUpdatedAt(now).
			Save(ctx)
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			return nil, service.ErrWebChatConversationNotFound
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true

	out := webChatMessageFromEnt(msg)
	out.Attachments = attachments
	return out, nil
}

func (r *webChatRepository) ListMessages(ctx context.Context, userID, conversationID int64) ([]service.WebChatMessage, error) {
	client := clientFromContext(ctx, r.client)
	if _, err := getWebChatConversationForUser(ctx, client, userID, conversationID); err != nil {
		return nil, err
	}
	items, err := client.WebChatMessage.Query().
		Where(
			dbwebchatmessage.UserIDEQ(userID),
			dbwebchatmessage.ConversationIDEQ(conversationID),
		).
		Order(dbent.Asc(dbwebchatmessage.FieldCreatedAt), dbent.Asc(dbwebchatmessage.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	messages := webChatMessageEntitiesToService(items)
	if len(messages) == 0 {
		return messages, nil
	}
	if err := populateWebChatMessageChildren(ctx, client, userID, messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (r *webChatRepository) UpdateMessage(ctx context.Context, userID, messageID int64, in service.UpdateWebChatMessageInput) (*service.WebChatMessage, error) {
	client := clientFromContext(ctx, r.client)
	existing, err := client.WebChatMessage.Query().
		Where(dbwebchatmessage.IDEQ(messageID), dbwebchatmessage.UserIDEQ(userID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrWebChatMessageNotFound, nil)
	}
	if _, err := getWebChatConversationForUser(ctx, client, userID, existing.ConversationID); err != nil {
		return nil, err
	}

	predicates := []predicate.WebChatMessage{
		dbwebchatmessage.IDEQ(messageID),
		dbwebchatmessage.UserIDEQ(userID),
	}
	if in.ExpectedConversationID != nil {
		predicates = append(predicates, dbwebchatmessage.ConversationIDEQ(*in.ExpectedConversationID))
	}
	if in.ExpectedRole != nil {
		predicates = append(predicates, dbwebchatmessage.RoleEQ(*in.ExpectedRole))
	}
	if len(in.ExpectedStatuses) > 0 {
		predicates = append(predicates, dbwebchatmessage.StatusIn(in.ExpectedStatuses...))
	}
	builder := client.WebChatMessage.Update().
		Where(predicates...).
		SetUpdatedAt(time.Now().UTC())
	if in.Model != nil {
		builder.SetModel(*in.Model)
	}
	if in.Provider != nil {
		builder.SetProvider(*in.Provider)
	}
	if in.ContentText != nil {
		builder.SetContentText(*in.ContentText)
	}
	if in.ContentJSON != nil {
		builder.SetContentJSON(*in.ContentJSON)
	}
	if in.Status != nil {
		builder.SetStatus(*in.Status)
	}
	if in.ErrorCode != nil {
		builder.SetErrorCode(*in.ErrorCode)
	}
	if in.ErrorMessage != nil {
		builder.SetErrorMessage(*in.ErrorMessage)
	}
	if in.UsageLogID != nil {
		builder.SetUsageLogID(*in.UsageLogID)
	}
	affected, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, service.ErrWebChatMessageNotFound
	}
	updated, err := client.WebChatMessage.Query().
		Where(dbwebchatmessage.IDEQ(messageID), dbwebchatmessage.UserIDEQ(userID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrWebChatMessageNotFound, nil)
	}
	return webChatMessageFromEnt(updated), nil
}

func (r *webChatRepository) CreateAttachment(ctx context.Context, in service.CreateWebChatAttachmentInput) (*service.WebChatAttachment, error) {
	client := clientFromContext(ctx, r.client)
	conversationID, messageID, err := validateWebChatAttachmentParents(ctx, client, in)
	if err != nil {
		return nil, err
	}
	builder := client.WebChatAttachment.Create().
		SetNillableMessageID(messageID).
		SetNillableConversationID(conversationID).
		SetUserID(in.UserID).
		SetKind(in.Kind).
		SetFilename(in.Filename).
		SetContentType(in.ContentType).
		SetSizeBytes(in.SizeBytes).
		SetStorageKey(in.StorageKey).
		SetSha256(in.SHA256).
		SetNillableTextPreview(in.TextPreview)
	if in.Status != "" {
		builder.SetStatus(in.Status)
	}
	created, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	return webChatAttachmentFromEnt(created), nil
}

func (r *webChatRepository) AttachUploadedFilesToMessage(ctx context.Context, userID, conversationID, messageID int64, attachmentIDs []int64) ([]service.WebChatAttachment, error) {
	if len(attachmentIDs) == 0 {
		return []service.WebChatAttachment{}, nil
	}
	if r.client == nil {
		return nil, fmt.Errorf("web chat repository not ready")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	client := tx.Client()
	if _, err := getWebChatConversationForUser(ctx, client, userID, conversationID); err != nil {
		return nil, err
	}
	if err := ensureWebChatMessageForUser(ctx, client, userID, conversationID, messageID); err != nil {
		return nil, err
	}
	attachments, err := attachWebChatFilesToMessage(ctx, client, userID, conversationID, messageID, attachmentIDs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return attachments, nil
}

func (r *webChatRepository) GetAttachmentForUser(ctx context.Context, userID, attachmentID int64) (*service.WebChatAttachment, error) {
	client := clientFromContext(ctx, r.client)
	attachment, err := client.WebChatAttachment.Query().
		Where(
			dbwebchatattachment.IDEQ(attachmentID),
			dbwebchatattachment.UserIDEQ(userID),
			dbwebchatattachment.StatusNEQ(service.WebChatAttachmentStatusDeleted),
		).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrWebChatAttachmentNotFound, nil)
	}
	if attachment.ConversationID != nil {
		if _, err := getWebChatConversationForUser(ctx, client, userID, *attachment.ConversationID); err != nil {
			return nil, service.ErrWebChatAttachmentNotFound.WithCause(err)
		}
	}
	return webChatAttachmentFromEnt(attachment), nil
}

func (r *webChatRepository) CreateArtifact(ctx context.Context, in service.CreateWebChatArtifactInput) (*service.WebChatArtifact, error) {
	client := clientFromContext(ctx, r.client)
	if _, err := getWebChatConversationForUser(ctx, client, in.UserID, in.ConversationID); err != nil {
		return nil, err
	}
	if err := ensureWebChatMessageForUser(ctx, client, in.UserID, in.ConversationID, in.MessageID); err != nil {
		return nil, err
	}
	created, err := client.WebChatArtifact.Create().
		SetMessageID(in.MessageID).
		SetConversationID(in.ConversationID).
		SetUserID(in.UserID).
		SetFilename(in.Filename).
		SetContentType(in.ContentType).
		SetSizeBytes(in.SizeBytes).
		SetStorageKey(in.StorageKey).
		SetSha256(in.SHA256).
		SetSource(in.Source).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return webChatArtifactFromEnt(created), nil
}

func (r *webChatRepository) GetArtifactForUser(ctx context.Context, userID, artifactID int64) (*service.WebChatArtifact, error) {
	client := clientFromContext(ctx, r.client)
	artifact, err := client.WebChatArtifact.Query().
		Where(dbwebchatartifact.IDEQ(artifactID), dbwebchatartifact.UserIDEQ(userID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrWebChatArtifactNotFound, nil)
	}
	if _, err := getWebChatConversationForUser(ctx, client, userID, artifact.ConversationID); err != nil {
		return nil, service.ErrWebChatArtifactNotFound.WithCause(err)
	}
	return webChatArtifactFromEnt(artifact), nil
}

func getWebChatConversationForUser(ctx context.Context, client *dbent.Client, userID, conversationID int64) (*dbent.WebChatConversation, error) {
	conv, err := client.WebChatConversation.Query().
		Where(
			dbwebchatconversation.IDEQ(conversationID),
			dbwebchatconversation.UserIDEQ(userID),
			dbwebchatconversation.StatusNEQ(service.WebChatConversationStatusDeleted),
		).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrWebChatConversationNotFound, nil)
	}
	return conv, nil
}

func ensureWebChatMessageForUser(ctx context.Context, client *dbent.Client, userID, conversationID, messageID int64) error {
	_, err := client.WebChatMessage.Query().
		Where(
			dbwebchatmessage.IDEQ(messageID),
			dbwebchatmessage.UserIDEQ(userID),
			dbwebchatmessage.ConversationIDEQ(conversationID),
		).
		Only(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrWebChatMessageNotFound, nil)
	}
	return nil
}

func getWebChatMessageForUser(ctx context.Context, client *dbent.Client, userID, messageID int64) (*dbent.WebChatMessage, error) {
	msg, err := client.WebChatMessage.Query().
		Where(dbwebchatmessage.IDEQ(messageID), dbwebchatmessage.UserIDEQ(userID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrWebChatMessageNotFound, nil)
	}
	return msg, nil
}

func validateWebChatAttachmentParents(ctx context.Context, client *dbent.Client, in service.CreateWebChatAttachmentInput) (*int64, *int64, error) {
	conversationID := in.ConversationID
	messageID := in.MessageID
	if messageID != nil {
		msg, err := getWebChatMessageForUser(ctx, client, in.UserID, *messageID)
		if err != nil {
			return nil, nil, err
		}
		if conversationID != nil && *conversationID != msg.ConversationID {
			return nil, nil, service.ErrWebChatMessageNotFound
		}
		if conversationID == nil {
			conversationID = &msg.ConversationID
		}
		if _, err := getWebChatConversationForUser(ctx, client, in.UserID, msg.ConversationID); err != nil {
			return nil, nil, err
		}
		return conversationID, messageID, nil
	}
	if conversationID != nil {
		if _, err := getWebChatConversationForUser(ctx, client, in.UserID, *conversationID); err != nil {
			return nil, nil, err
		}
	}
	return conversationID, messageID, nil
}

func attachWebChatFilesToMessage(ctx context.Context, client *dbent.Client, userID, conversationID, messageID int64, attachmentIDs []int64) ([]service.WebChatAttachment, error) {
	uniqueIDs := make([]int64, 0, len(attachmentIDs))
	seen := make(map[int64]struct{}, len(attachmentIDs))
	for _, id := range attachmentIDs {
		if id <= 0 {
			return nil, service.ErrWebChatAttachmentNotFound
		}
		if _, ok := seen[id]; ok {
			return nil, service.ErrWebChatAttachmentNotFound
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	found, err := client.WebChatAttachment.Query().
		Where(
			dbwebchatattachment.IDIn(uniqueIDs...),
			dbwebchatattachment.UserIDEQ(userID),
			dbwebchatattachment.MessageIDIsNil(),
			dbwebchatattachment.ConversationIDIsNil(),
			dbwebchatattachment.StatusNEQ(service.WebChatAttachmentStatusDeleted),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(found) != len(uniqueIDs) {
		return nil, service.ErrWebChatAttachmentNotFound
	}

	affected, err := client.WebChatAttachment.Update().
		Where(
			dbwebchatattachment.IDIn(uniqueIDs...),
			dbwebchatattachment.UserIDEQ(userID),
			dbwebchatattachment.MessageIDIsNil(),
			dbwebchatattachment.ConversationIDIsNil(),
			dbwebchatattachment.StatusNEQ(service.WebChatAttachmentStatusDeleted),
		).
		SetMessageID(messageID).
		SetConversationID(conversationID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if affected != len(uniqueIDs) {
		return nil, service.ErrWebChatAttachmentNotFound
	}

	updated, err := client.WebChatAttachment.Query().
		Where(dbwebchatattachment.IDIn(uniqueIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return orderWebChatAttachments(updated, attachmentIDs), nil
}

func populateWebChatMessageChildren(ctx context.Context, client *dbent.Client, userID int64, messages []service.WebChatMessage) error {
	messageIDs := make([]int64, 0, len(messages))
	indexByID := make(map[int64]int, len(messages))
	for i := range messages {
		messageIDs = append(messageIDs, messages[i].ID)
		indexByID[messages[i].ID] = i
	}

	attachments, err := client.WebChatAttachment.Query().
		Where(
			dbwebchatattachment.UserIDEQ(userID),
			dbwebchatattachment.MessageIDIn(messageIDs...),
			dbwebchatattachment.StatusNEQ(service.WebChatAttachmentStatusDeleted),
		).
		Order(dbent.Asc(dbwebchatattachment.FieldCreatedAt), dbent.Asc(dbwebchatattachment.FieldID)).
		All(ctx)
	if err != nil {
		return err
	}
	for _, attachment := range attachments {
		if attachment.MessageID == nil {
			continue
		}
		if idx, ok := indexByID[*attachment.MessageID]; ok {
			messages[idx].Attachments = append(messages[idx].Attachments, *webChatAttachmentFromEnt(attachment))
		}
	}

	artifacts, err := client.WebChatArtifact.Query().
		Where(
			dbwebchatartifact.UserIDEQ(userID),
			dbwebchatartifact.MessageIDIn(messageIDs...),
		).
		Order(dbent.Asc(dbwebchatartifact.FieldCreatedAt), dbent.Asc(dbwebchatartifact.FieldID)).
		All(ctx)
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if idx, ok := indexByID[artifact.MessageID]; ok {
			messages[idx].Artifacts = append(messages[idx].Artifacts, *webChatArtifactFromEnt(artifact))
		}
	}
	return nil
}

func orderWebChatAttachments(items []*dbent.WebChatAttachment, ids []int64) []service.WebChatAttachment {
	byID := make(map[int64]*dbent.WebChatAttachment, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	ordered := make([]service.WebChatAttachment, 0, len(ids))
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			ordered = append(ordered, *webChatAttachmentFromEnt(item))
		}
	}
	return ordered
}

func webChatConversationEntitiesToService(items []*dbent.WebChatConversation) []service.WebChatConversation {
	out := make([]service.WebChatConversation, 0, len(items))
	for _, item := range items {
		out = append(out, *webChatConversationFromEnt(item))
	}
	return out
}

func webChatConversationFromEnt(item *dbent.WebChatConversation) *service.WebChatConversation {
	if item == nil {
		return nil
	}
	return &service.WebChatConversation{
		ID:              item.ID,
		UserID:          item.UserID,
		Title:           item.Title,
		DefaultModel:    item.DefaultModel,
		DefaultProvider: item.DefaultProvider,
		LastModel:       item.LastModel,
		LastProvider:    item.LastProvider,
		Status:          item.Status,
		MessageCount:    item.MessageCount,
		LastMessageAt:   item.LastMessageAt,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func webChatMessageEntitiesToService(items []*dbent.WebChatMessage) []service.WebChatMessage {
	out := make([]service.WebChatMessage, 0, len(items))
	for _, item := range items {
		out = append(out, *webChatMessageFromEnt(item))
	}
	return out
}

func webChatMessageFromEnt(item *dbent.WebChatMessage) *service.WebChatMessage {
	if item == nil {
		return nil
	}
	contentJSON := item.ContentJSON
	if contentJSON == nil {
		contentJSON = []map[string]any{}
	}
	return &service.WebChatMessage{
		ID:             item.ID,
		ConversationID: item.ConversationID,
		UserID:         item.UserID,
		Role:           item.Role,
		Model:          item.Model,
		Provider:       item.Provider,
		ContentText:    item.ContentText,
		ContentJSON:    contentJSON,
		Status:         item.Status,
		ErrorCode:      item.ErrorCode,
		ErrorMessage:   item.ErrorMessage,
		UsageLogID:     item.UsageLogID,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func webChatAttachmentFromEnt(item *dbent.WebChatAttachment) *service.WebChatAttachment {
	if item == nil {
		return nil
	}
	return &service.WebChatAttachment{
		ID:             item.ID,
		MessageID:      item.MessageID,
		ConversationID: item.ConversationID,
		UserID:         item.UserID,
		Kind:           item.Kind,
		Filename:       item.Filename,
		ContentType:    item.ContentType,
		SizeBytes:      item.SizeBytes,
		StorageKey:     item.StorageKey,
		SHA256:         item.Sha256,
		TextPreview:    item.TextPreview,
		Status:         item.Status,
		CreatedAt:      item.CreatedAt,
	}
}

func webChatArtifactFromEnt(item *dbent.WebChatArtifact) *service.WebChatArtifact {
	if item == nil {
		return nil
	}
	return &service.WebChatArtifact{
		ID:             item.ID,
		MessageID:      item.MessageID,
		ConversationID: item.ConversationID,
		UserID:         item.UserID,
		Filename:       item.Filename,
		ContentType:    item.ContentType,
		SizeBytes:      item.SizeBytes,
		StorageKey:     item.StorageKey,
		SHA256:         item.Sha256,
		Source:         item.Source,
		CreatedAt:      item.CreatedAt,
	}
}
