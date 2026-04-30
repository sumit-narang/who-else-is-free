package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateConversation creates a new conversation and ensures the creator is a member.
func (r *EventRepository) CreateConversation(ctx context.Context, title *string, createdBy int64, memberIDs []int64, eventID *int64) (*Conversation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin conversation tx: %w", err)
	}

	role := "owner"
	var nullableTitle sql.NullString
	if title != nil {
		nullableTitle = sql.NullString{String: *title, Valid: true}
	}
	var nullableEventID sql.NullInt64
	if eventID != nil {
		nullableEventID = sql.NullInt64{Int64: *eventID, Valid: true}
	}

	res, err := tx.ExecContext(ctx, insertConversation, nullableTitle, createdBy, nullableEventID)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("insert conversation: %w", err)
	}

	convoID, err := res.LastInsertId()
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("fetch conversation id: %w", err)
	}

	// ensure creator is in the member list
	creatorIncluded := false
	for _, memberID := range memberIDs {
		if memberID == createdBy {
			creatorIncluded = true
			break
		}
	}
	if !creatorIncluded {
		memberIDs = append(memberIDs, createdBy)
	}

	for _, memberID := range memberIDs {
		memberRole := "member"
		if memberID == createdBy {
			memberRole = role
		}
		if _, err := tx.ExecContext(ctx, insertConversationMember, convoID, memberID, memberRole); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("insert conversation member: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit conversation: %w", err)
	}

	conversation := &Conversation{ID: convoID, CreatedBy: createdBy}
	if nullableTitle.Valid {
		value := nullableTitle.String
		conversation.Title = &value
	}
	if nullableEventID.Valid {
		value := nullableEventID.Int64
		conversation.EventID = &value
	}

	row := r.db.QueryRowContext(ctx, "SELECT created_at FROM conversations WHERE id = ?", convoID)
	if err := row.Scan(&conversation.CreatedAt); err != nil {
		return nil, fmt.Errorf("fetch conversation created_at: %w", err)
	}

	return conversation, nil
}

// ListConversations returns all conversations visible to the user, hydrated with participants and unread counts.
func (r *EventRepository) ListConversations(ctx context.Context, userID int64) ([]ConversationSummary, error) {
	rows, err := r.db.QueryContext(ctx, selectConversationsForUser, userID)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}

	var conversations []Conversation
	for rows.Next() {
		var convo Conversation
		var title sql.NullString
		var eventID sql.NullInt64
		if err := rows.Scan(&convo.ID, &title, &convo.CreatedBy, &convo.CreatedAt, &eventID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		if title.Valid {
			value := title.String
			convo.Title = &value
		}
		if eventID.Valid {
			value := eventID.Int64
			convo.EventID = &value
		}
		conversations = append(conversations, convo)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate conversations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close conversations rows: %w", err)
	}

	summaries := make([]ConversationSummary, 0, len(conversations))
	for _, convo := range conversations {
		summary, err := r.hydrateConversationSummary(ctx, convo, userID)
		if err != nil {
			if errors.Is(err, ErrEventNotFound) {
				// Skip conversations whose backing event has been deleted.
				continue
			}
			return nil, err
		}
		summaries = append(summaries, summary)
	}

	return summaries, nil
}

// ListConversationsForEvent returns all conversations linked to an event.
// Used for 1:1 events to list all host-requester private conversations.
func (r *EventRepository) ListConversationsForEvent(ctx context.Context, eventID, viewerID int64) ([]ConversationSummary, error) {
	rows, err := r.db.QueryContext(ctx, selectConversationsForEvent, eventID)
	if err != nil {
		return nil, fmt.Errorf("list conversations for event: %w", err)
	}

	var conversations []Conversation
	for rows.Next() {
		var convo Conversation
		var title sql.NullString
		var eventIDValue sql.NullInt64
		if err := rows.Scan(&convo.ID, &title, &convo.CreatedBy, &convo.CreatedAt, &eventIDValue); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		if title.Valid {
			value := title.String
			convo.Title = &value
		}
		if eventIDValue.Valid {
			value := eventIDValue.Int64
			convo.EventID = &value
		}
		conversations = append(conversations, convo)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate conversations for event: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close conversations rows: %w", err)
	}

	summaries := make([]ConversationSummary, 0, len(conversations))
	for _, convo := range conversations {
		summary, err := r.hydrateConversationSummary(ctx, convo, viewerID)
		if err != nil {
			if errors.Is(err, ErrEventNotFound) {
				continue
			}
			return nil, err
		}
		summaries = append(summaries, summary)
	}

	return summaries, nil
}

// ListMessages paginates messages for a given conversation.
func (r *EventRepository) ListMessages(ctx context.Context, conversationID int64, limit, offset int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.db.QueryContext(ctx, selectMessagesForConversation, conversationID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var attachment sql.NullString
		if err := rows.Scan(&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Body, &attachment, &msg.DeliveryStatus, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if attachment.Valid {
			msg.AttachmentURL = &attachment.String
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	return messages, nil
}

// CreateMessage stores a new message and returns the saved row for broadcasting.
func (r *EventRepository) CreateMessage(ctx context.Context, params CreateMessageParams) (*Message, error) {
	attachment := sql.NullString{}
	if params.AttachmentURL != nil {
		attachment = sql.NullString{String: *params.AttachmentURL, Valid: true}
	}

	var msg Message
	row := r.db.QueryRowContext(ctx, insertMessage, params.ConversationID, params.SenderID, params.Body, attachment, params.DeliveryStatus)
	var attachmentOut sql.NullString
	if err := row.Scan(&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Body, &attachmentOut, &msg.DeliveryStatus, &msg.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}
	if attachmentOut.Valid {
		msg.AttachmentURL = &attachmentOut.String
	}
	return &msg, nil
}

func scanJoinRequest(row *sql.Row) (*ConversationJoinRequest, error) {
	var req ConversationJoinRequest
	var decidedAt sql.NullTime
	var decidedBy sql.NullInt64
	var message sql.NullString
	if err := row.Scan(&req.ID, &req.EventID, &req.UserID, &req.Status, &message, &req.CreatedAt, &decidedAt, &decidedBy); err != nil {
		return nil, err
	}
	if message.Valid {
		req.Message = message.String
	}
	if decidedAt.Valid {
		t := decidedAt.Time
		req.DecidedAt = &t
	}
	if decidedBy.Valid {
		id := decidedBy.Int64
		req.DecidedBy = &id
	}
	return &req, nil
}

func fetchJoinRequestByID(ctx context.Context, q rowQuery, id int64) (*ConversationJoinRequest, error) {
	req, err := scanJoinRequest(q.QueryRowContext(ctx, selectJoinRequestByID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, fmt.Errorf("fetch join request: %w", err)
	}
	return req, nil
}

func fetchConversationByEventID(ctx context.Context, q rowQuery, eventID int64) (*Conversation, error) {
	row := q.QueryRowContext(ctx, selectConversationByEventID, eventID)
	var convo Conversation
	var title sql.NullString
	var eventIDValue sql.NullInt64
	if err := row.Scan(&convo.ID, &title, &convo.CreatedBy, &convo.CreatedAt, &eventIDValue); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, fmt.Errorf("fetch conversation by event: %w", err)
	}
	if title.Valid {
		value := title.String
		convo.Title = &value
	}
	if eventIDValue.Valid {
		value := eventIDValue.Int64
		convo.EventID = &value
	}
	return &convo, nil
}

func (r *EventRepository) GetEventByID(ctx context.Context, eventID int64) (*Event, error) {
	row := r.db.QueryRowContext(ctx, selectEventByID, eventID)
	var evt Event
	var scheduledAtStr sql.NullString
	if err := row.Scan(
		&evt.ID,
		&evt.UserID,
		&evt.Title,
		&evt.Location,
		&evt.Time,
		&evt.EventDate,
		&evt.Description,
		&evt.Gender,
		&evt.MinAge,
		&evt.MaxAge,
		&evt.DateLabel,
		&evt.GroupType,
		&evt.CoverKey,
		&scheduledAtStr,
		&evt.CreatedAt,
		&evt.HostName,
		&evt.HostAvatar,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("fetch event: %w", err)
	}

	// Parse scheduled_at if present
	if scheduledAtStr.Valid && scheduledAtStr.String != "" {
		if parsed, err := time.Parse(time.RFC3339, scheduledAtStr.String); err == nil {
			evt.ScheduledAt = &parsed
		} else if parsed, err := time.Parse("2006-01-02 15:04:05", scheduledAtStr.String); err == nil {
			// Handle SQLite datetime format
			utc := parsed.UTC()
			evt.ScheduledAt = &utc
		}
	}

	now := time.Now()
	if parsedDate, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(evt.EventDate), now.Location()); err == nil {
		today := startOfDay(now)
		eventDay := startOfDay(parsedDate)
		if eventDay.Equal(today) {
			evt.DateLabel = "Today"
		} else if eventDay.Equal(today.AddDate(0, 0, 1)) {
			evt.DateLabel = "Tmrw"
		}
	}
	return &evt, nil
}

func (r *EventRepository) GetConversationByEventID(ctx context.Context, eventID int64) (*Conversation, error) {
	return fetchConversationByEventID(ctx, r.db, eventID)
}

func (r *EventRepository) CreateJoinRequest(ctx context.Context, eventID, userID int64, message string) (*ConversationJoinRequest, error) {
	event, err := r.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if event.UserID == userID {
		return nil, ErrAlreadyConversationMember
	}
	blocked, err := r.AreUsersBlocked(ctx, event.UserID, userID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrUsersBlocked
	}

	// For Group events, check if user is already a member of the main conversation
	if event.GroupType == "Group" {
		convo, err := r.GetConversationByEventID(ctx, eventID)
		if err != nil {
			return nil, err
		}

		isMember, err := r.IsConversationMember(ctx, convo.ID, userID)
		if err != nil {
			return nil, err
		}
		if isMember {
			return nil, ErrAlreadyConversationMember
		}
	}

	// For 1:1 events, ensure the user does not already have an active private
	// conversation for this event before creating another request.
	if event.GroupType == "Single" {
		convo, err := r.findUserConversationForEvent(ctx, nil, eventID, userID)
		if err == nil && convo != nil {
			return nil, ErrAlreadyConversationMember
		}
		if err != nil && !errors.Is(err, ErrConversationNotFound) {
			return nil, err
		}
	}

	// Check for existing pending request
	if _, err := scanJoinRequest(r.db.QueryRowContext(ctx, selectPendingJoinRequest, eventID, userID)); err == nil {
		return nil, ErrJoinRequestExists
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check pending join request: %w", err)
	}

	// Create a pending join request for both Group and 1:1 events.
	res, err := r.db.ExecContext(ctx, insertJoinRequest, eventID, userID, message)
	if err != nil {
		return nil, fmt.Errorf("insert join request: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("fetch join request id: %w", err)
	}
	return fetchJoinRequestByID(ctx, r.db, id)
}

func (r *EventRepository) approveSingleJoinRequest(ctx context.Context, event *Event, userID, approverID int64) (*ConversationJoinRequest, error) {
	if convo, err := r.findUserConversationForEvent(ctx, nil, event.ID, userID); err == nil && convo != nil {
		return nil, ErrAlreadyConversationMember
	} else if err != nil && !errors.Is(err, ErrConversationNotFound) {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin approve single join tx: %w", err)
	}

	req, err := scanJoinRequest(tx.QueryRowContext(ctx, selectPendingJoinRequest, event.ID, userID))
	if err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, fmt.Errorf("fetch pending single join request: %w", err)
	}

	// Create a new conversation linked to this event (title = event title)
	nullableTitle := sql.NullString{String: event.Title, Valid: len(strings.TrimSpace(event.Title)) > 0}
	nullableEventID := sql.NullInt64{Int64: event.ID, Valid: true}

	convoRes, err := tx.ExecContext(ctx, insertConversation, nullableTitle, event.UserID, nullableEventID)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("insert approved single conversation: %w", err)
	}

	convoID, err := convoRes.LastInsertId()
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("fetch approved single conversation id: %w", err)
	}

	// Add host as "owner"
	if _, err := tx.ExecContext(ctx, insertConversationMember, convoID, event.UserID, "owner"); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("insert approved single conversation owner: %w", err)
	}

	// Add requester as "member"
	if _, err := tx.ExecContext(ctx, insertConversationMember, convoID, userID, "member"); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("insert approved single conversation member: %w", err)
	}

	trimmedMessage := strings.TrimSpace(req.Message)
	if trimmedMessage != "" {
		// Persist the request intro as the first message in the approved private chat.
		var msg Message
		var attachmentOut sql.NullString
		row := tx.QueryRowContext(ctx, insertMessage, convoID, userID, trimmedMessage, sql.NullString{}, "sent")
		if err := row.Scan(&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Body, &attachmentOut, &msg.DeliveryStatus, &msg.CreatedAt); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("insert approved single intro message: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, updateJoinRequestStatus, "approved", approverID, req.ID); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("approve single join request: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit approve single join: %w", err)
	}

	return fetchJoinRequestByID(ctx, r.db, req.ID)
}

func (r *EventRepository) ApproveJoinRequest(ctx context.Context, eventID, userID, approverID int64) (*ConversationJoinRequest, error) {
	event, err := r.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if event.UserID != approverID {
		return nil, ErrNotEventHost
	}

	if event.GroupType == "Single" {
		return r.approveSingleJoinRequest(ctx, event, userID, approverID)
	}

	convo, err := r.GetConversationByEventID(ctx, eventID)
	if err != nil {
		return nil, err
	}

	isMember, err := r.IsConversationMember(ctx, convo.ID, userID)
	if err != nil {
		return nil, err
	}
	if isMember {
		return nil, ErrAlreadyConversationMember
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin approve join tx: %w", err)
	}

	req, err := scanJoinRequest(tx.QueryRowContext(ctx, selectPendingJoinRequest, eventID, userID))
	if err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, fmt.Errorf("fetch pending join request: %w", err)
	}

	if _, err := tx.ExecContext(ctx, updateJoinRequestStatus, "approved", approverID, req.ID); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("approve join request: %w", err)
	}
	if _, err := tx.ExecContext(ctx, insertConversationMember, convo.ID, userID, "member"); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("add conversation member: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit join approval: %w", err)
	}

	return fetchJoinRequestByID(ctx, r.db, req.ID)
}

func (r *EventRepository) DenyJoinRequest(ctx context.Context, eventID, userID, approverID int64) (*ConversationJoinRequest, error) {
	event, err := r.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if event.UserID != approverID {
		return nil, ErrNotEventHost
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin deny join tx: %w", err)
	}

	req, err := scanJoinRequest(tx.QueryRowContext(ctx, selectPendingJoinRequest, eventID, userID))
	if err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrJoinRequestNotFound
		}
		return nil, fmt.Errorf("fetch join request: %w", err)
	}

	if _, err := tx.ExecContext(ctx, updateJoinRequestStatus, "denied", approverID, req.ID); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("deny join request: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit join denial: %w", err)
	}

	return fetchJoinRequestByID(ctx, r.db, req.ID)
}

// findUserConversationForEvent finds a conversation linked to an event where
// the given user is a member. Used for 1:1 events to find the private conversation.
func (r *EventRepository) findUserConversationForEvent(ctx context.Context, tx *sql.Tx, eventID, userID int64) (*Conversation, error) {
	const query = `
		SELECT c.id, c.title, c.created_by, c.created_at, c.event_id
		FROM conversations c
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.event_id = ? AND cm.user_id = ?
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT 1;
	`
	var convo Conversation
	var title sql.NullString
	var eventIDValue sql.NullInt64
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, query, eventID, userID)
	} else {
		row = r.db.QueryRowContext(ctx, query, eventID, userID)
	}
	if err := row.Scan(&convo.ID, &title, &convo.CreatedBy, &convo.CreatedAt, &eventIDValue); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, fmt.Errorf("find user conversation for event: %w", err)
	}
	if title.Valid {
		value := title.String
		convo.Title = &value
	}
	if eventIDValue.Valid {
		value := eventIDValue.Int64
		convo.EventID = &value
	}
	return &convo, nil
}

func (r *EventRepository) RemoveEventMember(ctx context.Context, eventID, userID int64) error {
	event, err := r.GetEventByID(ctx, eventID)
	if err != nil {
		return err
	}
	if event.UserID == userID {
		return ErrCannotRemoveHost
	}

	// Use findUserConversationForEvent to find the specific conversation where
	// the user is a member. This is important for 1:1 events where multiple
	// private conversations can exist per event (one for each joiner).
	convo, err := r.findUserConversationForEvent(ctx, nil, eventID, userID)
	if err != nil {
		if errors.Is(err, ErrConversationNotFound) {
			return ErrNotConversationMember
		}
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove member tx: %w", err)
	}

	if event.GroupType == "Single" {
		// In 1:1 events, close the private conversation entirely so the host
		// no longer sees stale unread or last-message previews after leave.
		if _, err := tx.ExecContext(ctx, deleteConversationByID, convo.ID); err != nil {
			tx.Rollback()
			return fmt.Errorf("delete single event conversation: %w", err)
		}
	} else {
		// Group events keep the shared conversation and only remove this member.
		if _, err := tx.ExecContext(ctx, deleteConversationMember, convo.ID, userID); err != nil {
			tx.Rollback()
			return fmt.Errorf("delete conversation member: %w", err)
		}
		if _, err := tx.ExecContext(ctx, deleteConversationReadState, convo.ID, userID); err != nil {
			tx.Rollback()
			return fmt.Errorf("delete conversation read state: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, deleteJoinRequestForEvent, eventID, userID); err != nil {
		tx.Rollback()
		return fmt.Errorf("delete join request: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove member: %w", err)
	}

	return nil
}

func (r *EventRepository) ListJoinRequests(ctx context.Context, eventID int64, includeApproved bool) ([]JoinRequestView, error) {
	event, err := r.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, err
	}

	query := selectPendingJoinRequestsForEvent
	if includeApproved {
		query = selectPendingOrApprovedJoinRequestsForEvent
	}

	rows, err := r.db.QueryContext(ctx, query, eventID)
	if err != nil {
		return nil, fmt.Errorf("list join requests: %w", err)
	}
	defer rows.Close()

	var requests []JoinRequestView
	for rows.Next() {
		var req JoinRequestView
		var message sql.NullString
		var decidedAt sql.NullTime
		var decidedBy sql.NullInt64
		var requesterName string
		var requesterAvatar *string
		if err := rows.Scan(
			&req.ID,
			&req.EventID,
			&req.UserID,
			&req.Status,
			&message,
			&req.CreatedAt,
			&decidedAt,
			&decidedBy,
			&requesterName,
			&requesterAvatar,
		); err != nil {
			return nil, fmt.Errorf("scan join request: %w", err)
		}
		if message.Valid {
			req.Message = message.String
		}
		if decidedAt.Valid {
			t := decidedAt.Time
			req.DecidedAt = &t
		}
		if decidedBy.Valid {
			id := decidedBy.Int64
			req.DecidedBy = &id
		}
		req.Requester = ConversationParticipant{
			ID:     req.UserID,
			Name:   requesterName,
			Avatar: requesterAvatar,
		}

		requests = append(requests, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate join requests: %w", err)
	}

	// Populate conversation IDs for approved 1:1 requests after closing rows
	// (SQLite single-connection doesn't support nested queries while rows are open)
	if event.GroupType == "Single" {
		for i := range requests {
			if requests[i].Status != "approved" {
				continue
			}
			convo, err := r.findUserConversationForEvent(ctx, nil, eventID, requests[i].UserID)
			if err == nil && convo != nil {
				requests[i].ConversationID = &convo.ID
			}
		}
	}

	return requests, nil
}

func (r *EventRepository) ListJoinRequestsByUser(ctx context.Context, userID int64, includeApproved bool) ([]JoinRequestView, error) {
	query := selectPendingJoinRequestsForUser
	if includeApproved {
		query = selectPendingOrApprovedJoinRequestsForUser
	}

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list user join requests: %w", err)
	}
	defer rows.Close()

	var requests []JoinRequestView
	for rows.Next() {
		var req JoinRequestView
		var message sql.NullString
		var decidedAt sql.NullTime
		var decidedBy sql.NullInt64
		var requesterName string
		var requesterAvatar *string
		if err := rows.Scan(
			&req.ID,
			&req.EventID,
			&req.UserID,
			&req.Status,
			&message,
			&req.CreatedAt,
			&decidedAt,
			&decidedBy,
			&requesterName,
			&requesterAvatar,
		); err != nil {
			return nil, fmt.Errorf("scan user join request: %w", err)
		}
		if message.Valid {
			req.Message = message.String
		}
		if decidedAt.Valid {
			t := decidedAt.Time
			req.DecidedAt = &t
		}
		if decidedBy.Valid {
			id := decidedBy.Int64
			req.DecidedBy = &id
		}
		req.Requester = ConversationParticipant{
			ID:     req.UserID,
			Name:   requesterName,
			Avatar: requesterAvatar,
		}
		requests = append(requests, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user join requests: %w", err)
	}
	return requests, nil
}

// hydrateConversationSummary enriches a conversation with participant info and unread counts for the viewer.
func (r *EventRepository) hydrateConversationSummary(ctx context.Context, convo Conversation, viewerID int64) (ConversationSummary, error) {
	participants, memberIDs, err := r.fetchConversationParticipants(ctx, convo.ID)
	if err != nil {
		return ConversationSummary{}, err
	}

	lastMessage, err := r.fetchLatestMessage(ctx, convo.ID)
	if err != nil {
		return ConversationSummary{}, err
	}

	unreadCount, err := r.countUnreadMessages(ctx, convo.ID, viewerID, lastMessage)
	if err != nil {
		return ConversationSummary{}, err
	}

	var eventMeta *ConversationEventMeta
	if convo.EventID != nil {
		evt, err := r.GetEventByID(ctx, *convo.EventID)
		if err != nil {
			if errors.Is(err, ErrEventNotFound) {
				return ConversationSummary{}, ErrEventNotFound
			}
			return ConversationSummary{}, err
		}
		eventMeta = &ConversationEventMeta{
			ID:          evt.ID,
			UserID:      evt.UserID,
			Title:       evt.Title,
			Location:    evt.Location,
			Time:        evt.Time,
			EventDate:   evt.EventDate,
			DateLabel:   evt.DateLabel,
			GroupType:   evt.GroupType,
			CoverKey:    evt.CoverKey,
			ScheduledAt: evt.ScheduledAt,
		}
	}

	summary := ConversationSummary{
		Conversation: convo,
		MemberIDs:    memberIDs,
		Participants: participants,
		Event:        eventMeta,
		UnreadCount:  unreadCount,
	}
	if lastMessage != nil {
		summary.LastMessage = lastMessage
	}
	return summary, nil
}

// fetchConversationParticipants returns the members of a conversation plus their IDs for fast lookup.
func (r *EventRepository) fetchConversationParticipants(ctx context.Context, conversationID int64) ([]ConversationParticipant, []int64, error) {
	rows, err := r.db.QueryContext(ctx, selectParticipantsForConversation, conversationID)
	if err != nil {
		return nil, nil, fmt.Errorf("list conversation participants: %w", err)
	}
	defer rows.Close()

	var participants []ConversationParticipant
	var memberIDs []int64
	for rows.Next() {
		var participant ConversationParticipant
		if err := rows.Scan(&participant.ID, &participant.Name, &participant.Avatar); err != nil {
			return nil, nil, fmt.Errorf("scan conversation participant: %w", err)
		}
		participants = append(participants, participant)
		memberIDs = append(memberIDs, participant.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate conversation participants: %w", err)
	}

	// Append former message senders to participants only (not memberIDs)
	rows2, err := r.db.QueryContext(ctx, selectFormerMessageSenders, conversationID, conversationID)
	if err != nil {
		return nil, nil, fmt.Errorf("list former message senders: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var p ConversationParticipant
		if err := rows2.Scan(&p.ID, &p.Name, &p.Avatar); err != nil {
			return nil, nil, fmt.Errorf("scan former message sender: %w", err)
		}
		participants = append(participants, p)
	}
	if err := rows2.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate former message senders: %w", err)
	}

	return participants, memberIDs, nil
}

// fetchLatestMessage grabs the newest message so we can show previews/unread counts.
func (r *EventRepository) fetchLatestMessage(ctx context.Context, conversationID int64) (*MessageSummary, error) {
	row := r.db.QueryRowContext(ctx, selectLatestMessageForConversation, conversationID)

	var msg Message
	var attachment sql.NullString
	if err := row.Scan(&msg.ID, &msg.ConversationID, &msg.SenderID, &msg.Body, &attachment, &msg.DeliveryStatus, &msg.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetch latest message: %w", err)
	}

	summary := &MessageSummary{
		ID:        msg.ID,
		SenderID:  msg.SenderID,
		Body:      msg.Body,
		CreatedAt: msg.CreatedAt,
	}

	return summary, nil
}

// countUnreadMessages uses the stored read cursor to compute unread totals.
func (r *EventRepository) countUnreadMessages(ctx context.Context, conversationID, userID int64, lastMessage *MessageSummary) (int, error) {
	if lastMessage == nil {
		return 0, nil
	}

	var lastReadID sql.NullInt64
	err := r.db.QueryRowContext(ctx, "SELECT last_read_message_id FROM conversation_read_state WHERE conversation_id = ? AND user_id = ?", conversationID, userID).Scan(&lastReadID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("fetch read cursor: %w", err)
	}

	if lastReadID.Valid && lastReadID.Int64 >= lastMessage.ID {
		return 0, nil
	}

	var count int
	query := "SELECT COUNT(1) FROM messages WHERE conversation_id = ? AND id > ?"
	threshold := int64(0)
	if lastReadID.Valid {
		threshold = lastReadID.Int64
	}
	if err := r.db.QueryRowContext(ctx, query, conversationID, threshold).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unread messages: %w", err)
	}

	return count, nil
}

// UpdateReadState advances a user's read cursor for a conversation.
func (r *EventRepository) UpdateReadState(ctx context.Context, conversationID, userID, lastReadMessageID int64) error {
	if lastReadMessageID <= 0 {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, upsertReadState, conversationID, userID, lastReadMessageID); err != nil {
		return fmt.Errorf("update read state: %w", err)
	}
	return nil
}

func (r *EventRepository) IsConversationMember(ctx context.Context, conversationID, userID int64) (bool, error) {
	var exists int
	if err := r.db.QueryRowContext(ctx, checkConversationMembership, conversationID, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check conversation membership: %w", err)
	}
	return true, nil
}

func (r *EventRepository) CancelJoinRequest(ctx context.Context, eventID, userID int64) error {
	result, err := r.db.ExecContext(ctx, cancelJoinRequestByUser, eventID, userID)
	if err != nil {
		return fmt.Errorf("cancel join request: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check cancel rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrJoinRequestNotFound
	}
	return nil
}
