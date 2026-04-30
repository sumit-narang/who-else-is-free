package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ChatHub owns the in-memory broker that orchestrates WebSocket traffic.
// It keeps long-lived goroutines, channels, and lookup tables that decide
// which connected clients should receive which conversation events.
type ChatHub struct {
	repo          *EventRepository
	signer        *tokenSigner
	pushSender    PushSender
	register      chan *ChatClient                   // fan-in of freshly upgraded sockets
	unregister    chan *ChatClient                   // fan-in of disconnecting sockets
	broadcast     chan chatBroadcast                 // queue of conversation payloads to fan back out
	membership    chan membershipUpdate              // join/leave notifications from the HTTP layer
	presence      chan presenceUpdate                // presence updates from readPump goroutines
	subscriptions map[int64]map[*ChatClient]struct{} // conversationID -> live clients in that room
	clientsByUser map[int64]map[*ChatClient]struct{} // userID -> live sockets for that user
	activeConvos  map[int64]int64                    // userID -> activeConversationID (for push suppression)
}

// chatBroadcast represents a message that should be fanned out to listeners.
type chatBroadcast struct {
	conversationID int64
	payload        []byte
}

type membershipUpdate struct {
	conversationID int64
	userID         int64
	action         string
}

type presenceUpdate struct {
	userID               int64
	activeConversationID int64 // 0 means cleared
}

type membershipEvent struct {
	Type           string `json:"type"`
	ConversationID int64  `json:"conversationId"`
	UserID         int64  `json:"userId"`
	Action         string `json:"action"`
}

type joinRequestEvent struct {
	Type           string             `json:"type"`
	ConversationID int64              `json:"conversationId"`
	Action         string             `json:"action"`
	Request        joinRequestPayload `json:"request"`
}

type joinRequestPayload struct {
	ID        int64                          `json:"id"`
	EventID   int64                          `json:"eventId"`
	UserID    int64                          `json:"userId"`
	Message   string                         `json:"message"`
	Status    string                         `json:"status"`
	CreatedAt string                         `json:"createdAt"`
	Requester conversationParticipantPayload `json:"requester"`
}

type conversationParticipantPayload struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Avatar *string `json:"avatar,omitempty"`
}

// ChatClient wraps a single WebSocket connection and bookkeeping that helps the
// hub keep track of which conversations this socket should hear about.
type ChatClient struct {
	hub            *ChatHub
	conn           *websocket.Conn
	send           chan []byte
	userID         int64
	subscriptions  map[int64]struct{}
	messageHistory []time.Time
}

const (
	// messageRateWindow/messageRateLimit implement a simple anti-spam window.
	messageRateWindow      = 10 * time.Second
	messageRateLimit       = 30
	messageHistoryCapacity = 64
)

type inboundEnvelope struct {
	Type           string `json:"type"`
	ConversationID int64  `json:"conversationId"`
	Body           string `json:"body"`
	TempID         string `json:"tempId"`
}

type outboundMessage struct {
	Type    string         `json:"type"`
	TempID  string         `json:"tempId,omitempty"`
	Message messagePayload `json:"message"`
}

type messagePayload struct {
	ID             int64  `json:"id"`
	ConversationID int64  `json:"conversationId"`
	SenderID       int64  `json:"senderId"`
	Body           string `json:"body"`
	CreatedAt      string `json:"createdAt"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func NewChatHub(repo *EventRepository, signer *tokenSigner, pushSender PushSender) *ChatHub {
	return &ChatHub{
		repo:          repo,
		signer:        signer,
		pushSender:    pushSender,
		register:      make(chan *ChatClient),
		unregister:    make(chan *ChatClient),
		broadcast:     make(chan chatBroadcast),
		membership:    make(chan membershipUpdate, 16),
		presence:      make(chan presenceUpdate, 16),
		subscriptions: make(map[int64]map[*ChatClient]struct{}),
		clientsByUser: make(map[int64]map[*ChatClient]struct{}),
		activeConvos:  make(map[int64]int64),
	}
}

// Run processes register/unregister/broadcast events on the hub.
func (h *ChatHub) Run() {
	for {
		select {
		case client := <-h.register:
			// A connection just completed the WS handshake: mirror the user's
			// conversation memberships into the hub's lookup table.
			for conversationID := range client.subscriptions {
				if _, ok := h.subscriptions[conversationID]; !ok {
					h.subscriptions[conversationID] = make(map[*ChatClient]struct{})
				}
				h.subscriptions[conversationID][client] = struct{}{}
			}
			h.attachClient(client)
		case client := <-h.unregister:
			// A connection has gone away: close it if needed and remove every
			// pointer to it so the GC can reclaim the client.
			if err := client.conn.Close(); err != nil {
				log.Printf("chat client close error: %v", err)
			}
			h.detachClient(client)
			// Clear presence if this was the last socket for the user.
			if _, hasOther := h.clientsByUser[client.userID]; !hasOther {
				delete(h.activeConvos, client.userID)
			}
			for conversationID := range client.subscriptions {
				if subs, ok := h.subscriptions[conversationID]; ok {
					delete(subs, client)
					if len(subs) == 0 {
						delete(h.subscriptions, conversationID)
					}
				}
			}
		case msg := <-h.broadcast:
			// Persisted message payloads are fanned out to every subscribed client.
			h.pushToConversation(msg.conversationID, msg.payload)
		case update := <-h.membership:
			// HTTP handlers report membership churn through this channel so the hub
			// can update live sockets and emit `conversation:membership` events.
			h.applyMembershipUpdate(update)
		case p := <-h.presence:
			if p.activeConversationID == 0 {
				delete(h.activeConvos, p.userID)
			} else {
				h.activeConvos[p.userID] = p.activeConversationID
			}
		}
	}
}

func (h *ChatHub) attachClient(client *ChatClient) {
	if _, ok := h.clientsByUser[client.userID]; !ok {
		h.clientsByUser[client.userID] = make(map[*ChatClient]struct{})
	}
	h.clientsByUser[client.userID][client] = struct{}{}
}

func (h *ChatHub) detachClient(client *ChatClient) {
	if peers, ok := h.clientsByUser[client.userID]; ok {
		delete(peers, client)
		if len(peers) == 0 {
			delete(h.clientsByUser, client.userID)
		}
	}
}

func (h *ChatHub) pushToConversation(conversationID int64, payload []byte) {
	subs := h.subscriptions[conversationID]
	if subs == nil {
		return
	}
	for client := range subs {
		select {
		case client.send <- payload:
		default:
			close(client.send)
			delete(subs, client)
			h.detachClient(client)
		}
	}
	if len(subs) == 0 {
		delete(h.subscriptions, conversationID)
	}
}

func (h *ChatHub) applyMembershipUpdate(update membershipUpdate) {
	event := membershipEvent{
		Type:           "conversation:membership",
		ConversationID: update.conversationID,
		UserID:         update.userID,
		Action:         update.action,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("marshal membership event failed: %v", err)
		return
	}

	switch update.action {
	case "added":
		// Ensure the conversation has a subscriber set, then mirror the new
		// membership down to any sockets owned by the joining user.
		if _, ok := h.subscriptions[update.conversationID]; !ok {
			h.subscriptions[update.conversationID] = make(map[*ChatClient]struct{})
		}
		if clients, ok := h.clientsByUser[update.userID]; ok {
			for client := range clients {
				client.subscriptions[update.conversationID] = struct{}{}
				h.subscriptions[update.conversationID][client] = struct{}{}
			}
		}
		h.pushToConversation(update.conversationID, payload)
	case "removed":
		// Broadcast first so removed clients receive the membership event
		// before being detached from the conversation.
		h.pushToConversation(update.conversationID, payload)

		// Remove the conversation from each socket owned by the departing user
		// and drop any room set that becomes empty.
		if clients, ok := h.clientsByUser[update.userID]; ok {
			if subs, ok := h.subscriptions[update.conversationID]; ok {
				for client := range clients {
					delete(client.subscriptions, update.conversationID)
					delete(subs, client)
				}
				if len(subs) == 0 {
					delete(h.subscriptions, update.conversationID)
				}
			}
		}
	default:
		log.Printf("unknown membership action: %s", update.action)
		return
	}
}

// shouldSuppressPush returns true if the user has a live socket currently viewing
// the given conversation, meaning a push notification would be redundant.
func (h *ChatHub) shouldSuppressPush(userID, conversationID int64) bool {
	// Check if user has a live socket
	if _, hasSocket := h.clientsByUser[userID]; !hasSocket {
		return false
	}
	// Check if they're actively viewing this conversation
	activeConvo, ok := h.activeConvos[userID]
	return ok && activeConvo == conversationID
}

func (h *ChatHub) NotifyMembership(conversationID, userID int64, action string) {
	update := membershipUpdate{
		conversationID: conversationID,
		userID:         userID,
		action:         action,
	}
	select {
	case h.membership <- update:
	default:
		go func() {
			h.membership <- update
		}()
	}
}

// handleWebSocket authenticates via token query param and upgrades to WS.
func (h *ChatHub) handleWebSocket(c *gin.Context) {
	token := c.Query("token")
	if strings.TrimSpace(token) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token is required"})
		return
	}

	claims, err := h.signer.verify(token)
	if err != nil {
		status := http.StatusUnauthorized
		if err == errExpiredToken {
			status = http.StatusUnauthorized
		}
		c.JSON(status, gin.H{"error": "invalid or expired token"})
		return
	}

	userID := claims.UserID

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	// JWTs are stateless, so a token from another device can outlive account
	// deletion. Check the user row before upgrading so deleted accounts cannot
	// keep opening new chat sockets.
	if _, err := h.repo.GetUserByID(ctx, userID); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired, please sign in again"})
			return
		}
		log.Printf("websocket user validation failed for %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to validate session"})
		return
	}

	// Upgrade the HTTP request into a WebSocket connection. From here on the
	// client and server communicate using frames handled by read/write pumps.
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}

	conversations, err := h.repo.ListConversations(ctx, userID)
	if err != nil {
		log.Printf("list conversations failed: %v", err)
		conn.Close()
		return
	}

	client := &ChatClient{
		hub:           h,
		conn:          conn,
		send:          make(chan []byte, 8),
		userID:        userID,
		subscriptions: make(map[int64]struct{}),
	}

	for _, convo := range conversations {
		client.subscriptions[convo.ID] = struct{}{}
	}

	// Registration hands the client to the hub goroutine. From this point the
	// hub owns the lifecycle and the pumps keep the socket alive.
	h.register <- client

	go client.writePump()
	client.readPump()
}

// readPump listens for incoming frames and dispatches recognized commands.
func (c *ChatClient) readPump() {
	defer func() {
		c.hub.unregister <- c
	}()
	c.conn.SetReadLimit(1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	// Loop forever until the client disconnects or an unrecoverable error occurs.
	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("readPump error: %v", err)
			}
			break
		}

		var inbound inboundEnvelope
		if err := json.Unmarshal(payload, &inbound); err != nil {
			log.Printf("invalid inbound payload: %v", err)
			continue
		}

		switch inbound.Type {
		case "message:send":
			c.handleSend(inbound)
		case "ping":
			c.send <- []byte(`{"type":"pong"}`)
		case "presence:active_conversation":
			c.hub.presence <- presenceUpdate{
				userID:               c.userID,
				activeConversationID: inbound.ConversationID, // 0 means cleared
			}
		default:
			log.Printf("unknown message type: %s", inbound.Type)
		}
	}
}

// writePump forwards outbound chat events and keep-alive pings.
func (c *ChatClient) writePump() {
	ticker := time.NewTicker(50 * time.Second)
	defer func() {
		ticker.Stop()
		if err := c.conn.Close(); err != nil {
			log.Printf("writePump close error: %v", err)
		}
	}()

	// The writer listens on the buffered send channel and flushes frames back to
	// the mobile clients. The periodic ping keeps intermediaries from closing the
	// connection when idle.
	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleSend validates membership, stores, and broadcasts a message.
func (c *ChatClient) handleSend(inbound inboundEnvelope) {
	if inbound.ConversationID == 0 || strings.TrimSpace(inbound.Body) == "" {
		return
	}
	now := time.Now()
	if !c.allowMessage(now) {
		log.Printf("user %d exceeded message rate limit", c.userID)
		c.send <- []byte(`{"type":"system:error","code":"rate_limited"}`)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	// Authorize against the DB to ensure the sender is still a member. This keeps
	// the hub's in-memory view honest even if membership changes while the socket
	// was offline or before the hub processed a membership update.
	allowed, err := c.hub.repo.IsConversationMember(ctx, inbound.ConversationID, c.userID)
	if err != nil {
		log.Printf("membership check failed: %v", err)
		return
	}
	if !allowed {
		log.Printf("user %d attempted to send to conversation %d without membership", c.userID, inbound.ConversationID)
		return
	}

	params := CreateMessageParams{
		ConversationID: inbound.ConversationID,
		SenderID:       c.userID,
		Body:           inbound.Body,
		DeliveryStatus: "sent",
	}

	msg, err := c.hub.repo.CreateMessage(ctx, params)
	if err != nil {
		log.Printf("create message failed: %v", err)
		return
	}

	if err := c.hub.repo.UpdateReadState(ctx, msg.ConversationID, c.userID, msg.ID); err != nil {
		log.Printf("update read state after send failed: %v", err)
	}

	c.hub.emitChatMessage(msg, inbound.TempID)

	// Send push notifications to offline/inactive conversation members.
	senderName := ""
	eventTitle := ""
	if sender, err := c.hub.repo.GetUserByID(ctx, c.userID); err == nil {
		senderName = sender.Name
	}
	// Try to resolve the event title for this conversation's push notification title.
	if convos, err := c.hub.repo.ListConversations(ctx, c.userID); err == nil {
		for _, conv := range convos {
			if conv.ID == msg.ConversationID && conv.Event != nil {
				eventTitle = conv.Event.Title
				break
			}
		}
	}
	c.hub.sendPushForChatMessage(msg, senderName, eventTitle)
}

// allowMessage implements a sliding window limiter to curb rapid sends.
func (c *ChatClient) allowMessage(now time.Time) bool {
	windowStart := now.Add(-messageRateWindow)
	filtered := c.messageHistory[:0]
	for _, ts := range c.messageHistory {
		if ts.After(windowStart) {
			filtered = append(filtered, ts)
		}
	}
	c.messageHistory = filtered

	if len(c.messageHistory) >= messageRateLimit {
		return false
	}

	c.messageHistory = append(c.messageHistory, now)
	if len(c.messageHistory) > messageHistoryCapacity {
		c.messageHistory = c.messageHistory[len(c.messageHistory)-messageHistoryCapacity:]
	}
	return true
}

// RegisterChatRoutes mounts all chat-related REST endpoints under the provided
// router group. The caller is expected to attach authentication middleware
// before invoking this so that handlers can read the session from context.
func RegisterChatRoutes(router *gin.RouterGroup, repo *EventRepository, hub *ChatHub) {
	handler := &ChatHTTPHandler{repo: repo, hub: hub}

	router.GET("/conversations", handler.listConversations)
	router.GET("/conversations/:id/messages", handler.listMessages)
	router.POST("/conversations", handler.createConversation)
	router.GET("/events/:id/chat/requests", handler.listJoinRequests)
	router.GET("/events/:id/conversations", handler.listEventConversations)
	router.GET("/chat/requests/me", handler.listUserJoinRequests)
	router.POST("/events/:id/chat/requests", handler.requestJoin)
	router.POST("/events/:id/chat/requests/:userId/approve", handler.approveJoin)
	router.POST("/events/:id/chat/requests/:userId/deny", handler.denyJoin)
	router.DELETE("/events/:id/chat/members/:userId", handler.removeMember)
	router.DELETE("/events/:id/chat/requests/me", handler.cancelJoinRequest)
	router.POST("/events/:id/members/:userId/report", handler.reportMember)
	router.DELETE("/events/:id/members/:userId/block", handler.unblockMember)
}

type ChatHTTPHandler struct {
	repo *EventRepository
	hub  *ChatHub
}

type createConversationRequest struct {
	Title     *string `json:"title"`
	MemberIDs []int64 `json:"memberIds"`
}

type createConversationResponse struct {
	Conversation ConversationSummary `json:"conversation"`
}

type listConversationResponse struct {
	Conversations []ConversationSummary `json:"conversations"`
}

type listMessagesResponse struct {
	Messages []messagePayload `json:"messages"`
}

type joinRequestResponse struct {
	Request JoinRequestView `json:"request"`
}

type joinRequestListResponse struct {
	Requests []JoinRequestView `json:"requests"`
}

type joinRequestBody struct {
	Message string `json:"message" binding:"required"`
}

// createConversation provisions a new conversation (optionally titled) and
// ensures the creator is a member. The request body accepts an optional title
// and a list of member IDs. The creator is automatically included if omitted.
//
// Responses:
//   - 201 with a hydrated ConversationSummary on success
//   - 401 if the caller has no session
//   - 400 for invalid JSON
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) createConversation(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	var payload createConversationRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	convo, err := h.repo.CreateConversation(ctx, payload.Title, claims.UserID, payload.MemberIDs, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create conversation"})
		return
	}

	summary, err := h.repo.hydrateConversationSummary(ctx, *convo, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load conversation details"})
		return
	}

	c.JSON(http.StatusCreated, createConversationResponse{Conversation: summary})
}

// listConversations returns all conversations visible to the current user,
// enriched with participants, last message preview, unread counts, and
// optional event metadata.
//
// Responses:
//   - 200 with a list of ConversationSummary items
//   - 401 if the caller has no session
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) listConversations(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	conversations, err := h.repo.ListConversations(ctx, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load conversations"})
		return
	}

	c.JSON(http.StatusOK, listConversationResponse{Conversations: conversations})
}

// listMessages returns the most recent messages for a conversation the user
// can access. It validates membership, supports basic limit/offset paging, and
// advances the caller's read cursor to the newest returned message.
//
// Query params: `limit` (default 20), `offset` (default 0).
// Responses:
//   - 200 with a chronologically ordered message list
//   - 401 if the caller has no session
//   - 400 for invalid conversation id
//   - 403 if the user is not a member of the conversation
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) listMessages(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	conversationIDParam := c.Param("id")
	conversationID, err := strconv.ParseInt(conversationIDParam, 10, 64)
	if err != nil || conversationID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return
	}

	limitParam := c.DefaultQuery("limit", "20")
	offsetParam := c.DefaultQuery("offset", "0")

	limit, err := strconv.Atoi(limitParam)
	if err != nil {
		limit = 20
	}
	offset, err := strconv.Atoi(offsetParam)
	if err != nil {
		offset = 0
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	isMember, err := h.repo.IsConversationMember(ctx, conversationID, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify membership"})
		return
	}
	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"error": "conversation access denied"})
		return
	}

	messages, err := h.repo.ListMessages(ctx, conversationID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load messages"})
		return
	}

	if len(messages) > 0 {
		latest := messages[0]
		if err := h.repo.UpdateReadState(ctx, conversationID, claims.UserID, latest.ID); err != nil {
			log.Printf("update read state failed: %v", err)
		}
	}

	payloads := make([]messagePayload, 0, len(messages))
	for _, msg := range messages {
		payloads = append(payloads, messagePayload{
			ID:             msg.ID,
			ConversationID: msg.ConversationID,
			SenderID:       msg.SenderID,
			Body:           msg.Body,
			CreatedAt:      msg.CreatedAt.Format(time.RFC3339Nano),
		})
	}

	c.JSON(http.StatusOK, listMessagesResponse{Messages: payloads})
}

// requestJoin creates a pending request for the current user to join an event.
// For Group events, the request is emitted to the event conversation.
// For 1:1 events, no private conversation is created until the host approves.
//
// Responses:
//   - 201 with the created join request
//   - 401 if the caller has no session
//   - 400 for invalid event id
//   - 404 if the event is missing
//   - 409 if a request already exists or the user is already a member
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) requestJoin(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	var payload joinRequestBody
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	message := strings.TrimSpace(payload.Message)
	if message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	req, err := h.repo.CreateJoinRequest(ctx, eventID, claims.UserID, message)
	if err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		case errors.Is(err, ErrUsersBlocked):
			c.JSON(http.StatusForbidden, gin.H{"error": "interaction blocked between users"})
		case errors.Is(err, ErrAlreadyConversationMember):
			c.JSON(http.StatusConflict, gin.H{"error": "already a member of this chat"})
		case errors.Is(err, ErrJoinRequestExists):
			c.JSON(http.StatusConflict, gin.H{"error": "a pending request already exists"})
		case errors.Is(err, ErrConversationNotFound):
			c.JSON(http.StatusInternalServerError, gin.H{"error": "chat conversation missing for event"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create join request"})
		}
		return
	}

	user, err := h.repo.GetUserByID(ctx, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load requester"})
		return
	}

	// Get event to check if it's a 1:1 event
	event, err := h.repo.GetEventByID(ctx, eventID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load event"})
		return
	}

	view := JoinRequestView{
		ConversationJoinRequest: *req,
		Requester: ConversationParticipant{
			ID:     user.ID,
			Name:   user.Name,
			Avatar: user.Avatar,
		},
	}

	if event.GroupType == "Group" {
		// For Group events, emit to the main conversation.
		convo, err := h.repo.GetConversationByEventID(ctx, eventID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "chat conversation missing for event"})
			return
		}
		h.hub.emitJoinRequestEvent(convo.ID, "created", view)

		// Push: notify host about new join request (includes conversation context).
		h.hub.sendPushToUser(event.UserID, map[string]string{
			"type":           "join_request.created",
			"eventId":        strconv.FormatInt(eventID, 10),
			"conversationId": strconv.FormatInt(convo.ID, 10),
			"title":          event.Title,
			"body":           fmt.Sprintf("%s wants to join your event", user.Name),
		})
	} else {
		// For 1:1 events, the request remains pending until host approval.
		h.hub.sendPushToUser(event.UserID, map[string]string{
			"type":    "join_request.created",
			"eventId": strconv.FormatInt(eventID, 10),
			"title":   event.Title,
			"body":    fmt.Sprintf("%s wants to join your event", user.Name),
		})
	}

	c.JSON(http.StatusCreated, joinRequestResponse{Request: view})
}

// listJoinRequests returns join requests for the specified event.
// Only the event host may view this list.
// By default it returns pending requests only.
// Set `include_approved=1` (or `true`) to include approved requests too.
func (h *ChatHTTPHandler) listJoinRequests(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	event, err := h.repo.GetEventByID(ctx, eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load event"})
		return
	}

	if event.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the event host can view join requests"})
		return
	}

	includeApproved := false
	switch strings.ToLower(strings.TrimSpace(c.Query("include_approved"))) {
	case "1", "true", "yes":
		includeApproved = true
	}

	requests, err := h.repo.ListJoinRequests(ctx, eventID, includeApproved)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list join requests"})
		return
	}

	c.JSON(http.StatusOK, joinRequestListResponse{Requests: requests})
}

// listUserJoinRequests returns join requests created by the current user.
// By default it includes only pending requests.
// Set `include_approved=1` (or `true`) to include approved requests as well.
func (h *ChatHTTPHandler) listUserJoinRequests(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	includeApproved := false
	switch strings.ToLower(strings.TrimSpace(c.Query("include_approved"))) {
	case "1", "true", "yes":
		includeApproved = true
	}

	requests, err := h.repo.ListJoinRequestsByUser(ctx, claims.UserID, includeApproved)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list join requests"})
		return
	}

	c.JSON(http.StatusOK, joinRequestListResponse{Requests: requests})
}

// approveJoin allows the event host to approve a user's pending join request.
// For Group events, the user is added to the shared event conversation.
// For 1:1 events, a private host/requester conversation is created.
//
// Responses:
//   - 200 with the approved request and `conversationId`
//   - 401 if the caller has no session
//   - 400 for invalid path params
//   - 403 if the caller is not the event host
//   - 404 if the event or pending request is not found
//   - 409 if the user is already a member
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) approveJoin(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	userIDParam := c.Param("userId")
	userID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	req, err := h.repo.ApproveJoinRequest(ctx, eventID, userID, claims.UserID)
	if err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		case errors.Is(err, ErrNotEventHost):
			c.JSON(http.StatusForbidden, gin.H{"error": "only the event host can approve requests"})
		case errors.Is(err, ErrJoinRequestNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "pending request not found"})
		case errors.Is(err, ErrAlreadyConversationMember):
			c.JSON(http.StatusConflict, gin.H{"error": "user already a member"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to approve join request"})
		}
		return
	}

	event, err := h.repo.GetEventByID(ctx, eventID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load event"})
		return
	}

	var convo *Conversation
	if event.GroupType == "Single" {
		convo, err = h.repo.findUserConversationForEventPublic(ctx, eventID, userID)
	} else {
		convo, err = h.repo.GetConversationByEventID(ctx, eventID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load conversation"})
		return
	}

	view, err := h.buildJoinRequestView(ctx, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load requester"})
		return
	}

	h.hub.NotifyMembership(convo.ID, userID, "added")
	if event.GroupType == "Single" && event.UserID != userID {
		// The host is already a member in DB; emit membership so host sockets
		// subscribe immediately to the newly created private conversation.
		h.hub.NotifyMembership(convo.ID, event.UserID, "added")
	}
	if event.GroupType == "Single" {
		if err := h.emitApprovedIntroMessage(ctx, convo.ID, userID, req.Message); err != nil {
			log.Printf("emit approved intro message failed: %v", err)
		}
	}
	if event.GroupType == "Group" {
		h.hub.emitJoinRequestEvent(convo.ID, "approved", view)
	}
	if err := h.postJoinAnnouncement(ctx, convo.ID, view); err != nil {
		log.Printf("post join announcement failed: %v", err)
	}

	// Push: notify the requester they were approved
	h.hub.sendPushToUser(userID, map[string]string{
		"type":           "join_request.approved",
		"eventId":        strconv.FormatInt(eventID, 10),
		"conversationId": strconv.FormatInt(convo.ID, 10),
		"title":          event.Title,
		"body":           "Your request to join was approved!",
	})

	c.JSON(http.StatusOK, gin.H{
		"request":        view,
		"conversationId": convo.ID,
	})
}

// denyJoin allows the event host to deny a user's pending join request.
// This does not alter conversation membership and simply records the denial.
//
// Responses:
//   - 200 with the updated (denied) request
//   - 401 if the caller has no session
//   - 400 for invalid path params
//   - 403 if the caller is not the event host
//   - 404 if the event or pending request is not found
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) denyJoin(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	userIDParam := c.Param("userId")
	userID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	req, err := h.repo.DenyJoinRequest(ctx, eventID, userID, claims.UserID)
	if err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		case errors.Is(err, ErrNotEventHost):
			c.JSON(http.StatusForbidden, gin.H{"error": "only the event host can deny requests"})
		case errors.Is(err, ErrJoinRequestNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "pending request not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to deny join request"})
		}
		return
	}

	view, err := h.buildJoinRequestView(ctx, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load requester"})
		return
	}

	convo, err := h.repo.GetConversationByEventID(ctx, eventID)
	if err == nil {
		h.hub.emitJoinRequestEvent(convo.ID, "denied", view)
	}

	// Push: notify the requester they were denied
	event, _ := h.repo.GetEventByID(ctx, eventID)
	eventTitle := ""
	if event != nil {
		eventTitle = event.Title
	}
	h.hub.sendPushToUser(userID, map[string]string{
		"type":    "join_request.denied",
		"eventId": strconv.FormatInt(eventID, 10),
		"title":   eventTitle,
		"body":    "Your request to join was declined",
	})

	c.JSON(http.StatusOK, joinRequestResponse{Request: view})
}

// cancelJoinRequest allows a user to withdraw their pending join request.
//
// Responses:
//   - 200 on success
//   - 401 if the caller has no session
//   - 400 for invalid event id
//   - 404 if no pending request exists
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) cancelJoinRequest(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	err = h.repo.CancelJoinRequest(ctx, eventID, claims.UserID)
	if err != nil {
		if errors.Is(err, ErrJoinRequestNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "no pending request found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "request cancelled"})
}

// removeMember removes a user from an event's group conversation. Only the
// event host can remove others; any user can remove themselves (leave). The
// hub is notified so live sockets stop receiving that conversation's events.
//
// Responses:
//   - 204 on success
//   - 401 if the caller has no session
//   - 400 for invalid path params or trying to remove the host
//   - 403 if not authorized to update membership
//   - 404 if the event or target membership is not found
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) removeMember(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	userIDParam := c.Param("userId")
	userID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	event, err := h.repo.GetEventByID(ctx, eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load event"})
		return
	}

	if claims.UserID != event.UserID && claims.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized to update membership"})
		return
	}

	// Find the user's conversation BEFORE removal so we have the ID for WebSocket notification.
	// This is important for 1:1 events where multiple private conversations can exist.
	convo, _ := h.repo.findUserConversationForEventPublic(ctx, eventID, userID)

	if err := h.repo.RemoveEventMember(ctx, eventID, userID); err != nil {
		switch {
		case errors.Is(err, ErrCannotRemoveHost):
			c.JSON(http.StatusBadRequest, gin.H{"error": "event host cannot leave the event chat"})
		case errors.Is(err, ErrNotConversationMember):
			c.JSON(http.StatusNotFound, gin.H{"error": "user is not part of this chat"})
		case errors.Is(err, ErrEventNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update membership"})
		}
		return
	}

	if convo != nil {
		h.hub.NotifyMembership(convo.ID, userID, "removed")
	}
	if h.hub != nil && claims.UserID == event.UserID && userID != claims.UserID {
		h.hub.sendPushToUser(userID, map[string]string{
			"type":            "event.member_removed",
			"eventId":         strconv.FormatInt(eventID, 10),
			"title":           event.Title,
			"body":            "The host removed you from this event.",
			"removedUserId":   strconv.FormatInt(userID, 10),
			"removedByUserId": strconv.FormatInt(claims.UserID, 10),
		})
	}

	c.Status(http.StatusNoContent)
}

// containsInt64 reports whether target is present in values. Small helper used
// when constructing membership lists.
func containsInt64(values []int64, target int64) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func (h *ChatHub) emitChatMessage(msg *Message, tempID string) {
	envelope := outboundMessage{
		Type:   "message:new",
		TempID: tempID,
		Message: messagePayload{
			ID:             msg.ID,
			ConversationID: msg.ConversationID,
			SenderID:       msg.SenderID,
			Body:           msg.Body,
			CreatedAt:      msg.CreatedAt.Format(time.RFC3339Nano),
		},
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		log.Printf("marshal outbound failed: %v", err)
		return
	}
	h.broadcast <- chatBroadcast{conversationID: msg.ConversationID, payload: payload}
}

func (h *ChatHub) emitJoinRequestEvent(conversationID int64, action string, view JoinRequestView) {
	envelope := joinRequestEvent{
		Type:           "conversation:join_request",
		ConversationID: conversationID,
		Action:         action,
		Request:        mapJoinRequestPayload(view),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		log.Printf("marshal join request event failed: %v", err)
		return
	}
	h.broadcast <- chatBroadcast{conversationID: conversationID, payload: payload}
}

// sendPushForChatMessage sends push notifications to all conversation members
// who are not the sender and not actively viewing the conversation.
func (h *ChatHub) sendPushForChatMessage(msg *Message, senderName, eventTitle string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		memberIDs, err := h.repo.ListConversationMemberIDs(ctx, msg.ConversationID)
		if err != nil {
			log.Printf("push: list conversation members failed: %v", err)
			return
		}

		var recipientIDs []int64
		for _, id := range memberIDs {
			if id == msg.SenderID {
				continue
			}
			if h.shouldSuppressPush(id, msg.ConversationID) {
				continue
			}
			recipientIDs = append(recipientIDs, id)
		}
		if len(recipientIDs) == 0 {
			return
		}

		tokens, err := h.repo.ListPushTokensByUserIDs(ctx, recipientIDs)
		if err != nil {
			log.Printf("push: list push tokens failed: %v", err)
			return
		}
		if len(tokens) == 0 {
			return
		}

		title := eventTitle
		if title == "" {
			title = senderName
		}
		bodyPreview := msg.Body
		if len(bodyPreview) > 100 {
			bodyPreview = bodyPreview[:100] + "..."
		}
		body := fmt.Sprintf("%s: %s", senderName, bodyPreview)

		var notifications []PushNotification
		for _, t := range tokens {
			notifications = append(notifications, PushNotification{
				Token: t.Token,
				Data: map[string]string{
					"type":           "chat.message",
					"conversationId": strconv.FormatInt(msg.ConversationID, 10),
					"senderId":       strconv.FormatInt(msg.SenderID, 10),
					"senderName":     senderName,
					"title":          title,
					"body":           body,
				},
			})
		}

		if err := h.pushSender.SendBatch(ctx, notifications); err != nil {
			log.Printf("push: send batch failed: %v", err)
		}
	}()
}

// sendPushToUser sends a push notification to a specific user's devices.
func (h *ChatHub) sendPushToUser(userID int64, data map[string]string) {
	h.sendPushToUsers([]int64{userID}, data)
}

// sendPushToUsers sends the same push payload to multiple users.
func (h *ChatHub) sendPushToUsers(userIDs []int64, data map[string]string) {
	if len(userIDs) == 0 {
		return
	}

	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		unique = append(unique, userID)
	}
	if len(unique) == 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tokens, err := h.repo.ListPushTokensByUserIDs(ctx, unique)
		if err != nil {
			log.Printf("push: list push tokens for users %v failed: %v", unique, err)
			return
		}
		if len(tokens) == 0 {
			return
		}

		var notifications []PushNotification
		for _, t := range tokens {
			notifications = append(notifications, PushNotification{
				Token: t.Token,
				Data:  data,
			})
		}

		if err := h.pushSender.SendBatch(ctx, notifications); err != nil {
			log.Printf("push: send to users %v failed: %v", unique, err)
		}
	}()
}

func mapJoinRequestPayload(view JoinRequestView) joinRequestPayload {
	return joinRequestPayload{
		ID:        view.ID,
		EventID:   view.EventID,
		UserID:    view.UserID,
		Message:   view.Message,
		Status:    view.Status,
		CreatedAt: view.CreatedAt.Format(time.RFC3339Nano),
		Requester: conversationParticipantPayload{
			ID:     view.Requester.ID,
			Name:   view.Requester.Name,
			Avatar: view.Requester.Avatar,
		},
	}
}

func (h *ChatHTTPHandler) buildJoinRequestView(ctx context.Context, req *ConversationJoinRequest) (JoinRequestView, error) {
	user, err := h.repo.GetUserByID(ctx, req.UserID)
	if err != nil {
		return JoinRequestView{}, err
	}
	return JoinRequestView{
		ConversationJoinRequest: *req,
		Requester: ConversationParticipant{
			ID:     user.ID,
			Name:   user.Name,
			Avatar: user.Avatar,
		},
	}, nil
}

func (h *ChatHTTPHandler) postJoinAnnouncement(ctx context.Context, conversationID int64, view JoinRequestView) error {
	msgBody := fmt.Sprintf("%s joined the chat", view.Requester.Name)
	msg, err := h.repo.CreateMessage(ctx, CreateMessageParams{
		ConversationID: conversationID,
		SenderID:       view.UserID,
		Body:           msgBody,
		DeliveryStatus: "sent",
	})
	if err != nil {
		return err
	}
	h.hub.emitChatMessage(msg, "")
	return nil
}

func (h *ChatHTTPHandler) emitApprovedIntroMessage(ctx context.Context, conversationID, senderID int64, intro string) error {
	trimmed := strings.TrimSpace(intro)
	if trimmed == "" {
		return nil
	}

	// Intro text is persisted during approval; emit the latest persisted row so
	// connected clients see it immediately without a manual refresh.
	messages, err := h.repo.ListMessages(ctx, conversationID, 1, 0)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}

	latest := messages[0]
	if latest.SenderID != senderID || strings.TrimSpace(latest.Body) != trimmed {
		return nil
	}

	h.hub.emitChatMessage(&latest, "")
	return nil
}

type listEventConversationsResponse struct {
	Conversations []ConversationSummary `json:"conversations"`
}

// listEventConversations returns all conversations linked to an event.
// Used for 1:1 events to list all host-requester private conversations.
// Only the event host can call this endpoint.
//
// Responses:
//   - 200 with a list of conversations (each includes unread_count)
//   - 401 if the caller has no session
//   - 400 for invalid event id
//   - 403 if the caller is not the event host
//   - 404 if the event is not found
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) listEventConversations(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	event, err := h.repo.GetEventByID(ctx, eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load event"})
		return
	}

	// Only event host can list all conversations for the event
	if event.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the event host can list event conversations"})
		return
	}

	conversations, err := h.repo.ListConversationsForEvent(ctx, eventID, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load conversations"})
		return
	}

	c.JSON(http.StatusOK, listEventConversationsResponse{Conversations: conversations})
}

type reportMemberBody struct {
	Reason string `json:"reason" binding:"required"`
}

// reportMember allows the event host to report and block a specific accepted member.
// It creates a member report, persists a mutual block between host/member, and
// removes the member from all host-owned event conversations they are part of.
//
// Responses:
//   - 201 with the created report
//   - 401 if the caller has no session
//   - 400 for invalid path params or missing reason
//   - 403 if the caller is not the event host
//   - 404 if the event is not found
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) reportMember(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	userIDParam := c.Param("userId")
	reportedUserID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil || reportedUserID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var payload reportMemberBody
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	event, err := h.repo.GetEventByID(ctx, eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load event"})
		return
	}

	// Only event host can report members
	if event.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the event host can report members"})
		return
	}

	// Cannot report yourself
	if reportedUserID == claims.UserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot report yourself"})
		return
	}

	// Accepted-members only: pending/non-members cannot be report-blocked.
	_, convoErr := h.repo.findUserConversationForEventPublic(ctx, eventID, reportedUserID)
	if convoErr != nil {
		if errors.Is(convoErr, ErrConversationNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "member is not in accepted state"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify member state"})
		return
	}

	if err := h.repo.CreateMutualBlock(ctx, claims.UserID, reportedUserID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to block member"})
		return
	}

	// Create the member report (idempotent with block semantics).
	report, err := h.repo.CreateMemberReport(ctx, eventID, claims.UserID, reportedUserID, reason)
	alreadyReported := false
	if err != nil {
		if errors.Is(err, ErrReportAlreadyExists) {
			alreadyReported = true
			report = nil
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to submit report"})
			return
		}
	}

	// Remove memberships in both directions:
	// 1) reported user from all reporter-hosted events
	// 2) reporter from all reported-user-hosted events
	removeMembershipAcrossHostEvents := func(hostUserID, memberUserID, mustIncludeEventID int64) {
		hostEventIDs, hostEventErr := h.repo.ListHostEventIDsForMember(ctx, hostUserID, memberUserID)
		if hostEventErr != nil {
			log.Printf(
				"failed to list host events for host %d member %d: %v",
				hostUserID,
				memberUserID,
				hostEventErr,
			)
		}
		eventIDSet := make(map[int64]struct{}, len(hostEventIDs)+1)
		for _, memberEventID := range hostEventIDs {
			if memberEventID > 0 {
				eventIDSet[memberEventID] = struct{}{}
			}
		}
		if mustIncludeEventID > 0 {
			eventIDSet[mustIncludeEventID] = struct{}{}
		}

		for memberEventID := range eventIDSet {
			memberConvo, err := h.repo.findUserConversationForEventPublic(ctx, memberEventID, memberUserID)
			if err != nil && !errors.Is(err, ErrConversationNotFound) {
				log.Printf(
					"failed to find member conversation for event %d user %d: %v",
					memberEventID,
					memberUserID,
					err,
				)
			}

			removeErr := h.repo.RemoveEventMember(ctx, memberEventID, memberUserID)
			if removeErr != nil &&
				!errors.Is(removeErr, ErrNotConversationMember) &&
				!errors.Is(removeErr, ErrCannotRemoveHost) {
				log.Printf(
					"failed to remove member %d from event %d after report: %v",
					memberUserID,
					memberEventID,
					removeErr,
				)
			}

			if memberConvo != nil {
				h.hub.NotifyMembership(memberConvo.ID, memberUserID, "removed")
			}
		}
	}

	removeMembershipAcrossHostEvents(claims.UserID, reportedUserID, eventID)
	removeMembershipAcrossHostEvents(reportedUserID, claims.UserID, 0)

	status := http.StatusCreated
	if alreadyReported {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{
		"report":           report,
		"blocked":          true,
		"already_reported": alreadyReported,
	})
}

// unblockMember allows the event host to undo a prior report-block relation
// with a member for testing and recovery workflows.
//
// Responses:
//   - 200 with unblock state details
//   - 401 if the caller has no session
//   - 400 for invalid path params or self target
//   - 403 if the caller is not the event host
//   - 404 if the event is missing or no host-member report relation exists
//   - 500 for repository/database failures
func (h *ChatHTTPHandler) unblockMember(c *gin.Context) {
	claims, ok := sessionFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing session"})
		return
	}

	eventIDParam := c.Param("id")
	eventID, err := strconv.ParseInt(eventIDParam, 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}

	userIDParam := c.Param("userId")
	targetUserID, err := strconv.ParseInt(userIDParam, 10, 64)
	if err != nil || targetUserID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	if targetUserID == claims.UserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot unblock yourself"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	event, err := h.repo.GetEventByID(ctx, eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load event"})
		return
	}

	if event.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "only the event host can unblock members"})
		return
	}

	hasMemberReport, err := h.repo.HasMemberReport(ctx, eventID, claims.UserID, targetUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify report relationship"})
		return
	}
	if !hasMemberReport {
		hasBlock, err := h.repo.IsUserBlocked(ctx, claims.UserID, targetUserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check block status"})
			return
		}
		if !hasBlock {
			c.JSON(http.StatusNotFound, gin.H{"error": "no report-block relationship found for this member"})
			return
		}
	}

	deletedAny, err := h.repo.DeleteMutualBlock(ctx, claims.UserID, targetUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unblock member"})
		return
	}

	if err := h.repo.DeleteMemberReport(ctx, eventID, claims.UserID, targetUserID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete member report"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"unblocked":         true,
		"already_unblocked": !deletedAny,
		"event_id":          eventID,
		"user_id":           targetUserID,
	})
}
