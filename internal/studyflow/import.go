package studyflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"studyflow/internal/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Bounds for one import. They are checked before the idempotency key is claimed
// so that an oversized request cannot burn a key, and they keep the single
// transaction small. The title limits mirror the column widths; the minute and
// text limits keep out-of-range values from reaching MySQL, where strict mode
// would turn them into 1264/1406 and surface as a 500.
const (
	importMinKeyLength      = 8
	importMaxKeyLength      = 200
	importMaxMilestones     = 50
	importMaxTasks          = 200
	importMaxPlanTitle      = 160
	importMaxMilestoneTitle = 160
	importMaxTaskTitle      = 255
	importMaxText           = 2000
	importMaxMinutes        = 100000
)

// errImportReplay reports that the idempotency key was already claimed. It is
// handled inside ImportPlanTree and never escapes to a caller.
var errImportReplay = errors.New("plan import replayed")

// ImportPlanTreeInput is the payload of a batch import. Tasks are nested under
// the milestone they belong to, which makes "the task belongs to a milestone of
// this version" a structural property instead of a lookup, and lets the server
// assign every position from array order — so no client input can collide with
// the per-version position constraints.
type ImportPlanTreeInput struct {
	GoalID                string                 `json:"goal_id"`
	Title                 string                 `json:"title"`
	Description           string                 `json:"description"`
	Mode                  string                 `json:"mode"`
	WeeklyCapacityMinutes uint                   `json:"weekly_capacity_minutes"`
	StartDate             *string                `json:"start_date"`
	EndDate               *string                `json:"end_date"`
	Milestones            []ImportMilestoneInput `json:"milestones"`
	Tasks                 []ImportTaskInput      `json:"tasks"`
}

// These types are input-only, so omitempty changes nothing for the REST API
// (Go ignores it when decoding) but does tell the MCP schema which fields are
// genuinely optional.
type ImportMilestoneInput struct {
	Title   string            `json:"title" jsonschema:"阶段名称"`
	Outcome string            `json:"outcome,omitempty" jsonschema:"这个阶段要达成什么"`
	Tasks   []ImportTaskInput `json:"tasks,omitempty" jsonschema:"属于这个阶段的任务，顺序即任务顺序"`
}

type ImportTaskInput struct {
	Title           string  `json:"title" jsonschema:"任务名称"`
	Description     string  `json:"description,omitempty" jsonschema:"任务说明"`
	EstimateMinutes uint    `json:"estimate_minutes,omitempty" jsonschema:"预计投入分钟数"`
	ScheduledDate   *string `json:"scheduled_date,omitempty" jsonschema:"计划日期 YYYY-MM-DD；留空表示暂不安排日期。按课时的计划不能填"`
}

type ImportPlanTreeResult struct {
	ImportID      string      `json:"import_id"`
	Replayed      bool        `json:"replayed"`
	PlanID        string      `json:"plan_id"`
	PlanVersionID string      `json:"plan_version_id"`
	Plan          *PlanDetail `json:"plan"`
}

// The digest covers only the fields that determine the persisted tree, listed
// explicitly rather than by hashing the request DTO. Adding a field to a DTO
// later therefore cannot silently change what counts as "the same request".
type importDigest struct {
	GoalID                string              `json:"goal_id"`
	Title                 string              `json:"title"`
	Description           string              `json:"description"`
	Mode                  string              `json:"mode"`
	WeeklyCapacityMinutes uint                `json:"weekly_capacity_minutes"`
	StartDate             *string             `json:"start_date"`
	EndDate               *string             `json:"end_date"`
	Milestones            []importDigestGroup `json:"milestones"`
	Tasks                 []importDigestItem  `json:"tasks"`
}

type importDigestGroup struct {
	Title   string             `json:"title"`
	Outcome string             `json:"outcome"`
	Tasks   []importDigestItem `json:"tasks"`
}

type importDigestItem struct {
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	EstimateMinutes uint    `json:"estimate_minutes"`
	ScheduledDate   *string `json:"scheduled_date"`
}

// ImportPlanTree creates a whole plan tree in one transaction, guarded by a
// user-scoped idempotency key.
//
// Repeating a request with the same key and the same payload returns the plan
// created the first time instead of creating a second one. Reusing a key with a
// different payload is rejected. Any validation failure rolls the transaction
// back, which also releases the key.
func (s *Service) ImportPlanTree(ctx context.Context, userID, idempotencyKey string, input ImportPlanTreeInput) (*ImportPlanTreeResult, error) {
	key := strings.TrimSpace(idempotencyKey)
	if err := validateImportKey(key); err != nil {
		return nil, err
	}
	input, err := normalizeImportInput(input)
	if err != nil {
		return nil, err
	}
	if err := validateImportBounds(input); err != nil {
		return nil, err
	}
	digest, err := digestImport(input)
	if err != nil {
		return nil, err
	}

	importID := uuid.NewString()
	plan := model.Plan{ID: uuid.NewString(), UserID: userID, GoalID: input.GoalID, Title: input.Title, Description: input.Description, Mode: input.Mode}
	version := model.PlanVersion{ID: uuid.NewString(), UserID: userID, PlanID: plan.ID, VersionNo: 1, Status: model.PlanVersionDraft, WeeklyCapacityMinutes: input.WeeklyCapacityMinutes, StartDate: parseCanonicalDate(input.StartDate), EndDate: parseCanonicalDate(input.EndDate), StructureRevision: 1}
	receipt := model.PlanImport{ID: importID, UserID: userID, IdempotencyKey: key, RequestDigest: digest, PlanID: plan.ID, PlanVersionID: version.ID, CreatedAt: s.now().UTC()}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.insertPlanWithVersion(ctx, tx, userID, input.GoalID, plan, version); err != nil {
			return err
		}
		// Claim the key before any bulk write. The unique index does the work, so
		// concurrent duplicates queue on this one row lock instead of racing
		// through a check-then-insert. The receipt has to come after the plan
		// because it carries foreign keys to it, but it still precedes every
		// milestone and task, and because it commits and rolls back together with
		// the tree a claimed key always has an effect behind it.
		if err := tx.Create(&receipt).Error; err != nil {
			if isDuplicate(err) {
				return errImportReplay
			}
			return err
		}
		for index, group := range input.Milestones {
			milestoneInput, err := normalizeMilestoneInput(CreateMilestoneInput{Title: group.Title, Outcome: group.Outcome})
			if err != nil {
				return err
			}
			milestone, err := insertMilestone(tx, userID, version.ID, milestoneInput, uint(index+1))
			if err != nil {
				return err
			}
			for position, task := range group.Tasks {
				if err := insertImportedTask(tx, userID, version.ID, &milestone.ID, input.Mode, task, uint(position+1)); err != nil {
					return err
				}
			}
		}
		for position, task := range input.Tasks {
			if err := insertImportedTask(tx, userID, version.ID, nil, input.Mode, task, uint(position+1)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errImportReplay) {
			return s.replayImport(ctx, userID, key, digest)
		}
		return nil, mapNotFound(err)
	}
	detail, err := s.GetPlan(ctx, userID, plan.ID)
	if err != nil {
		return nil, err
	}
	return &ImportPlanTreeResult{ImportID: importID, PlanID: plan.ID, PlanVersionID: version.ID, Plan: detail}, nil
}

// replayImport answers a request whose key was already claimed. The receipt is
// read outside the aborted transaction on purpose: under REPEATABLE READ a read
// inside it would not see the row the winning request committed.
func (s *Service) replayImport(ctx context.Context, userID, key, digest string) (*ImportPlanTreeResult, error) {
	var receipt model.PlanImport
	if err := s.db.WithContext(ctx).Where("user_id = ? AND idempotency_key = ?", userID, key).First(&receipt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrConflict
		}
		return nil, err
	}
	if receipt.RequestDigest != digest {
		return nil, ErrIdempotencyConflict
	}
	detail, err := s.GetPlan(ctx, userID, receipt.PlanID)
	if err != nil {
		return nil, err
	}
	return &ImportPlanTreeResult{ImportID: receipt.ID, Replayed: true, PlanID: receipt.PlanID, PlanVersionID: receipt.PlanVersionID, Plan: detail}, nil
}

// insertImportedTask validates one item and writes it. Validation stays per item
// so that a failure part-way through the batch rolls back everything already
// written, which is the guarantee the import promises.
func insertImportedTask(tx *gorm.DB, userID, versionID string, milestoneID *string, mode string, task ImportTaskInput, position uint) error {
	input, date, err := normalizeTaskInput(CreateTaskInput{MilestoneID: milestoneID, Title: task.Title, Description: task.Description, EstimateMinutes: task.EstimateMinutes, ScheduledDate: task.ScheduledDate})
	if err != nil {
		return err
	}
	if err := validateTaskSchedule(mode, date); err != nil {
		return err
	}
	_, err = insertTask(tx, userID, versionID, input, date, position)
	return err
}

// normalizeImportInput validates the plan-level fields and rewrites every string
// into its canonical form. The canonical form is what the digest hashes, so a
// retry that differs only in surrounding whitespace, or that sends an empty
// string where the first attempt sent nothing, still hashes to the same digest.
// Dates are stored back in the single layout parseDate accepts, so the digest
// covers the value that is actually persisted.
func normalizeImportInput(input ImportPlanTreeInput) (ImportPlanTreeInput, error) {
	planInput, startDate, endDate, err := normalizePlanInput(CreatePlanInput{
		Title:                 input.Title,
		Description:           input.Description,
		Mode:                  input.Mode,
		WeeklyCapacityMinutes: input.WeeklyCapacityMinutes,
		StartDate:             input.StartDate,
		EndDate:               input.EndDate,
	})
	if err != nil {
		return input, err
	}
	input.Title = planInput.Title
	input.Description = planInput.Description
	input.Mode = planInput.Mode
	input.WeeklyCapacityMinutes = planInput.WeeklyCapacityMinutes
	input.StartDate = formatDatePtr(startDate)
	input.EndDate = formatDatePtr(endDate)

	for index := range input.Milestones {
		input.Milestones[index].Title = strings.TrimSpace(input.Milestones[index].Title)
		input.Milestones[index].Outcome = strings.TrimSpace(input.Milestones[index].Outcome)
		for position := range input.Milestones[index].Tasks {
			task, err := normalizeImportTask(input.Milestones[index].Tasks[position])
			if err != nil {
				return input, err
			}
			input.Milestones[index].Tasks[position] = task
		}
	}
	for index := range input.Tasks {
		task, err := normalizeImportTask(input.Tasks[index])
		if err != nil {
			return input, err
		}
		input.Tasks[index] = task
	}
	return input, nil
}

func normalizeImportTask(task ImportTaskInput) (ImportTaskInput, error) {
	task.Title = strings.TrimSpace(task.Title)
	task.Description = strings.TrimSpace(task.Description)
	date, err := parseOptionalDate(task.ScheduledDate)
	if err != nil {
		return task, err
	}
	task.ScheduledDate = formatDatePtr(date)
	return task, nil
}

func validateImportKey(key string) error {
	if len(key) < importMinKeyLength || len(key) > importMaxKeyLength {
		return fmt.Errorf("%w: idempotency key must be %d to %d characters", ErrValidation, importMinKeyLength, importMaxKeyLength)
	}
	for index := 0; index < len(key); index++ {
		if key[index] < 0x21 || key[index] > 0x7E {
			return fmt.Errorf("%w: idempotency key must be printable ascii", ErrValidation)
		}
	}
	return nil
}

func validateImportBounds(input ImportPlanTreeInput) error {
	if len(input.Milestones) > importMaxMilestones {
		return fmt.Errorf("%w: at most %d milestones per import", ErrValidation, importMaxMilestones)
	}
	total := len(input.Tasks)
	for _, group := range input.Milestones {
		total += len(group.Tasks)
	}
	if total > importMaxTasks {
		return fmt.Errorf("%w: at most %d tasks per import", ErrValidation, importMaxTasks)
	}
	if len(input.Title) > importMaxPlanTitle {
		return fmt.Errorf("%w: plan title too long", ErrValidation)
	}
	if len(input.Description) > importMaxText {
		return fmt.Errorf("%w: plan description too long", ErrValidation)
	}
	if input.WeeklyCapacityMinutes > importMaxMinutes {
		return fmt.Errorf("%w: weekly capacity out of range", ErrValidation)
	}
	for _, group := range input.Milestones {
		if len(group.Title) > importMaxMilestoneTitle {
			return fmt.Errorf("%w: milestone title too long", ErrValidation)
		}
		if len(group.Outcome) > importMaxText {
			return fmt.Errorf("%w: milestone outcome too long", ErrValidation)
		}
		for _, task := range group.Tasks {
			if err := validateImportTaskBounds(task); err != nil {
				return err
			}
		}
	}
	for _, task := range input.Tasks {
		if err := validateImportTaskBounds(task); err != nil {
			return err
		}
	}
	return nil
}

func validateImportTaskBounds(task ImportTaskInput) error {
	if len(task.Title) > importMaxTaskTitle {
		return fmt.Errorf("%w: task title too long", ErrValidation)
	}
	if len(task.Description) > importMaxText {
		return fmt.Errorf("%w: task description too long", ErrValidation)
	}
	if task.EstimateMinutes > importMaxMinutes {
		return fmt.Errorf("%w: task estimate out of range", ErrValidation)
	}
	return nil
}

// digestImport hashes the canonical payload. Go marshals struct fields in
// declaration order, so the encoding is stable across processes. The idempotency
// key is deliberately not part of it: the key is the lookup, not the effect.
func digestImport(input ImportPlanTreeInput) (string, error) {
	canonical := importDigest{
		GoalID:                input.GoalID,
		Title:                 input.Title,
		Description:           input.Description,
		Mode:                  input.Mode,
		WeeklyCapacityMinutes: input.WeeklyCapacityMinutes,
		StartDate:             input.StartDate,
		EndDate:               input.EndDate,
		Milestones:            make([]importDigestGroup, 0, len(input.Milestones)),
		Tasks:                 make([]importDigestItem, 0, len(input.Tasks)),
	}
	for _, group := range input.Milestones {
		entry := importDigestGroup{Title: group.Title, Outcome: group.Outcome, Tasks: make([]importDigestItem, 0, len(group.Tasks))}
		for _, task := range group.Tasks {
			entry.Tasks = append(entry.Tasks, digestItem(task))
		}
		canonical.Milestones = append(canonical.Milestones, entry)
	}
	for _, task := range input.Tasks {
		canonical.Tasks = append(canonical.Tasks, digestItem(task))
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func digestItem(task ImportTaskInput) importDigestItem {
	return importDigestItem{Title: task.Title, Description: task.Description, EstimateMinutes: task.EstimateMinutes, ScheduledDate: task.ScheduledDate}
}

// parseCanonicalDate turns an already-normalized date string back into a value.
// The input has been through normalizeImportInput, so a parse error here is not
// reachable.
func parseCanonicalDate(value *string) *time.Time {
	date, err := parseOptionalDate(value)
	if err != nil {
		return nil
	}
	return date
}
