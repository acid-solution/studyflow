package studyflow

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"studyflow/internal/database"
	"studyflow/internal/identity"
	"studyflow/internal/model"
)

func notificationDB(t *testing.T) (*gorm.DB, Repository, string) {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.NewString()
	t.Cleanup(func() {
		db.Where("user_id = ?", user).Delete(&model.NotificationOutbox{})
		db.Where("user_id = ?", user).Delete(&model.NotificationStream{})
		cleanupUser(t, db, user)
	})
	return db, NewGORMRepository(db), user
}

type failNotificationRepository struct{ Repository }

func (r failNotificationRepository) WithinTransaction(ctx context.Context, fn func(Repository) error) error {
	return r.Repository.WithinTransaction(ctx, func(repo Repository) error { return fn(failNotificationRepository{repo}) })
}
func (r failNotificationRepository) AppendNotification(ctx context.Context, user, kind string, resources []NotificationResource, topics []string, now time.Time) error {
	if err := r.Repository.AppendNotification(ctx, user, kind, resources, topics, now); err != nil {
		return err
	}
	return errors.New("injected failure after event insertion")
}

func TestNotificationTransactionAndSequenceIntegration(t *testing.T) {
	db, repo, user := notificationDB(t)
	ctx := t.Context()
	broken := NewServiceWithRepository(failNotificationRepository{repo}, nil)
	if _, err := broken.CreateGoal(ctx, user, CreateGoalInput{Title: "must roll back"}); err == nil {
		t.Fatal("notification insertion failure was ignored")
	}
	var count int64
	db.Model(&model.Goal{}).Where("user_id = ?", user).Count(&count)
	if count != 0 {
		t.Fatal("business row escaped the rollback")
	}
	head, err := repo.NotificationHead(ctx, user)
	if err != nil || head.LastSequence != 0 {
		t.Fatalf("sequence escaped rollback: %+v %v", head, err)
	}
	service := NewServiceWithRepository(repo, nil)
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := service.CreateGoal(ctx, user, CreateGoalInput{Title: fmt.Sprintf("goal %d", i)})
			failures <- err
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	head, _ = repo.NotificationHead(ctx, user)
	events, err := repo.NotificationsAfter(ctx, user, 0, head.LastSequence, 100)
	if err != nil || len(events) != 12 {
		t.Fatalf("missing notifications: %d %v", len(events), err)
	}
	for i, event := range events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("sequence hole: %+v", events)
		}
	}

	// An allocated sequence remains invisible until its whole transaction commits.
	allocated := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 2)
	go func() {
		finished <- repo.WithinTransaction(ctx, func(tx Repository) error {
			if err := tx.AppendNotification(ctx, user, "test.first", nil, []string{"goals"}, time.Now()); err != nil {
				return err
			}
			close(allocated)
			<-release
			return nil
		})
	}()
	select {
	case <-allocated:
	case <-time.After(3 * time.Second):
		t.Fatal("allocation blocked")
	}
	go func() {
		finished <- repo.WithinTransaction(ctx, func(tx Repository) error {
			return tx.AppendNotification(ctx, user, "test.second", nil, []string{"goals"}, time.Now())
		})
	}()
	select {
	case err := <-finished:
		t.Fatalf("later transaction overtook uncommitted sequence: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	head, _ = repo.NotificationHead(ctx, user)
	if head.LastSequence != 12 {
		t.Fatal("uncommitted sequence became visible")
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	events, _ = repo.NotificationsAfter(ctx, user, 12, 14, 100)
	if len(events) != 2 || events[0].Kind != "test.first" || events[1].Kind != "test.second" {
		t.Fatalf("commit order lost: %+v", events)
	}
}

func TestNotificationMergeReplayAndRetentionIntegration(t *testing.T) {
	_, repo, user := notificationDB(t)
	ctx := t.Context()
	s := NewServiceWithRepository(repo, nil)
	goal, err := s.CreateGoal(ctx, user, CreateGoalInput{Title: "notify"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.CreatePlan(ctx, user, goal.ID, CreatePlanInput{Title: "plan", Objective: "objective", Mode: model.PlanModeCalendar})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, user, plan.Versions[0].ID, CreateTaskInput{Title: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActivatePlanVersion(ctx, user, plan.Versions[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSession(ctx, user, task.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.NotificationHead(ctx, user)
	if _, err = s.SetTaskStatus(ctx, user, task.ID, "complete"); err != nil {
		t.Fatal(err)
	}
	after, _ := repo.NotificationHead(ctx, user)
	if after.LastSequence != before.LastSequence+1 {
		t.Fatal("compound completion produced multiple notifications")
	}
	events, _ := repo.NotificationsAfter(ctx, user, before.LastSequence, after.LastSequence, 100)
	resources := map[string]bool{}
	for _, item := range events[0].Resources {
		resources[item.Type] = true
	}
	if !resources["task"] || !resources["session"] {
		t.Fatal("completion omitted timer invalidation")
	}
	if _, err = s.SetTaskStatus(ctx, user, task.ID, "complete"); err != nil {
		t.Fatal(err)
	}
	idempotent, _ := repo.NotificationHead(ctx, user)
	if idempotent.LastSequence != after.LastSequence {
		t.Fatal("idempotent completion notified again")
	}
	input := ImportPlanTreeInput{GoalID: goal.ID, Title: "import", Objective: "target", Mode: model.PlanModeSequence, Tasks: []ImportTaskInput{{Title: "lesson"}}}
	if _, err = s.ImportPlanTree(ctx, user, "notify-import-key", input); err != nil {
		t.Fatal(err)
	}
	before, _ = repo.NotificationHead(ctx, user)
	if _, err = s.ImportPlanTree(ctx, user, "notify-import-key", input); err != nil {
		t.Fatal(err)
	}
	after, _ = repo.NotificationHead(ctx, user)
	if before.LastSequence != after.LastSequence {
		t.Fatal("import replay created another event")
	}
	other, _ := repo.NotificationsAfter(ctx, uuid.NewString(), 0, after.LastSequence, 100)
	if len(other) != 0 {
		t.Fatal("user data leaked")
	}
	if _, err = s.GetTask(ctx, uuid.NewString(), task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("task detail leaked across users")
	}

	old := time.Now().Add(-8 * 24 * time.Hour)
	// Cleanup deletes an old prefix only, and leaves the recent suffix replayable.
	if err = repo.WithinTransaction(ctx, func(tx Repository) error {
		return tx.AppendNotification(ctx, user, "test.old", nil, []string{"goals"}, old)
	}); err != nil {
		t.Fatal(err)
	}
	if err = repo.PruneNotifications(ctx, time.Now().Add(-7*24*time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	after, _ = repo.NotificationHead(ctx, user)
	if after.PrunedThrough != 0 {
		t.Fatal("cleanup deleted a non-expired prefix to reach a later timestamp")
	}
}

type sseTestFrame struct{ event, id, data string }
type sseTestClient struct {
	response *http.Response
	frames   chan sseTestFrame
	finished chan error
}

func openNotificationClient(t *testing.T, url, token, cursor string) *sseTestClient {
	t.Helper()
	request, _ := http.NewRequest("GET", url+"/api/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if cursor != "" {
		request.Header.Set("Last-Event-ID", cursor)
	}
	response, err := (&http.Client{Timeout: 6 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		t.Fatalf("stream status %d", response.StatusCode)
	}
	client := &sseTestClient{response: response, frames: make(chan sseTestFrame, 128), finished: make(chan error, 1)}
	t.Cleanup(func() { response.Body.Close() })
	go func() {
		scanner := bufio.NewScanner(response.Body)
		frame := sseTestFrame{}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if frame.data != "" {
					client.frames <- frame
				}
				frame = sseTestFrame{}
			} else if strings.HasPrefix(line, "id: ") {
				frame.id = strings.TrimPrefix(line, "id: ")
			} else if strings.HasPrefix(line, "event: ") {
				frame.event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				frame.data = strings.TrimPrefix(line, "data: ")
			}
		}
		client.finished <- scanner.Err()
	}()
	return client
}
func (c *sseTestClient) next(t *testing.T, kind string) sseTestFrame {
	t.Helper()
	select {
	case frame := <-c.frames:
		if frame.event != kind {
			t.Fatalf("expected %s, got %+v", kind, frame)
		}
		return frame
	case err := <-c.finished:
		t.Fatalf("stream closed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("notification timeout")
	}
	return sseTestFrame{}
}

func notificationTestAuth(t *testing.T) (*identity.Authenticator, func(string, time.Duration) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "notifications", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	t.Cleanup(jwks.Close)
	auth := identity.NewAuthenticator(jwks.URL, "shared-auth", "studyflow", time.Minute)
	sign := func(user string, ttl time.Duration) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, struct {
			SessionID string `json:"sid"`
			jwt.RegisteredClaims
		}{uuid.NewString(), jwt.RegisteredClaims{Issuer: "shared-auth", Subject: user, Audience: jwt.ClaimStrings{"studyflow"}, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl))}})
		token.Header["kid"] = "notifications"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	return auth, sign
}

func TestSSEMultiClientReplayAndExpiryIntegration(t *testing.T) {
	db, repo, user := notificationDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	auth, sign := notificationTestAuth(t)
	hub := NewNotificationHub(repo, nil, nil, NotificationOptions{Heartbeat: 20 * time.Millisecond})
	hub.Start(ctx)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	service := NewServiceWithRepository(repo, nil)
	RegisterRoutes(router, NewHTTPHandler(service), auth.Middleware(), db)
	RegisterNotifications(router, hub, auth.Middleware())
	server := httptest.NewServer(router)
	defer server.Close()
	token := sign(user, time.Minute)
	one := openNotificationClient(t, server.URL, token, "")
	two := openNotificationClient(t, server.URL, token, "")
	other := openNotificationClient(t, server.URL, sign(uuid.NewString(), time.Minute), "")
	for _, client := range []*sseTestClient{one, two, other} {
		client.next(t, "sync.reset")
		client.next(t, "sync.ready")
	}
	request, _ := http.NewRequest("POST", server.URL+"/api/v1/goals", strings.NewReader(`{"title":"created through REST"}`))
	started := time.Now()
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("REST write status %d", response.StatusCode)
	}
	first := one.next(t, "business.changed")
	second := two.next(t, "business.changed")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("online sync exceeded two seconds: %s", elapsed)
	} else {
		t.Logf("two online clients received REST update within %s", elapsed)
	}
	if first.id != "1" || second.id != first.id {
		t.Fatal("clients did not see identical ordered event")
	}
	select {
	case frame := <-other.frames:
		t.Fatalf("cross-user notification: %+v", frame)
	case <-time.After(80 * time.Millisecond):
	}
	one.response.Body.Close()
	if _, err := service.CreateGoal(t.Context(), user, CreateGoalInput{Title: "while disconnected"}); err != nil {
		t.Fatal(err)
	}
	two.next(t, "business.changed")
	resumed := openNotificationClient(t, server.URL, token, first.id)
	replayed := resumed.next(t, "business.changed")
	if replayed.id != "2" {
		t.Fatalf("replay missed event: %+v", replayed)
	}
	resumed.next(t, "sync.ready")
	resumed.response.Body.Close()
	two.response.Body.Close()
	other.response.Body.Close()
	// A fresh hub has no memory of previous connections; the log still replays.
	cancel()
	hub.closeAll()
	newHub := NewNotificationHub(repo, nil, nil, NotificationOptions{})
	newRouter := gin.New()
	RegisterNotifications(newRouter, newHub, auth.Middleware())
	restarted := httptest.NewServer(newRouter)
	defer restarted.Close()
	defer newHub.closeAll()
	afterRestart := openNotificationClient(t, restarted.URL, token, "1")
	afterRestart.next(t, "business.changed")
	afterRestart.next(t, "sync.ready")
	afterRestart.response.Body.Close()
	ahead := openNotificationClient(t, restarted.URL, token, "999")
	if frame := ahead.next(t, "sync.reset"); !strings.Contains(frame.data, "ahead") {
		t.Fatal("invalid cursor did not reset")
	}
	ahead.next(t, "sync.ready")
	ahead.response.Body.Close()
	short := openNotificationClient(t, restarted.URL, sign(user, 1500*time.Millisecond), "2")
	short.next(t, "sync.ready")
	select {
	case err := <-short.finished:
		if err != nil {
			t.Fatalf("expiry closed with client timeout: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream survived token expiry")
	}
}

func TestNotificationSlowConsumerAndPrunedCursorIntegration(t *testing.T) {
	_, repo, user := notificationDB(t)
	ctx := t.Context()
	for i := 0; i < 3; i++ {
		if err := repo.WithinTransaction(ctx, func(tx Repository) error {
			return tx.AppendNotification(ctx, user, "test.old", []NotificationResource{{"goal", uuid.NewString()}}, []string{"goals"}, time.Now().Add(-8*24*time.Hour))
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.PruneNotifications(ctx, time.Now().Add(-7*24*time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	head, _ := repo.NotificationHead(ctx, user)
	if head.PrunedThrough != 3 || head.LastSequence != 3 {
		t.Fatalf("invalid cleanup boundary: %+v", head)
	}
	hub := NewNotificationHub(repo, nil, nil, NotificationOptions{QueueSize: 1})
	defer hub.closeAll()
	client, unsubscribe, err := hub.subscribe(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	for i := 0; i < 2; i++ {
		if _, err := NewServiceWithRepository(repo, nil).CreateGoal(ctx, user, CreateGoalInput{Title: "burst"}); err != nil {
			t.Fatal(err)
		}
	}
	hub.PollOnce(ctx)
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("slow consumer was not disconnected")
	}
	auth, sign := notificationTestAuth(t)
	router := gin.New()
	RegisterNotifications(router, hub, auth.Middleware())
	server := httptest.NewServer(router)
	defer server.Close()
	stream := openNotificationClient(t, server.URL, sign(user, time.Minute), "1")
	reset := stream.next(t, "sync.reset")
	if !strings.Contains(reset.data, "expired") {
		t.Fatal("pruned cursor was treated as current")
	}
	stream.next(t, "sync.ready")
	stream.response.Body.Close()
}

type gatedReplayRepository struct {
	Repository
	once             sync.Once
	entered, release chan struct{}
}

func (r *gatedReplayRepository) NotificationsAfter(ctx context.Context, user string, after, through uint64, limit int) ([]Notification, error) {
	r.once.Do(func() {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
		}
	})
	return r.Repository.NotificationsAfter(ctx, user, after, through, limit)
}

func TestSSENoGapBetweenReplayAndLiveIntegration(t *testing.T) {
	_, repo, user := notificationDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := NewServiceWithRepository(repo, nil)
	if _, err := s.CreateGoal(ctx, user, CreateGoalInput{Title: "before connecting"}); err != nil {
		t.Fatal(err)
	}
	gate := &gatedReplayRepository{Repository: repo, entered: make(chan struct{}), release: make(chan struct{})}
	hub := NewNotificationHub(gate, nil, nil, NotificationOptions{PollInterval: 20 * time.Millisecond})
	hub.Start(ctx)
	defer hub.closeAll()
	auth, sign := notificationTestAuth(t)
	router := gin.New()
	RegisterNotifications(router, hub, auth.Middleware())
	server := httptest.NewServer(router)
	defer server.Close()
	clients := make(chan *sseTestClient, 1)
	go func() { clients <- openNotificationClient(t, server.URL, sign(user, time.Minute), "0") }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("replay was not started")
	}
	if _, err := s.CreateGoal(ctx, user, CreateGoalInput{Title: "committed during replay"}); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	client := <-clients
	if frame := client.next(t, "business.changed"); frame.id != "1" {
		t.Fatalf("missing replay event: %+v", frame)
	}
	client.next(t, "sync.ready")
	if frame := client.next(t, "business.changed"); frame.id != "2" {
		t.Fatalf("missing handoff event: %+v", frame)
	}
	client.response.Body.Close()
}
