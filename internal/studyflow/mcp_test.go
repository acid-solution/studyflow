package studyflow

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"studyflow/internal/database"
	"studyflow/internal/identity"
	"studyflow/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearerTransport adds the access token to every MCP request, the way a real
// client would.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func mcpClient(token string) *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "studyflow-test", Version: "v1"}, nil)
}

func connectMCP(ctx context.Context, endpoint, token string, retries int) (*mcp.ClientSession, error) {
	return mcpClient(token).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: &bearerTransport{token: token, base: http.DefaultTransport}},
		// The server is stateless and these tools never call back into the client,
		// so there is no server-initiated stream to open.
		DisableStandaloneSSE: true,
		MaxRetries:           retries,
	}, nil)
}

func TestMCPToolsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	userID := uuid.NewString()
	defer cleanupUser(t, db, userID)
	ctx := t.Context()

	// A stand-in for shared-auth: a JWKS endpoint plus the matching signer.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "mcp-test-key"
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exponent := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
			"N": base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
			"E": base64.RawURLEncoding.EncodeToString(exponent),
		}}})
	}))
	defer jwksServer.Close()

	sign := func(audience string) string {
		now := time.Now().UTC()
		value := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": "shared-auth",
			"sub": userID,
			"sid": uuid.NewString(),
			"aud": audience,
			"iat": now.Unix(),
			"exp": now.Add(time.Minute).Unix(),
		})
		value.Header["kid"] = kid
		raw, err := value.SignedString(privateKey)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// The real router with the MCP endpoint mounted on it.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	authenticator := identity.NewAuthenticator(jwksServer.URL, "shared-auth", "studyflow", time.Minute)
	RegisterMCP(router, service, authenticator, nil)
	api := httptest.NewServer(router)
	defer api.Close()
	endpoint := api.URL + "/mcp"

	// The token itself must pass the same check the REST API applies.
	if _, err := authenticator.Parse(ctx, sign("studyflow")); err != nil {
		t.Fatalf("the test token does not pass Parse: %v", err)
	}

	// 1. A token minted for another audience must not open a session, and neither
	// must a missing token.
	if session, err := connectMCP(ctx, endpoint, sign("jobpilot"), -1); err == nil {
		session.Close()
		t.Fatal("a jobpilot audience token was accepted by the MCP endpoint")
	}
	if session, err := connectMCP(ctx, endpoint, "", -1); err == nil {
		session.Close()
		t.Fatal("an unauthenticated MCP session was accepted")
	}

	session, err := connectMCP(ctx, endpoint, sign("studyflow"), 0)
	if err != nil {
		t.Fatalf("a valid studyflow token was rejected: %v", err)
	}
	defer session.Close()

	// 2. The three tools the resume claims are actually exposed.
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"import_plan_tree", "query_progress", "reschedule_task"} {
		if !names[want] {
			t.Fatalf("tool %q is not exposed; got %v", want, names)
		}
	}

	goal, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "MCP 目标"})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Import through MCP.
	importArgs := map[string]any{
		"goal_id":                 goal.ID,
		"idempotency_key":         "mcp-import-key-0001",
		"title":                   "MCP 导入的计划",
		"mode":                    model.PlanModeCalendar,
		"weekly_capacity_minutes": 240,
		"milestones": []any{
			map[string]any{"title": "第一阶段", "outcome": "打基础", "tasks": []any{
				map[string]any{"title": "MCP 任务 A", "estimate_minutes": 30, "scheduled_date": "2026-04-01"},
				map[string]any{"title": "MCP 任务 B", "estimate_minutes": 30},
			}},
			map[string]any{"title": "第二阶段", "tasks": []any{
				map[string]any{"title": "MCP 任务 C", "estimate_minutes": 30},
			}},
		},
		"tasks": []any{map[string]any{"title": "MCP 未分阶段任务", "estimate_minutes": 15}},
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "import_plan_tree", Arguments: importArgs})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("import failed: %s", textOf(result))
	}
	imported := decodeStructured[ImportPlanTreeOutput](t, result)
	// 3 tasks inside the two milestones plus 1 unassigned.
	if imported.Replayed || imported.Milestones != 2 || imported.Tasks != 4 {
		t.Fatalf("unexpected import result: %+v", imported)
	}
	if imported.VersionStatus != model.PlanVersionDraft {
		t.Fatalf("import should leave a draft, got %q", imported.VersionStatus)
	}

	// 4. The same idempotency key replays instead of importing twice.
	replay, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "import_plan_tree", Arguments: importArgs})
	if err != nil {
		t.Fatal(err)
	}
	replayed := decodeStructured[ImportPlanTreeOutput](t, replay)
	if !replayed.Replayed || replayed.PlanID != imported.PlanID || replayed.ImportID != imported.ImportID {
		t.Fatalf("expected a replay of the same plan: %+v", replayed)
	}

	// 5. Reusing the key with different content is a tool error the agent can read.
	conflicting := map[string]any{}
	for name, value := range importArgs {
		conflicting[name] = value
	}
	conflicting["title"] = "另一份内容"
	conflict, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "import_plan_tree", Arguments: conflicting})
	if err != nil {
		t.Fatal(err)
	}
	if !conflict.IsError {
		t.Fatal("a reused idempotency key with different content should be a tool error")
	}

	// 6. Query progress and pick up the version number the reschedule needs.
	progress, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "query_progress", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if progress.IsError {
		t.Fatalf("query failed: %s", textOf(progress))
	}
	view := decodeStructured[QueryProgressOutput](t, progress)
	var found *PlanProgress
	for index := range view.Plans {
		if view.Plans[index].PlanID == imported.PlanID {
			found = &view.Plans[index]
		}
	}
	if found == nil {
		t.Fatalf("the imported plan is missing from query_progress: %+v", view.Plans)
	}
	// The imported version is a draft, so it has no active version and its tasks
	// are not part of the active-version task list yet.
	if found.ActiveVersion != "" {
		t.Fatalf("a fresh import should not be active yet: %+v", found)
	}

	// 7. Activate so the tasks become queryable and reschedulable.
	if _, err := service.ActivatePlanVersion(ctx, userID, imported.PlanVersionID); err != nil {
		t.Fatal(err)
	}
	progress, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "query_progress", Arguments: map[string]any{"plan_id": imported.PlanID}})
	if err != nil {
		t.Fatal(err)
	}
	view = decodeStructured[QueryProgressOutput](t, progress)
	if len(view.Tasks) != 4 {
		t.Fatalf("want 4 tasks from the active version, got %d", len(view.Tasks))
	}
	var target TaskProgress
	for _, task := range view.Tasks {
		if task.Title == "MCP 任务 A" {
			target = task
		}
	}
	if target.TaskID == "" || target.Version == 0 || target.ScheduledDate != "2026-04-01" {
		t.Fatalf("unexpected task from query_progress: %+v", target)
	}

	// 8. Reschedule with the version from the query.
	moved, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "reschedule_task", Arguments: map[string]any{
		"task_id": target.TaskID, "version": target.Version, "scheduled_date": "2026-04-10",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if moved.IsError {
		t.Fatalf("reschedule failed: %s", textOf(moved))
	}
	rescheduled := decodeStructured[RescheduleTaskOutput](t, moved)
	if rescheduled.ScheduledDate != "2026-04-10" || rescheduled.Version != target.Version+1 {
		t.Fatalf("unexpected reschedule result: %+v", rescheduled)
	}

	// 9. A stale version is rejected rather than silently overwriting.
	stale, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "reschedule_task", Arguments: map[string]any{
		"task_id": target.TaskID, "version": target.Version, "scheduled_date": "2026-04-20",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !stale.IsError {
		t.Fatal("a stale version should be a tool error, not a silent overwrite")
	}

	// 10. Clearing the date is the other half of rescheduling.
	cleared, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "reschedule_task", Arguments: map[string]any{
		"task_id": target.TaskID, "version": rescheduled.Version, "clear_scheduled_date": true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.IsError {
		t.Fatalf("clearing the date failed: %s", textOf(cleared))
	}
	if final := decodeStructured[RescheduleTaskOutput](t, cleared); final.ScheduledDate != "" {
		t.Fatalf("expected the date to be cleared: %+v", final)
	}
}

func decodeStructured[T any](t *testing.T, result *mcp.CallToolResult) T {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decoding structured content %s: %v", raw, err)
	}
	return value
}

func textOf(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return "(no text content)"
}
