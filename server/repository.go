package main

import (
	"context"
	"database/sql"
	"errors"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrUserNotFound = errors.New("user not found")
var ErrEventNotFound = errors.New("event not found")
var ErrConversationNotFound = errors.New("conversation not found")
var ErrAlreadyConversationMember = errors.New("user already a conversation member")
var ErrJoinRequestExists = errors.New("join request already pending")
var ErrJoinRequestNotFound = errors.New("join request not found")
var ErrNotEventHost = errors.New("user is not the event host")
var ErrCannotRemoveHost = errors.New("event host cannot be removed from the conversation")
var ErrNotConversationMember = errors.New("user is not a conversation member")
var ErrReportAlreadyExists = errors.New("report already exists")
var ErrUsersBlocked = errors.New("users are blocked")
var ErrAppleAccountLinkedToDifferentUser = errors.New("apple account is already linked to a different user")

type rowQuery interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type EventRepository struct {
	db *sql.DB
}

type EventConversationMember struct {
	ConversationID int64
	UserID         int64
}

type AccountDeletionHostedEvent struct {
	ID           int64
	Title        string
	RecipientIDs []int64
}

type DeleteUserAccountResult struct {
	DeletedUserID           int64
	HostedEvents            []AccountDeletionHostedEvent
	MembershipNotifications []EventConversationMember
}

type EventUpdateTransition struct {
	RemovedMemberships           []EventConversationMember
	AddedMemberships             []EventConversationMember
	PostMigrationConversationIDs []int64
}

func NewEventRepository(db *sql.DB) *EventRepository {
	return &EventRepository{db: db}
}
