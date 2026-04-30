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

func (r *EventRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	var profileComplete int
	if err := r.db.QueryRowContext(ctx, selectUserByEmail, email).Scan(
		&user.ID,
		&user.Name,
		&user.Email,
		&user.Password,
		&user.Gender,
		&user.Age,
		&user.Avatar,
		&profileComplete,
		&user.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("lookup user: %w", err)
	}
	user.ProfileComplete = profileComplete == 1
	return &user, nil
}

func (r *EventRepository) CreateUserWithPassword(ctx context.Context, name, email, password string) (*User, error) {
	if _, err := r.db.ExecContext(ctx, insertUser, name, email, password); err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return r.GetUserByEmail(ctx, email)
}

func (r *EventRepository) GetUserByAppleSubject(ctx context.Context, subject string) (*User, error) {
	var user User
	var profileComplete int

	if err := r.db.QueryRowContext(ctx, selectUserByAppleSubject, strings.TrimSpace(subject)).Scan(
		&user.ID,
		&user.Name,
		&user.Email,
		&user.Password,
		&user.Gender,
		&user.Age,
		&user.Avatar,
		&profileComplete,
		&user.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("lookup user by apple subject: %w", err)
	}

	user.ProfileComplete = profileComplete == 1
	return &user, nil
}

func (r *EventRepository) LinkAppleAccount(ctx context.Context, subject string, userID int64, email string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("link apple account: subject is required")
	}

	trimmedEmail := strings.TrimSpace(email)
	var existingUserID int64
	var existingEmail sql.NullString

	err := r.db.QueryRowContext(ctx, selectAppleAccountBySubject, subject).Scan(&existingUserID, &existingEmail)
	if err == nil {
		if existingUserID != userID {
			return ErrAppleAccountLinkedToDifferentUser
		}
		if trimmedEmail != "" && (!existingEmail.Valid || existingEmail.String != trimmedEmail) {
			if _, updateErr := r.db.ExecContext(ctx, updateAppleAccountEmailBySubject, trimmedEmail, subject); updateErr != nil {
				return fmt.Errorf("update apple account email: %w", updateErr)
			}
		}
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup apple account: %w", err)
	}

	var emailValue any
	if trimmedEmail != "" {
		emailValue = trimmedEmail
	}

	if _, err := r.db.ExecContext(ctx, insertAppleAccount, subject, userID, emailValue); err != nil {
		return fmt.Errorf("insert apple account: %w", err)
	}
	return nil
}
func (r *EventRepository) AuthenticateUser(ctx context.Context, email, password string) (*User, error) {
	user, err := r.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if user.Password != password {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

func (r *EventRepository) GetUserByID(ctx context.Context, id int64) (*User, error) {
	var user User
	var profileComplete int
	if err := r.db.QueryRowContext(ctx, selectUserByID, id).Scan(
		&user.ID,
		&user.Name,
		&user.Email,
		&user.Password,
		&user.Gender,
		&user.Age,
		&user.Avatar,
		&profileComplete,
		&user.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("lookup user by id: %w", err)
	}
	user.ProfileComplete = profileComplete == 1
	return &user, nil
}

type UpdateProfileParams struct {
	Name   string
	Gender *string
	Age    *int
	Avatar *string
}

func (r *EventRepository) UpdateUserProfile(ctx context.Context, userID int64, params UpdateProfileParams) (*User, error) {
	profileComplete := 1
	if _, err := r.db.ExecContext(ctx, updateUserProfile, params.Name, params.Gender, params.Age, params.Avatar, profileComplete, userID); err != nil {
		return nil, fmt.Errorf("update user profile: %w", err)
	}
	return r.GetUserByID(ctx, userID)
}

func (r *EventRepository) DeleteUserAccount(ctx context.Context, userID int64) (*DeleteUserAccountResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin delete account tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := ensureUserExistsTx(ctx, tx, userID); err != nil {
		return nil, err
	}

	hostedEvents, hostedNotifications, err := collectHostedEventDeletionEffectsTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}

	joinedNotifications, err := collectJoinedEventDeletionEffectsTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}

	directNotifications, err := collectDirectConversationDeletionEffectsTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}

	// Hosted events must go first: events.user_id references users.id and
	// event-backed conversations/join-requests/reports cascade from events.
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE user_id = ?;`, userID); err != nil {
		return nil, fmt.Errorf("delete hosted events: %w", err)
	}

	// A joined Single event has a private event conversation. Deleting the
	// conversation mirrors RemoveEventMember semantics and removes both sides'
	// private chat without deleting the host's event.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM conversations
		WHERE id IN (
			SELECT c.id
			FROM conversations c
			JOIN events e ON e.id = c.event_id
			JOIN conversation_members cm ON cm.conversation_id = c.id
			WHERE cm.user_id = ?
			  AND e.user_id <> ?
			  AND e.group_type = 'Single'
		);
	`, userID, userID); err != nil {
		return nil, fmt.Errorf("delete joined single-event conversations: %w", err)
	}

	// Remaining messages from the deleting user would block users.id deletion
	// because messages.sender_id is a foreign key without ON DELETE CASCADE.
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE sender_id = ?;`, userID); err != nil {
		return nil, fmt.Errorf("delete user messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_read_state WHERE user_id = ?;`, userID); err != nil {
		return nil, fmt.Errorf("delete user read state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_members WHERE user_id = ?;`, userID); err != nil {
		return nil, fmt.Errorf("delete user conversation memberships: %w", err)
	}

	// Some old direct/legacy conversations are not event-backed. If the deleted
	// user created them, remove the whole conversation so conversations.created_by
	// no longer references the account.
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE created_by = ? AND event_id IS NULL;`, userID); err != nil {
		return nil, fmt.Errorf("delete direct conversations created by user: %w", err)
	}

	// Defensive legacy repair: if an event-backed conversation still claims the
	// deleting user as creator but the event belongs to someone else, hand it
	// back to the event owner instead of deleting another user's event chat.
	if _, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET created_by = (
			SELECT e.user_id
			FROM events e
			WHERE e.id = conversations.event_id
		)
		WHERE created_by = ?
		  AND event_id IS NOT NULL
		  AND EXISTS (
			SELECT 1
			FROM events e
			WHERE e.id = conversations.event_id
			  AND e.user_id <> ?
		  );
	`, userID, userID); err != nil {
		return nil, fmt.Errorf("repair legacy conversation ownership: %w", err)
	}

	// Requests and moderation rows reference users in several roles. Removing
	// any row involving the account keeps the delete privacy-preserving and
	// avoids dangling decided_by/reviewed_by references.
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_join_requests WHERE user_id = ? OR decided_by = ?;`, userID, userID); err != nil {
		return nil, fmt.Errorf("delete user join requests: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM event_reports WHERE user_id = ? OR reported_user_id = ? OR reviewed_by = ?;`, userID, userID, userID); err != nil {
		return nil, fmt.Errorf("delete user reports: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_blocks WHERE blocker_user_id = ? OR blocked_user_id = ?;`, userID, userID); err != nil {
		return nil, fmt.Errorf("delete user blocks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_tokens WHERE user_id = ?;`, userID); err != nil {
		return nil, fmt.Errorf("delete user push tokens: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM apple_accounts WHERE user_id = ?;`, userID); err != nil {
		return nil, fmt.Errorf("delete apple account links: %w", err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?;`, userID)
	if err != nil {
		return nil, fmt.Errorf("delete user row: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("check deleted user rows: %w", err)
	}
	if rowsAffected == 0 {
		return nil, ErrUserNotFound
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete account tx: %w", err)
	}
	committed = true

	notifications := append(hostedNotifications, joinedNotifications...)
	notifications = append(notifications, directNotifications...)

	return &DeleteUserAccountResult{
		DeletedUserID:           userID,
		HostedEvents:            hostedEvents,
		MembershipNotifications: dedupeMembershipNotifications(notifications),
	}, nil
}

func ensureUserExistsTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ? LIMIT 1;`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("verify user exists: %w", err)
	}
	return nil
}

func collectHostedEventDeletionEffectsTx(ctx context.Context, tx *sql.Tx, userID int64) ([]AccountDeletionHostedEvent, []EventConversationMember, error) {
	eventRows, err := tx.QueryContext(ctx, `
		SELECT id, title
		FROM events
		WHERE user_id = ?
		ORDER BY id ASC;
	`, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("collect hosted events: %w", err)
	}
	defer eventRows.Close()

	hostedEvents := make([]AccountDeletionHostedEvent, 0)
	eventIndex := make(map[int64]int)
	for eventRows.Next() {
		var event AccountDeletionHostedEvent
		if err := eventRows.Scan(&event.ID, &event.Title); err != nil {
			return nil, nil, fmt.Errorf("scan hosted event: %w", err)
		}
		eventIndex[event.ID] = len(hostedEvents)
		hostedEvents = append(hostedEvents, event)
	}
	if err := eventRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate hosted events: %w", err)
	}

	memberRows, err := tx.QueryContext(ctx, `
		SELECT e.id, c.id, cm.user_id
		FROM events e
		JOIN conversations c ON c.event_id = e.id
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE e.user_id = ?
		  AND cm.user_id <> ?
		ORDER BY e.id ASC, c.id ASC, cm.user_id ASC;
	`, userID, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("collect hosted event members: %w", err)
	}
	defer memberRows.Close()

	notifications := make([]EventConversationMember, 0)
	recipientSets := make(map[int64]map[int64]struct{})
	for memberRows.Next() {
		var eventID int64
		var member EventConversationMember
		if err := memberRows.Scan(&eventID, &member.ConversationID, &member.UserID); err != nil {
			return nil, nil, fmt.Errorf("scan hosted event member: %w", err)
		}
		notifications = append(notifications, member)
		if _, ok := recipientSets[eventID]; !ok {
			recipientSets[eventID] = make(map[int64]struct{})
		}
		recipientSets[eventID][member.UserID] = struct{}{}
	}
	if err := memberRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate hosted event members: %w", err)
	}

	for eventID, recipients := range recipientSets {
		index, ok := eventIndex[eventID]
		if !ok {
			continue
		}
		hostedEvents[index].RecipientIDs = sortedInt64Keys(recipients)
	}

	return hostedEvents, notifications, nil
}

func collectJoinedEventDeletionEffectsTx(ctx context.Context, tx *sql.Tx, userID int64) ([]EventConversationMember, error) {
	notifications := make([]EventConversationMember, 0)

	// Single-event private conversations are deleted, so every current member
	// should receive a self-targeted removal and drop the conversation locally.
	singleRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT c.id, cm.user_id
		FROM conversations c
		JOIN events e ON e.id = c.event_id
		JOIN conversation_members self ON self.conversation_id = c.id AND self.user_id = ?
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE e.user_id <> ?
		  AND e.group_type = 'Single'
		ORDER BY c.id ASC, cm.user_id ASC;
	`, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("collect joined single-event notifications: %w", err)
	}
	defer singleRows.Close()
	for singleRows.Next() {
		var member EventConversationMember
		if err := singleRows.Scan(&member.ConversationID, &member.UserID); err != nil {
			return nil, fmt.Errorf("scan joined single-event notification: %w", err)
		}
		notifications = append(notifications, member)
	}
	if err := singleRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate joined single-event notifications: %w", err)
	}

	// Group chats survive; a removal targeted at the deleted user makes that
	// user's sockets leave, while remaining members refresh because userId differs.
	groupRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT c.id, ?
		FROM conversations c
		JOIN events e ON e.id = c.event_id
		JOIN conversation_members self ON self.conversation_id = c.id AND self.user_id = ?
		WHERE e.user_id <> ?
		  AND e.group_type = 'Group'
		ORDER BY c.id ASC;
	`, userID, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("collect joined group-event notifications: %w", err)
	}
	defer groupRows.Close()
	for groupRows.Next() {
		var member EventConversationMember
		if err := groupRows.Scan(&member.ConversationID, &member.UserID); err != nil {
			return nil, fmt.Errorf("scan joined group-event notification: %w", err)
		}
		notifications = append(notifications, member)
	}
	if err := groupRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate joined group-event notifications: %w", err)
	}

	return notifications, nil
}

func collectDirectConversationDeletionEffectsTx(ctx context.Context, tx *sql.Tx, userID int64) ([]EventConversationMember, error) {
	notifications := make([]EventConversationMember, 0)

	// Direct conversations created by the deleted user are removed entirely, so
	// every participant should get a self-removal event.
	createdRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT c.id, cm.user_id
		FROM conversations c
		JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.event_id IS NULL
		  AND c.created_by = ?
		ORDER BY c.id ASC, cm.user_id ASC;
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("collect direct-created conversation notifications: %w", err)
	}
	defer createdRows.Close()
	for createdRows.Next() {
		var member EventConversationMember
		if err := createdRows.Scan(&member.ConversationID, &member.UserID); err != nil {
			return nil, fmt.Errorf("scan direct-created conversation notification: %w", err)
		}
		notifications = append(notifications, member)
	}
	if err := createdRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate direct-created conversation notifications: %w", err)
	}

	// Direct conversations owned by someone else survive; notify the deleted
	// user's removal so remaining sockets refresh participant/previews.
	memberRows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT c.id, ?
		FROM conversations c
		JOIN conversation_members self ON self.conversation_id = c.id AND self.user_id = ?
		WHERE c.event_id IS NULL
		  AND c.created_by <> ?
		ORDER BY c.id ASC;
	`, userID, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("collect direct-member conversation notifications: %w", err)
	}
	defer memberRows.Close()
	for memberRows.Next() {
		var member EventConversationMember
		if err := memberRows.Scan(&member.ConversationID, &member.UserID); err != nil {
			return nil, fmt.Errorf("scan direct-member conversation notification: %w", err)
		}
		notifications = append(notifications, member)
	}
	if err := memberRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate direct-member conversation notifications: %w", err)
	}

	return notifications, nil
}

func dedupeMembershipNotifications(notifications []EventConversationMember) []EventConversationMember {
	seen := make(map[string]struct{}, len(notifications))
	deduped := make([]EventConversationMember, 0, len(notifications))
	for _, notification := range notifications {
		key := fmt.Sprintf("%d:%d", notification.ConversationID, notification.UserID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, notification)
	}
	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].ConversationID == deduped[j].ConversationID {
			return deduped[i].UserID < deduped[j].UserID
		}
		return deduped[i].ConversationID < deduped[j].ConversationID
	})
	return deduped
}

func sortedInt64Keys(values map[int64]struct{}) []int64 {
	keys := make([]int64, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	return keys
}

func (r *EventRepository) CreateEventReport(ctx context.Context, eventID, userID int64, reason string) (*EventReport, error) {
	res, err := r.db.ExecContext(ctx, insertEventReport, eventID, userID, reason)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrReportAlreadyExists
		}
		return nil, fmt.Errorf("insert event report: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("fetch event report id: %w", err)
	}
	return &EventReport{
		ID:        id,
		EventID:   eventID,
		UserID:    userID,
		Reason:    reason,
		Status:    "pending",
		CreatedAt: time.Now(),
	}, nil
}

// CreateMemberReport creates a report for a specific member of an event.
// The reporter_id is the user submitting the report, reported_user_id is the target.
func (r *EventRepository) CreateMemberReport(ctx context.Context, eventID, reporterID, reportedUserID int64, reason string) (*EventReport, error) {
	res, err := r.db.ExecContext(ctx, insertMemberReport, eventID, reporterID, reportedUserID, reason)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrReportAlreadyExists
		}
		return nil, fmt.Errorf("insert member report: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("fetch member report id: %w", err)
	}
	return &EventReport{
		ID:             id,
		EventID:        eventID,
		UserID:         reporterID,
		ReportedUserID: &reportedUserID,
		Reason:         reason,
		Status:         "pending",
		CreatedAt:      time.Now(),
	}, nil
}

// CreateMutualBlock stores a bidirectional block relation between two users.
// The operation is idempotent.
func (r *EventRepository) CreateMutualBlock(ctx context.Context, userA, userB int64) error {
	if userA <= 0 || userB <= 0 {
		return fmt.Errorf("create mutual block: invalid user ids")
	}
	if userA == userB {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mutual block tx: %w", err)
	}

	if _, err := tx.ExecContext(ctx, insertUserBlock, userA, userB); err != nil {
		tx.Rollback()
		return fmt.Errorf("insert block %d->%d: %w", userA, userB, err)
	}
	if _, err := tx.ExecContext(ctx, insertUserBlock, userB, userA); err != nil {
		tx.Rollback()
		return fmt.Errorf("insert block %d->%d: %w", userB, userA, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mutual block: %w", err)
	}
	return nil
}

// DeleteMutualBlock removes both user->user block rows and reports whether
// either direction existed before deletion.
func (r *EventRepository) DeleteMutualBlock(ctx context.Context, userA, userB int64) (bool, error) {
	if userA <= 0 || userB <= 0 {
		return false, fmt.Errorf("delete mutual block: invalid user ids")
	}
	if userA == userB {
		return false, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete mutual block tx: %w", err)
	}

	deletedAny := false

	resA, err := tx.ExecContext(ctx, deleteUserBlock, userA, userB)
	if err != nil {
		tx.Rollback()
		return false, fmt.Errorf("delete block %d->%d: %w", userA, userB, err)
	}
	rowsA, err := resA.RowsAffected()
	if err != nil {
		tx.Rollback()
		return false, fmt.Errorf("rows affected for delete block %d->%d: %w", userA, userB, err)
	}
	if rowsA > 0 {
		deletedAny = true
	}

	resB, err := tx.ExecContext(ctx, deleteUserBlock, userB, userA)
	if err != nil {
		tx.Rollback()
		return false, fmt.Errorf("delete block %d->%d: %w", userB, userA, err)
	}
	rowsB, err := resB.RowsAffected()
	if err != nil {
		tx.Rollback()
		return false, fmt.Errorf("rows affected for delete block %d->%d: %w", userB, userA, err)
	}
	if rowsB > 0 {
		deletedAny = true
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delete mutual block: %w", err)
	}

	return deletedAny, nil
}

// HasMemberReport returns whether a host previously submitted a member report
// for the target user within the given event.
func (r *EventRepository) HasMemberReport(ctx context.Context, eventID, hostUserID, memberUserID int64) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(
		ctx,
		selectMemberReportRelationship,
		eventID,
		hostUserID,
		memberUserID,
	).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check member report relationship: %w", err)
	}
	return true, nil
}

func (r *EventRepository) DeleteMemberReport(ctx context.Context, eventID, reporterUserID, reportedUserID int64) error {
	_, err := r.db.ExecContext(ctx, deleteMemberReportByEventAndUsers, eventID, reporterUserID, reportedUserID)
	if err != nil {
		return fmt.Errorf("delete member report: %w", err)
	}
	return nil
}

// ListHostEventIDsForMember returns event IDs owned by hostUserID where
// memberUserID is currently an accepted participant (conversation member).
func (r *EventRepository) ListHostEventIDsForMember(ctx context.Context, hostUserID, memberUserID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, selectHostEventIDsForMember, hostUserID, memberUserID)
	if err != nil {
		return nil, fmt.Errorf("list host event ids for member: %w", err)
	}
	defer rows.Close()

	eventIDs := make([]int64, 0)
	for rows.Next() {
		var eventID int64
		if err := rows.Scan(&eventID); err != nil {
			return nil, fmt.Errorf("scan host event id for member: %w", err)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate host event ids for member: %w", err)
	}
	return eventIDs, nil
}

// ListBlockedUserIDs returns all users blocked by blockerUserID.
func (r *EventRepository) ListBlockedUserIDs(ctx context.Context, blockerUserID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, selectBlockedUserIDsForUser, blockerUserID)
	if err != nil {
		return nil, fmt.Errorf("list blocked users: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan blocked user id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate blocked users: %w", err)
	}
	return ids, nil
}

// IsUserBlocked reports whether blockerUserID blocks blockedUserID.
func (r *EventRepository) IsUserBlocked(ctx context.Context, blockerUserID, blockedUserID int64) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx, selectUserBlockRelationship, blockerUserID, blockedUserID).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check block relationship: %w", err)
	}
	return true, nil
}

// AreUsersBlocked reports whether either user has blocked the other.
func (r *EventRepository) AreUsersBlocked(ctx context.Context, userA, userB int64) (bool, error) {
	if userA <= 0 || userB <= 0 || userA == userB {
		return false, nil
	}

	aBlocksB, err := r.IsUserBlocked(ctx, userA, userB)
	if err != nil {
		return false, err
	}
	if aBlocksB {
		return true, nil
	}

	bBlocksA, err := r.IsUserBlocked(ctx, userB, userA)
	if err != nil {
		return false, err
	}
	return bBlocksA, nil
}

// findUserConversationForEventPublic is a public wrapper around findUserConversationForEvent
// that doesn't require a transaction.
func (r *EventRepository) findUserConversationForEventPublic(ctx context.Context, eventID, userID int64) (*Conversation, error) {
	return r.findUserConversationForEvent(ctx, nil, eventID, userID)
}

// UpsertPushToken inserts or updates a push token for a user/device pair.
func (r *EventRepository) UpsertPushToken(ctx context.Context, userID int64, token, deviceID, platform string) error {
	_, err := r.db.ExecContext(ctx, upsertPushToken, userID, token, deviceID, platform)
	if err != nil {
		return fmt.Errorf("upsert push token: %w", err)
	}
	return nil
}

// DeletePushToken removes a specific push token for a user.
func (r *EventRepository) DeletePushToken(ctx context.Context, userID int64, token string) error {
	result, err := r.db.ExecContext(ctx, deletePushTokenByValue, userID, token)
	if err != nil {
		return fmt.Errorf("delete push token: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("push token not found")
	}
	return nil
}

// ListPushTokensByUser returns all push tokens for a user.
func (r *EventRepository) ListPushTokensByUser(ctx context.Context, userID int64) ([]PushToken, error) {
	rows, err := r.db.QueryContext(ctx, selectPushTokensByUserID, userID)
	if err != nil {
		return nil, fmt.Errorf("list push tokens: %w", err)
	}
	defer rows.Close()
	var tokens []PushToken
	for rows.Next() {
		var t PushToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Token, &t.DeviceID, &t.Platform, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan push token: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// ListPushTokensByUserIDs returns all push tokens for a set of user IDs.
func (r *EventRepository) ListPushTokensByUserIDs(ctx context.Context, userIDs []int64) ([]PushToken, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(userIDs))
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(selectPushTokensByUserIDs, strings.Join(placeholders, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list push tokens by user ids: %w", err)
	}
	defer rows.Close()
	var tokens []PushToken
	for rows.Next() {
		var t PushToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Token, &t.DeviceID, &t.Platform, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan push token: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// ListConversationMemberIDs returns all user IDs that are members of a conversation.
func (r *EventRepository) ListConversationMemberIDs(ctx context.Context, conversationID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, selectConversationMemberIDs, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list conversation member ids: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan member id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListEventConversationMembers returns conversation-member pairs for every
// conversation linked to an event. This captures approved participants for both
// group events (single shared conversation) and 1:1 events (private per-user
// conversations).
func (r *EventRepository) ListEventConversationMembers(ctx context.Context, eventID int64) ([]EventConversationMember, error) {
	rows, err := r.db.QueryContext(ctx, selectEventConversationMembers, eventID)
	if err != nil {
		return nil, fmt.Errorf("list event conversation members: %w", err)
	}
	defer rows.Close()

	var members []EventConversationMember
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
