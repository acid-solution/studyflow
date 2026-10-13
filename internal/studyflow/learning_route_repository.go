package studyflow

import (
	"context"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
	"studyflow/internal/model"
)

func (r *gormRepository) LockLearningRoute(ctx context.Context, user, route string) (*model.LearningRouteBinding, error) {
	value := model.LearningRouteBinding{ID: uuid.NewString(), UserID: user, SourceRouteID: route, PlanRefs: "{}"}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&value).Error; err != nil {
		return nil, err
	}
	var found model.LearningRouteBinding
	err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND source_route_id=?", user, route).First(&found).Error
	return &found, err
}
func (r *gormRepository) SaveLearningRoute(ctx context.Context, value *model.LearningRouteBinding) error {
	return r.db.WithContext(ctx).Model(value).Updates(map[string]any{"goal_id": value.GoalID, "last_source_version": value.LastSourceVersion, "plan_refs": value.PlanRefs}).Error
}
func (r *gormRepository) CreateLearningRouteImport(ctx context.Context, value *model.LearningRouteImport) error {
	err := r.db.WithContext(ctx).Create(value).Error
	if repositoryDuplicate(err) {
		return errImportReplay
	}
	return err
}
func (r *gormRepository) LearningRouteImport(ctx context.Context, user, key string) (*model.LearningRouteImport, error) {
	var value model.LearningRouteImport
	err := r.db.WithContext(ctx).Where("user_id=? AND idempotency_key=?", user, key).First(&value).Error
	return &value, repositoryNotFound(err)
}
