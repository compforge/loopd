package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/compforge/loopd/api"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	defaultOperationTimeout = 10 * time.Second
	defaultPageSize         = 100
	maxPageSize             = 500
)

type StoreConfig struct {
	Path             string
	OperationTimeout time.Duration
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
}

type Store struct {
	db               *gorm.DB
	operationTimeout time.Duration
}

func OpenStore(config StoreConfig) (*Store, error) {
	if config.Path == "" {
		config.Path = "loopd.db"
	}
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = defaultOperationTimeout
	}
	if config.MaxOpenConns <= 0 {
		config.MaxOpenConns = 1
	}
	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 1
	}
	if config.ConnMaxLifetime <= 0 {
		config.ConnMaxLifetime = 30 * time.Minute
	}
	if config.ConnMaxIdleTime <= 0 {
		config.ConnMaxIdleTime = 5 * time.Minute
	}

	db, err := gorm.Open(sqlite.Open(config.Path), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open loopd database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access loopd database pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(config.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(config.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	store := &Store{db: db, operationTimeout: config.OperationTimeout}
	ctx, cancel := store.withTimeout(context.Background())
	defer cancel()
	if err := db.WithContext(ctx).AutoMigrate(
		&conversationPO{},
		&messagePO{},
		&invocationPO{},
		&activityPO{},
		&harnessCallPO{},
		&interactionPO{},
		&invocationEventPO{},
	); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate loopd database: %w", err)
	}
	return store, nil
}

func (store *Store) Close() error {
	db, err := store.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

func (store *Store) withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, store.operationTimeout)
}

func (store *Store) CreateConversation(ctx context.Context, title string) (api.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	row := conversationPO{ID: newID("conv"), Title: strings.TrimSpace(title)}
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return api.Conversation{}, err
	}
	return conversationFromPO(row), nil
}

func (store *Store) GetConversation(ctx context.Context, id string) (api.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row conversationPO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return api.Conversation{}, mapDBError(err)
	}
	return conversationFromPO(row), nil
}

func (store *Store) ListMessages(ctx context.Context, conversationID string, after, through int64, limit int) ([]api.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	limit = normalizeLimit(limit)
	query := store.db.WithContext(ctx).Where("conversation_id = ? AND sequence > ?", conversationID, after)
	if through > 0 {
		query = query.Where("sequence <= ?", through)
	}
	var rows []messagePO
	if err := query.Order("sequence ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]api.Message, 0, len(rows))
	for _, row := range rows {
		result = append(result, messageFromPO(row))
	}
	return result, nil
}

func (store *Store) CreateMessageInvocation(
	ctx context.Context,
	conversationID string,
	request api.CreateMessageRequest,
) (api.CreateMessageResponse, error) {
	if strings.TrimSpace(request.Content) == "" || !request.Responder.Valid() {
		return api.CreateMessageResponse{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var message messagePO
	var invocation invocationPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&conversationPO{}).
			Where("id = ?", conversationID).
			UpdateColumn("last_sequence", gorm.Expr("last_sequence + ?", 1))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		var conversation conversationPO
		if err := tx.First(&conversation, "id = ?", conversationID).Error; err != nil {
			return mapDBError(err)
		}

		now := time.Now().UTC()
		message = messagePO{
			ID:             newID("msg"),
			ConversationID: conversationID,
			Sequence:       conversation.LastSequence,
			Role:           string(api.RoleUser),
			AuthorID:       "human",
			Content:        strings.TrimSpace(request.Content),
			CreatedAt:      now,
		}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		invocation = invocationPO{
			ID:                newID("inv"),
			ConversationID:    conversationID,
			InputMessageID:    message.ID,
			ResponderRole:     string(request.Responder.Role),
			ResponderID:       request.Responder.ID,
			ContextThroughSeq: message.Sequence,
			Phase:             string(api.InvocationQueued),
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := tx.Create(&invocation).Error; err != nil {
			return err
		}
		return appendEvent(tx, invocation.ID, "", "invocation.created", map[string]any{
			"invocation": invocationFromPO(invocation),
			"message":    messageFromPO(message),
		})
	})
	if err != nil {
		return api.CreateMessageResponse{}, err
	}
	return api.CreateMessageResponse{
		Message:    messageFromPO(message),
		Invocation: invocationFromPO(invocation),
	}, nil
}

func (store *Store) GetInvocation(ctx context.Context, id string) (api.Invocation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row invocationPO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return api.Invocation{}, mapDBError(err)
	}
	return invocationFromPO(row), nil
}

func (store *Store) GetInvocationContext(ctx context.Context, id string) (api.InvocationContext, error) {
	invocation, err := store.GetInvocation(ctx, id)
	if err != nil {
		return api.InvocationContext{}, err
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var input messagePO
	if err := store.db.WithContext(ctx).First(&input, "id = ?", invocation.InputMessageID).Error; err != nil {
		return api.InvocationContext{}, mapDBError(err)
	}
	history, hasEarlier, err := store.listContextMessages(ctx, invocation.ConversationID, invocation.ContextThroughSeq)
	if err != nil {
		return api.InvocationContext{}, err
	}
	var fromSeq int64
	if len(history) > 0 {
		fromSeq = history[0].Sequence
	}
	return api.InvocationContext{
		Invocation:     invocation,
		Input:          messageFromPO(input),
		History:        history,
		HistoryFromSeq: fromSeq,
		HasEarlier:     hasEarlier,
	}, nil
}

func (store *Store) listContextMessages(ctx context.Context, conversationID string, through int64) ([]api.Message, bool, error) {
	var rows []messagePO
	if err := store.db.WithContext(ctx).Where("conversation_id = ? AND sequence <= ?", conversationID, through).
		Order("sequence DESC").Limit(maxPageSize + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasEarlier := len(rows) > maxPageSize
	if hasEarlier {
		rows = rows[:maxPageSize]
	}
	result := make([]api.Message, len(rows))
	for index, row := range rows {
		result[len(rows)-1-index] = messageFromPO(row)
	}
	return result, hasEarlier, nil
}

func (store *Store) ListPendingInvocations(ctx context.Context, operatorID string, limit int) ([]api.Invocation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []invocationPO
	err := store.db.WithContext(ctx).
		Where("responder_role = ? AND responder_id = ? AND phase = ?", api.RoleOperator, operatorID, api.InvocationQueued).
		Order("created_at ASC").
		Limit(normalizeLimit(limit)).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]api.Invocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, invocationFromPO(row))
	}
	return result, nil
}

func (store *Store) AcceptInvocation(ctx context.Context, id string, resource api.ResourceRef) (api.Invocation, error) {
	if resource.APIVersion == "" || resource.Kind == "" || resource.Name == "" || resource.UID == "" {
		return api.Invocation{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row invocationPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if api.InvocationPhase(row.Phase) != api.InvocationQueued {
			if row.ResourceUID == resource.UID {
				return nil
			}
			return ErrConflict
		}
		row.Phase = string(api.InvocationRunning)
		row.ResourceAPIVersion = resource.APIVersion
		row.ResourceKind = resource.Kind
		row.ResourceNamespace = resource.Namespace
		row.ResourceName = resource.Name
		row.ResourceUID = resource.UID
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return appendEvent(tx, id, "", "invocation.accepted", map[string]any{"resource": resource})
	})
	return invocationFromPO(row), err
}

func (store *Store) StartInvocation(ctx context.Context, id string) (api.Invocation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row invocationPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if api.InvocationPhase(row.Phase) == api.InvocationRunning {
			return nil
		}
		if api.InvocationPhase(row.Phase) != api.InvocationQueued {
			return ErrConflict
		}
		row.Phase = string(api.InvocationRunning)
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return appendEvent(tx, id, "", "invocation.started", map[string]any{"phase": row.Phase})
	})
	return invocationFromPO(row), err
}

func (store *Store) CompleteInvocation(
	ctx context.Context,
	id string,
	role api.Role,
	authorID string,
	content string,
) (api.Invocation, error) {
	if (role != api.RoleHarness && role != api.RoleOperator) || authorID == "" || strings.TrimSpace(content) == "" {
		return api.Invocation{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var invocation invocationPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&invocation, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if api.InvocationPhase(invocation.Phase) == api.InvocationSucceeded {
			return nil
		}
		if api.InvocationPhase(invocation.Phase).Terminal() {
			return ErrConflict
		}
		result := tx.Model(&conversationPO{}).
			Where("id = ?", invocation.ConversationID).
			UpdateColumn("last_sequence", gorm.Expr("last_sequence + ?", 1))
		if result.Error != nil {
			return result.Error
		}
		var conversation conversationPO
		if err := tx.First(&conversation, "id = ?", invocation.ConversationID).Error; err != nil {
			return err
		}
		message := messagePO{
			ID:             newID("msg"),
			ConversationID: invocation.ConversationID,
			Sequence:       conversation.LastSequence,
			Role:           string(role),
			AuthorID:       authorID,
			Content:        strings.TrimSpace(content),
			CreatedAt:      time.Now().UTC(),
		}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		invocation.OutputMessageID = message.ID
		invocation.Phase = string(api.InvocationSucceeded)
		invocation.Error = ""
		if err := tx.Save(&invocation).Error; err != nil {
			return err
		}
		return appendEvent(tx, id, "", "invocation.completed", map[string]any{
			"invocation": invocationFromPO(invocation),
			"message":    messageFromPO(message),
		})
	})
	return invocationFromPO(invocation), err
}

func (store *Store) FailInvocation(ctx context.Context, id, reason string) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row invocationPO
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if api.InvocationPhase(row.Phase).Terminal() {
			return nil
		}
		row.Phase = string(api.InvocationFailed)
		row.Error = reason
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return appendEvent(tx, id, "", "invocation.failed", map[string]any{"error": reason})
	})
}

func (store *Store) UpsertActivity(ctx context.Context, invocationID string, request api.ActivityRequest) (api.Activity, error) {
	if request.Key == "" || !request.Actor.Valid() || request.Kind == "" || request.Title == "" || request.Phase == "" {
		return api.Activity{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row activityPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("invocation_id = ? AND key = ?", invocationID, request.Key).First(&row).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			row = activityPO{
				ID:           newID("act"),
				InvocationID: invocationID,
				Key:          request.Key,
				CreatedAt:    time.Now().UTC(),
			}
		case err != nil:
			return err
		}
		row.ParentID = request.ParentID
		row.ActorRole = string(request.Actor.Role)
		row.ActorID = request.Actor.ID
		row.Kind = request.Kind
		row.Title = request.Title
		row.Detail = request.Detail
		row.Phase = string(request.Phase)
		if row.ID == "" {
			return ErrInvalid
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return appendEvent(tx, invocationID, "", "activity.updated", activityFromPO(row))
	})
	return activityFromPO(row), err
}

func (store *Store) ListInvocationEvents(ctx context.Context, invocationID string, after uint64, limit int) ([]api.InvocationEvent, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []invocationEventPO
	if err := store.db.WithContext(ctx).
		Where("invocation_id = ? AND cursor > ?", invocationID, after).
		Order("cursor ASC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]api.InvocationEvent, 0, len(rows))
	for _, row := range rows {
		result = append(result, api.InvocationEvent{
			Cursor: row.Cursor, InvocationID: row.InvocationID, CallID: row.CallID, Kind: row.Kind,
			Data: json.RawMessage(row.Data), CreatedAt: row.CreatedAt,
		})
	}
	return result, nil
}

func (store *Store) LatestInvocationCursor(ctx context.Context, invocationID string) (uint64, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row invocationEventPO
	err := store.db.WithContext(ctx).Where("invocation_id = ?", invocationID).
		Order("cursor DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return row.Cursor, err
}

func (store *Store) ListOperatorEvents(ctx context.Context, operatorID string, after uint64, limit int) ([]api.OperatorEvent, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []invocationEventPO
	err := store.db.WithContext(ctx).Table("invocation_events").
		Select("invocation_events.*").
		Joins("JOIN invocations ON invocations.id = invocation_events.invocation_id").
		Where("invocations.responder_role = ? AND invocations.responder_id = ? AND invocation_events.cursor > ?",
			api.RoleOperator, operatorID, after).
		Where("invocation_events.kind IN ?", []string{
			"invocation.created", "interaction.resolved", "harness_call.updated",
		}).
		Order("invocation_events.cursor ASC").Limit(normalizeLimit(limit)).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	invocationIDs := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, exists := seen[row.InvocationID]; !exists {
			seen[row.InvocationID] = struct{}{}
			invocationIDs = append(invocationIDs, row.InvocationID)
		}
	}
	var invocations []invocationPO
	if len(invocationIDs) > 0 {
		if err := store.db.WithContext(ctx).Where("id IN ?", invocationIDs).Find(&invocations).Error; err != nil {
			return nil, err
		}
	}
	byID := make(map[string]api.Invocation, len(invocations))
	for _, invocation := range invocations {
		byID[invocation.ID] = invocationFromPO(invocation)
	}
	result := make([]api.OperatorEvent, 0, len(rows))
	for _, row := range rows {
		invocation, exists := byID[row.InvocationID]
		if !exists {
			return nil, fmt.Errorf("%w: Invocation %q", ErrNotFound, row.InvocationID)
		}
		result = append(result, api.OperatorEvent{
			Event: api.InvocationEvent{
				Cursor: row.Cursor, InvocationID: row.InvocationID, CallID: row.CallID, Kind: row.Kind,
				Data: json.RawMessage(row.Data), CreatedAt: row.CreatedAt,
			},
			Invocation: invocation,
		})
	}
	return result, nil
}

func (store *Store) ListActivities(ctx context.Context, invocationID string) ([]api.Activity, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []activityPO
	if err := store.db.WithContext(ctx).Where("invocation_id = ?", invocationID).
		Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]api.Activity, 0, len(rows))
	for _, row := range rows {
		result = append(result, activityFromPO(row))
	}
	return result, nil
}

func (store *Store) ListHarnessCalls(ctx context.Context, invocationID string) ([]api.HarnessCall, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []harnessCallPO
	if err := store.db.WithContext(ctx).Where("invocation_id = ?", invocationID).
		Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]api.HarnessCall, 0, len(rows))
	for _, row := range rows {
		result = append(result, callFromPO(row))
	}
	return result, nil
}

func (store *Store) ListInteractions(ctx context.Context, invocationID string) ([]api.Interaction, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []interactionPO
	if err := store.db.WithContext(ctx).Where("invocation_id = ?", invocationID).
		Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]api.Interaction, 0, len(rows))
	for _, row := range rows {
		result = append(result, interactionFromPO(row))
	}
	return result, nil
}

func (store *Store) GetMessage(ctx context.Context, id string) (api.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row messagePO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return api.Message{}, mapDBError(err)
	}
	return messageFromPO(row), nil
}

func appendEvent(tx *gorm.DB, invocationID, callID, kind string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return tx.Create(&invocationEventPO{
		InvocationID: invocationID,
		CallID:       callID,
		Kind:         kind,
		Data:         encoded,
		CreatedAt:    time.Now().UTC(),
	}).Error
}

func hashPromptRequest(request api.PromptRequest) (string, []byte, error) {
	tools, err := json.Marshal(request.Tools)
	if err != nil {
		return "", nil, err
	}
	return hashJSON(struct {
		Target string          `json:"target"`
		Prompt string          `json:"prompt"`
		Tools  json.RawMessage `json:"tools"`
	}{request.Target, strings.TrimSpace(request.Prompt), tools}), tools, nil
}

func harnessTextDelta(data json.RawMessage) string {
	var payload struct {
		Content string `json:"content"`
		Text    string `json:"text"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	if payload.Content != "" {
		return payload.Content
	}
	return payload.Text
}

func hashInteractionRequest(request api.InteractionRequest) (string, []byte, error) {
	options, err := json.Marshal(request.Options)
	if err != nil {
		return "", nil, err
	}
	return hashJSON(struct {
		Requester api.ResponderRef    `json:"requester"`
		Kind      api.InteractionKind `json:"kind"`
		Title     string              `json:"title"`
		Prompt    string              `json:"prompt"`
		Options   json.RawMessage     `json:"options"`
		ExpiresAt *time.Time          `json:"expires_at"`
	}{request.Requester, request.Kind, request.Title, strings.TrimSpace(request.Prompt), options, request.ExpiresAt}), options, nil
}

func hashJSON(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func newID(prefix string) string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		panic(fmt.Sprintf("read cryptographic randomness: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(random[:])
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	if limit > maxPageSize {
		return maxPageSize
	}
	return limit
}

func mapDBError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func conversationFromPO(row conversationPO) api.Conversation {
	return api.Conversation{ID: row.ID, Title: row.Title, Timestamped: api.Timestamped{
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}}
}

func messageFromPO(row messagePO) api.Message {
	return api.Message{
		ID: row.ID, ConversationID: row.ConversationID, Sequence: row.Sequence,
		Role: api.Role(row.Role), AuthorID: row.AuthorID, Content: row.Content, CreatedAt: row.CreatedAt,
	}
}

func invocationFromPO(row invocationPO) api.Invocation {
	result := api.Invocation{
		ID: row.ID, ConversationID: row.ConversationID, InputMessageID: row.InputMessageID,
		OutputMessageID:   row.OutputMessageID,
		Responder:         api.ResponderRef{Role: api.Role(row.ResponderRole), ID: row.ResponderID},
		ContextThroughSeq: row.ContextThroughSeq, Phase: api.InvocationPhase(row.Phase), Error: row.Error,
		Timestamped: api.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
	if row.ResourceUID != "" {
		result.Resource = &api.ResourceRef{
			APIVersion: row.ResourceAPIVersion, Kind: row.ResourceKind, Namespace: row.ResourceNamespace,
			Name: row.ResourceName, UID: row.ResourceUID,
		}
	}
	return result
}

func activityFromPO(row activityPO) api.Activity {
	return api.Activity{
		ID: row.ID, InvocationID: row.InvocationID, Key: row.Key, ParentID: row.ParentID,
		Actor: api.ResponderRef{Role: api.Role(row.ActorRole), ID: row.ActorID},
		Kind:  row.Kind, Title: row.Title, Detail: row.Detail, Phase: api.ActivityPhase(row.Phase),
		Timestamped: api.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
}

func callFromPO(row harnessCallPO) api.HarnessCall {
	return api.HarnessCall{
		ID: row.ID, InvocationID: row.InvocationID, OwnerUID: row.OwnerUID, EffectKey: row.EffectKey,
		Target: row.Target, Phase: api.CallPhase(row.Phase), ExternalRef: row.ExternalRef,
		ProviderCursor: row.ProviderCursor, LastEventCursor: row.LastEventCursor,
		StreamText: row.StreamText, Result: row.Result, Error: row.Error,
		LastActivityAt: row.LastActivityAt,
		Timestamped:    api.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
}

func interactionFromPO(row interactionPO) api.Interaction {
	var options []api.InteractionOption
	_ = json.Unmarshal(row.OptionsJSON, &options)
	return api.Interaction{
		ID: row.ID, InvocationID: row.InvocationID, OwnerUID: row.OwnerUID, EffectKey: row.EffectKey,
		Requester: api.ResponderRef{Role: api.Role(row.RequesterRole), ID: row.RequesterID},
		Kind:      api.InteractionKind(row.Kind), Title: row.Title, Prompt: row.Prompt, Options: options,
		Phase: api.InteractionPhase(row.Phase), Answer: row.Answer, ExpiresAt: row.ExpiresAt,
		ResolvedAt:  row.ResolvedAt,
		Timestamped: api.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
}
