package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (r *EventRepository) Create(ctx context.Context, params CreateEventParams) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin event tx: %w", err)
	}

	params.EventDate = strings.TrimSpace(params.EventDate)
	coverKey := strings.TrimSpace(params.CoverKey)
	if coverKey == "" {
		coverKey = defaultCoverKey
	}
	if strings.TrimSpace(params.GroupType) == "" {
		params.GroupType = "Single"
	}
	params.DateLabel = normalizeLegacyDateLabel(
		params.DateLabel,
		deriveDateLabel(params.EventDate, time.Now()),
	)

	// Handle scheduled_at - store as nullable string
	var scheduledAtStr sql.NullString
	if params.ScheduledAt != "" {
		scheduledAtStr = sql.NullString{String: params.ScheduledAt, Valid: true}
	}

	res, err := tx.ExecContext(ctx, insertEvent,
		params.UserID,
		params.Title,
		params.Location,
		params.Time,
		params.EventDate,
		params.Description,
		params.Gender,
		params.MinAge,
		params.MaxAge,
		params.DateLabel,
		params.GroupType,
		coverKey,
		scheduledAtStr,
	)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("insert event: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("fetch event id: %w", err)
	}

	// Only create initial conversation for Group events
	// For Single (1:1) events, conversations are created when requesters join
	if params.GroupType != "Single" {
		nullableTitle := sql.NullString{String: params.Title, Valid: len(strings.TrimSpace(params.Title)) > 0}
		nullableEventID := sql.NullInt64{Int64: id, Valid: true}

		convoRes, err := tx.ExecContext(ctx, insertConversation, nullableTitle, params.UserID, nullableEventID)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("insert event conversation: %w", err)
		}

		convoID, err := convoRes.LastInsertId()
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("fetch event conversation id: %w", err)
		}

		if _, err := tx.ExecContext(ctx, insertConversationMember, convoID, params.UserID, "owner"); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("insert event conversation owner: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit event: %w", err)
	}

	return id, nil
}

func (r *EventRepository) Update(ctx context.Context, id int64, userID int64, params UpdateEventParams) (*EventUpdateTransition, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin event update tx: %w", err)
	}

	params.EventDate = strings.TrimSpace(params.EventDate)
	coverKeyParam := ""
	if params.CoverKey != nil {
		value := strings.TrimSpace(*params.CoverKey)
		if value == "" {
			value = defaultCoverKey
		}
		coverKeyParam = value
	}
	if strings.TrimSpace(params.GroupType) == "" {
		params.GroupType = "Single"
	}
	params.DateLabel = normalizeLegacyDateLabel(
		params.DateLabel,
		deriveDateLabel(params.EventDate, time.Now()),
	)

	var previousGroupType string
	if err := tx.QueryRowContext(ctx, selectEventGroupTypeForOwner, id, userID).Scan(&previousGroupType); err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("fetch event group type before update: %w", err)
	}

	preMembers, err := r.listEventConversationMembersTx(ctx, tx, id)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("list pre-update event conversation members: %w", err)
	}

	// Handle scheduled_at - store as nullable string
	var scheduledAtStr sql.NullString
	if params.ScheduledAt != "" {
		scheduledAtStr = sql.NullString{String: params.ScheduledAt, Valid: true}
	}

	result, err := tx.ExecContext(ctx, updateEvent,
		params.Title,
		params.Location,
		params.Time,
		params.EventDate,
		params.Description,
		params.Gender,
		params.MinAge,
		params.MaxAge,
		params.DateLabel,
		params.GroupType,
		coverKeyParam,
		scheduledAtStr,
		id,
		userID,
	)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("update event: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("check rows affected: %w", err)
	}

	if rowsAffected == 0 {
		tx.Rollback()
		return nil, ErrEventNotFound
	}

	if previousGroupType != params.GroupType {
		members, err := r.listDistinctNonHostEventMemberIDsTx(ctx, tx, id, userID)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("list approved members for event migration: %w", err)
		}
		if _, err := tx.ExecContext(ctx, deleteConversationsByEventID, id); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("delete event conversations during migration: %w", err)
		}

		switch {
		case previousGroupType == "Group" && params.GroupType == "Single":
			for _, memberID := range members {
				if _, err := r.createEventConversationTx(ctx, tx, id, userID, params.Title, []int64{memberID}); err != nil {
					tx.Rollback()
					return nil, fmt.Errorf("create migrated private conversation: %w", err)
				}
			}
		case previousGroupType == "Single" && params.GroupType == "Group":
			if _, err := r.createEventConversationTx(ctx, tx, id, userID, params.Title, members); err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("create migrated group conversation: %w", err)
			}
		}
	} else if params.GroupType != "Single" {
		// Existing behavior for Group events: ensure one event conversation exists.
		_, convoErr := fetchConversationByEventID(ctx, tx, id)
		if errors.Is(convoErr, ErrConversationNotFound) {
			if _, err := r.createEventConversationTx(ctx, tx, id, userID, params.Title, nil); err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("insert event conversation on update: %w", err)
			}
		} else if convoErr != nil {
			tx.Rollback()
			return nil, fmt.Errorf("check event conversation on update: %w", convoErr)
		}
	}

	postMembers, err := r.listEventConversationMembersTx(ctx, tx, id)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("list post-update event conversation members: %w", err)
	}

	postConversationIDs, err := r.listEventConversationIDsTx(ctx, tx, id)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("list post-update conversation ids: %w", err)
	}

	transition := &EventUpdateTransition{
		RemovedMemberships:           diffEventConversationMembers(preMembers, postMembers),
		AddedMemberships:             diffEventConversationMembers(postMembers, preMembers),
		PostMigrationConversationIDs: postConversationIDs,
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit event update: %w", err)
	}

	return transition, nil
}

func (r *EventRepository) listDistinctNonHostEventMemberIDsTx(ctx context.Context, tx *sql.Tx, eventID, hostUserID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, selectDistinctNonHostEventMemberIDs, eventID, hostUserID)
	if err != nil {
		return nil, fmt.Errorf("query distinct non-host event members: %w", err)
	}
	defer rows.Close()

	memberIDs := make([]int64, 0)
	for rows.Next() {
		var memberID int64
		if err := rows.Scan(&memberID); err != nil {
			return nil, fmt.Errorf("scan distinct non-host member id: %w", err)
		}
		memberIDs = append(memberIDs, memberID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distinct non-host event members: %w", err)
	}
	return memberIDs, nil
}

func (r *EventRepository) createEventConversationTx(
	ctx context.Context,
	tx *sql.Tx,
	eventID, hostUserID int64,
	title string,
	memberUserIDs []int64,
) (int64, error) {
	nullableTitle := sql.NullString{String: strings.TrimSpace(title), Valid: len(strings.TrimSpace(title)) > 0}
	nullableEventID := sql.NullInt64{Int64: eventID, Valid: true}

	convoRes, err := tx.ExecContext(ctx, insertConversation, nullableTitle, hostUserID, nullableEventID)
	if err != nil {
		return 0, fmt.Errorf("insert event conversation: %w", err)
	}

	conversationID, err := convoRes.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("fetch event conversation id: %w", err)
	}

	if _, err := tx.ExecContext(ctx, insertConversationMember, conversationID, hostUserID, "owner"); err != nil {
		return 0, fmt.Errorf("insert event conversation owner: %w", err)
	}

	seen := make(map[int64]struct{}, len(memberUserIDs))
	for _, memberUserID := range memberUserIDs {
		if memberUserID == hostUserID {
			continue
		}
		if _, exists := seen[memberUserID]; exists {
			continue
		}
		seen[memberUserID] = struct{}{}
		if _, err := tx.ExecContext(ctx, insertConversationMember, conversationID, memberUserID, "member"); err != nil {
			return 0, fmt.Errorf("insert event conversation member %d: %w", memberUserID, err)
		}
	}

	return conversationID, nil
}

func (r *EventRepository) listEventConversationMembersTx(ctx context.Context, tx *sql.Tx, eventID int64) ([]EventConversationMember, error) {
	rows, err := tx.QueryContext(ctx, selectEventConversationMembers, eventID)
	if err != nil {
		return nil, fmt.Errorf("query event conversation members: %w", err)
	}
	defer rows.Close()

	members := make([]EventConversationMember, 0)
	for rows.Next() {
		var member EventConversationMember
		if err := rows.Scan(&member.ConversationID, &member.UserID); err != nil {
			return nil, fmt.Errorf("scan event conversation member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event conversation members: %w", err)
	}
	return members, nil
}

func (r *EventRepository) listEventConversationIDsTx(ctx context.Context, tx *sql.Tx, eventID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, selectConversationIDsForEvent, eventID)
	if err != nil {
		return nil, fmt.Errorf("query event conversation ids: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var conversationID int64
		if err := rows.Scan(&conversationID); err != nil {
			return nil, fmt.Errorf("scan event conversation id: %w", err)
		}
		ids = append(ids, conversationID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event conversation ids: %w", err)
	}
	return ids, nil
}

func diffEventConversationMembers(source, target []EventConversationMember) []EventConversationMember {
	targetSet := make(map[string]struct{}, len(target))
	for _, member := range target {
		key := fmt.Sprintf("%d:%d", member.ConversationID, member.UserID)
		targetSet[key] = struct{}{}
	}

	diff := make([]EventConversationMember, 0)
	for _, member := range source {
		key := fmt.Sprintf("%d:%d", member.ConversationID, member.UserID)
		if _, exists := targetSet[key]; exists {
			continue
		}
		diff = append(diff, member)
	}
	return diff
}

func (r *EventRepository) Delete(ctx context.Context, id int64, userID int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM events WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check delete rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrEventNotFound
	}

	return nil
}

func (r *EventRepository) List(ctx context.Context) ([]Event, error) {
	rows, err := r.db.QueryContext(ctx, selectEvents)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []Event
	now := time.Now()

	for rows.Next() {
		var evt Event
		var scheduledAtStr sql.NullString
		if err := rows.Scan(
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
			return nil, fmt.Errorf("scan event: %w", err)
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

		// Filter out past events using scheduled_at (UTC comparison)
		if evt.ScheduledAt != nil {
			if evt.ScheduledAt.Before(now.UTC()) {
				continue // Skip past events
			}
		}

		events = append(events, evt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	// Update date labels based on current time
	for i := range events {
		events[i].DateLabel = deriveDateLabel(events[i].EventDate, now)
	}

	sort.Slice(events, func(i, j int) bool {
		// Sort by scheduled_at if both have it
		if events[i].ScheduledAt != nil && events[j].ScheduledAt != nil {
			if events[i].ScheduledAt.Equal(*events[j].ScheduledAt) {
				return events[i].CreatedAt.After(events[j].CreatedAt)
			}
			return events[i].ScheduledAt.Before(*events[j].ScheduledAt)
		}
		// Fall back to legacy sorting
		if events[i].EventDate == events[j].EventDate {
			leftMinutes, _ := parseEventTimeLabel(events[i].Time)
			rightMinutes, _ := parseEventTimeLabel(events[j].Time)
			if leftMinutes == rightMinutes {
				return events[i].CreatedAt.After(events[j].CreatedAt)
			}
			return leftMinutes < rightMinutes
		}
		return events[i].EventDate < events[j].EventDate
	})

	return events, nil
}

// ListUserPastEvents returns past events the user created or joined, newest first.
func (r *EventRepository) ListUserPastEvents(ctx context.Context, userID int64) ([]Event, error) {
	rows, err := r.db.QueryContext(ctx, selectUserPastEvents, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("query past events: %w", err)
	}
	defer rows.Close()

	var events []Event
	now := time.Now()

	for rows.Next() {
		var evt Event
		var scheduledAtStr sql.NullString
		if err := rows.Scan(
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
			return nil, fmt.Errorf("scan past event: %w", err)
		}

		if scheduledAtStr.Valid && scheduledAtStr.String != "" {
			if parsed, err := time.Parse(time.RFC3339, scheduledAtStr.String); err == nil {
				evt.ScheduledAt = &parsed
			} else if parsed, err := time.Parse("2006-01-02 15:04:05", scheduledAtStr.String); err == nil {
				utc := parsed.UTC()
				evt.ScheduledAt = &utc
			}
		}

		evt.DateLabel = deriveDateLabel(evt.EventDate, now)
		events = append(events, evt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate past events: %w", err)
	}

	return events, nil
}

// ListForViewer returns visible events for a specific viewer, excluding events
// hosted by users this viewer has blocked.
func (r *EventRepository) ListForViewer(ctx context.Context, viewerUserID int64) ([]Event, error) {
	events, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	if viewerUserID <= 0 {
		return events, nil
	}

	blockedIDs, err := r.ListBlockedUserIDs(ctx, viewerUserID)
	if err != nil {
		return nil, err
	}
	if len(blockedIDs) == 0 {
		return events, nil
	}

	blockedSet := make(map[int64]struct{}, len(blockedIDs))
	for _, id := range blockedIDs {
		blockedSet[id] = struct{}{}
	}

	filtered := make([]Event, 0, len(events))
	for _, evt := range events {
		if _, blocked := blockedSet[evt.UserID]; blocked {
			continue
		}
		filtered = append(filtered, evt)
	}
	return filtered, nil
}
