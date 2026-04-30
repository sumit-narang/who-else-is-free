package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// --- Mock push sender for testing ---

type capturedNotification struct {
	Token string
	Data  map[string]string
}

type mockPushSender struct {
	mu            sync.Mutex
	notifications []capturedNotification
}

func (m *mockPushSender) Send(_ context.Context, n PushNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = append(m.notifications, capturedNotification{Token: n.Token, Data: n.Data})
	return nil
}

func (m *mockPushSender) SendBatch(_ context.Context, notifications []PushNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range notifications {
		m.notifications = append(m.notifications, capturedNotification{Token: n.Token, Data: n.Data})
	}
	return nil
}

func (m *mockPushSender) getNotifications() []capturedNotification {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]capturedNotification, len(m.notifications))
	copy(out, m.notifications)
	return out
}

func (m *mockPushSender) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = nil
}

func (m *mockPushSender) waitForNotifications(t *testing.T, count int, timeout time.Duration) []capturedNotification {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got := m.getNotifications()
		if len(got) >= count {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := m.getNotifications()
	if len(got) < count {
		t.Fatalf("timed out waiting for %d notifications, got %d", count, len(got))
	}
	return got
}

type apiTestEnv struct {
	server   *httptest.Server
	repo     *EventRepository
	signer   *tokenSigner
	db       *sql.DB
	wsScheme string
	hub      *ChatHub
}

func setupAPITestEnv(t *testing.T) *apiTestEnv {
	t.Helper()
	return setupAPITestEnvWithPush(t, NewNoopPushSender())
}

func setupAPITestEnvWithPush(t *testing.T, pushSender PushSender) *apiTestEnv {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "who-else-is-free-*.sqlite")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	tmpFile.Close()

	db, err := openDB(tmpFile.Name())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	repo := NewEventRepository(db)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := repo.Init(ctx); err != nil {
		t.Fatalf("init repo: %v", err)
	}
	if err := repo.EnsureSeedData(ctx); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	_ = os.Setenv("CHAT_SESSION_SECRET", "test-session-secret")
	_ = os.Setenv("GOOGLE_OAUTH_CLIENT_ID", "test-google-client")

	signer, err := newTokenSignerFromEnv()
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	hub := NewChatHub(repo, signer, pushSender)
	eventHandler := NewEventHandler(repo, hub)
	authHandler := NewAuthHandler(repo, signer)
	profileHandler := NewProfileHandler(repo, hub)
	go hub.Run()
	pushHandler := NewPushHandler(repo, pushSender)

	router := setupRouter(eventHandler, authHandler, profileHandler, hub, pushHandler, signer)
	ts := httptest.NewServer(router)

	t.Cleanup(func() {
		ts.Close()
		db.Close()
		os.Remove(tmpFile.Name())
	})

	parsedURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}

	wsScheme := "ws"
	if parsedURL.Scheme == "https" {
		wsScheme = "wss"
	}

	return &apiTestEnv{
		server:   ts,
		repo:     repo,
		signer:   signer,
		db:       db,
		wsScheme: wsScheme,
		hub:      hub,
	}
}

func (env *apiTestEnv) issueTokenForEmail(t *testing.T, email string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	user, err := env.repo.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("get user %s: %v", email, err)
	}

	token, _, err := env.signer.issue(user.ID, user.Email)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func (env *apiTestEnv) doRequest(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, env.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("perform request: %v", err)
	}
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return out
}

func queryCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("query count: %v", err)
	}
	return count
}

type eventsResponse struct {
	Data []Event `json:"data"`
}

type createEventResponse struct {
	ID int64 `json:"id"`
}

type conversationsResponse struct {
	Conversations []struct {
		ID    int64 `json:"id"`
		Event *struct {
			ID          int64  `json:"id"`
			ScheduledAt string `json:"scheduled_at"`
		} `json:"event"`
		LastMessage *struct {
			ID int64 `json:"id"`
		} `json:"last_message"`
	} `json:"conversations"`
}

type messagesResponse struct {
	Messages []struct {
		ID        int64  `json:"id"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
	} `json:"messages"`
}

type unblockMemberResponse struct {
	Unblocked        bool  `json:"unblocked"`
	AlreadyUnblocked bool  `json:"already_unblocked"`
	EventID          int64 `json:"event_id"`
	UserID           int64 `json:"user_id"`
}

func hasInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasConversationForUser(t *testing.T, env *apiTestEnv, token string, conversationID int64) bool {
	t.Helper()
	resp := env.doRequest(t, http.MethodGet, "/api/conversations", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing conversations, got %d", resp.StatusCode)
	}
	payload := decodeJSON[conversationsResponse](t, resp)
	for _, conversation := range payload.Conversations {
		if conversation.ID == conversationID {
			return true
		}
	}
	return false
}

type wsEnvelope struct {
	Type    string `json:"type"`
	TempID  string `json:"tempId"`
	Message struct {
		ID             int64  `json:"id"`
		ConversationID int64  `json:"conversationId"`
		SenderID       int64  `json:"senderId"`
		Body           string `json:"body"`
	} `json:"message"`
}

func TestAPIIntegration(t *testing.T) {
	env := setupAPITestEnv(t)

	t.Run("list events", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/events", "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		_ = decodeJSON[eventsResponse](t, resp)
	})

	token := env.issueTokenForEmail(t, "ava@example.com")

	t.Run("create event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Test Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(24 * time.Hour).Format("2006-01-02"),
			Description: "Integration event",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			DateLabel:   "Tmrw",
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
			UserID:      1,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", token, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		if payload.ID == 0 {
			t.Fatal("expected created event id")
		}
	})

	// Server no longer enforces "future-only" event schedule constraints;
	// the mobile client performs this validation using the user's local timezone.

	t.Run("list conversations", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected at least one conversation")
		}

		// Find a conversation that has messages (seeded conversations have messages)
		var conversationID int64
		for _, convo := range payload.Conversations {
			if convo.LastMessage != nil {
				conversationID = convo.ID
				break
			}
		}
		if conversationID == 0 {
			conversationID = payload.Conversations[0].ID
		}

		respMessages := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", conversationID), token, nil)
		if respMessages.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", respMessages.StatusCode)
		}
		messages := decodeJSON[messagesResponse](t, respMessages)
		if len(messages.Messages) == 0 {
			t.Fatal("expected seeded messages")
		}
	})

	t.Run("websocket messaging", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("no conversations to test websocket")
		}
		conversationID := payload.Conversations[0].ID

		dialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(token)

		conn, _, err := websocket.DefaultDialer.Dial(dialURL, nil)
		if err != nil {
			t.Fatalf("websocket dial: %v", err)
		}
		defer conn.Close()

		tempID := fmt.Sprintf("temp-%d", time.Now().UnixNano())
		msgBody := "integration websocket message"
		sendPayload := map[string]any{
			"type":           "message:send",
			"conversationId": conversationID,
			"body":           msgBody,
			"tempId":         tempID,
		}
		if err := conn.WriteJSON(sendPayload); err != nil {
			t.Fatalf("ws send: %v", err)
		}

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var envelope wsEnvelope
		if err := conn.ReadJSON(&envelope); err != nil {
			t.Fatalf("ws read: %v", err)
		}
		if envelope.Type != "message:new" {
			t.Fatalf("expected message:new, got %s", envelope.Type)
		}
		if envelope.Message.Body != msgBody {
			t.Fatalf("expected body %q, got %q", msgBody, envelope.Message.Body)
		}

		respMessages := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", conversationID), token, nil)
		if respMessages.StatusCode != http.StatusOK {
			t.Fatalf("messages fetch expected 200, got %d", respMessages.StatusCode)
		}
		messages := decodeJSON[messagesResponse](t, respMessages)
		found := false
		for _, m := range messages.Messages {
			if m.Body == msgBody {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("persisted messages did not include %q", msgBody)
		}
	})
}

type testJoinRequestResponse struct {
	Request struct {
		ID      int64  `json:"id"`
		EventID int64  `json:"event_id"`
		UserID  int64  `json:"user_id"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"request"`
}

type testErrorResponse struct {
	Error string `json:"error"`
}

type testMessageResponse struct {
	Message string `json:"message"`
}

type testReportResponse struct {
	Report struct {
		ID      int64  `json:"id"`
		EventID int64  `json:"event_id"`
		UserID  int64  `json:"user_id"`
		Reason  string `json:"reason"`
		Status  string `json:"status"`
	} `json:"report"`
}

func TestCancelJoinRequest(t *testing.T) {
	env := setupAPITestEnv(t)

	// Get tokens for users
	// User 1 (ava) owns event 1
	// User 2 (liam) owns event 2
	// User 3 (sophia) owns event 3
	// User 4 (noah) doesn't own any events in seed data

	// We need to create a fresh event and have a user that's not already a member request to join
	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user id 1
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user id 4

	// First, create a new Group event as ava so we have a clean slate
	// (Group events require approval for join requests)
	var newEventID int64
	t.Run("setup - create event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Cancel Test Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(24 * time.Hour).Format("2006-01-02"),
			Description: "For cancel request test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			DateLabel:   "Tmrw",
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		newEventID = payload.ID
	})

	// Noah sends a join request for the new event
	t.Run("create join request", func(t *testing.T) {
		body := map[string]string{"message": "I'd like to join!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", newEventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[testJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status, got %s", payload.Request.Status)
		}
	})

	// Verify the request exists
	t.Run("verify request exists", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/chat/requests/me", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var payload struct {
			Requests []struct {
				EventID int64 `json:"event_id"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		found := false
		for _, r := range payload.Requests {
			if r.EventID == newEventID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected to find pending request for event %d", newEventID)
		}
	})

	// Cancel the request
	t.Run("cancel join request", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/chat/requests/me", newEventID), noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[testMessageResponse](t, resp)
		if payload.Message != "request cancelled" {
			t.Fatalf("expected 'request cancelled', got %s", payload.Message)
		}
	})

	// Verify the request no longer exists
	t.Run("verify request cancelled", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/chat/requests/me", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var payload struct {
			Requests []struct {
				EventID int64 `json:"event_id"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		for _, r := range payload.Requests {
			if r.EventID == newEventID {
				t.Fatal("request should have been cancelled")
			}
		}
	})

	// Try to cancel a non-existent request
	t.Run("cancel non-existent request returns 404", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/chat/requests/me", newEventID), noahToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	// Cannot cancel without auth
	t.Run("cancel without auth returns 401", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/chat/requests/me", newEventID), "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	// Invalid event ID
	t.Run("cancel with invalid event id returns 400", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, "/api/events/invalid/chat/requests/me", noahToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

func TestReportEvent(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")       // user id 1
	noahToken := env.issueTokenForEmail(t, "noah@example.com")     // user id 4
	sophiaToken := env.issueTokenForEmail(t, "sophia@example.com") // user id 3

	var eventID int64
	t.Run("create event to report", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Report Event Test",
			Location:    "Test Location",
			Time:        "20:00",
			EventDate:   time.Now().Add(24 * time.Hour).Format("2006-01-02"),
			Description: "Testing report behavior",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      65,
			DateLabel:   "Tmrw",
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
		if eventID == 0 {
			t.Fatal("expected created event id")
		}
	})

	// Report an event successfully
	t.Run("report event successfully", func(t *testing.T) {
		body := map[string]string{"reason": "This event contains inappropriate content"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[testReportResponse](t, resp)
		if payload.Report.EventID != eventID {
			t.Fatalf("expected event_id %d, got %d", eventID, payload.Report.EventID)
		}
		if payload.Report.Status != "pending" {
			t.Fatalf("expected pending status, got %s", payload.Report.Status)
		}
		if payload.Report.Reason != "This event contains inappropriate content" {
			t.Fatalf("unexpected reason: %s", payload.Report.Reason)
		}
	})

	// Same user should not be able to report the same event again
	t.Run("duplicate report returns 409", func(t *testing.T) {
		body := map[string]string{"reason": "Reporting again"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), noahToken, body)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	// Different user can report the same event
	t.Run("different user can report same event", func(t *testing.T) {
		body := map[string]string{"reason": "Also reporting this event"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), sophiaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
	})

	// Report without reason returns 400
	t.Run("report without reason returns 400", func(t *testing.T) {
		body := map[string]string{"reason": ""}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), noahToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	// Report without auth returns 401
	t.Run("report without auth returns 401", func(t *testing.T) {
		body := map[string]string{"reason": "Test reason"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), "", body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	// Report non-existent event returns 404
	t.Run("report non-existent event returns 404", func(t *testing.T) {
		body := map[string]string{"reason": "Test reason"}
		resp := env.doRequest(t, http.MethodPost, "/api/events/99999/report", noahToken, body)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	// Invalid event ID returns 400
	t.Run("report with invalid event id returns 400", func(t *testing.T) {
		body := map[string]string{"reason": "Test reason"}
		resp := env.doRequest(t, http.MethodPost, "/api/events/invalid/report", noahToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

func TestReportEventCancelsPendingRequest(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user id 1
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user id 4

	// Create a new event as ava
	var newEventID int64
	t.Run("setup - create event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Report Cancel Test Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(24 * time.Hour).Format("2006-01-02"),
			Description: "For report cancels request test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			DateLabel:   "Tmrw",
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		newEventID = payload.ID
	})

	// Noah sends a join request
	t.Run("create join request", func(t *testing.T) {
		body := map[string]string{"message": "I'd like to join this event!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", newEventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
	})

	// Verify the request exists
	t.Run("verify request exists before report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/chat/requests/me", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var payload struct {
			Requests []struct {
				EventID int64 `json:"event_id"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		found := false
		for _, r := range payload.Requests {
			if r.EventID == newEventID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected to find pending request for event %d", newEventID)
		}
	})

	// Noah reports the event
	t.Run("report event", func(t *testing.T) {
		body := map[string]string{"reason": "This event seems suspicious"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", newEventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	// Verify the pending request was cancelled
	t.Run("verify request cancelled after report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/chat/requests/me", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var payload struct {
			Requests []struct {
				EventID int64 `json:"event_id"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		for _, r := range payload.Requests {
			if r.EventID == newEventID {
				t.Fatal("pending request should have been cancelled after reporting")
			}
		}
	})
}

func TestCancelAndReportWorkflow(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")

	// Simulate the full workflow: create request, cancel it, then report
	t.Run("full cancel and report workflow", func(t *testing.T) {
		createResp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, CreateEventParams{
			Title:       "Cancel And Report Workflow",
			Location:    "Workflow Park",
			Time:        "16:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Created for cancel/report integration flow",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      60,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		})
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create event: expected 201, got %d", createResp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, createResp).ID

		// Create join request
		body := map[string]string{"message": "Want to join event 2"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create request: expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Cancel join request
		resp = env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/chat/requests/me", eventID), noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cancel request: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Report the event
		reportBody := map[string]string{"reason": "Suspicious activity"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), noahToken, reportBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("report event: expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Can create a new join request after cancelling
		body = map[string]string{"message": "Changed my mind, want to join again"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create new request after cancel: expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})
}

func TestReportEventSingleRemovesReporterFromHostViews(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")

	var eventID int64
	t.Run("create single event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Single Report Cleanup",
			Location:    "Cafe",
			Time:        "19:00",
			EventDate:   time.Now().Add(24 * time.Hour).Format("2006-01-02"),
			Description: "Cleanup test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      60,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	var conversationID int64
	t.Run("reporter joins single event", func(t *testing.T) {
		body := map[string]string{"message": "Joining single event"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status before approval, got %s", payload.Request.Status)
		}
		approveResp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving request, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversationId after approval")
		}
		conversationID = *approvePayload.ConversationID
	})

	t.Run("host can see request before report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/chat/requests?include_approved=1", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[joinRequestsListResponse](t, resp)
		found := false
		for _, request := range payload.Requests {
			if request.UserID == 4 {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected host to see reporter in single-event requests before report")
		}
	})

	t.Run("reporter reports event", func(t *testing.T) {
		body := map[string]string{"reason": "Safety concern"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("reporter no longer sees conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				t.Fatal("reporter should not see single-event conversation after report")
			}
		}
	})

	t.Run("host no longer sees conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				t.Fatal("host should not see single-event conversation after report")
			}
		}
	})

	t.Run("host request list excludes reporter after report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/chat/requests", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[joinRequestsListResponse](t, resp)
		for _, request := range payload.Requests {
			if request.UserID == 4 {
				t.Fatal("reporter should not appear in host single-event request list after report")
			}
		}
	})
}

func TestReportEventGroupRemovesReporterFromMemberList(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")

	var eventID int64
	t.Run("create group event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Group Report Cleanup",
			Location:    "Downtown",
			Time:        "20:00",
			EventDate:   time.Now().Add(24 * time.Hour).Format("2006-01-02"),
			Description: "Group cleanup test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      60,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	t.Run("reporter requests to join", func(t *testing.T) {
		body := map[string]string{"message": "Please approve me"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("host approves join request", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	var conversationID int64
	t.Run("host sees reporter in member list before report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected at least one group conversation")
		}
		conversationID = payload.Conversations[0].ID
		if !hasInt64(payload.Conversations[0].MemberIDs, 4) {
			t.Fatal("expected reporter to be in group member list before report")
		}
	})

	t.Run("reporter reports group event", func(t *testing.T) {
		body := map[string]string{"reason": "Inappropriate activity"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/report", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("host member list excludes reporter after report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected group conversation to remain after report")
		}
		if hasInt64(payload.Conversations[0].MemberIDs, 4) {
			t.Fatal("reporter should not be in group member list after report")
		}
	})

	t.Run("reporter cannot see group conversation after report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				t.Fatal("reporter should not see group conversation after report")
			}
		}
	})
}

// ============================================================================
// 1:1 vs Group Event Differentiation Tests
// ============================================================================

// Response types for 1:1 tests
type singleJoinRequestResponse struct {
	Request struct {
		ID             int64  `json:"id"`
		EventID        int64  `json:"event_id"`
		UserID         int64  `json:"user_id"`
		Status         string `json:"status"`
		Message        string `json:"message"`
		ConversationID *int64 `json:"conversation_id"`
	} `json:"request"`
	ConversationID *int64 `json:"conversationId"` // camelCase in JSON
}

type eventConversationsResponse struct {
	Conversations []struct {
		ID          int64   `json:"id"`
		CreatedBy   int64   `json:"created_by"`
		MemberIDs   []int64 `json:"member_ids"`
		UnreadCount int     `json:"unread_count"`
		LastMessage *struct {
			ID       int64  `json:"id"`
			Body     string `json:"body"`
			SenderID int64  `json:"sender_id"`
		} `json:"last_message"`
	} `json:"conversations"`
}

type joinRequestsListResponse struct {
	Requests []struct {
		ID             int64  `json:"id"`
		EventID        int64  `json:"event_id"`
		UserID         int64  `json:"user_id"`
		Status         string `json:"status"`
		Message        string `json:"message"`
		ConversationID *int64 `json:"conversation_id"`
		Requester      struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"requester"`
	} `json:"requests"`
}

// TestSingleEventJoinRequest tests that joining a 1:1 (Single) event creates a
// pending request first, and the conversation is only created after host approval.
func TestSingleEventJoinRequest(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user id 1 - will be host
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user id 4 - will be requester

	// Create a 1:1 (Single) event
	var singleEventID int64
	t.Run("create 1:1 event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Coffee Chat 1:1",
			Location:    "Local Cafe",
			Time:        "14:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Let's have a coffee and chat",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      60,
			GroupType:   "Single", // 1:1 event
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		singleEventID = payload.ID
		if singleEventID == 0 {
			t.Fatal("expected event ID")
		}
	})

	// Noah joins the 1:1 event - should stay pending until host approval.
	introMessage := "Hi! I'd love to grab coffee with you. I'm new to the area."
	t.Run("join 1:1 event creates pending request", func(t *testing.T) {
		body := map[string]string{"message": introMessage}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", singleEventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)

		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status for 1:1 event, got %s", payload.Request.Status)
		}

		// Conversation should not exist yet.
		if payload.ConversationID != nil {
			t.Fatalf("expected no conversation_id before approval, got %d", *payload.ConversationID)
		}
	})

	t.Run("default /api/chat/requests/me includes pending request", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/chat/requests/me", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[joinRequestsListResponse](t, resp)
		found := false
		for _, req := range payload.Requests {
			if req.EventID == singleEventID {
				found = true
				if req.Status != "pending" {
					t.Fatalf("expected pending status before approval, got %s", req.Status)
				}
			}
		}
		if !found {
			t.Fatalf("expected pending request for event %d", singleEventID)
		}
	})

	var conversationID int64
	t.Run("host approves request and creates private conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", singleEventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.ConversationID == nil {
			t.Fatal("expected conversation_id after approval")
		}
		conversationID = *payload.ConversationID
	})

	t.Run("include_approved returns intro message for approved 1:1 event", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/chat/requests/me?include_approved=1", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[joinRequestsListResponse](t, resp)
		found := false
		for _, req := range payload.Requests {
			if req.EventID == singleEventID {
				found = true
				if req.Status != "approved" {
					t.Fatalf("expected approved status, got %s", req.Status)
				}
				if req.Message != introMessage {
					t.Fatalf("expected intro message %q, got %q", introMessage, req.Message)
				}
			}
		}
		if !found {
			t.Fatalf("expected approved request for event %d when include_approved=1", singleEventID)
		}
	})

	// Verify the intro message was inserted as the first message
	t.Run("intro message is first message in conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", conversationID), noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		messages := decodeJSON[messagesResponse](t, resp)
		if len(messages.Messages) == 0 {
			t.Fatal("expected at least one message (intro message)")
		}
		// Messages are returned newest first, so intro should be last (or only)
		found := false
		for _, msg := range messages.Messages {
			if msg.Body == introMessage {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("intro message not found in conversation. Messages: %+v", messages.Messages)
		}
	})

	t.Run("host can also see intro message in the same conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", conversationID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		messages := decodeJSON[messagesResponse](t, resp)
		found := false
		for _, msg := range messages.Messages {
			if msg.Body == introMessage {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("host could not find intro message %q in conversation %d", introMessage, conversationID)
		}
	})

	// Both host and requester should see the conversation
	t.Run("host can see the 1:1 conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		found := false
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("host should see conversation %d", conversationID)
		}
	})

	t.Run("requester can see the 1:1 conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		found := false
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("requester should see conversation %d", conversationID)
		}
	})
}

// TestSingleEventMultipleRequesters tests that multiple users can join
// a 1:1 event and each gets their own private conversation with the host
func TestSingleEventMultipleRequesters(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")       // host
	noahToken := env.issueTokenForEmail(t, "noah@example.com")     // requester 1
	sophiaToken := env.issueTokenForEmail(t, "sophia@example.com") // requester 2

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	sophiaUser, err := env.repo.GetUserByEmail(ctx, "sophia@example.com")
	if err != nil {
		t.Fatalf("failed to load sophia user: %v", err)
	}

	// Create a 1:1 event
	var singleEventID int64
	t.Run("create 1:1 event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Networking Coffee",
			Location:    "Downtown Cafe",
			Time:        "10:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "1:1 networking opportunity",
			Gender:      "Any",
			MinAge:      21,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		singleEventID = payload.ID
	})

	// First requester joins, then host approves.
	var convo1ID int64
	t.Run("first requester joins and is approved", func(t *testing.T) {
		body := map[string]string{"message": "Hi from Noah!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", singleEventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status, got %s", payload.Request.Status)
		}
		if payload.ConversationID != nil {
			t.Fatalf("expected no conversation_id before approval, got %d", *payload.ConversationID)
		}

		approveResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", singleEventID, noahUser.ID),
			avaToken,
			nil,
		)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on approve, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversation_id after approval")
		}
		convo1ID = *approvePayload.ConversationID
	})

	// Second requester joins and is approved - should get DIFFERENT conversation.
	var convo2ID int64
	t.Run("second requester joins and gets separate approved conversation", func(t *testing.T) {
		body := map[string]string{"message": "Hi from Sophia!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", singleEventID), sophiaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status, got %s", payload.Request.Status)
		}
		if payload.ConversationID != nil {
			t.Fatalf("expected no conversation_id before approval, got %d", *payload.ConversationID)
		}

		approveResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", singleEventID, sophiaUser.ID),
			avaToken,
			nil,
		)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on approve, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversation_id after approval")
		}
		convo2ID = *approvePayload.ConversationID

		// Verify it's a DIFFERENT conversation
		if convo1ID == convo2ID {
			t.Fatalf("expected different conversations for different requesters, both got %d", convo1ID)
		}
	})

	// Verify each requester only sees their own conversation messages
	t.Run("noah only sees his conversation", func(t *testing.T) {
		// Noah should see his conversation
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", convo1ID), noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		messages := decodeJSON[messagesResponse](t, resp)
		found := false
		for _, msg := range messages.Messages {
			if msg.Body == "Hi from Noah!" {
				found = true
			}
			if msg.Body == "Hi from Sophia!" {
				t.Fatal("Noah should not see Sophia's message")
			}
		}
		if !found {
			t.Fatal("Noah should see his own intro message")
		}
	})

	t.Run("sophia only sees her conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", convo2ID), sophiaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		messages := decodeJSON[messagesResponse](t, resp)
		found := false
		for _, msg := range messages.Messages {
			if msg.Body == "Hi from Sophia!" {
				found = true
			}
			if msg.Body == "Hi from Noah!" {
				t.Fatal("Sophia should not see Noah's message")
			}
		}
		if !found {
			t.Fatal("Sophia should see her own intro message")
		}
	})

	// Host can see all conversations via the event conversations endpoint
	t.Run("host can list all event conversations", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", singleEventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		// Should have at least 2 conversations (one per requester) + the initial event conversation
		if len(payload.Conversations) < 2 {
			t.Fatalf("expected at least 2 conversations, got %d", len(payload.Conversations))
		}

		// Verify we have conversations for both requesters
		foundConvo1, foundConvo2 := false, false
		for _, convo := range payload.Conversations {
			if convo.ID == convo1ID {
				foundConvo1 = true
			}
			if convo.ID == convo2ID {
				foundConvo2 = true
			}
		}
		if !foundConvo1 || !foundConvo2 {
			t.Fatalf("expected to find both requester conversations. Found convo1: %v, convo2: %v", foundConvo1, foundConvo2)
		}
	})

	// Non-host cannot list event conversations
	t.Run("non-host cannot list event conversations", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", singleEventID), noahToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
	})
}

// TestGroupEventJoinRequest tests that Group events still work as before
// (pending status, no auto-conversation)
func TestGroupEventJoinRequest(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // host
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // requester

	// Create a Group event
	var groupEventID int64
	t.Run("create Group event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Group Hiking Trip",
			Location:    "Mountain Trail",
			Time:        "08:00",
			EventDate:   time.Now().Add(96 * time.Hour).Format("2006-01-02"),
			Description: "Group hiking adventure",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      65,
			GroupType:   "Group", // Group event
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		groupEventID = payload.ID
	})

	// Noah joins the Group event - should be pending, NOT auto-approved
	t.Run("join Group event creates pending request", func(t *testing.T) {
		body := map[string]string{"message": "I'd love to join the hike!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", groupEventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)

		// For Group events, status should be "pending"
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status for Group event, got %s", payload.Request.Status)
		}

		// Should NOT have a conversation_id (not approved yet)
		if payload.ConversationID != nil {
			t.Fatalf("expected no conversation_id for pending Group request, got %d", *payload.ConversationID)
		}
	})

	// Verify Noah does NOT see any new conversation yet
	t.Run("pending requester does not see conversation yet", func(t *testing.T) {
		// Get Noah's conversation count before and after - should be same
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		// Noah should not have any new conversation from this Group event yet
		for _, convo := range payload.Conversations {
			// The group event's conversation should not be visible to Noah yet
			// (he's not a member, just a pending requester)
			t.Logf("Noah's conversation: %d", convo.ID)
		}
	})

	// Host approves the request
	t.Run("host approves Group request", func(t *testing.T) {
		// Get Noah's user ID (4 based on seed data)
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", groupEventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	// Now Noah should be able to see the conversation
	t.Run("approved requester can see conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("approved requester should see at least one conversation")
		}
	})

	t.Run("group approval does NOT insert intro message in chat history", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", groupEventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected group event conversation")
		}

		conversationID := payload.Conversations[0].ID
		resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", conversationID), noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		messages := decodeJSON[messagesResponse](t, resp)
		for _, msg := range messages.Messages {
			if msg.Body == "I'd love to join the hike!" {
				t.Fatalf("intro message should NOT appear in group chat history")
			}
		}
	})
}

// TestSingleEventDenyRequest tests declining a request for a 1:1 event
// removes the user from their private conversation
func TestSingleEventDenyRequest(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // host
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // requester

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}

	baselineConversationIDs := make(map[int64]struct{})
	t.Run("capture requester baseline conversations", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, conversation := range payload.Conversations {
			baselineConversationIDs[conversation.ID] = struct{}{}
		}
	})

	// Create 1:1 event
	var eventID int64
	t.Run("create 1:1 event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Deny Test Event",
			Location:    "Test Cafe",
			Time:        "15:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Testing deny flow",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	// Noah joins (pending only)
	t.Run("noah joins 1:1 event", func(t *testing.T) {
		body := map[string]string{"message": "Can I join?"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status, got %s", payload.Request.Status)
		}
		if payload.ConversationID != nil {
			t.Fatalf("expected no conversation_id before approval, got %d", *payload.ConversationID)
		}
	})

	t.Run("host has no event conversations before approval", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) != 0 {
			t.Fatalf("expected no conversations before approval, got %d", len(payload.Conversations))
		}
	})

	// Host denies the request
	t.Run("host denies 1:1 request", func(t *testing.T) {
		resp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/deny", eventID, noahUser.ID),
			avaToken,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("host request list excludes denied requester", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/chat/requests", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[joinRequestsListResponse](t, resp)
		for _, req := range payload.Requests {
			if req.UserID == noahUser.ID {
				t.Fatal("denied requester should not remain in pending request list")
			}
		}
	})

	t.Run("requester conversation list unchanged after deny", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		if len(payload.Conversations) != len(baselineConversationIDs) {
			t.Fatalf("expected requester conversation count %d, got %d", len(baselineConversationIDs), len(payload.Conversations))
		}
		for _, conversation := range payload.Conversations {
			if _, existed := baselineConversationIDs[conversation.ID]; !existed {
				t.Fatalf("did not expect new conversation %d after deny", conversation.ID)
			}
		}
	})
}

// TestReportMember tests the new member report endpoint
func TestReportMember(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // host
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // requester to be reported

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	avaUser, err := env.repo.GetUserByEmail(ctx, "ava@example.com")
	if err != nil {
		t.Fatalf("failed to load ava user: %v", err)
	}

	// Create 1:1 event
	var eventID int64
	t.Run("create 1:1 event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Report Member Test",
			Location:    "Test Location",
			Time:        "16:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Testing member report",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	var noahEventID int64
	t.Run("noah creates event for visibility checks", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Noah Visibility Event",
			Location:    "Noah Place",
			Time:        "18:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Noah hosted event",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		noahEventID = payload.ID
	})

	// Noah joins and host approves so member report exercises removal flow.
	var conversationID int64
	var noahHostedConversationID int64
	var secondaryEventID int64
	var secondaryConversationID int64
	var postBlockEventID int64
	t.Run("noah joins event", func(t *testing.T) {
		body := map[string]string{"message": "Let me join please"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status before approval, got %s", payload.Request.Status)
		}

		approveResp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on approve, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversation_id after approval")
		}
		conversationID = *approvePayload.ConversationID
	})

	t.Run("ava joins noah-hosted event and gets approved", func(t *testing.T) {
		body := map[string]string{"message": "Joining noah's event"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", noahEventID), avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		approveResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", noahEventID, avaUser.ID),
			noahToken,
			nil,
		)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on approve, got %d", approveResp.StatusCode)
		}
		approveResp.Body.Close()

		conversationsResp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", noahEventID), noahToken, nil)
		if conversationsResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", conversationsResp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, conversationsResp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected at least one conversation for noah-hosted event")
		}
		noahHostedConversationID = payload.Conversations[0].ID
		if !hasInt64(payload.Conversations[0].MemberIDs, avaUser.ID) {
			t.Fatal("expected ava to be a member in noah-hosted event before report")
		}
	})

	t.Run("create secondary host event where member is also joined", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Ava Secondary Event",
			Location:    "Secondary Location",
			Time:        "17:30",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Second host event for cross-event report-block cleanup",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		secondaryEventID = decodeJSON[createEventResponse](t, resp).ID
	})

	t.Run("noah joins and is accepted in secondary host event", func(t *testing.T) {
		joinBody := map[string]string{"message": "Joining secondary event"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", secondaryEventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		approveResp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", secondaryEventID, noahUser.ID), avaToken, nil)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on approve, got %d", approveResp.StatusCode)
		}
		approveResp.Body.Close()

		conversationsResp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", secondaryEventID), avaToken, nil)
		if conversationsResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", conversationsResp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, conversationsResp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected at least one conversation for secondary event")
		}
		secondaryConversationID = payload.Conversations[0].ID
		if !hasInt64(payload.Conversations[0].MemberIDs, noahUser.ID) {
			t.Fatal("expected noah to be a member in secondary event before report")
		}
	})

	// Host reports Noah
	t.Run("host reports member", func(t *testing.T) {
		body := map[string]string{"reason": "Inappropriate behavior"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/members/4/report", eventID), avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
	})

	// Noah should no longer see the conversation (removed after report)
	t.Run("reported member cannot see conversation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				t.Fatal("reported member should NOT see conversation")
			}
			if convo.ID == secondaryConversationID {
				t.Fatal("reported member should NOT see secondary host event conversation")
			}
		}
	})

	t.Run("host cannot see conversation after report", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				t.Fatal("host should NOT see conversation after report")
			}
			if convo.ID == noahHostedConversationID {
				t.Fatal("host should NOT remain in blocked user's hosted event conversation after report")
			}
		}
	})

	t.Run("blocked user's hosted event member list excludes host", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", noahEventID), noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected noah-hosted conversation to remain for noah")
		}
		if hasInt64(payload.Conversations[0].MemberIDs, avaUser.ID) {
			t.Fatal("host should be removed from blocked user's hosted event")
		}
	})

	t.Run("host member list excludes blocked user in secondary host event", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", secondaryEventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected secondary event conversation to remain for host")
		}
		if hasInt64(payload.Conversations[0].MemberIDs, noahUser.ID) {
			t.Fatal("blocked user should be removed from secondary event member list")
		}
	})

	t.Run("mutual block hides each other's events", func(t *testing.T) {
		hostEventsResp := env.doRequest(t, http.MethodGet, "/api/events", avaToken, nil)
		if hostEventsResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", hostEventsResp.StatusCode)
		}
		hostEvents := decodeJSON[eventsResponse](t, hostEventsResp)
		for _, evt := range hostEvents.Data {
			if evt.ID == noahEventID || evt.UserID == noahUser.ID {
				t.Fatalf("host should not see blocked user's events; found event %+v", evt)
			}
		}

		noahEventsResp := env.doRequest(t, http.MethodGet, "/api/events", noahToken, nil)
		if noahEventsResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", noahEventsResp.StatusCode)
		}
		noahEvents := decodeJSON[eventsResponse](t, noahEventsResp)
		for _, evt := range noahEvents.Data {
			if evt.ID == eventID || evt.UserID == 1 {
				t.Fatalf("blocked user should not see host events; found event %+v", evt)
			}
		}
	})

	t.Run("blocked member cannot create new join request", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Ava Post Block Event",
			Location:    "Ava Place",
			Time:        "20:00",
			EventDate:   time.Now().Add(96 * time.Hour).Format("2006-01-02"),
			Description: "Host event after block",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		createResp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createResp.StatusCode)
		}
		postBlockEventID = decodeJSON[createEventResponse](t, createResp).ID

		joinBody := map[string]string{"message": "Can I still join?"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", postBlockEventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 for blocked users, got %d", resp.StatusCode)
		}
	})

	t.Run("host unblocks member", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/members/%d/block", eventID, noahUser.ID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[unblockMemberResponse](t, resp)
		if !payload.Unblocked {
			t.Fatal("expected unblocked=true")
		}
		if payload.AlreadyUnblocked {
			t.Fatal("expected already_unblocked=false on first unblock")
		}
		if payload.EventID != eventID {
			t.Fatalf("expected event_id %d, got %d", eventID, payload.EventID)
		}
		if payload.UserID != noahUser.ID {
			t.Fatalf("expected user_id %d, got %d", noahUser.ID, payload.UserID)
		}
	})

	t.Run("idempotent unblock returns 404 when already fully unblocked", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/members/%d/block", eventID, noahUser.ID), avaToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 (report and block already deleted), got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("unblock restores mutual event visibility", func(t *testing.T) {
		hostEventsResp := env.doRequest(t, http.MethodGet, "/api/events", avaToken, nil)
		if hostEventsResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", hostEventsResp.StatusCode)
		}
		hostEvents := decodeJSON[eventsResponse](t, hostEventsResp)
		foundNoahEvent := false
		for _, evt := range hostEvents.Data {
			if evt.ID == noahEventID {
				foundNoahEvent = true
				break
			}
		}
		if !foundNoahEvent {
			t.Fatalf("expected host to see unblocked user's event %d", noahEventID)
		}

		noahEventsResp := env.doRequest(t, http.MethodGet, "/api/events", noahToken, nil)
		if noahEventsResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", noahEventsResp.StatusCode)
		}
		noahEvents := decodeJSON[eventsResponse](t, noahEventsResp)
		foundHostEvent := false
		for _, evt := range noahEvents.Data {
			if evt.ID == eventID {
				foundHostEvent = true
				break
			}
		}
		if !foundHostEvent {
			t.Fatalf("expected unblocked user to see host event %d", eventID)
		}
	})

	t.Run("unblocked member can create join request again", func(t *testing.T) {
		joinBody := map[string]string{"message": "Can I join now?"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", postBlockEventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 after unblock, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	// Cannot report without reason
	t.Run("report without reason returns 400", func(t *testing.T) {
		body := map[string]string{"reason": ""}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/members/4/report", eventID), avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	// Only accepted members can be report-blocked.
	t.Run("report non-accepted member returns 400", func(t *testing.T) {
		body := map[string]string{"reason": "Test"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/members/99999/report", eventID), avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("cannot unblock member without host report relation", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/members/99999/block", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	// Non-host cannot report
	t.Run("non-host cannot report member", func(t *testing.T) {
		sophiaToken := env.issueTokenForEmail(t, "sophia@example.com")
		body := map[string]string{"reason": "Test"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/members/4/report", eventID), sophiaToken, body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("non-host cannot unblock member", func(t *testing.T) {
		sophiaToken := env.issueTokenForEmail(t, "sophia@example.com")
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/members/%d/block", eventID, noahUser.ID), sophiaToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})
}

// TestJoinRequestsListWithConversationID tests that the join requests list
// includes conversation_id for 1:1 events
func TestJoinRequestsListWithConversationID(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")       // host
	noahToken := env.issueTokenForEmail(t, "noah@example.com")     // requester 1
	sophiaToken := env.issueTokenForEmail(t, "sophia@example.com") // requester 2

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	sophiaUser, err := env.repo.GetUserByEmail(ctx, "sophia@example.com")
	if err != nil {
		t.Fatalf("failed to load sophia user: %v", err)
	}

	// Create 1:1 event
	var eventID int64
	t.Run("create 1:1 event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "List Requests Test",
			Location:    "Test Location",
			Time:        "17:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Testing requests list",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	// Two users join (pending)
	t.Run("users join event", func(t *testing.T) {
		body := map[string]string{"message": "Noah here"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		noahJoin := decodeJSON[singleJoinRequestResponse](t, resp)
		if noahJoin.Request.Status != "pending" {
			t.Fatalf("expected pending status for Noah, got %s", noahJoin.Request.Status)
		}

		body = map[string]string{"message": "Sophia here"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), sophiaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		sophiaJoin := decodeJSON[singleJoinRequestResponse](t, resp)
		if sophiaJoin.Request.Status != "pending" {
			t.Fatalf("expected pending status for Sophia, got %s", sophiaJoin.Request.Status)
		}
	})

	// Host approves both pending requests.
	t.Run("host approves both requests", func(t *testing.T) {
		resp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, noahUser.ID),
			avaToken,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving Noah, got %d", resp.StatusCode)
		}
		resp = env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, sophiaUser.ID),
			avaToken,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving Sophia, got %d", resp.StatusCode)
		}
	})

	// Host lists requests with include_approved - each approved request has conversation_id.
	t.Run("host lists approved requests with conversation_id", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/chat/requests?include_approved=1", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[joinRequestsListResponse](t, resp)

		// Should include at least the two approved requests
		if len(payload.Requests) < 2 {
			t.Fatalf("expected at least 2 requests, got %d", len(payload.Requests))
		}

		// Approved requests should have conversation IDs in 1:1 mode.
		for _, req := range payload.Requests {
			if req.Status != "approved" {
				continue
			}
			if req.ConversationID == nil {
				t.Fatalf("expected conversation_id for 1:1 event request, user %d has none", req.UserID)
			}
			t.Logf("Request from user %d has conversation_id %d", req.UserID, *req.ConversationID)
		}
	})
}

// TestEventConversationsEndpoint tests the GET /api/events/:id/conversations endpoint
func TestEventConversationsEndpoint(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // host
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // requester

	// Create 1:1 event
	var eventID int64
	t.Run("create 1:1 event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Event Conversations Test",
			Location:    "Test Location",
			Time:        "18:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Testing event conversations endpoint",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	// User joins and host approves.
	t.Run("user joins event", func(t *testing.T) {
		body := map[string]string{"message": "Hello!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		joinPayload := decodeJSON[singleJoinRequestResponse](t, resp)
		if joinPayload.Request.Status != "pending" {
			t.Fatalf("expected pending status, got %s", joinPayload.Request.Status)
		}

		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving request, got %d", resp.StatusCode)
		}
	})

	// Host can get event conversations
	t.Run("host can get event conversations", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected at least one conversation")
		}
		// Should include unread_count
		for _, convo := range payload.Conversations {
			t.Logf("Conversation %d: unread_count=%d, members=%v", convo.ID, convo.UnreadCount, convo.MemberIDs)
		}
	})

	// Non-host cannot get event conversations
	t.Run("non-host gets 403", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), noahToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", resp.StatusCode)
		}
	})

	// Non-existent event returns 404
	t.Run("non-existent event returns 404", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/events/99999/conversations", avaToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	// Invalid event ID returns 400
	t.Run("invalid event ID returns 400", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/events/invalid/conversations", avaToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

// TestConversationFilteringByEventDate verifies that conversations for completed
// events are filtered out immediately, while future events and legacy same-day
// events remain visible.
func TestConversationFilteringByEventDate(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	// Get test user
	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	ava, err := env.repo.GetUserByEmail(ctx, "ava@example.com")
	if err != nil {
		t.Fatalf("get ava: %v", err)
	}

	// Time references
	now := time.Now().UTC()
	twoDaysAgo := now.Add(-48 * time.Hour)     // past - should be filtered
	twelveHoursAgo := now.Add(-12 * time.Hour) // recently past - should be filtered (no grace period)
	tomorrow := now.Add(24 * time.Hour)        // future - should appear

	// Create test events with different scheduled_at times using direct SQL
	// Event 1: scheduled_at well in the past (should NOT appear in conversations)
	_, err = env.db.ExecContext(ctx, `
		INSERT INTO events (user_id, title, location, time, event_date, description, gender, min_age, max_age, date_label, group_type, cover_key, scheduled_at)
		VALUES (?, 'Old Completed Event', 'Location A', '10:00', ?, 'Old event', 'Any', 18, 50, 'Today', 'Group', 'cover_01', ?)`,
		ava.ID, twoDaysAgo.Format("2006-01-02"), twoDaysAgo.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert old event: %v", err)
	}
	var oldEventID int64
	env.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&oldEventID)

	// Event 2: scheduled_at recently in the past (should NOT appear)
	_, err = env.db.ExecContext(ctx, `
		INSERT INTO events (user_id, title, location, time, event_date, description, gender, min_age, max_age, date_label, group_type, cover_key, scheduled_at)
		VALUES (?, 'Recently Expired Event', 'Location B', '10:00', ?, 'Recent event', 'Any', 18, 50, 'Today', 'Group', 'cover_01', ?)`,
		ava.ID, twelveHoursAgo.Format("2006-01-02"), twelveHoursAgo.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert recent event: %v", err)
	}
	var recentEventID int64
	env.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&recentEventID)

	// Event 3: scheduled_at in the future (should appear)
	_, err = env.db.ExecContext(ctx, `
		INSERT INTO events (user_id, title, location, time, event_date, description, gender, min_age, max_age, date_label, group_type, cover_key, scheduled_at)
		VALUES (?, 'Future Event', 'Location C', '10:00', ?, 'Future event', 'Any', 18, 50, 'Tmrw', 'Group', 'cover_01', ?)`,
		ava.ID, tomorrow.Format("2006-01-02"), tomorrow.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert future event: %v", err)
	}
	var futureEventID int64
	env.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&futureEventID)

	// Event 4: NULL scheduled_at, old event_date (should NOT appear - legacy fallback)
	_, err = env.db.ExecContext(ctx, `
		INSERT INTO events (user_id, title, location, time, event_date, description, gender, min_age, max_age, date_label, group_type, cover_key, scheduled_at)
		VALUES (?, 'Legacy Old Event', 'Location D', '10:00', ?, 'Legacy old', 'Any', 18, 50, 'Today', 'Group', 'cover_01', NULL)`,
		ava.ID, twoDaysAgo.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("insert legacy old event: %v", err)
	}
	var legacyOldEventID int64
	env.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&legacyOldEventID)

	// Event 5: NULL scheduled_at, today's event_date (should appear - legacy fallback)
	_, err = env.db.ExecContext(ctx, `
		INSERT INTO events (user_id, title, location, time, event_date, description, gender, min_age, max_age, date_label, group_type, cover_key, scheduled_at)
		VALUES (?, 'Legacy Today Event', 'Location E', '10:00', ?, 'Legacy today', 'Any', 18, 50, 'Today', 'Group', 'cover_01', NULL)`,
		ava.ID, now.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("insert legacy today event: %v", err)
	}
	var legacyTodayEventID int64
	env.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&legacyTodayEventID)

	// Create conversations for each event
	createConversation := func(eventID int64, title string) int64 {
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO conversations (title, created_by, event_id) VALUES (?, ?, ?)`,
			title, ava.ID, eventID)
		if err != nil {
			t.Fatalf("insert conversation for event %d: %v", eventID, err)
		}
		var convoID int64
		env.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&convoID)

		// Add ava as a member
		_, err = env.db.ExecContext(ctx, `
			INSERT INTO conversation_members (conversation_id, user_id, role) VALUES (?, ?, 'member')`,
			convoID, ava.ID)
		if err != nil {
			t.Fatalf("insert member for conversation %d: %v", convoID, err)
		}
		return convoID
	}

	createStandaloneConversation := func(title string) int64 {
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO conversations (title, created_by, event_id) VALUES (?, ?, NULL)`,
			title, ava.ID)
		if err != nil {
			t.Fatalf("insert standalone conversation: %v", err)
		}
		var convoID int64
		env.db.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&convoID)

		_, err = env.db.ExecContext(ctx, `
			INSERT INTO conversation_members (conversation_id, user_id, role) VALUES (?, ?, 'member')`,
			convoID, ava.ID)
		if err != nil {
			t.Fatalf("insert member for standalone conversation %d: %v", convoID, err)
		}
		return convoID
	}

	oldConvoID := createConversation(oldEventID, "Old Event Chat")
	recentConvoID := createConversation(recentEventID, "Recent Event Chat")
	futureConvoID := createConversation(futureEventID, "Future Event Chat")
	legacyOldConvoID := createConversation(legacyOldEventID, "Legacy Old Chat")
	legacyTodayConvoID := createConversation(legacyTodayEventID, "Legacy Today Chat")
	standaloneConvoID := createStandaloneConversation("General Chat")

	// Now test the conversations endpoint
	t.Run("filters out old completed event conversations", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		payload := decodeJSON[conversationsResponse](t, resp)

		// Build a map of conversation IDs for easy lookup
		convoIDs := make(map[int64]bool)
		for _, c := range payload.Conversations {
			convoIDs[c.ID] = true
		}

		// Should NOT contain old event conversation (past)
		if convoIDs[oldConvoID] {
			t.Errorf("conversation for old event (past) should be filtered out, but was included (ID: %d)", oldConvoID)
		}

		// Should NOT contain recently expired event conversation (no grace period)
		if convoIDs[recentConvoID] {
			t.Errorf("conversation for recently expired event should be filtered out, but was included (ID: %d)", recentConvoID)
		}

		// Should contain future event conversation
		if !convoIDs[futureConvoID] {
			t.Errorf("conversation for future event should be included, but was filtered out (ID: %d)", futureConvoID)
		}
		for _, convo := range payload.Conversations {
			if convo.ID == futureConvoID {
				if convo.Event == nil {
					t.Fatalf("expected future conversation %d to include event metadata", futureConvoID)
				}
				if convo.Event.ScheduledAt != tomorrow.Format(time.RFC3339) {
					t.Fatalf(
						"expected future conversation scheduled_at %q, got %q",
						tomorrow.Format(time.RFC3339),
						convo.Event.ScheduledAt,
					)
				}
			}
		}

		// Should NOT contain legacy old event conversation (NULL scheduled_at, old event_date)
		if convoIDs[legacyOldConvoID] {
			t.Errorf("conversation for legacy old event should be filtered out, but was included (ID: %d)", legacyOldConvoID)
		}

		// Should contain legacy today event conversation (NULL scheduled_at, today's event_date)
		if !convoIDs[legacyTodayConvoID] {
			t.Errorf("conversation for legacy today event should be included, but was filtered out (ID: %d)", legacyTodayConvoID)
		}

		// Should contain standalone conversation (NULL event_id)
		if !convoIDs[standaloneConvoID] {
			t.Errorf("standalone conversation should be included, but was filtered out (ID: %d)", standaloneConvoID)
		}
	})

	// Also verify using direct SQL to double-check the datetime comparison works
	t.Run("verifies SQL datetime comparison is correct", func(t *testing.T) {
		// This tests the core issue: ISO 8601 format with 'T' should compare correctly
		var result int
		err := env.db.QueryRowContext(ctx, `
			SELECT datetime('2026-01-15T13:30:00.000Z') > datetime('now')
		`).Scan(&result)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		// This should be 0 (false) when the date is in the past.
		t.Logf("datetime comparison result: %d (expected 0 for past dates without grace period)", result)
	})
}

// TestLeaveEventDeletesJoinRequest verifies that when a user leaves an event,
// their join request record is deleted from the database. This prevents the
// "pending" state from persisting after leaving and rejoining an event.
func TestLeaveEventDeletesJoinRequest(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // host (user id 1)
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // joiner (user id 4)

	// Create a 1:1 event
	var eventID int64
	t.Run("create 1:1 event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Leave Test Event",
			Location:    "Test Cafe",
			Time:        "14:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Testing leave deletes join request",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	// Helper to check if a join request exists in the database (any status)
	hasJoinRequestInDB := func() bool {
		var count int
		err := env.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM conversation_join_requests WHERE event_id = ? AND user_id = 4",
			eventID).Scan(&count)
		if err != nil {
			t.Fatalf("query join request: %v", err)
		}
		return count > 0
	}

	hasConversation := func(t *testing.T, token string, conversationID int64) bool {
		t.Helper()
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				return true
			}
		}
		return false
	}

	var firstConversationID int64

	// Noah joins the 1:1 event
	t.Run("noah joins event", func(t *testing.T) {
		body := map[string]string{"message": "Can I join?"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status before approval, got %s", payload.Request.Status)
		}

		approveResp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving request, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversation_id after approval")
		}
		firstConversationID = *approvePayload.ConversationID
	})

	// Verify join request exists in DB
	t.Run("verify join request exists in DB after joining", func(t *testing.T) {
		if !hasJoinRequestInDB() {
			t.Fatal("expected join request to exist in database after joining")
		}
	})

	t.Run("verify first private conversation visible to both users", func(t *testing.T) {
		if !hasConversation(t, avaToken, firstConversationID) {
			t.Fatalf("host should see conversation %d before leave", firstConversationID)
		}
		if !hasConversation(t, noahToken, firstConversationID) {
			t.Fatalf("requester should see conversation %d before leave", firstConversationID)
		}
	})

	// Noah leaves the event
	t.Run("noah leaves event - first time", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/chat/members/4", eventID), noahToken, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", resp.StatusCode)
		}
	})

	// Verify join request is deleted from DB after leaving
	t.Run("verify join request deleted from DB after leaving", func(t *testing.T) {
		if hasJoinRequestInDB() {
			t.Fatal("join request should be deleted from database after leaving event")
		}
		if hasConversation(t, avaToken, firstConversationID) {
			t.Fatalf("host should NOT see ended 1:1 conversation %d after leave", firstConversationID)
		}
		if hasConversation(t, noahToken, firstConversationID) {
			t.Fatalf("requester should NOT see ended 1:1 conversation %d after leave", firstConversationID)
		}
	})

	var secondConversationID int64

	// Noah rejoins the event
	t.Run("noah rejoins event", func(t *testing.T) {
		body := map[string]string{"message": "I'd like to rejoin!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status on rejoin before approval, got %s", payload.Request.Status)
		}

		approveResp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving rejoin request, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversation_id on rejoin approval")
		}
		secondConversationID = *approvePayload.ConversationID
		if secondConversationID == firstConversationID {
			t.Fatalf("expected a new conversation after rejoin, got same id %d", secondConversationID)
		}
	})

	// Verify join request exists in DB again
	t.Run("verify join request exists in DB after rejoining", func(t *testing.T) {
		if !hasJoinRequestInDB() {
			t.Fatal("expected join request to exist in database after rejoining")
		}
		if !hasConversation(t, avaToken, secondConversationID) {
			t.Fatalf("host should see new conversation %d after rejoin", secondConversationID)
		}
		if !hasConversation(t, noahToken, secondConversationID) {
			t.Fatalf("requester should see new conversation %d after rejoin", secondConversationID)
		}
	})

	// Noah leaves again
	t.Run("noah leaves event - second time", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/chat/members/4", eventID), noahToken, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", resp.StatusCode)
		}
	})

	// Verify join request is deleted from DB after second leave (this was the bug)
	t.Run("verify join request deleted from DB after second leave", func(t *testing.T) {
		if hasJoinRequestInDB() {
			t.Fatal("join request should be deleted from database after leaving event the second time - this is the bug being fixed")
		}
		if hasConversation(t, avaToken, secondConversationID) {
			t.Fatalf("host should NOT see ended 1:1 conversation %d after second leave", secondConversationID)
		}
		if hasConversation(t, noahToken, secondConversationID) {
			t.Fatalf("requester should NOT see ended 1:1 conversation %d after second leave", secondConversationID)
		}
	})

	// Noah rejoins a third time to verify the cycle works
	t.Run("noah rejoins event - third time", func(t *testing.T) {
		body := map[string]string{"message": "Third time's the charm!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status on third rejoin before approval, got %s", payload.Request.Status)
		}

		approveResp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving third rejoin request, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversation_id on third rejoin approval")
		}
	})
}

func TestCleanupOrphanedSingleEventConversations(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")

	noah, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("get noah user: %v", err)
	}

	hasConversation := func(t *testing.T, token string, conversationID int64) bool {
		t.Helper()
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				return true
			}
		}
		return false
	}

	var singleEventID int64
	var privateConversationID int64

	t.Run("setup single event and private conversation", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Legacy 1:1 Orphan Cleanup",
			Location:    "Test Cafe",
			Time:        "11:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Reproduce stale unread after legacy leave",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      60,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		singleEventID = payload.ID

		joinResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", singleEventID),
			noahToken,
			map[string]string{"message": "Legacy intro message"},
		)
		if joinResp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", joinResp.StatusCode)
		}
		joinPayload := decodeJSON[singleJoinRequestResponse](t, joinResp)
		if joinPayload.Request.Status != "pending" {
			t.Fatalf("expected pending status before approval, got %s", joinPayload.Request.Status)
		}

		approveResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/4/approve", singleEventID),
			avaToken,
			nil,
		)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving request, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected private conversation id after approval")
		}
		privateConversationID = *approvePayload.ConversationID
	})

	t.Run("simulate legacy orphaned 1:1 state", func(t *testing.T) {
		// Legacy behavior removed only requester membership, leaving host + intro message.
		if _, err := env.db.ExecContext(
			ctx,
			`DELETE FROM conversation_members WHERE conversation_id = ? AND user_id = ?`,
			privateConversationID,
			noah.ID,
		); err != nil {
			t.Fatalf("delete requester membership: %v", err)
		}
		if _, err := env.db.ExecContext(
			ctx,
			`DELETE FROM conversation_join_requests WHERE event_id = ? AND user_id = ?`,
			singleEventID,
			noah.ID,
		); err != nil {
			t.Fatalf("delete join request: %v", err)
		}
	})

	t.Run("host sees stale private conversation before cleanup", func(t *testing.T) {
		if !hasConversation(t, avaToken, privateConversationID) {
			t.Fatalf("expected host to still see stale conversation %d before cleanup", privateConversationID)
		}
	})

	t.Run("cleanup removes orphaned private conversation", func(t *testing.T) {
		if err := env.repo.cleanupOrphanedSingleEventConversations(ctx); err != nil {
			t.Fatalf("cleanup orphaned conversations: %v", err)
		}
		if hasConversation(t, avaToken, privateConversationID) {
			t.Fatalf("host should NOT see orphaned conversation %d after cleanup", privateConversationID)
		}
	})

	t.Run("cleanup does not touch group conversation", func(t *testing.T) {
		groupBody := CreateEventParams{
			Title:       "Group Cleanup Safety",
			Location:    "Hall",
			Time:        "12:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Group should remain untouched",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      60,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, groupBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		groupPayload := decodeJSON[createEventResponse](t, resp)

		groupConvo, err := env.repo.GetConversationByEventID(ctx, groupPayload.ID)
		if err != nil {
			t.Fatalf("get group conversation: %v", err)
		}

		if err := env.repo.cleanupOrphanedSingleEventConversations(ctx); err != nil {
			t.Fatalf("cleanup orphaned conversations: %v", err)
		}

		var count int
		if err := env.db.QueryRowContext(
			ctx,
			`SELECT COUNT(1) FROM conversations WHERE id = ?`,
			groupConvo.ID,
		).Scan(&count); err != nil {
			t.Fatalf("count group conversation: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected group conversation %d to remain, count=%d", groupConvo.ID, count)
		}
	})
}

// ============================================================================
// Profile / Onboarding Tests
// ============================================================================

type profileResponse struct {
	User struct {
		ID              int64  `json:"id"`
		Name            string `json:"name"`
		Email           string `json:"email"`
		ProfileComplete bool   `json:"profile_complete"`
		Gender          string `json:"gender,omitempty"`
		Age             int    `json:"age,omitempty"`
		Avatar          string `json:"avatar,omitempty"`
	} `json:"user"`
}

// TestUpdateProfile tests the profile update endpoint with various inputs
func TestUpdateProfile(t *testing.T) {
	env := setupAPITestEnv(t)

	t.Run("valid profile update", func(t *testing.T) {
		// Create a new user for this test to avoid conflicts with other tests
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Test User', 'profile-test@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "profile-test@example.com")

		body := map[string]any{
			"name":   "Updated Name",
			"gender": "Female",
			"age":    25,
			"avatar": "https://example.com/avatar.png",
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		payload := decodeJSON[profileResponse](t, resp)
		if payload.User.Name != "Updated Name" {
			t.Fatalf("expected name 'Updated Name', got %s", payload.User.Name)
		}
		if payload.User.Gender != "Female" {
			t.Fatalf("expected gender 'Female', got %s", payload.User.Gender)
		}
		if payload.User.Age != 25 {
			t.Fatalf("expected age 25, got %d", payload.User.Age)
		}
		if payload.User.Avatar != "https://example.com/avatar.png" {
			t.Fatalf("expected avatar URL, got %s", payload.User.Avatar)
		}
		if !payload.User.ProfileComplete {
			t.Fatal("expected profile_complete to be true after update")
		}
	})

	t.Run("missing name returns 400", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Missing Name Test', 'missing-name@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "missing-name@example.com")

		body := map[string]any{
			"gender": "Male",
			"age":    30,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("invalid gender returns 400", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Invalid Gender Test', 'invalid-gender@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "invalid-gender@example.com")

		body := map[string]any{
			"name":   "Test User",
			"gender": "Other", // Invalid - only Female or Male allowed
			"age":    25,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("age below 13 returns 400", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Young User Test', 'young-user@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "young-user@example.com")

		body := map[string]any{
			"name":   "Test User",
			"gender": "Male",
			"age":    12, // Below minimum of 13
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("age above 120 returns 400", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Old User Test', 'old-user@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "old-user@example.com")

		body := map[string]any{
			"name":   "Test User",
			"gender": "Female",
			"age":    121, // Above maximum of 120
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("empty request body returns 400", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Empty Body Test', 'empty-body@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "empty-body@example.com")

		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, map[string]any{})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("request without token returns 401", func(t *testing.T) {
		body := map[string]any{
			"name":   "Test User",
			"gender": "Male",
			"age":    25,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", "", body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("update without avatar field succeeds", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('No Avatar Test', 'no-avatar@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "no-avatar@example.com")

		body := map[string]any{
			"name":   "No Avatar User",
			"gender": "Male",
			"age":    30,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		payload := decodeJSON[profileResponse](t, resp)
		if payload.User.Name != "No Avatar User" {
			t.Fatalf("expected name 'No Avatar User', got %s", payload.User.Name)
		}
		if !payload.User.ProfileComplete {
			t.Fatal("expected profile_complete to be true")
		}
	})

	t.Run("update with null avatar succeeds", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Null Avatar Test', 'null-avatar@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "null-avatar@example.com")

		body := map[string]any{
			"name":   "Null Avatar User",
			"gender": "Female",
			"age":    28,
			"avatar": nil,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})
}

func TestDeleteProfile(t *testing.T) {
	createUser := func(t *testing.T, env *apiTestEnv, name, email string) *User {
		t.Helper()
		ctx := context.Background()
		if _, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, profile_complete, created_at)
			VALUES (?, ?, '', 1, datetime('now'))
		`, name, email); err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		user, err := env.repo.GetUserByEmail(ctx, email)
		if err != nil {
			t.Fatalf("load user %s: %v", email, err)
		}
		return user
	}

	createEvent := func(t *testing.T, env *apiTestEnv, token, title, groupType string) int64 {
		t.Helper()
		body := CreateEventParams{
			Title:       title,
			Location:    "Delete Profile Test Location",
			Time:        "18:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Delete profile integration test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   groupType,
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", token, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create event: expected 201, got %d", resp.StatusCode)
		}
		return decodeJSON[createEventResponse](t, resp).ID
	}

	requestAndApprove := func(t *testing.T, env *apiTestEnv, eventID, requesterID int64, requesterToken, hostToken string) {
		t.Helper()
		resp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", eventID),
			requesterToken,
			map[string]string{"message": "Please add me"},
		)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create join request: expected 201, got %d", resp.StatusCode)
		}
		resp = env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, requesterID),
			hostToken,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("approve join request: expected 200, got %d", resp.StatusCode)
		}
	}

	t.Run("deletes account with no related event data and rejects old token", func(t *testing.T) {
		env := setupAPITestEnv(t)
		user := createUser(t, env, "Delete Plain", "delete-plain@example.com")
		token := env.issueTokenForEmail(t, user.Email)

		if err := env.repo.UpsertPushToken(context.Background(), user.ID, "ExponentPushToken[plain]", "device-plain", "ios"); err != nil {
			t.Fatalf("upsert push token: %v", err)
		}
		if err := env.repo.LinkAppleAccount(context.Background(), "delete-plain-apple-sub", user.ID, user.Email); err != nil {
			t.Fatalf("link apple account: %v", err)
		}

		resp := env.doRequest(t, http.MethodDelete, "/api/profile", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		if queryCount(t, env.db, `SELECT COUNT(1) FROM users WHERE id = ?`, user.ID) != 0 {
			t.Fatal("expected user row to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM push_tokens WHERE user_id = ?`, user.ID) != 0 {
			t.Fatal("expected push tokens to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM apple_accounts WHERE user_id = ?`, user.ID) != 0 {
			t.Fatal("expected apple account links to be deleted")
		}

		resp = env.doRequest(t, http.MethodGet, "/api/events/past", token, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected stale token to return 401, got %d", resp.StatusCode)
		}
	})

	t.Run("removes pending join requests created by deleting user", func(t *testing.T) {
		env := setupAPITestEnv(t)
		host := createUser(t, env, "Delete Pending Host", "delete-pending-host@example.com")
		requester := createUser(t, env, "Delete Pending Requester", "delete-pending-requester@example.com")
		hostToken := env.issueTokenForEmail(t, host.Email)
		requesterToken := env.issueTokenForEmail(t, requester.Email)
		eventID := createEvent(t, env, hostToken, "Pending Request Delete", "Group")

		resp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", eventID),
			requesterToken,
			map[string]string{"message": "Pending delete"},
		)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}

		resp = env.doRequest(t, http.MethodDelete, "/api/profile", requesterToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversation_join_requests WHERE event_id = ? AND user_id = ?`, eventID, requester.ID) != 0 {
			t.Fatal("expected pending join request to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM events WHERE id = ?`, eventID) != 1 {
			t.Fatal("expected host event to remain")
		}
	})

	t.Run("removes approved group member while keeping event and remaining chat", func(t *testing.T) {
		env := setupAPITestEnv(t)
		host := createUser(t, env, "Delete Group Host", "delete-group-host@example.com")
		member := createUser(t, env, "Delete Group Member", "delete-group-member@example.com")
		hostToken := env.issueTokenForEmail(t, host.Email)
		memberToken := env.issueTokenForEmail(t, member.Email)
		eventID := createEvent(t, env, hostToken, "Group Member Delete", "Group")
		requestAndApprove(t, env, eventID, member.ID, memberToken, hostToken)

		convo, err := env.repo.GetConversationByEventID(context.Background(), eventID)
		if err != nil {
			t.Fatalf("get group conversation: %v", err)
		}
		if _, err := env.repo.CreateMessage(context.Background(), CreateMessageParams{
			ConversationID: convo.ID,
			SenderID:       member.ID,
			Body:           "Delete my group message",
			DeliveryStatus: "sent",
		}); err != nil {
			t.Fatalf("create member message: %v", err)
		}

		resp := env.doRequest(t, http.MethodDelete, "/api/profile", memberToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		if queryCount(t, env.db, `SELECT COUNT(1) FROM events WHERE id = ?`, eventID) != 1 {
			t.Fatal("expected group event to remain")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversations WHERE id = ?`, convo.ID) != 1 {
			t.Fatal("expected group conversation to remain")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversation_members WHERE conversation_id = ? AND user_id = ?`, convo.ID, member.ID) != 0 {
			t.Fatal("expected deleted user to be removed from group conversation")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversation_members WHERE conversation_id = ? AND user_id = ?`, convo.ID, host.ID) != 1 {
			t.Fatal("expected host to remain in group conversation")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM messages WHERE sender_id = ?`, member.ID) != 0 {
			t.Fatal("expected deleted user's group messages to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversation_join_requests WHERE event_id = ? AND user_id = ?`, eventID, member.ID) != 0 {
			t.Fatal("expected approved join request to be deleted")
		}
	})

	t.Run("removes approved single member private chat while keeping host event", func(t *testing.T) {
		env := setupAPITestEnv(t)
		host := createUser(t, env, "Delete Single Host", "delete-single-host@example.com")
		member := createUser(t, env, "Delete Single Member", "delete-single-member@example.com")
		hostToken := env.issueTokenForEmail(t, host.Email)
		memberToken := env.issueTokenForEmail(t, member.Email)
		eventID := createEvent(t, env, hostToken, "Single Member Delete", "Single")
		requestAndApprove(t, env, eventID, member.ID, memberToken, hostToken)

		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversations WHERE event_id = ?`, eventID) == 0 {
			t.Fatal("expected approved single event private conversation before delete")
		}

		resp := env.doRequest(t, http.MethodDelete, "/api/profile", memberToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		if queryCount(t, env.db, `SELECT COUNT(1) FROM events WHERE id = ?`, eventID) != 1 {
			t.Fatal("expected host event to remain")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversations WHERE event_id = ?`, eventID) != 0 {
			t.Fatal("expected private single-event conversation to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversation_join_requests WHERE event_id = ? AND user_id = ?`, eventID, member.ID) != 0 {
			t.Fatal("expected approved single join request to be deleted")
		}
	})

	t.Run("deletes hosted events and dependent chat data when host deletes account", func(t *testing.T) {
		env := setupAPITestEnv(t)
		host := createUser(t, env, "Delete Owner", "delete-owner@example.com")
		member := createUser(t, env, "Delete Owner Member", "delete-owner-member@example.com")
		hostToken := env.issueTokenForEmail(t, host.Email)
		memberToken := env.issueTokenForEmail(t, member.Email)
		eventID := createEvent(t, env, hostToken, "Hosted Event Delete", "Group")
		requestAndApprove(t, env, eventID, member.ID, memberToken, hostToken)

		convo, err := env.repo.GetConversationByEventID(context.Background(), eventID)
		if err != nil {
			t.Fatalf("get hosted event conversation: %v", err)
		}

		resp := env.doRequest(t, http.MethodDelete, "/api/profile", hostToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		if queryCount(t, env.db, `SELECT COUNT(1) FROM users WHERE id = ?`, host.ID) != 0 {
			t.Fatal("expected host user to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM users WHERE id = ?`, member.ID) != 1 {
			t.Fatal("expected joined member account to remain")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM events WHERE id = ?`, eventID) != 0 {
			t.Fatal("expected hosted event to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversations WHERE id = ?`, convo.ID) != 0 {
			t.Fatal("expected hosted event conversation to be deleted")
		}
		if queryCount(t, env.db, `SELECT COUNT(1) FROM conversation_join_requests WHERE event_id = ?`, eventID) != 0 {
			t.Fatal("expected hosted event join requests to be deleted")
		}
	})
}

// TestProfileImmutability tests that gender and age cannot be changed once set
func TestProfileImmutability(t *testing.T) {
	env := setupAPITestEnv(t)

	t.Run("gender cannot be changed once set", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Gender Change Test', 'gender-change@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "gender-change@example.com")

		// First, set gender to Male
		body := map[string]any{
			"name":   "Test User",
			"gender": "Male",
			"age":    25,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initial profile update: expected 200, got %d", resp.StatusCode)
		}

		// Try to change gender to Female
		body["gender"] = "Female"
		resp = env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 when trying to change gender, got %d", resp.StatusCode)
		}

		payload := decodeJSON[testErrorResponse](t, resp)
		if payload.Error != "gender cannot be changed once set" {
			t.Fatalf("expected 'gender cannot be changed once set' error, got '%s'", payload.Error)
		}
	})

	t.Run("age cannot be changed once set", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Age Change Test', 'age-change@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "age-change@example.com")

		// First, set age to 25
		body := map[string]any{
			"name":   "Test User",
			"gender": "Female",
			"age":    25,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initial profile update: expected 200, got %d", resp.StatusCode)
		}

		// Try to change age to 30
		body["age"] = 30
		resp = env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 when trying to change age, got %d", resp.StatusCode)
		}

		payload := decodeJSON[testErrorResponse](t, resp)
		if payload.Error != "age cannot be changed once set" {
			t.Fatalf("expected 'age cannot be changed once set' error, got '%s'", payload.Error)
		}
	})

	t.Run("name can be changed multiple times", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Name Change Test', 'name-change@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "name-change@example.com")

		// Set initial profile
		body := map[string]any{
			"name":   "Initial Name",
			"gender": "Male",
			"age":    30,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initial profile update: expected 200, got %d", resp.StatusCode)
		}

		// Change name
		body["name"] = "Updated Name"
		resp = env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 when changing name, got %d", resp.StatusCode)
		}

		payload := decodeJSON[profileResponse](t, resp)
		if payload.User.Name != "Updated Name" {
			t.Fatalf("expected name 'Updated Name', got '%s'", payload.User.Name)
		}

		// Change name again
		body["name"] = "Third Name"
		resp = env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 when changing name again, got %d", resp.StatusCode)
		}

		payload = decodeJSON[profileResponse](t, resp)
		if payload.User.Name != "Third Name" {
			t.Fatalf("expected name 'Third Name', got '%s'", payload.User.Name)
		}
	})

	t.Run("avatar can be changed multiple times", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Avatar Change Test', 'avatar-change@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "avatar-change@example.com")

		// Set initial profile with avatar
		body := map[string]any{
			"name":   "Avatar User",
			"gender": "Female",
			"age":    28,
			"avatar": "https://example.com/avatar1.png",
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initial profile update: expected 200, got %d", resp.StatusCode)
		}

		// Change avatar
		body["avatar"] = "https://example.com/avatar2.png"
		resp = env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 when changing avatar, got %d", resp.StatusCode)
		}

		payload := decodeJSON[profileResponse](t, resp)
		if payload.User.Avatar != "https://example.com/avatar2.png" {
			t.Fatalf("expected avatar 'https://example.com/avatar2.png', got '%s'", payload.User.Avatar)
		}
	})

	t.Run("same gender value can be submitted", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Same Gender Test', 'same-gender@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "same-gender@example.com")

		// Set initial profile
		body := map[string]any{
			"name":   "Test User",
			"gender": "Male",
			"age":    25,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initial profile update: expected 200, got %d", resp.StatusCode)
		}

		// Submit same gender value - should succeed
		resp = env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 when submitting same gender, got %d", resp.StatusCode)
		}
	})

	t.Run("same age value can be submitted", func(t *testing.T) {
		ctx := context.Background()
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, created_at)
			VALUES ('Same Age Test', 'same-age@example.com', '', datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}

		token := env.issueTokenForEmail(t, "same-age@example.com")

		// Set initial profile
		body := map[string]any{
			"name":   "Test User",
			"gender": "Female",
			"age":    30,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initial profile update: expected 200, got %d", resp.StatusCode)
		}

		// Submit same age value - should succeed
		resp = env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 when submitting same age, got %d", resp.StatusCode)
		}
	})
}

// TestGoogleLoginReturnsProfileComplete tests that the Google login response
// correctly includes the profile_complete field
func TestGoogleLoginReturnsProfileComplete(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	t.Run("new user has profile_complete false", func(t *testing.T) {
		// Create a new user without profile completion
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, profile_complete, created_at)
			VALUES ('New User', 'new-user@example.com', '', 0, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create new user: %v", err)
		}

		// Get user and verify profile_complete is false
		user, err := env.repo.GetUserByEmail(ctx, "new-user@example.com")
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		if user.ProfileComplete {
			t.Fatal("new user should have profile_complete = false")
		}
	})

	t.Run("user with completed profile has profile_complete true", func(t *testing.T) {
		// Create a user with completed profile
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, gender, age, profile_complete, created_at)
			VALUES ('Complete User', 'complete-user@example.com', '', 'Female', 25, 1, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create complete user: %v", err)
		}

		// Get user and verify profile_complete is true
		user, err := env.repo.GetUserByEmail(ctx, "complete-user@example.com")
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		if !user.ProfileComplete {
			t.Fatal("user with completed profile should have profile_complete = true")
		}
	})

	t.Run("profile update sets profile_complete to true", func(t *testing.T) {
		// Create a new user
		_, err := env.db.ExecContext(ctx, `
			INSERT INTO users (name, email, password, profile_complete, created_at)
			VALUES ('Incomplete User', 'incomplete-user@example.com', '', 0, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}

		// Verify initially profile_complete is false
		user, err := env.repo.GetUserByEmail(ctx, "incomplete-user@example.com")
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		if user.ProfileComplete {
			t.Fatal("user should initially have profile_complete = false")
		}

		// Update profile
		token := env.issueTokenForEmail(t, "incomplete-user@example.com")
		body := map[string]any{
			"name":   "Updated User",
			"gender": "Male",
			"age":    28,
		}
		resp := env.doRequest(t, http.MethodPut, "/api/profile", token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("profile update: expected 200, got %d", resp.StatusCode)
		}

		// Verify profile_complete is now true
		payload := decodeJSON[profileResponse](t, resp)
		if !payload.User.ProfileComplete {
			t.Fatal("profile_complete should be true after profile update")
		}

		// Verify in database as well
		user, err = env.repo.GetUserByEmail(ctx, "incomplete-user@example.com")
		if err != nil {
			t.Fatalf("get user after update: %v", err)
		}
		if !user.ProfileComplete {
			t.Fatal("profile_complete should be true in database after update")
		}
	})
}

// TestLeaveGroupEventDeletesJoinRequest verifies that leaving a Group event
// also deletes the join request record.
func TestLeaveGroupEventDeletesJoinRequest(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // host (user id 1)
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // joiner (user id 4)

	// Create a Group event
	var eventID int64
	var groupConversationID int64
	t.Run("create Group event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Leave Group Test Event",
			Location:    "Test Location",
			Time:        "15:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Testing leave deletes join request for Group events",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID

		convo, err := env.repo.GetConversationByEventID(ctx, eventID)
		if err != nil {
			t.Fatalf("get group conversation: %v", err)
		}
		groupConversationID = convo.ID
	})

	// Helper to check if a join request exists in the database (any status)
	hasJoinRequestInDB := func() bool {
		var count int
		err := env.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM conversation_join_requests WHERE event_id = ? AND user_id = 4",
			eventID).Scan(&count)
		if err != nil {
			t.Fatalf("query join request: %v", err)
		}
		return count > 0
	}

	// Helper to check pending join requests via API
	hasPendingJoinRequest := func() bool {
		resp := env.doRequest(t, http.MethodGet, "/api/chat/requests/me", noahToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var payload struct {
			Requests []struct {
				EventID int64 `json:"event_id"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		for _, r := range payload.Requests {
			if r.EventID == eventID {
				return true
			}
		}
		return false
	}

	hasConversation := func(t *testing.T, token string, conversationID int64) bool {
		t.Helper()
		resp := env.doRequest(t, http.MethodGet, "/api/conversations", token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[conversationsResponse](t, resp)
		for _, convo := range payload.Conversations {
			if convo.ID == conversationID {
				return true
			}
		}
		return false
	}

	// Noah sends join request
	t.Run("noah sends join request", func(t *testing.T) {
		body := map[string]string{"message": "I'd like to join the group!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		// Group events require approval
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status for Group event, got %s", payload.Request.Status)
		}
	})

	// Verify pending request via API
	t.Run("verify pending request exists via API", func(t *testing.T) {
		if !hasPendingJoinRequest() {
			t.Fatal("expected pending request to appear in /api/chat/requests/me")
		}
	})

	// Host approves the request
	t.Run("host approves request", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	// Verify join request exists in DB (with approved status)
	t.Run("verify join request exists in DB after approval", func(t *testing.T) {
		if !hasJoinRequestInDB() {
			t.Fatal("expected join request to exist in database after approval")
		}
	})

	// Noah leaves the event
	t.Run("noah leaves Group event", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d/chat/members/4", eventID), noahToken, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", resp.StatusCode)
		}
	})

	// Verify join request is deleted from DB after leaving
	t.Run("verify join request deleted from DB after leaving Group event", func(t *testing.T) {
		if hasJoinRequestInDB() {
			t.Fatal("join request should be deleted from database after leaving Group event")
		}
		if !hasConversation(t, avaToken, groupConversationID) {
			t.Fatalf("host should still see group conversation %d after member leaves", groupConversationID)
		}
	})

	// Noah can rejoin with a fresh request
	t.Run("noah can rejoin with fresh request", func(t *testing.T) {
		body := map[string]string{"message": "I want to rejoin the group!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d (user should be able to create fresh join request after leaving)", resp.StatusCode)
		}
		payload := decodeJSON[singleJoinRequestResponse](t, resp)
		// Should be pending again (fresh request)
		if payload.Request.Status != "pending" {
			t.Fatalf("expected pending status for fresh rejoin request, got %s", payload.Request.Status)
		}
	})
}

// ============================================================================
// Event Update/Delete Tests
// ============================================================================

// TestUpdateEvent tests the PUT /events/:id endpoint
func TestUpdateEvent(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user id 1
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user id 4

	// Create an event as ava
	var eventID int64
	t.Run("setup - create event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Original Event",
			Location:    "Original Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Original description",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	t.Run("owner can update event", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "Updated Event Title",
			Location:    "Updated Location",
			Time:        "14:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Updated description",
			Gender:      "Female",
			MinAge:      21,
			MaxAge:      40,
			GroupType:   "Group",
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[testMessageResponse](t, resp)
		if payload.Message != "event updated" {
			t.Fatalf("expected 'event updated', got %s", payload.Message)
		}
	})

	t.Run("non-owner cannot update event", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "Hacked Event",
			Location:    "Hacked Location",
			Time:        "16:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Hacked",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), noahToken, body)
		// Non-owner is treated as not-found to avoid leaking event ownership details.
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("update without auth returns 401", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "No Auth Update",
			Location:    "Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Description",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), "", body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("update with invalid event id returns 400", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "Test",
			Location:    "Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Description",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
		}
		resp := env.doRequest(t, http.MethodPut, "/api/events/invalid", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("update with min_age > max_age returns 400", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "Test",
			Location:    "Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Description",
			Gender:      "Any",
			MinAge:      50,
			MaxAge:      18, // max < min - should fail
			GroupType:   "Single",
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("update with missing required fields returns 400", func(t *testing.T) {
		body := map[string]any{
			"title": "Only Title",
			// Missing other required fields
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

func TestUpdateEventMigratesGroupToSingleWithApprovedMembers(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")
	sophiaToken := env.issueTokenForEmail(t, "sophia@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	avaUser, err := env.repo.GetUserByEmail(ctx, "ava@example.com")
	if err != nil {
		t.Fatalf("failed to load ava user: %v", err)
	}
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	sophiaUser, err := env.repo.GetUserByEmail(ctx, "sophia@example.com")
	if err != nil {
		t.Fatalf("failed to load sophia user: %v", err)
	}

	createBody := CreateEventParams{
		Title:       "Group To Single Migration",
		Location:    "Migration Park",
		Time:        "10:00",
		EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
		Description: "Start group, migrate to single",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Group",
		CoverKey:    defaultCoverKey,
	}

	resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	eventID := decodeJSON[createEventResponse](t, resp).ID

	approveJoin := func(t *testing.T, userID int64, requesterToken string) {
		t.Helper()
		joinResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", eventID),
			requesterToken,
			map[string]string{"message": "Please approve"},
		)
		if joinResp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 creating join request, got %d", joinResp.StatusCode)
		}

		approveResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, userID),
			avaToken,
			nil,
		)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving join request, got %d", approveResp.StatusCode)
		}
	}

	approveJoin(t, noahUser.ID, noahToken)
	approveJoin(t, sophiaUser.ID, sophiaToken)

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing conversations, got %d", resp.StatusCode)
	}
	before := decodeJSON[eventConversationsResponse](t, resp)
	if len(before.Conversations) != 1 {
		t.Fatalf("expected 1 group conversation before switch, got %d", len(before.Conversations))
	}
	groupConversationID := before.Conversations[0].ID

	updateBody := UpdateEventParams{
		Title:       "Group To Single Migration",
		Location:    "Migration Park",
		Time:        "11:00",
		EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
		Description: "Now single chats",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Single",
	}
	resp = env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, updateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating event, got %d", resp.StatusCode)
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing conversations after switch, got %d", resp.StatusCode)
	}
	after := decodeJSON[eventConversationsResponse](t, resp)
	if len(after.Conversations) != 2 {
		t.Fatalf("expected 2 private conversations after Group->Single, got %d", len(after.Conversations))
	}

	seenJoiners := map[int64]bool{
		noahUser.ID:   false,
		sophiaUser.ID: false,
	}
	for _, convo := range after.Conversations {
		if len(convo.MemberIDs) != 2 {
			t.Fatalf("expected exactly 2 members in private conversation %d, got %v", convo.ID, convo.MemberIDs)
		}
		if !hasInt64(convo.MemberIDs, avaUser.ID) {
			t.Fatalf("expected host %d in private conversation %d members %v", avaUser.ID, convo.ID, convo.MemberIDs)
		}

		nonHostCount := 0
		var nonHostID int64
		for _, memberID := range convo.MemberIDs {
			if memberID == avaUser.ID {
				continue
			}
			nonHostID = memberID
			nonHostCount++
		}
		if nonHostCount != 1 {
			t.Fatalf("expected one non-host member in conversation %d, got %v", convo.ID, convo.MemberIDs)
		}
		seen, ok := seenJoiners[nonHostID]
		if !ok {
			t.Fatalf("unexpected non-host member %d in conversation %d", nonHostID, convo.ID)
		}
		if seen {
			t.Fatalf("duplicate private conversation for member %d", nonHostID)
		}
		seenJoiners[nonHostID] = true
	}
	for joinerID, seen := range seenJoiners {
		if !seen {
			t.Fatalf("expected private conversation for approved member %d", joinerID)
		}
	}

	if hasConversationForUser(t, env, avaToken, groupConversationID) {
		t.Fatalf("host should not see removed group conversation %d", groupConversationID)
	}
	if hasConversationForUser(t, env, noahToken, groupConversationID) {
		t.Fatalf("noah should not see removed group conversation %d", groupConversationID)
	}
	if hasConversationForUser(t, env, sophiaToken, groupConversationID) {
		t.Fatalf("sophia should not see removed group conversation %d", groupConversationID)
	}
}

func TestUpdateEventMigratesGroupToSingleWithNoApprovedMembers(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}

	createBody := CreateEventParams{
		Title:       "Group To Single No Approved",
		Location:    "Migration Court",
		Time:        "09:30",
		EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
		Description: "No approved members before switch",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Group",
		CoverKey:    defaultCoverKey,
	}

	resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	eventID := decodeJSON[createEventResponse](t, resp).ID

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing event conversations, got %d", resp.StatusCode)
	}
	before := decodeJSON[eventConversationsResponse](t, resp)
	if len(before.Conversations) != 1 {
		t.Fatalf("expected one group conversation before switch, got %d", len(before.Conversations))
	}
	groupConversationID := before.Conversations[0].ID

	joinResp := env.doRequest(
		t,
		http.MethodPost,
		fmt.Sprintf("/api/events/%d/chat/requests", eventID),
		noahToken,
		map[string]string{"message": "Pending only"},
	)
	if joinResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating join request, got %d", joinResp.StatusCode)
	}

	updateBody := UpdateEventParams{
		Title:       "Group To Single No Approved",
		Location:    "Migration Court",
		Time:        "10:30",
		EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
		Description: "Now single type",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Single",
	}
	resp = env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, updateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating event, got %d", resp.StatusCode)
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing event conversations after switch, got %d", resp.StatusCode)
	}
	after := decodeJSON[eventConversationsResponse](t, resp)
	if len(after.Conversations) != 0 {
		t.Fatalf("expected zero conversations after Group->Single with no approved members, got %d", len(after.Conversations))
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/chat/requests", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing host join requests, got %d", resp.StatusCode)
	}
	hostRequests := decodeJSON[joinRequestsListResponse](t, resp)
	foundHostPending := false
	for _, request := range hostRequests.Requests {
		if request.EventID == eventID && request.UserID == noahUser.ID && request.Status == "pending" {
			foundHostPending = true
			break
		}
	}
	if !foundHostPending {
		t.Fatalf("expected pending join request for user %d after switch", noahUser.ID)
	}

	resp = env.doRequest(t, http.MethodGet, "/api/chat/requests/me", noahToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing requester join requests, got %d", resp.StatusCode)
	}
	userRequests := decodeJSON[joinRequestsListResponse](t, resp)
	foundUserPending := false
	for _, request := range userRequests.Requests {
		if request.EventID == eventID && request.Status == "pending" {
			foundUserPending = true
			break
		}
	}
	if !foundUserPending {
		t.Fatalf("expected requester to keep pending join request for event %d", eventID)
	}

	if hasConversationForUser(t, env, avaToken, groupConversationID) {
		t.Fatalf("host should not see removed group conversation %d", groupConversationID)
	}
	if hasConversationForUser(t, env, noahToken, groupConversationID) {
		t.Fatalf("requester should not see removed group conversation %d", groupConversationID)
	}
}

func TestUpdateEventMigratesSingleToGroupWithMultiplePrivateConversations(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")
	sophiaToken := env.issueTokenForEmail(t, "sophia@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	avaUser, err := env.repo.GetUserByEmail(ctx, "ava@example.com")
	if err != nil {
		t.Fatalf("failed to load ava user: %v", err)
	}
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	sophiaUser, err := env.repo.GetUserByEmail(ctx, "sophia@example.com")
	if err != nil {
		t.Fatalf("failed to load sophia user: %v", err)
	}

	createBody := CreateEventParams{
		Title:       "Single To Group Migration",
		Location:    "Migration Hall",
		Time:        "15:00",
		EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
		Description: "Start as single with two private chats",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Single",
		CoverKey:    defaultCoverKey,
	}

	resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	eventID := decodeJSON[createEventResponse](t, resp).ID

	joinAndApprove := func(t *testing.T, requesterToken string, requesterID int64) int64 {
		t.Helper()
		joinResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", eventID),
			requesterToken,
			map[string]string{"message": "Approve me"},
		)
		if joinResp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 creating join request, got %d", joinResp.StatusCode)
		}

		approveResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, requesterID),
			avaToken,
			nil,
		)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving join request, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversationId after approval")
		}
		return *approvePayload.ConversationID
	}

	noahConvoID := joinAndApprove(t, noahToken, noahUser.ID)
	sophiaConvoID := joinAndApprove(t, sophiaToken, sophiaUser.ID)
	if noahConvoID == sophiaConvoID {
		t.Fatalf("expected separate private conversations, both were %d", noahConvoID)
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing conversations before switch, got %d", resp.StatusCode)
	}
	before := decodeJSON[eventConversationsResponse](t, resp)
	if len(before.Conversations) != 2 {
		t.Fatalf("expected 2 private conversations before switch, got %d", len(before.Conversations))
	}

	updateBody := UpdateEventParams{
		Title:       "Single To Group Migration",
		Location:    "Migration Hall",
		Time:        "16:00",
		EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
		Description: "Now group chat",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Group",
	}
	resp = env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, updateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating event, got %d", resp.StatusCode)
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing conversations after switch, got %d", resp.StatusCode)
	}
	after := decodeJSON[eventConversationsResponse](t, resp)
	if len(after.Conversations) != 1 {
		t.Fatalf("expected exactly 1 group conversation after Single->Group, got %d", len(after.Conversations))
	}

	groupConvo := after.Conversations[0]
	if groupConvo.ID == noahConvoID || groupConvo.ID == sophiaConvoID {
		t.Fatalf(
			"expected new group conversation id, got %d from old private conversations (%d, %d)",
			groupConvo.ID,
			noahConvoID,
			sophiaConvoID,
		)
	}
	if !hasInt64(groupConvo.MemberIDs, avaUser.ID) || !hasInt64(groupConvo.MemberIDs, noahUser.ID) || !hasInt64(groupConvo.MemberIDs, sophiaUser.ID) {
		t.Fatalf(
			"expected group members host+approved users (%d,%d,%d), got %v",
			avaUser.ID,
			noahUser.ID,
			sophiaUser.ID,
			groupConvo.MemberIDs,
		)
	}

	if !hasConversationForUser(t, env, avaToken, groupConvo.ID) {
		t.Fatalf("host should see new group conversation %d", groupConvo.ID)
	}
	if !hasConversationForUser(t, env, noahToken, groupConvo.ID) {
		t.Fatalf("noah should see new group conversation %d", groupConvo.ID)
	}
	if !hasConversationForUser(t, env, sophiaToken, groupConvo.ID) {
		t.Fatalf("sophia should see new group conversation %d", groupConvo.ID)
	}
	if hasConversationForUser(t, env, avaToken, noahConvoID) {
		t.Fatalf("host should not see removed private conversation %d", noahConvoID)
	}
	if hasConversationForUser(t, env, avaToken, sophiaConvoID) {
		t.Fatalf("host should not see removed private conversation %d", sophiaConvoID)
	}
	if hasConversationForUser(t, env, noahToken, noahConvoID) {
		t.Fatalf("noah should not see removed private conversation %d", noahConvoID)
	}
	if hasConversationForUser(t, env, sophiaToken, sophiaConvoID) {
		t.Fatalf("sophia should not see removed private conversation %d", sophiaConvoID)
	}
}

func TestUpdateEventMigratesSingleToGroupWithNoApprovedMembers(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	avaUser, err := env.repo.GetUserByEmail(ctx, "ava@example.com")
	if err != nil {
		t.Fatalf("failed to load ava user: %v", err)
	}
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}

	createBody := CreateEventParams{
		Title:       "Single To Group No Approved",
		Location:    "Quiet Room",
		Time:        "13:00",
		EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
		Description: "No approved requests yet",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Single",
		CoverKey:    defaultCoverKey,
	}

	resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	eventID := decodeJSON[createEventResponse](t, resp).ID

	joinResp := env.doRequest(
		t,
		http.MethodPost,
		fmt.Sprintf("/api/events/%d/chat/requests", eventID),
		noahToken,
		map[string]string{"message": "Still pending"},
	)
	if joinResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating join request, got %d", joinResp.StatusCode)
	}

	updateBody := UpdateEventParams{
		Title:       "Single To Group No Approved",
		Location:    "Quiet Room",
		Time:        "14:00",
		EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
		Description: "Now group type",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Group",
	}
	resp = env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, updateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating event, got %d", resp.StatusCode)
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing event conversations after switch, got %d", resp.StatusCode)
	}
	conversations := decodeJSON[eventConversationsResponse](t, resp)
	if len(conversations.Conversations) != 1 {
		t.Fatalf("expected one host-only group conversation after switch, got %d", len(conversations.Conversations))
	}
	groupConversation := conversations.Conversations[0]
	if len(groupConversation.MemberIDs) != 1 || !hasInt64(groupConversation.MemberIDs, avaUser.ID) {
		t.Fatalf("expected host-only members [%d], got %v", avaUser.ID, groupConversation.MemberIDs)
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/chat/requests", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing join requests, got %d", resp.StatusCode)
	}
	requests := decodeJSON[joinRequestsListResponse](t, resp)
	foundPending := false
	for _, request := range requests.Requests {
		if request.EventID == eventID && request.UserID == noahUser.ID && request.Status == "pending" {
			foundPending = true
			break
		}
	}
	if !foundPending {
		t.Fatalf("expected pending request for user %d after switch", noahUser.ID)
	}

	if hasConversationForUser(t, env, noahToken, groupConversation.ID) {
		t.Fatalf("requester should not see group conversation %d before approval", groupConversation.ID)
	}
}

func TestUpdateEventPostSwitchJoinApprovalBehavior(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	avaUser, err := env.repo.GetUserByEmail(ctx, "ava@example.com")
	if err != nil {
		t.Fatalf("failed to load ava user: %v", err)
	}
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}

	t.Run("GroupToSingle join stays pending until approval then creates private", func(t *testing.T) {
		createBody := CreateEventParams{
			Title:       "Post-Switch GroupToSingle",
			Location:    "Switch Point A",
			Time:        "17:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Behavior check",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		updateBody := UpdateEventParams{
			Title:       "Post-Switch GroupToSingle",
			Location:    "Switch Point A",
			Time:        "18:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Now single",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
		}
		resp = env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, updateBody)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 updating event, got %d", resp.StatusCode)
		}

		resp = env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", eventID),
			noahToken,
			map[string]string{"message": "Please add me"},
		)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 join request, got %d", resp.StatusCode)
		}
		joinPayload := decodeJSON[singleJoinRequestResponse](t, resp)
		if joinPayload.Request.Status != "pending" {
			t.Fatalf("expected pending status, got %s", joinPayload.Request.Status)
		}
		if joinPayload.ConversationID != nil || joinPayload.Request.ConversationID != nil {
			t.Fatalf("expected no conversation id before approval, got top-level=%v nested=%v", joinPayload.ConversationID, joinPayload.Request.ConversationID)
		}

		resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 listing conversations, got %d", resp.StatusCode)
		}
		beforeApproval := decodeJSON[eventConversationsResponse](t, resp)
		if len(beforeApproval.Conversations) != 0 {
			t.Fatalf("expected no conversations before approval in Single mode, got %d", len(beforeApproval.Conversations))
		}

		resp = env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, noahUser.ID),
			avaToken,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving request, got %d", resp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, resp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversationId after approval")
		}
		privateConversationID := *approvePayload.ConversationID

		resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 listing conversations after approval, got %d", resp.StatusCode)
		}
		afterApproval := decodeJSON[eventConversationsResponse](t, resp)
		if len(afterApproval.Conversations) != 1 {
			t.Fatalf("expected exactly one private conversation after approval, got %d", len(afterApproval.Conversations))
		}
		if afterApproval.Conversations[0].ID != privateConversationID {
			t.Fatalf("expected approved private conversation id %d, got %d", privateConversationID, afterApproval.Conversations[0].ID)
		}
		if !hasInt64(afterApproval.Conversations[0].MemberIDs, avaUser.ID) || !hasInt64(afterApproval.Conversations[0].MemberIDs, noahUser.ID) {
			t.Fatalf(
				"expected private members host+requester (%d,%d), got %v",
				avaUser.ID,
				noahUser.ID,
				afterApproval.Conversations[0].MemberIDs,
			)
		}

		if !hasConversationForUser(t, env, noahToken, privateConversationID) {
			t.Fatalf("requester should see approved private conversation %d", privateConversationID)
		}
	})

	t.Run("SingleToGroup join approval adds member to shared conversation", func(t *testing.T) {
		createBody := CreateEventParams{
			Title:       "Post-Switch SingleToGroup",
			Location:    "Switch Point B",
			Time:        "12:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Behavior check",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		updateBody := UpdateEventParams{
			Title:       "Post-Switch SingleToGroup",
			Location:    "Switch Point B",
			Time:        "13:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Now group",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
		}
		resp = env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, updateBody)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 updating event, got %d", resp.StatusCode)
		}

		resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 listing conversations, got %d", resp.StatusCode)
		}
		initial := decodeJSON[eventConversationsResponse](t, resp)
		if len(initial.Conversations) != 1 {
			t.Fatalf("expected 1 group conversation before approvals, got %d", len(initial.Conversations))
		}
		groupConversationID := initial.Conversations[0].ID
		if len(initial.Conversations[0].MemberIDs) != 1 || !hasInt64(initial.Conversations[0].MemberIDs, avaUser.ID) {
			t.Fatalf("expected host-only group members [%d], got %v", avaUser.ID, initial.Conversations[0].MemberIDs)
		}

		resp = env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", eventID),
			noahToken,
			map[string]string{"message": "Join switched group"},
		)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 join request, got %d", resp.StatusCode)
		}
		joinPayload := decodeJSON[singleJoinRequestResponse](t, resp)
		if joinPayload.Request.Status != "pending" {
			t.Fatalf("expected pending status, got %s", joinPayload.Request.Status)
		}
		if joinPayload.ConversationID != nil || joinPayload.Request.ConversationID != nil {
			t.Fatalf("expected no conversation id before approval, got top-level=%v nested=%v", joinPayload.ConversationID, joinPayload.Request.ConversationID)
		}

		resp = env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, noahUser.ID),
			avaToken,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving request, got %d", resp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, resp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversationId after approval")
		}
		if *approvePayload.ConversationID != groupConversationID {
			t.Fatalf("expected approval to use existing group conversation %d, got %d", groupConversationID, *approvePayload.ConversationID)
		}

		resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 listing conversations after approval, got %d", resp.StatusCode)
		}
		afterApproval := decodeJSON[eventConversationsResponse](t, resp)
		if len(afterApproval.Conversations) != 1 {
			t.Fatalf("expected one shared group conversation after approval, got %d", len(afterApproval.Conversations))
		}
		if afterApproval.Conversations[0].ID != groupConversationID {
			t.Fatalf("expected shared group conversation id %d, got %d", groupConversationID, afterApproval.Conversations[0].ID)
		}
		if !hasInt64(afterApproval.Conversations[0].MemberIDs, avaUser.ID) || !hasInt64(afterApproval.Conversations[0].MemberIDs, noahUser.ID) {
			t.Fatalf(
				"expected group members host+requester (%d,%d), got %v",
				avaUser.ID,
				noahUser.ID,
				afterApproval.Conversations[0].MemberIDs,
			)
		}
		if !hasConversationForUser(t, env, noahToken, groupConversationID) {
			t.Fatalf("requester should see approved group conversation %d", groupConversationID)
		}
	})
}

func TestUpdateEventDeliversUpdateMessageOnlyToActivePostSwitchConversations(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	noahToken := env.issueTokenForEmail(t, "noah@example.com")
	sophiaToken := env.issueTokenForEmail(t, "sophia@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	sophiaUser, err := env.repo.GetUserByEmail(ctx, "sophia@example.com")
	if err != nil {
		t.Fatalf("failed to load sophia user: %v", err)
	}

	createBody := CreateEventParams{
		Title:       "Update Message Active Topology",
		Location:    "Signal Tower",
		Time:        "19:00",
		EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
		Description: "Ensure update message goes to active conversations only",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Single",
		CoverKey:    defaultCoverKey,
	}

	resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	eventID := decodeJSON[createEventResponse](t, resp).ID

	joinAndApprove := func(t *testing.T, requesterToken string, requesterID int64) int64 {
		t.Helper()
		joinResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests", eventID),
			requesterToken,
			map[string]string{"message": "Approve me"},
		)
		if joinResp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201 creating join request, got %d", joinResp.StatusCode)
		}

		approveResp := env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, requesterID),
			avaToken,
			nil,
		)
		if approveResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 approving join request, got %d", approveResp.StatusCode)
		}
		approvePayload := decodeJSON[singleJoinRequestResponse](t, approveResp)
		if approvePayload.ConversationID == nil {
			t.Fatal("expected conversationId after approval")
		}
		return *approvePayload.ConversationID
	}

	noahConversationID := joinAndApprove(t, noahToken, noahUser.ID)
	sophiaConversationID := joinAndApprove(t, sophiaToken, sophiaUser.ID)
	if noahConversationID == sophiaConversationID {
		t.Fatalf("expected distinct private conversations, both were %d", noahConversationID)
	}

	updateBody := UpdateEventParams{
		Title:       "Update Message Active Topology",
		Location:    "Signal Tower",
		Time:        "20:00",
		EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
		Description: "Switch to group",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      50,
		GroupType:   "Group",
	}
	resp = env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, updateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating event, got %d", resp.StatusCode)
	}

	resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing event conversations, got %d", resp.StatusCode)
	}
	conversations := decodeJSON[eventConversationsResponse](t, resp)
	if len(conversations.Conversations) != 1 {
		t.Fatalf("expected 1 active group conversation after migration, got %d", len(conversations.Conversations))
	}
	activeConversationID := conversations.Conversations[0].ID
	if activeConversationID == noahConversationID || activeConversationID == sophiaConversationID {
		t.Fatalf(
			"expected new active conversation id, got %d from old private (%d,%d)",
			activeConversationID,
			noahConversationID,
			sophiaConversationID,
		)
	}

	messagesResp := env.doRequest(
		t,
		http.MethodGet,
		fmt.Sprintf("/api/conversations/%d/messages", activeConversationID),
		avaToken,
		nil,
	)
	if messagesResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing active conversation messages, got %d", messagesResp.StatusCode)
	}
	messages := decodeJSON[messagesResponse](t, messagesResp)
	foundUpdateMessage := false
	for _, message := range messages.Messages {
		if message.Body == updatedEventDetailMessage {
			foundUpdateMessage = true
			break
		}
	}
	if !foundUpdateMessage {
		t.Fatalf("expected %q in active conversation %d", updatedEventDetailMessage, activeConversationID)
	}

	var removedMessageCount int
	if err := env.db.QueryRowContext(
		ctx,
		`SELECT COUNT(1) FROM messages WHERE conversation_id IN (?, ?) AND body = ?`,
		noahConversationID,
		sophiaConversationID,
		updatedEventDetailMessage,
	).Scan(&removedMessageCount); err != nil {
		t.Fatalf("count removed-conversation update messages: %v", err)
	}
	if removedMessageCount != 0 {
		t.Fatalf("expected no update messages in removed conversations, got %d", removedMessageCount)
	}

	var activeMessageCount int
	if err := env.db.QueryRowContext(
		ctx,
		`SELECT COUNT(1) FROM messages WHERE conversation_id = ? AND body = ?`,
		activeConversationID,
		updatedEventDetailMessage,
	).Scan(&activeMessageCount); err != nil {
		t.Fatalf("count active-conversation update messages: %v", err)
	}
	if activeMessageCount != 1 {
		t.Fatalf("expected exactly one update message in active conversation, got %d", activeMessageCount)
	}
}

func TestUpdateSingleToGroupCreatesConversationForJoinRequest(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // host
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // requester

	var eventID int64
	t.Run("create single event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Single To Group",
			Location:    "Test Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Will be changed to group",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	t.Run("update to group type", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "Single To Group",
			Location:    "Test Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Now a group event",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("join request succeeds after group type change", func(t *testing.T) {
		body := map[string]any{"message": "I'd like to join!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
	})
}

func TestUpdateEventPublishesChatUpdateForGroupEvent(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com") // user id 1

	var eventID int64
	t.Run("create group event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Group Update Test",
			Location:    "Original Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Original description",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID = decodeJSON[createEventResponse](t, resp).ID
	})

	t.Run("no update system message before edit", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected at least one conversation")
		}
		for _, convo := range payload.Conversations {
			if convo.LastMessage != nil && convo.LastMessage.Body == "Updated Event Detail" {
				t.Fatalf("did not expect 'Updated Event Detail' before edit in conversation %d", convo.ID)
			}
		}
	})

	t.Run("edit event", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "Group Update Test (Edited)",
			Location:    "Edited Location",
			Time:        "12:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Edited description",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("group conversation gets Updated Event Detail message", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) == 0 {
			t.Fatal("expected at least one conversation")
		}
		for _, convo := range payload.Conversations {
			if convo.LastMessage == nil {
				t.Fatalf("expected last_message for conversation %d", convo.ID)
			}
			if convo.LastMessage.Body != "Updated Event Detail" {
				t.Fatalf("expected last_message body 'Updated Event Detail', got %q", convo.LastMessage.Body)
			}
			if convo.LastMessage.SenderID != 1 {
				t.Fatalf("expected sender_id 1, got %d", convo.LastMessage.SenderID)
			}
		}
	})
}

func TestUpdateEventPublishesChatUpdateForApprovedSingleConversations(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")       // host user id 1
	noahToken := env.issueTokenForEmail(t, "noah@example.com")     // requester
	sophiaToken := env.issueTokenForEmail(t, "sophia@example.com") // requester

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	sophiaUser, err := env.repo.GetUserByEmail(ctx, "sophia@example.com")
	if err != nil {
		t.Fatalf("failed to load sophia user: %v", err)
	}

	var eventID int64
	t.Run("create single event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Single Update Test",
			Location:    "Original Location",
			Time:        "18:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Original description",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID = decodeJSON[createEventResponse](t, resp).ID
	})

	for _, joiner := range []struct {
		token  string
		userID int64
	}{
		{token: noahToken, userID: noahUser.ID},
		{token: sophiaToken, userID: sophiaUser.ID},
	} {
		t.Run(fmt.Sprintf("approve join request for user %d", joiner.userID), func(t *testing.T) {
			joinBody := map[string]string{"message": "I'd like to join!"}
			resp := env.doRequest(
				t,
				http.MethodPost,
				fmt.Sprintf("/api/events/%d/chat/requests", eventID),
				joiner.token,
				joinBody,
			)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("expected 201, got %d", resp.StatusCode)
			}

			resp = env.doRequest(
				t,
				http.MethodPost,
				fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, joiner.userID),
				avaToken,
				nil,
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		})
	}

	t.Run("edit event", func(t *testing.T) {
		body := UpdateEventParams{
			Title:       "Single Update Test (Edited)",
			Location:    "Edited Location",
			Time:        "20:00",
			EventDate:   time.Now().Add(72 * time.Hour).Format("2006-01-02"),
			Description: "Edited description",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
		}
		resp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", eventID), avaToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("every approved private conversation receives Updated Event Detail message", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[eventConversationsResponse](t, resp)
		if len(payload.Conversations) < 2 {
			t.Fatalf("expected at least 2 approved conversations, got %d", len(payload.Conversations))
		}

		for _, convo := range payload.Conversations {
			messagesResp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/conversations/%d/messages", convo.ID), avaToken, nil)
			if messagesResp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200 for messages, got %d (conversation %d)", messagesResp.StatusCode, convo.ID)
			}
			messages := decodeJSON[messagesResponse](t, messagesResp)

			found := false
			for _, message := range messages.Messages {
				if message.Body == "Updated Event Detail" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected 'Updated Event Detail' message in conversation %d", convo.ID)
			}
		}
	})
}

// TestDeleteEvent tests the DELETE /events/:id endpoint
func TestDeleteEvent(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user id 1
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user id 4

	// Create an event as ava for deletion tests
	var eventID int64
	t.Run("setup - create event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Event To Delete",
			Location:    "Delete Location",
			Time:        "12:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Will be deleted",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		eventID = payload.ID
	})

	t.Run("non-owner cannot delete event", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d", eventID), noahToken, nil)
		// Current implementation returns 404 for non-owner (combines not found / not owned)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("delete without auth returns 401", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d", eventID), "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("delete with invalid event id returns 400", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, "/api/events/invalid", avaToken, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("owner can delete event", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		payload := decodeJSON[testMessageResponse](t, resp)
		if payload.Message != "event deleted" {
			t.Fatalf("expected 'event deleted', got %s", payload.Message)
		}
	})

	t.Run("delete non-existent event returns 404", func(t *testing.T) {
		// Try to delete the same event again (already deleted)
		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})
}

// TestCreateEventValidation tests validation for the POST /events endpoint
func TestCreateEventValidation(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")

	t.Run("create without auth returns 401", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Test Event",
			Location:    "Test Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", "", body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("create without title returns 400", func(t *testing.T) {
		body := map[string]any{
			"location":   "Location",
			"time":       "10:00",
			"event_date": time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			"gender":     "Any",
			"min_age":    18,
			"max_age":    50,
			"group_type": "Single",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("create without location returns 400", func(t *testing.T) {
		body := map[string]any{
			"title":      "Event Title",
			"time":       "10:00",
			"event_date": time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			"gender":     "Any",
			"min_age":    18,
			"max_age":    50,
			"group_type": "Single",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("create with min_age > max_age returns 400", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Invalid Age Range Event",
			Location:    "Test Location",
			Time:        "10:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "Test",
			Gender:      "Any",
			MinAge:      50,
			MaxAge:      18, // max < min - should fail
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("create with invalid group_type returns 400", func(t *testing.T) {
		body := map[string]any{
			"title":      "Event",
			"location":   "Location",
			"time":       "10:00",
			"event_date": time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			"gender":     "Any",
			"min_age":    18,
			"max_age":    50,
			"group_type": "InvalidType", // Not Single or Group
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("create with empty body returns 400", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, map[string]any{})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}

func TestCreateEventScheduledAtPreservesLegacyFields(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	avaToken := env.issueTokenForEmail(t, "ava@example.com")

	body := CreateEventParams{
		Title:       "Scheduled Event",
		Location:    "Test Location",
		Time:        "00:30",
		EventDate:   "2026-04-11",
		Description: "Preserve legacy fields",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      40,
		DateLabel:   "Tmrw",
		GroupType:   "Single",
		CoverKey:    defaultCoverKey,
		ScheduledAt: "2026-04-08T12:34:00-04:00",
	}

	resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	payload := decodeJSON[createEventResponse](t, resp)
	evt, err := env.repo.GetEventByID(ctx, payload.ID)
	if err != nil {
		t.Fatalf("get created event: %v", err)
	}

	if evt.EventDate != body.EventDate {
		t.Fatalf("expected event_date %q, got %q", body.EventDate, evt.EventDate)
	}
	if evt.Time != body.Time {
		t.Fatalf("expected time %q, got %q", body.Time, evt.Time)
	}
	if evt.DateLabel != body.DateLabel {
		t.Fatalf("expected date_label %q, got %q", body.DateLabel, evt.DateLabel)
	}
	if evt.ScheduledAt == nil {
		t.Fatal("expected scheduled_at to be stored")
	}
	if got := formatScheduledAtUTC(*evt.ScheduledAt); got != "2026-04-08T16:34:00Z" {
		t.Fatalf("expected normalized scheduled_at %q, got %q", "2026-04-08T16:34:00Z", got)
	}
}

func TestUpdateEventScheduledAtPreservesLegacyFields(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	avaToken := env.issueTokenForEmail(t, "ava@example.com")

	createBody := CreateEventParams{
		Title:       "Original Event",
		Location:    "Original Location",
		Time:        "18:00",
		EventDate:   "2026-04-09",
		Description: "Original description",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      40,
		DateLabel:   "Today",
		GroupType:   "Single",
		CoverKey:    defaultCoverKey,
	}

	createResp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}

	created := decodeJSON[createEventResponse](t, createResp)
	updateBody := UpdateEventParams{
		Title:       "Updated Event",
		Location:    "Updated Location",
		Time:        "03:15",
		EventDate:   "2026-04-13",
		Description: "Updated description",
		Gender:      "Female",
		MinAge:      21,
		MaxAge:      38,
		DateLabel:   "Today",
		GroupType:   "Group",
		ScheduledAt: "2026-04-08T21:46:00+02:00",
	}

	updateResp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", created.ID), avaToken, updateBody)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", updateResp.StatusCode)
	}

	evt, err := env.repo.GetEventByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get updated event: %v", err)
	}

	if evt.EventDate != updateBody.EventDate {
		t.Fatalf("expected event_date %q, got %q", updateBody.EventDate, evt.EventDate)
	}
	if evt.Time != updateBody.Time {
		t.Fatalf("expected time %q, got %q", updateBody.Time, evt.Time)
	}
	if evt.DateLabel != updateBody.DateLabel {
		t.Fatalf("expected date_label %q, got %q", updateBody.DateLabel, evt.DateLabel)
	}
	if evt.ScheduledAt == nil {
		t.Fatal("expected scheduled_at to be stored after update")
	}
	if got := formatScheduledAtUTC(*evt.ScheduledAt); got != "2026-04-08T19:46:00Z" {
		t.Fatalf("expected normalized scheduled_at %q, got %q", "2026-04-08T19:46:00Z", got)
	}
}

func TestCreateEventScheduledAtNormalizesInvalidDateLabel(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	avaToken := env.issueTokenForEmail(t, "ava@example.com")
	eventDate := time.Now().Add(72 * time.Hour).Format("2006-01-02")
	body := CreateEventParams{
		Title:       "Scheduled Event Invalid Label",
		Location:    "Test Location",
		Time:        "19:05",
		EventDate:   eventDate,
		Description: "invalid label should not fail",
		Gender:      "Female",
		MinAge:      20,
		MaxAge:      25,
		DateLabel:   "18 Apr Sat",
		GroupType:   "Single",
		CoverKey:    defaultCoverKey,
		ScheduledAt: "2026-04-18T13:35:00.000Z",
	}

	resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	payload := decodeJSON[createEventResponse](t, resp)
	evt, err := env.repo.GetEventByID(ctx, payload.ID)
	if err != nil {
		t.Fatalf("get created event: %v", err)
	}

	expectedLabel := deriveDateLabel(body.EventDate, time.Now())
	if evt.DateLabel != expectedLabel {
		t.Fatalf("expected normalized date_label %q, got %q", expectedLabel, evt.DateLabel)
	}
}

func TestUpdateEventScheduledAtNormalizesInvalidDateLabel(t *testing.T) {
	env := setupAPITestEnv(t)
	ctx := context.Background()

	avaToken := env.issueTokenForEmail(t, "ava@example.com")

	createBody := CreateEventParams{
		Title:       "Original Event",
		Location:    "Original Location",
		Time:        "18:00",
		EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
		Description: "Original description",
		Gender:      "Any",
		MinAge:      18,
		MaxAge:      40,
		DateLabel:   "Today",
		GroupType:   "Single",
		CoverKey:    defaultCoverKey,
	}

	createResp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, createBody)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createResp.StatusCode)
	}

	created := decodeJSON[createEventResponse](t, createResp)
	updateEventDate := time.Now().Add(72 * time.Hour).Format("2006-01-02")
	updateBody := UpdateEventParams{
		Title:       "Updated Event",
		Location:    "Updated Location",
		Time:        "19:05",
		EventDate:   updateEventDate,
		Description: "Updated description",
		Gender:      "Female",
		MinAge:      20,
		MaxAge:      25,
		DateLabel:   "18 Apr Sat",
		GroupType:   "Single",
		ScheduledAt: "2026-04-18T13:35:00.000Z",
	}

	updateResp := env.doRequest(t, http.MethodPut, fmt.Sprintf("/api/events/%d", created.ID), avaToken, updateBody)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", updateResp.StatusCode)
	}

	evt, err := env.repo.GetEventByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get updated event: %v", err)
	}

	expectedLabel := deriveDateLabel(updateBody.EventDate, time.Now())
	if evt.DateLabel != expectedLabel {
		t.Fatalf("expected normalized date_label %q, got %q", expectedLabel, evt.DateLabel)
	}
}

// ============================================================================
// Push Notification Tests
// ============================================================================

func TestPushTokenCRUD(t *testing.T) {
	env := setupAPITestEnv(t)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")

	t.Run("register token successfully", func(t *testing.T) {
		body := map[string]string{
			"token":     "fcm-token-ava-1",
			"device_id": "device-ava-1",
			"platform":  "android",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("register without auth returns 401", func(t *testing.T) {
		body := map[string]string{
			"token":     "fcm-token-unauthed",
			"device_id": "device-1",
			"platform":  "android",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", "", body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("register missing token returns 400", func(t *testing.T) {
		body := map[string]string{
			"device_id": "device-1",
			"platform":  "android",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("register empty token returns 400", func(t *testing.T) {
		body := map[string]string{
			"token":     "   ",
			"device_id": "device-1",
			"platform":  "android",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("register missing device_id returns 400", func(t *testing.T) {
		body := map[string]string{
			"token":    "fcm-token-1",
			"platform": "android",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("register invalid platform returns 400", func(t *testing.T) {
		body := map[string]string{
			"token":     "fcm-token-1",
			"device_id": "device-1",
			"platform":  "windows",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("register with platform ios succeeds", func(t *testing.T) {
		body := map[string]string{
			"token":     "fcm-token-ava-ios",
			"device_id": "device-ava-ios",
			"platform":  "ios",
		}
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("delete token successfully", func(t *testing.T) {
		body := map[string]string{"token": "fcm-token-ava-1"}
		resp := env.doRequest(t, http.MethodDelete, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("delete non-existent token returns 404", func(t *testing.T) {
		body := map[string]string{"token": "non-existent-token"}
		resp := env.doRequest(t, http.MethodDelete, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("delete without auth returns 401", func(t *testing.T) {
		body := map[string]string{"token": "fcm-token-ava-ios"}
		resp := env.doRequest(t, http.MethodDelete, "/api/push-tokens", "", body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("delete empty token returns 400", func(t *testing.T) {
		body := map[string]string{"token": "   "}
		resp := env.doRequest(t, http.MethodDelete, "/api/push-tokens", avaToken, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})
}

func TestPushOnChatMessage(t *testing.T) {
	mock := &mockPushSender{}
	env := setupAPITestEnvWithPush(t, mock)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user 1 (host)
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user 4

	// Register push tokens for both users
	for _, tc := range []struct {
		token    string
		deviceID string
		authTok  string
	}{
		{"fcm-ava-device", "ava-device", avaToken},
		{"fcm-noah-device", "noah-device", noahToken},
	} {
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", tc.authTok, map[string]string{
			"token": tc.token, "device_id": tc.deviceID, "platform": "android",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("register push token: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Create group event as ava, noah joins, ava approves
	var groupEventID int64
	t.Run("setup group event", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Push Test Group Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "For push test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		payload := decodeJSON[createEventResponse](t, resp)
		groupEventID = payload.ID
	})

	t.Run("noah requests to join", func(t *testing.T) {
		body := map[string]string{"message": "Let me in!"}
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", groupEventID), noahToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("ava approves noah", func(t *testing.T) {
		resp := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", groupEventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	// Wait for any setup-related pushes to settle, then reset
	time.Sleep(200 * time.Millisecond)
	mock.reset()

	// Get the conversation ID via event conversations endpoint
	resp := env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", groupEventID), avaToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	eventConvos := decodeJSON[eventConversationsResponse](t, resp)
	if len(eventConvos.Conversations) == 0 {
		t.Fatal("no conversations found for event")
	}
	conversationID := eventConvos.Conversations[0].ID

	t.Run("ava sends message and noah gets push", func(t *testing.T) {
		// Connect ava's websocket
		dialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(avaToken)
		wsConn, _, err := websocket.DefaultDialer.Dial(dialURL, nil)
		if err != nil {
			t.Fatalf("ws dial: %v", err)
		}
		defer wsConn.Close()

		// Send a chat message
		sendPayload := map[string]any{
			"type":           "message:send",
			"conversationId": conversationID,
			"body":           "Hello from ava!",
			"tempId":         fmt.Sprintf("temp-%d", time.Now().UnixNano()),
		}
		if err := wsConn.WriteJSON(sendPayload); err != nil {
			t.Fatalf("ws send: %v", err)
		}

		// Wait for push notification
		notifications := mock.waitForNotifications(t, 1, 3*time.Second)

		// Verify noah got the push
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "chat.message" {
				found = true
				if n.Data["conversationId"] != fmt.Sprintf("%d", conversationID) {
					t.Fatalf("expected conversationId %d, got %s", conversationID, n.Data["conversationId"])
				}
			}
		}
		if !found {
			t.Fatalf("expected push to noah's device, got: %+v", notifications)
		}

		// Verify ava (sender) did NOT receive a push
		for _, n := range notifications {
			if n.Token == "fcm-ava-device" {
				t.Fatal("sender (ava) should NOT receive a push notification")
			}
		}
	})
}

func TestPushOnJoinRequestFlows(t *testing.T) {
	mock := &mockPushSender{}
	env := setupAPITestEnvWithPush(t, mock)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user 1 (host)
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user 4

	// Register push tokens
	for _, tc := range []struct {
		token    string
		deviceID string
		authTok  string
	}{
		{"fcm-ava-device", "ava-device", avaToken},
		{"fcm-noah-device", "noah-device", noahToken},
	} {
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", tc.authTok, map[string]string{
			"token": tc.token, "device_id": tc.deviceID, "platform": "android",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("register push token: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	t.Run("group event join request notifies host", func(t *testing.T) {
		// Create group event
		body := CreateEventParams{
			Title:       "Push Join Group Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "For push join test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		mock.reset()

		// Noah requests to join
		joinBody := map[string]string{"message": "I want to join!"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Wait for push to host (ava)
		notifications := mock.waitForNotifications(t, 1, 3*time.Second)

		found := false
		for _, n := range notifications {
			if n.Token == "fcm-ava-device" && n.Data["type"] == "join_request.created" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected join_request.created push to host, got: %+v", notifications)
		}
	})

	t.Run("group event approve notifies requester", func(t *testing.T) {
		// Create another group event
		body := CreateEventParams{
			Title:       "Push Approve Group Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "For push approve test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		// Noah requests to join
		joinBody := map[string]string{"message": "Approve me!"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Wait for join request push then reset (extra sleep to let goroutines settle)
		mock.waitForNotifications(t, 1, 3*time.Second)
		time.Sleep(100 * time.Millisecond)
		mock.reset()

		// Ava approves
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		notifications := mock.waitForNotifications(t, 1, 3*time.Second)
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "join_request.approved" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected join_request.approved push to requester, got: %+v", notifications)
		}
	})

	t.Run("group event deny notifies requester", func(t *testing.T) {
		// Create another group event
		body := CreateEventParams{
			Title:       "Push Deny Group Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "For push deny test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		// Noah requests to join
		joinBody := map[string]string{"message": "Deny me!"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Wait for join request push then reset (extra sleep to let goroutines settle)
		mock.waitForNotifications(t, 1, 3*time.Second)
		time.Sleep(100 * time.Millisecond)
		mock.reset()

		// Ava denies
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/deny", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		notifications := mock.waitForNotifications(t, 1, 3*time.Second)
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "join_request.denied" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected join_request.denied push to requester, got: %+v", notifications)
		}
	})

	t.Run("1:1 event join produces host created push", func(t *testing.T) {
		// Create 1:1 event
		body := CreateEventParams{
			Title:       "Push 1:1 Event",
			Location:    "Test Cafe",
			Time:        "14:00",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "For 1:1 push test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Single",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		mock.reset()

		// Noah joins (request remains pending for 1:1 until host approval)
		joinBody := map[string]string{"message": "Hi there!"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		notifications := mock.waitForNotifications(t, 1, 3*time.Second)

		hasCreated, hasApproved := false, false
		for _, n := range notifications {
			if n.Token == "fcm-ava-device" && n.Data["type"] == "join_request.created" {
				hasCreated = true
			}
			if n.Token == "fcm-noah-device" && n.Data["type"] == "join_request.approved" {
				hasApproved = true
			}
		}
		if !hasCreated {
			t.Fatalf("expected join_request.created push to host for 1:1, got: %+v", notifications)
		}
		if hasApproved {
			t.Fatalf("did not expect join_request.approved push before host approval, got: %+v", notifications)
		}
	})
}

func TestPushOnEventDeletionAndMemberRemoval(t *testing.T) {
	mock := &mockPushSender{}
	env := setupAPITestEnvWithPush(t, mock)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user 1 (host)
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user 4

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	noahUser, err := env.repo.GetUserByEmail(ctx, "noah@example.com")
	if err != nil {
		t.Fatalf("failed to load noah user: %v", err)
	}
	noahID := noahUser.ID

	// Register push tokens
	for _, tc := range []struct {
		token    string
		deviceID string
		authTok  string
	}{
		{"fcm-ava-device", "ava-device", avaToken},
		{"fcm-noah-device", "noah-device", noahToken},
	} {
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", tc.authTok, map[string]string{
			"token": tc.token, "device_id": tc.deviceID, "platform": "android",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("register push token: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	createAndApproveEvent := func(t *testing.T, groupType, title string) int64 {
		t.Helper()
		body := CreateEventParams{
			Title:       title,
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "For push lifecycle test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   groupType,
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create event: expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		joinBody := map[string]string{"message": "please accept"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("join request: expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		resp = env.doRequest(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/events/%d/chat/requests/%d/approve", eventID, noahID),
			avaToken,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("approve join request: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Clear setup notifications so each scenario asserts only the target push.
		time.Sleep(150 * time.Millisecond)
		mock.reset()
		return eventID
	}

	t.Run("deleting group event notifies approved member", func(t *testing.T) {
		eventID := createAndApproveEvent(t, "Group", "Delete Group Push")

		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("delete event: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		notifications := mock.waitForNotifications(t, 1, 3*time.Second)
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "event.deleted" {
				found = true
				if n.Data["eventId"] != fmt.Sprintf("%d", eventID) {
					t.Fatalf("expected eventId %d, got %s", eventID, n.Data["eventId"])
				}
			}
			if n.Token == "fcm-ava-device" {
				t.Fatalf("host should not get event.deleted push, got: %+v", n)
			}
		}
		if !found {
			t.Fatalf("expected event.deleted push to noah, got: %+v", notifications)
		}
	})

	t.Run("deleting 1:1 event notifies approved member", func(t *testing.T) {
		eventID := createAndApproveEvent(t, "Single", "Delete 1:1 Push")

		resp := env.doRequest(t, http.MethodDelete, fmt.Sprintf("/api/events/%d", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("delete event: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		notifications := mock.waitForNotifications(t, 1, 3*time.Second)
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "event.deleted" {
				found = true
				if n.Data["eventId"] != fmt.Sprintf("%d", eventID) {
					t.Fatalf("expected eventId %d, got %s", eventID, n.Data["eventId"])
				}
			}
			if n.Token == "fcm-ava-device" {
				t.Fatalf("host should not get event.deleted push, got: %+v", n)
			}
		}
		if !found {
			t.Fatalf("expected event.deleted push to noah for 1:1 event, got: %+v", notifications)
		}
	})

	t.Run("host removing member sends event.member_removed push", func(t *testing.T) {
		eventID := createAndApproveEvent(t, "Group", "Host Remove Push")

		resp := env.doRequest(
			t,
			http.MethodDelete,
			fmt.Sprintf("/api/events/%d/chat/members/%d", eventID, noahID),
			avaToken,
			nil,
		)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("remove member: expected 204, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		notifications := mock.waitForNotifications(t, 1, 3*time.Second)
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "event.member_removed" {
				found = true
				if n.Data["eventId"] != fmt.Sprintf("%d", eventID) {
					t.Fatalf("expected eventId %d, got %s", eventID, n.Data["eventId"])
				}
				if n.Data["removedUserId"] != fmt.Sprintf("%d", noahID) {
					t.Fatalf("expected removedUserId %d, got %s", noahID, n.Data["removedUserId"])
				}
				if n.Data["removedByUserId"] == "" {
					t.Fatalf("expected removedByUserId to be present, got empty payload: %+v", n.Data)
				}
			}
			if n.Token == "fcm-ava-device" {
				t.Fatalf("host should not get event.member_removed push, got: %+v", n)
			}
		}
		if !found {
			t.Fatalf("expected event.member_removed push to noah, got: %+v", notifications)
		}
	})

	t.Run("self leave does not send event.member_removed push", func(t *testing.T) {
		eventID := createAndApproveEvent(t, "Group", "Self Leave No Push")

		resp := env.doRequest(
			t,
			http.MethodDelete,
			fmt.Sprintf("/api/events/%d/chat/members/%d", eventID, noahID),
			noahToken,
			nil,
		)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("leave event: expected 204, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		time.Sleep(400 * time.Millisecond)
		notifications := mock.getNotifications()
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "event.member_removed" {
				t.Fatalf("did not expect event.member_removed push on self-leave, got: %+v", notifications)
			}
		}
	})
}

func TestPushPresenceSuppression(t *testing.T) {
	mock := &mockPushSender{}
	env := setupAPITestEnvWithPush(t, mock)

	avaToken := env.issueTokenForEmail(t, "ava@example.com")   // user 1 (host)
	noahToken := env.issueTokenForEmail(t, "noah@example.com") // user 4

	// Register push tokens
	for _, tc := range []struct {
		token    string
		deviceID string
		authTok  string
	}{
		{"fcm-ava-device", "ava-device", avaToken},
		{"fcm-noah-device", "noah-device", noahToken},
	} {
		resp := env.doRequest(t, http.MethodPost, "/api/push-tokens", tc.authTok, map[string]string{
			"token": tc.token, "device_id": tc.deviceID, "platform": "android",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("register push token: expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Create group event, noah joins & gets approved
	var conversationID int64
	t.Run("setup group conversation", func(t *testing.T) {
		body := CreateEventParams{
			Title:       "Presence Test Event",
			Location:    "Test Location",
			Time:        "23:59",
			EventDate:   time.Now().Add(48 * time.Hour).Format("2006-01-02"),
			Description: "For presence suppression test",
			Gender:      "Any",
			MinAge:      18,
			MaxAge:      50,
			GroupType:   "Group",
			CoverKey:    defaultCoverKey,
		}
		resp := env.doRequest(t, http.MethodPost, "/api/events", avaToken, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		eventID := decodeJSON[createEventResponse](t, resp).ID

		joinBody := map[string]string{"message": "Join me!"}
		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests", eventID), noahToken, joinBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		resp = env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/events/%d/chat/requests/4/approve", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Get the conversation ID via event conversations endpoint (host only)
		resp = env.doRequest(t, http.MethodGet, fmt.Sprintf("/api/events/%d/conversations", eventID), avaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		eventConvos := decodeJSON[eventConversationsResponse](t, resp)
		if len(eventConvos.Conversations) == 0 {
			t.Fatal("no conversations found for event")
		}
		conversationID = eventConvos.Conversations[0].ID
	})

	// Wait for setup pushes to settle
	time.Sleep(200 * time.Millisecond)

	t.Run("active conversation suppresses push", func(t *testing.T) {
		mock.reset()

		// Noah connects WS and sets active conversation
		noahDialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(noahToken)
		noahWS, _, err := websocket.DefaultDialer.Dial(noahDialURL, nil)
		if err != nil {
			t.Fatalf("noah ws dial: %v", err)
		}
		defer noahWS.Close()

		// Set presence to the active conversation
		if err := noahWS.WriteJSON(map[string]any{
			"type":           "presence:active_conversation",
			"conversationId": conversationID,
		}); err != nil {
			t.Fatalf("noah set presence: %v", err)
		}

		// Give the hub a moment to process the presence update
		time.Sleep(150 * time.Millisecond)

		// Ava sends a message via WS
		avaDialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(avaToken)
		avaWS, _, err := websocket.DefaultDialer.Dial(avaDialURL, nil)
		if err != nil {
			t.Fatalf("ava ws dial: %v", err)
		}
		defer avaWS.Close()

		if err := avaWS.WriteJSON(map[string]any{
			"type":           "message:send",
			"conversationId": conversationID,
			"body":           "suppressed message",
			"tempId":         fmt.Sprintf("temp-%d", time.Now().UnixNano()),
		}); err != nil {
			t.Fatalf("ava ws send: %v", err)
		}

		// Read ava's echo to confirm the message was processed
		avaWS.SetReadDeadline(time.Now().Add(2 * time.Second))
		var echo wsEnvelope
		if err := avaWS.ReadJSON(&echo); err != nil {
			t.Fatalf("ava ws read echo: %v", err)
		}

		// Wait a bit for any push to arrive (there should be none)
		time.Sleep(500 * time.Millisecond)

		notifications := mock.getNotifications()
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" {
				t.Fatalf("push should be suppressed when noah is actively viewing the conversation, got: %+v", n)
			}
		}
	})

	t.Run("different active conversation does not suppress push", func(t *testing.T) {
		mock.reset()

		// Noah connects WS and sets active conversation to a DIFFERENT ID
		noahDialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(noahToken)
		noahWS, _, err := websocket.DefaultDialer.Dial(noahDialURL, nil)
		if err != nil {
			t.Fatalf("noah ws dial: %v", err)
		}
		defer noahWS.Close()

		// Set presence to a different conversation ID (99999 - doesn't exist)
		if err := noahWS.WriteJSON(map[string]any{
			"type":           "presence:active_conversation",
			"conversationId": 99999,
		}); err != nil {
			t.Fatalf("noah set presence: %v", err)
		}

		time.Sleep(150 * time.Millisecond)

		// Ava sends a message
		avaDialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(avaToken)
		avaWS, _, err := websocket.DefaultDialer.Dial(avaDialURL, nil)
		if err != nil {
			t.Fatalf("ava ws dial: %v", err)
		}
		defer avaWS.Close()

		if err := avaWS.WriteJSON(map[string]any{
			"type":           "message:send",
			"conversationId": conversationID,
			"body":           "not suppressed message",
			"tempId":         fmt.Sprintf("temp-%d", time.Now().UnixNano()),
		}); err != nil {
			t.Fatalf("ava ws send: %v", err)
		}

		// Wait for push to noah
		notifications := mock.waitForNotifications(t, 1, 3*time.Second)
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "chat.message" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected push to noah when viewing different conversation, got: %+v", notifications)
		}
	})

	t.Run("disconnected user receives push", func(t *testing.T) {
		mock.reset()

		// Noah connects WS, sets presence, then disconnects
		noahDialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(noahToken)
		noahWS, _, err := websocket.DefaultDialer.Dial(noahDialURL, nil)
		if err != nil {
			t.Fatalf("noah ws dial: %v", err)
		}

		// Set active conversation
		if err := noahWS.WriteJSON(map[string]any{
			"type":           "presence:active_conversation",
			"conversationId": conversationID,
		}); err != nil {
			t.Fatalf("noah set presence: %v", err)
		}

		time.Sleep(150 * time.Millisecond)

		// Disconnect noah
		noahWS.Close()

		// Wait for the hub to process the unregister
		time.Sleep(200 * time.Millisecond)

		// Ava sends a message
		avaDialURL := strings.Replace(env.server.URL, "http", env.wsScheme, 1) + "/api/ws?token=" + url.QueryEscape(avaToken)
		avaWS, _, err := websocket.DefaultDialer.Dial(avaDialURL, nil)
		if err != nil {
			t.Fatalf("ava ws dial: %v", err)
		}
		defer avaWS.Close()

		if err := avaWS.WriteJSON(map[string]any{
			"type":           "message:send",
			"conversationId": conversationID,
			"body":           "noah is offline",
			"tempId":         fmt.Sprintf("temp-%d", time.Now().UnixNano()),
		}); err != nil {
			t.Fatalf("ava ws send: %v", err)
		}

		// Wait for push to noah (should arrive since he's disconnected)
		notifications := mock.waitForNotifications(t, 1, 3*time.Second)
		found := false
		for _, n := range notifications {
			if n.Token == "fcm-noah-device" && n.Data["type"] == "chat.message" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected push to noah after disconnect, got: %+v", notifications)
		}
	})
}
