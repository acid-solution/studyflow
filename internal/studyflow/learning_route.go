package studyflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"studyflow/internal/model"
)

type RoutePlanInput struct {
	Role string              `json:"role"`
	Plan ImportPlanTreeInput `json:"plan"`
}
type LearningRouteInput struct {
	SourceRouteID      string           `json:"source_route_id"`
	SourceVersion      uint64           `json:"source_version"`
	GoalTitle          string           `json:"goal_title"`
	GoalDescription    string           `json:"goal_description"`
	WeeklyTotalMinutes uint             `json:"weekly_total_minutes"`
	Plans              []RoutePlanInput `json:"plans"`
}
type LearningRouteArgs struct {
	IdempotencyKey string             `json:"idempotency_key"`
	Payload        LearningRouteInput `json:"payload"`
}
type RoutePlanRef struct {
	Role                  string `json:"role"`
	PlanID                string `json:"plan_id"`
	PlanVersionID         string `json:"plan_version_id"`
	VersionNo             uint   `json:"version_no"`
	ContentDigest         string `json:"content_digest"`
	WeeklyCapacityMinutes uint   `json:"weekly_capacity_minutes"`
}
type LearningRouteReceipt struct {
	ImportID      string         `json:"import_id"`
	SourceRouteID string         `json:"source_route_id"`
	SourceVersion uint64         `json:"source_version"`
	ContentDigest string         `json:"content_digest"`
	GoalID        string         `json:"goal_id"`
	Plans         []RoutePlanRef `json:"plans"`
	Replayed      bool           `json:"replayed"`
}

// Sorted-key JSON is the cross-service digest contract; it hashes the received
// immutable payload, before any storage normalization, with no transport key.
func LearningRouteDigest(payload LearningRouteInput) (string, error) { return routeDigest(payload) }
func routeDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var document any
	if err = json.Unmarshal(raw, &document); err != nil {
		return "", err
	}
	raw, err = json.Marshal(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) GetLearningRouteImport(ctx context.Context, user, key string) (*LearningRouteReceipt, error) {
	if err := validateImportKey(key); err != nil {
		return nil, err
	}
	row, err := s.repo.LearningRouteImport(ctx, user, key)
	if err != nil {
		return nil, err
	}
	var result LearningRouteReceipt
	if err = json.Unmarshal([]byte(row.Receipt), &result); err != nil {
		return nil, err
	}
	result.Replayed = true
	return &result, nil
}

func (s *Service) ImportLearningRoute(ctx context.Context, user, key string, input LearningRouteInput) (*LearningRouteReceipt, error) {
	if err := validateImportKey(key); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(input.SourceRouteID); err != nil || input.SourceVersion == 0 || len(input.Plans) < 1 || len(input.Plans) > 2 || strings.TrimSpace(input.GoalTitle) == "" || len(input.GoalTitle) > 160 || len(input.GoalDescription) > 2000 || input.WeeklyTotalMinutes == 0 || input.WeeklyTotalMinutes > 100000 {
		return nil, ErrValidation
	}
	digest, err := LearningRouteDigest(input)
	if err != nil {
		return nil, err
	}
	normalized := make([]RoutePlanInput, len(input.Plans))
	seen := map[string]bool{}
	for i, p := range input.Plans {
		if (p.Role != "project" && p.Role != "knowledge") || seen[p.Role] || p.Plan.GoalID != "" {
			return nil, ErrValidation
		}
		seen[p.Role] = true
		p.Plan, err = normalizeImportInput(p.Plan)
		if err != nil {
			return nil, err
		}
		if err = validateImportBounds(p.Plan); err != nil {
			return nil, err
		}
		total := len(p.Plan.Tasks)
		for _, m := range p.Plan.Milestones {
			total += len(m.Tasks)
		}
		if total == 0 || p.Plan.WeeklyCapacityMinutes == 0 {
			return nil, ErrValidation
		}
		normalized[i] = p
	}
	// Read prior receipt before conflict checks so retries still work after activation.
	if previous, err := s.GetLearningRouteImport(ctx, user, key); err == nil {
		if previous.ContentDigest != digest {
			return nil, ErrIdempotencyConflict
		}
		return previous, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var result LearningRouteReceipt
	err = s.repo.WithinTransaction(ctx, func(repo Repository) error {
		binding, err := repo.LockLearningRoute(ctx, user, input.SourceRouteID)
		if err != nil {
			return err
		}
		// Recheck after lock: a concurrent identical import may just have committed.
		if row, e := repo.LearningRouteImport(ctx, user, key); e == nil {
			if row.RequestDigest != digest {
				return ErrIdempotencyConflict
			}
			if e = json.Unmarshal([]byte(row.Receipt), &result); e != nil {
				return e
			}
			result.Replayed = true
			return nil
		} else if !errors.Is(e, ErrNotFound) {
			return e
		}
		if input.SourceVersion <= binding.LastSourceVersion {
			return ErrVersionConflict
		}
		refs := map[string]RoutePlanRef{}
		if err = json.Unmarshal([]byte(binding.PlanRefs), &refs); err != nil {
			return err
		}
		if binding.GoalID == nil {
			if len(normalized) != 2 {
				return fmt.Errorf("%w: first import requires both plans", ErrValidation)
			}
			goal := model.Goal{ID: uuid.NewString(), UserID: user, Title: strings.TrimSpace(input.GoalTitle), Description: strings.TrimSpace(input.GoalDescription), SuccessCriteria: "完成项目实践与知识准备计划", Status: model.GoalStatusActive, Version: 1}
			if err = repo.CreateGoal(ctx, &goal); err != nil {
				return err
			}
			binding.GoalID = &goal.ID
		}
		goal, err := repo.Goal(ctx, user, *binding.GoalID, true)
		if err != nil {
			return err
		}
		if goal.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		// Use stable role order when locking plans to avoid crossed-import deadlocks.
		for _, role := range []string{"project", "knowledge"} {
			for _, entry := range normalized {
				if entry.Role != role {
					continue
				}
				p := entry.Plan
				planHash, err := routeDigest(p)
				if err != nil {
					return err
				}
				prior, exists := refs[role]
				if exists && prior.ContentDigest == planHash {
					continue
				}
				var plan *model.Plan
				versionNo := uint(1)
				if exists {
					plan, err = repo.Plan(ctx, user, prior.PlanID, true)
					if err != nil {
						return err
					}
					if plan.Status != model.GoalStatusActive || plan.GoalID != *binding.GoalID {
						return ErrInvalidState
					}
					count, err := repo.CountPlanVersions(ctx, user, plan.ID, model.PlanVersionDraft)
					if err != nil {
						return err
					}
					if count > 0 {
						return fmt.Errorf("%w: existing draft must be activated or canceled", ErrConflict)
					}
					max, err := repo.MaxPlanVersionNumber(ctx, user, plan.ID)
					if err != nil {
						return err
					}
					versionNo = max + 1
				} else {
					plan = &model.Plan{ID: uuid.NewString(), UserID: user, GoalID: *binding.GoalID, Title: p.Title, Description: p.Description, Objective: p.Objective, SuccessCriteria: p.SuccessCriteria, TargetDate: parseCanonicalDate(p.TargetDate), Status: model.GoalStatusActive, Revision: 1, Source: model.SourceAgent}
					if err = repo.CreatePlan(ctx, plan); err != nil {
						return err
					}
				}
				v := model.PlanVersion{ID: uuid.NewString(), UserID: user, PlanID: plan.ID, VersionNo: versionNo, Status: model.PlanVersionDraft, Mode: p.Mode, WeeklyCapacityMinutes: p.WeeklyCapacityMinutes, StartDate: parseCanonicalDate(p.StartDate), EndDate: parseCanonicalDate(p.EndDate), StructureRevision: 1}
				if err = repo.CreatePlanVersion(ctx, &v); err != nil {
					return err
				}
				for i, group := range p.Milestones {
					mi, e := normalizeMilestoneInput(CreateMilestoneInput{Title: group.Title, Outcome: group.Outcome})
					if e != nil {
						return e
					}
					m, e := insertMilestone(ctx, repo, user, v.ID, mi, uint(i+1))
					if e != nil {
						return e
					}
					for j, task := range group.Tasks {
						if e = insertImportedTask(ctx, repo, user, v.ID, &m.ID, p.Mode, task, uint(j+1)); e != nil {
							return e
						}
					}
				}
				for j, task := range p.Tasks {
					if err = insertImportedTask(ctx, repo, user, v.ID, nil, p.Mode, task, uint(j+1)); err != nil {
						return err
					}
				}
				refs[role] = RoutePlanRef{Role: role, PlanID: plan.ID, PlanVersionID: v.ID, VersionNo: versionNo, ContentDigest: planHash, WeeklyCapacityMinutes: p.WeeklyCapacityMinutes}
			}
		}
		if uint64(refs["project"].WeeklyCapacityMinutes)+uint64(refs["knowledge"].WeeklyCapacityMinutes) > uint64(input.WeeklyTotalMinutes) {
			return fmt.Errorf("%w: combined weekly budget exceeded", ErrValidation)
		}
		result = LearningRouteReceipt{ImportID: uuid.NewString(), SourceRouteID: input.SourceRouteID, SourceVersion: input.SourceVersion, ContentDigest: digest, GoalID: *binding.GoalID, Plans: []RoutePlanRef{refs["project"], refs["knowledge"]}}
		binding.LastSourceVersion = input.SourceVersion
		raw, _ := json.Marshal(refs)
		binding.PlanRefs = string(raw)
		if err = repo.SaveLearningRoute(ctx, binding); err != nil {
			return err
		}
		raw, err = json.Marshal(result)
		if err != nil {
			return err
		}
		return repo.CreateLearningRouteImport(ctx, &model.LearningRouteImport{ID: result.ImportID, UserID: user, IdempotencyKey: key, SourceRouteID: input.SourceRouteID, SourceVersion: input.SourceVersion, RequestDigest: digest, Receipt: string(raw), CreatedAt: s.now().UTC()})
	})
	if errors.Is(err, errImportReplay) {
		previous, e := s.GetLearningRouteImport(ctx, user, key)
		if e != nil {
			return nil, e
		}
		if previous.ContentDigest != digest {
			return nil, ErrIdempotencyConflict
		}
		return previous, nil
	}
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, user)
	return &result, nil
}
