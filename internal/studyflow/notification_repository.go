package studyflow

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"studyflow/internal/model"
)

// The per-user row stays locked until commit, so a later sequence cannot become
// visible before an earlier one. Rolled-back transactions also roll back it.
func (r *gormRepository) AppendNotification(ctx context.Context, userID, kind string, resources []NotificationResource, topics []string, now time.Time) error {
	if resources == nil {
		resources = []NotificationResource{}
	}
	if topics == nil {
		topics = []string{}
	}
	stream := model.NotificationStream{UserID: userID}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&stream).Error; err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&stream).Error; err != nil {
		return err
	}
	stream.LastSequence++
	if err := r.db.WithContext(ctx).Model(&stream).Update("last_sequence", stream.LastSequence).Error; err != nil {
		return err
	}
	resourceJSON, err := json.Marshal(resources)
	if err != nil {
		return err
	}
	topicJSON, err := json.Marshal(topics)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(&model.NotificationOutbox{UserID: userID, Sequence: stream.LastSequence, Kind: kind, Resources: string(resourceJSON), Topics: string(topicJSON), OccurredAt: now.UTC()}).Error
}

func (r *gormRepository) NotificationHead(ctx context.Context, userID string) (model.NotificationStream, error) {
	stream := model.NotificationStream{UserID: userID}
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Limit(1).Find(&stream).Error
	return stream, err
}

func (r *gormRepository) NotificationsAfter(ctx context.Context, userID string, after, through uint64, limit int) ([]Notification, error) {
	var rows []model.NotificationOutbox
	err := r.db.WithContext(ctx).Where("user_id = ? AND sequence > ? AND sequence <= ?", userID, after, through).Order("sequence").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]Notification, 0, len(rows))
	for _, row := range rows {
		value := Notification{Sequence: row.Sequence, Kind: row.Kind, OccurredAt: row.OccurredAt}
		if err := json.Unmarshal([]byte(row.Resources), &value.Resources); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(row.Topics), &value.Topics); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// Delete only an expired prefix. A timestamp discontinuity can never create a
// hole in the replay log. Stream and deletion boundary change atomically.
func (r *gormRepository) PruneNotifications(ctx context.Context, before time.Time, limit int) error {
	var users []string
	if err := r.db.WithContext(ctx).Model(&model.NotificationOutbox{}).Where("occurred_at < ?", before).Distinct("user_id").Limit(limit).Pluck("user_id", &users).Error; err != nil {
		return err
	}
	for _, userID := range users {
		if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var stream model.NotificationStream
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&stream).Error; err != nil {
				return err
			}
			var rows []model.NotificationOutbox
			if err := tx.Where("user_id = ?", userID).Order("sequence").Limit(limit).Find(&rows).Error; err != nil {
				return err
			}
			through := stream.PrunedThrough
			for _, row := range rows {
				if !row.OccurredAt.Before(before) {
					break
				}
				through = row.Sequence
			}
			if through == stream.PrunedThrough {
				return nil
			}
			if err := tx.Where("user_id = ? AND sequence <= ?", userID, through).Delete(&model.NotificationOutbox{}).Error; err != nil {
				return err
			}
			return tx.Model(&stream).Update("pruned_through", through).Error
		}); err != nil {
			return err
		}
	}
	return nil
}
