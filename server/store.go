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

	loopd "github.com/compforge/loopd"
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

func (store *Store) CreateConversation(ctx context.Context, title string) (loopd.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	row := conversationPO{ID: newID("conv"), Title: strings.TrimSpace(title)}
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return loopd.Conversation{}, err
	}
	return conversationFromPO(row), nil
}

func (store *Store) GetConversation(ctx context.Context, id string) (loopd.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row conversationPO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return loopd.Conversation{}, mapDBError(err)
	}
	return conversationFromPO(row), nil
}

func (store *Store) ListMessages(ctx context.Context, conversationID string, after, through int64, limit int) ([]loopd.Message, error) {
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
	result := make([]loopd.Message, 0, len(rows))
	for _, row := range rows {
		result = append(result, messageFromPO(row))
	}
	return result, nil
}

func (store *Store) CreateMessageInvocation(
	ctx context.Context,
	conversationID string,
	request createMessageRequest,
) (createMessageResponse, error) {
	if strings.TrimSpace(request.Content) == "" || !request.Responder.Valid() {
		return createMessageResponse{}, ErrInvalid
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
			Role:           string(loopd.RoleUser),
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
			Phase:             string(loopd.InvocationQueued),
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
		return createMessageResponse{}, err
	}
	return createMessageResponse{
		Message:    messageFromPO(message),
		Invocation: invocationFromPO(invocation),
	}, nil
}

func (store *Store) GetInvocation(ctx context.Context, id string) (loopd.Invocation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row invocationPO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return loopd.Invocation{}, mapDBError(err)
	}
	return invocationFromPO(row), nil
}

func (store *Store) GetInvocationContext(ctx context.Context, id string) (loopd.InvocationContext, error) {
	invocation, err := store.GetInvocation(ctx, id)
	if err != nil {
		return loopd.InvocationContext{}, err
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var input messagePO
	if err := store.db.WithContext(ctx).First(&input, "id = ?", invocation.InputMessageID).Error; err != nil {
		return loopd.InvocationContext{}, mapDBError(err)
	}
	history, hasEarlier, err := store.listContextMessages(ctx, invocation.ConversationID, invocation.ContextThroughSeq)
	if err != nil {
		return loopd.InvocationContext{}, err
	}
	var fromSeq int64
	if len(history) > 0 {
		fromSeq = history[0].Sequence
	}
	return loopd.InvocationContext{
		Invocation:     invocation,
		Input:          messageFromPO(input),
		History:        history,
		HistoryFromSeq: fromSeq,
		HasEarlier:     hasEarlier,
	}, nil
}

func (store *Store) listContextMessages(ctx context.Context, conversationID string, through int64) ([]loopd.Message, bool, error) {
	var rows []messagePO
	if err := store.db.WithContext(ctx).Where("conversation_id = ? AND sequence <= ?", conversationID, through).
		Order("sequence DESC").Limit(maxPageSize + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasEarlier := len(rows) > maxPageSize
	if hasEarlier {
		rows = rows[:maxPageSize]
	}
	result := make([]loopd.Message, len(rows))
	for index, row := range rows {
		result[len(rows)-1-index] = messageFromPO(row)
	}
	return result, hasEarlier, nil
}

func (store *Store) ListPendingInvocations(ctx context.Context, operatorID string, limit int) ([]loopd.Invocation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []invocationPO
	err := store.db.WithContext(ctx).
		Where("responder_role = ? AND responder_id = ? AND phase = ?", loopd.RoleOperator, operatorID, loopd.InvocationQueued).
		Order("created_at ASC").
		Limit(normalizeLimit(limit)).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]loopd.Invocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, invocationFromPO(row))
	}
	return result, nil
}

func (store *Store) AcceptInvocation(ctx context.Context, id string, resource loopd.ResourceRef) (loopd.Invocation, error) {
	if resource.APIVersion == "" || resource.Kind == "" || resource.Name == "" || resource.UID == "" {
		return loopd.Invocation{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row invocationPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if loopd.InvocationPhase(row.Phase) != loopd.InvocationQueued {
			if row.ResourceUID == resource.UID {
				return nil
			}
			return ErrConflict
		}
		row.Phase = string(loopd.InvocationRunning)
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

func (store *Store) StartInvocation(ctx context.Context, id string) (loopd.Invocation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row invocationPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if loopd.InvocationPhase(row.Phase) == loopd.InvocationRunning {
			return nil
		}
		if loopd.InvocationPhase(row.Phase) != loopd.InvocationQueued {
			return ErrConflict
		}
		row.Phase = string(loopd.InvocationRunning)
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
	role loopd.Role,
	authorID string,
	content string,
) (loopd.Invocation, error) {
	if (role != loopd.RoleHarness && role != loopd.RoleOperator) || authorID == "" || strings.TrimSpace(content) == "" {
		return loopd.Invocation{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var invocation invocationPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&invocation, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if loopd.InvocationPhase(invocation.Phase) == loopd.InvocationSucceeded {
			return nil
		}
		if loopd.InvocationPhase(invocation.Phase).Terminal() {
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
		invocation.Phase = string(loopd.InvocationSucceeded)
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
		if loopd.InvocationPhase(row.Phase).Terminal() {
			return nil
		}
		row.Phase = string(loopd.InvocationFailed)
		row.Error = reason
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return appendEvent(tx, id, "", "invocation.failed", map[string]any{"error": reason})
	})
}

func (store *Store) UpsertActivity(ctx context.Context, invocationID string, request loopd.ActivityUpdate) (loopd.Activity, error) {
	if request.Key == "" || !request.Actor.Valid() || request.Kind == "" || request.Title == "" || request.Phase == "" {
		return loopd.Activity{}, ErrInvalid
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

func (store *Store) ListInvocationEvents(ctx context.Context, invocationID string, after uint64, limit int) ([]loopd.InvocationEvent, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []invocationEventPO
	if err := store.db.WithContext(ctx).
		Where("invocation_id = ? AND cursor > ?", invocationID, after).
		Order("cursor ASC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]loopd.InvocationEvent, 0, len(rows))
	for _, row := range rows {
		result = append(result, loopd.InvocationEvent{
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

func (store *Store) ListOperatorEvents(ctx context.Context, operatorID string, after uint64, limit int) ([]loopd.OperatorEvent, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []invocationEventPO
	err := store.db.WithContext(ctx).Table("invocation_events").
		Select("invocation_events.*").
		Joins("JOIN invocations ON invocations.id = invocation_events.invocation_id").
		Where("invocations.responder_role = ? AND invocations.responder_id = ? AND invocation_events.cursor > ?",
			loopd.RoleOperator, operatorID, after).
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
	byID := make(map[string]loopd.Invocation, len(invocations))
	for _, invocation := range invocations {
		byID[invocation.ID] = invocationFromPO(invocation)
	}
	result := make([]loopd.OperatorEvent, 0, len(rows))
	for _, row := range rows {
		invocation, exists := byID[row.InvocationID]
		if !exists {
			return nil, fmt.Errorf("%w: Invocation %q", ErrNotFound, row.InvocationID)
		}
		result = append(result, loopd.OperatorEvent{
			Event: loopd.InvocationEvent{
				Cursor: row.Cursor, InvocationID: row.InvocationID, CallID: row.CallID, Kind: row.Kind,
				Data: json.RawMessage(row.Data), CreatedAt: row.CreatedAt,
			},
			Invocation: invocation,
		})
	}
	return result, nil
}

func (store *Store) ListActivities(ctx context.Context, invocationID string) ([]loopd.Activity, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []activityPO
	if err := store.db.WithContext(ctx).Where("invocation_id = ?", invocationID).
		Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]loopd.Activity, 0, len(rows))
	for _, row := range rows {
		result = append(result, activityFromPO(row))
	}
	return result, nil
}

func (store *Store) ListHarnessCalls(ctx context.Context, invocationID string) ([]loopd.HarnessCall, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []harnessCallPO
	if err := store.db.WithContext(ctx).Where("invocation_id = ?", invocationID).
		Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]loopd.HarnessCall, 0, len(rows))
	for _, row := range rows {
		result = append(result, callFromPO(row))
	}
	return result, nil
}

func (store *Store) ListInteractions(ctx context.Context, invocationID string) ([]loopd.Interaction, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []interactionPO
	if err := store.db.WithContext(ctx).Where("invocation_id = ?", invocationID).
		Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]loopd.Interaction, 0, len(rows))
	for _, row := range rows {
		result = append(result, interactionFromPO(row))
	}
	return result, nil
}

func (store *Store) GetMessage(ctx context.Context, id string) (loopd.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row messagePO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return loopd.Message{}, mapDBError(err)
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

func hashPromptRequest(request promptRequest) (string, []byte, error) {
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

func hashInteractionRequest(request interactionRequest) (string, []byte, error) {
	options, err := json.Marshal(request.Options)
	if err != nil {
		return "", nil, err
	}
	return hashJSON(struct {
		Requester loopd.ResponderRef    `json:"requester"`
		Kind      loopd.InteractionKind `json:"kind"`
		Title     string                `json:"title"`
		Prompt    string                `json:"prompt"`
		Options   json.RawMessage       `json:"options"`
		ExpiresAt *time.Time            `json:"expires_at"`
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

func conversationFromPO(row conversationPO) loopd.Conversation {
	return loopd.Conversation{ID: row.ID, Title: row.Title, Timestamped: loopd.Timestamped{
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}}
}

func messageFromPO(row messagePO) loopd.Message {
	return loopd.Message{
		ID: row.ID, ConversationID: row.ConversationID, Sequence: row.Sequence,
		Role: loopd.Role(row.Role), AuthorID: row.AuthorID, Content: row.Content, CreatedAt: row.CreatedAt,
	}
}

func invocationFromPO(row invocationPO) loopd.Invocation {
	result := loopd.Invocation{
		ID: row.ID, ConversationID: row.ConversationID, InputMessageID: row.InputMessageID,
		OutputMessageID:   row.OutputMessageID,
		Responder:         loopd.ResponderRef{Role: loopd.Role(row.ResponderRole), ID: row.ResponderID},
		ContextThroughSeq: row.ContextThroughSeq, Phase: loopd.InvocationPhase(row.Phase), Error: row.Error,
		Timestamped: loopd.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
	if row.ResourceUID != "" {
		result.Resource = &loopd.ResourceRef{
			APIVersion: row.ResourceAPIVersion, Kind: row.ResourceKind, Namespace: row.ResourceNamespace,
			Name: row.ResourceName, UID: row.ResourceUID,
		}
	}
	return result
}

func activityFromPO(row activityPO) loopd.Activity {
	return loopd.Activity{
		ID: row.ID, InvocationID: row.InvocationID, Key: row.Key, ParentID: row.ParentID,
		Actor: loopd.ResponderRef{Role: loopd.Role(row.ActorRole), ID: row.ActorID},
		Kind:  row.Kind, Title: row.Title, Detail: row.Detail, Phase: loopd.ActivityPhase(row.Phase),
		Timestamped: loopd.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
}

func callFromPO(row harnessCallPO) loopd.HarnessCall {
	return loopd.HarnessCall{
		ID: row.ID, InvocationID: row.InvocationID, OwnerUID: row.OwnerUID, EffectKey: row.EffectKey,
		Target: row.Target, Phase: loopd.CallPhase(row.Phase), ExternalRef: row.ExternalRef,
		ProviderCursor: row.ProviderCursor, LastEventCursor: row.LastEventCursor,
		StreamText: row.StreamText, Result: row.Result, Error: row.Error,
		LastActivityAt: row.LastActivityAt,
		Timestamped:    loopd.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
}

func interactionFromPO(row interactionPO) loopd.Interaction {
	var options []loopd.InteractionOption
	_ = json.Unmarshal(row.OptionsJSON, &options)
	return loopd.Interaction{
		ID: row.ID, InvocationID: row.InvocationID, OwnerUID: row.OwnerUID, EffectKey: row.EffectKey,
		Requester: loopd.ResponderRef{Role: loopd.Role(row.RequesterRole), ID: row.RequesterID},
		Kind:      loopd.InteractionKind(row.Kind), Title: row.Title, Prompt: row.Prompt, Options: options,
		Phase: loopd.InteractionPhase(row.Phase), Answer: row.Answer, ExpiresAt: row.ExpiresAt,
		ResolvedAt:  row.ResolvedAt,
		Timestamped: loopd.Timestamped{CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt},
	}
}
