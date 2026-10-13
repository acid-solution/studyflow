package studyflow

import (
	"encoding/json"
	"studyflow/internal/model"
	"testing"
)

func TestNormalizeImportPreservesCallerPayload(t *testing.T) {
	input := ImportPlanTreeInput{Title: " plan ", Objective: " objective ", Mode: model.PlanModeSequence, Milestones: []ImportMilestoneInput{{Title: " stage ", Tasks: []ImportTaskInput{{Title: " nested "}}}}, Tasks: []ImportTaskInput{{Title: " loose "}}}
	before, _ := json.Marshal(input)
	normalized, err := normalizeImportInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Milestones[0].Title != "stage" || normalized.Milestones[0].Tasks[0].Title != "nested" {
		t.Fatal("normalization did not trim content")
	}
	normalized.Milestones[0].Tasks[0].Title = "changed"
	normalized.Tasks[0].Title = "changed"
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("normalization mutated caller-owned slices")
	}
}
