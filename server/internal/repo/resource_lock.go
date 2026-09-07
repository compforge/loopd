package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/compforge/loopd/server/internal/lock"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) Lock(ctx context.Context, resource string, ttl time.Duration) (lock.Token, error) {
	if resource == "" || ttl < 3*time.Millisecond {
		return lock.Token{}, fmt.Errorf("resource and lease TTL >= 3ms required")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	now := time.Now().UTC()
	token := lock.Token{Resource: resource, LockerID: uuid.V7(), TTL: ttl}
	q := s.db.WithContext(ctx).Model(&model.ResourceLock{}).Where("resource = ? AND expires_at <= ?", resource, now).Updates(map[string]any{"locker_id": token.LockerID, "expires_at": now.Add(ttl), "updated_at": now})
	if q.Error != nil {
		return lock.Token{}, q.Error
	}
	if q.RowsAffected == 1 {
		return token, nil
	}
	row := model.ResourceLock{Resource: resource, LockerID: token.LockerID, ExpiresAt: now.Add(ttl)}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return lock.Token{}, err
	}
	var n int64
	if err := s.db.WithContext(ctx).Model(&row).Where("resource = ? AND locker_id = ?", resource, token.LockerID).Count(&n).Error; err != nil {
		return lock.Token{}, err
	}
	if n != 1 {
		return lock.Token{}, lock.ErrLocked
	}
	return token, nil
}
func (s *Store) Renew(ctx context.Context, t lock.Token) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	now := time.Now().UTC()
	q := s.db.WithContext(ctx).Model(&model.ResourceLock{}).Where("resource = ? AND locker_id = ? AND expires_at > ?", t.Resource, t.LockerID, now).Updates(map[string]any{"expires_at": now.Add(t.TTL), "updated_at": now})
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected != 1 {
		return lock.ErrLost
	}
	return nil
}
func (s *Store) Unlock(ctx context.Context, t lock.Token) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return s.db.WithContext(ctx).Where("resource = ? AND locker_id = ?", t.Resource, t.LockerID).Delete(&model.ResourceLock{}).Error
}

// fence obtains a database write lock before touching protected rows. Takeover
// and renew use the same row, so validation and the subsequent write are atomic.
func fence(tx *gorm.DB, t lock.Token) error {
	now := time.Now().UTC()
	q := tx.Model(&model.ResourceLock{}).Where("resource = ? AND locker_id = ? AND expires_at > ?", t.Resource, t.LockerID, now).UpdateColumn("updated_at", now)
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected == 0 {
		var n int64
		if err := tx.Model(&model.ResourceLock{}).Where("resource = ? AND locker_id = ? AND expires_at > ?", t.Resource, t.LockerID, now).Count(&n).Error; err != nil {
			return err
		}
		if n != 1 {
			return lock.ErrLost
		}
	}
	return nil
}
