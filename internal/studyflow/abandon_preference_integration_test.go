package studyflow

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"studyflow/internal/model"
)

func TestAbandonPreferenceIntegration(t *testing.T) {
	db, repo, user := notificationDB(t)
	service := NewServiceWithRepository(repo, nil)
	ctx := t.Context()
	initial, err := service.GetPreferences(ctx, user)
	if err != nil || initial.DefaultAbandonParentEffect != model.AbandonPreferenceAsk {
		t.Fatalf("first use must require an explicit choice: %+v %v", initial, err)
	}
	other := uuid.NewString()
	t.Cleanup(func() { cleanupUser(t, db, other) })
	for _, choice := range []string{model.AbandonPreferenceExclude, model.AbandonPreferenceBlock, model.AbandonPreferenceAsk} {
		value, err := service.UpdatePreferences(ctx, user, UpdatePreferenceInput{DefaultAbandonParentEffect: &choice})
		if err != nil || value.DefaultAbandonParentEffect != choice {
			t.Fatalf("save preference %q: %+v %v", choice, value, err)
		}
		// Use a new service instance to verify this is an account setting rather
		// than a process-local flag. Other users keep their own default.
		fresh := NewService(db)
		stored, err := fresh.GetPreferences(ctx, user)
		if err != nil || stored.DefaultAbandonParentEffect != choice || stored.Timezone != initial.Timezone || stored.DefaultPlanMode != initial.DefaultPlanMode {
			t.Fatalf("preference was not preserved independently: %+v %v", stored, err)
		}
		isolated, err := fresh.GetPreferences(ctx, other)
		if err != nil || isolated.DefaultAbandonParentEffect != model.AbandonPreferenceAsk {
			t.Fatalf("preference leaked to another user: %+v %v", isolated, err)
		}
	}
	head, err := repo.NotificationHead(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	invalid := "skip"
	if _, err := service.UpdatePreferences(ctx, user, UpdatePreferenceInput{DefaultAbandonParentEffect: &invalid}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown default accepted: %v", err)
	}
	after, err := repo.NotificationHead(ctx, user)
	if err != nil || after.LastSequence != head.LastSequence {
		t.Fatalf("invalid preference generated an event: %+v %v", after, err)
	}
	stored, _ := service.GetPreferences(ctx, user)
	if stored.DefaultAbandonParentEffect != model.AbandonPreferenceAsk {
		t.Fatal("invalid preference changed the saved default")
	}
}
