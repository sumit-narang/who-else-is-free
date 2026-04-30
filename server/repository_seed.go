package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Seed events disabled - no longer creating dummy data on startup
var seedEvents = []CreateEventParams{}

func (r *EventRepository) EnsureSeedData(ctx context.Context) error {
	if err := r.ensureSeedUsers(ctx); err != nil {
		return err
	}
	if err := r.ensureEventsUserIDColumn(ctx); err != nil {
		return err
	}
	if err := r.ensureEventCoverKeyColumn(ctx); err != nil {
		return err
	}
	if err := r.ensureEventDateColumn(ctx); err != nil {
		return err
	}
	if err := r.ensureEventGroupTypeColumn(ctx); err != nil {
		return err
	}
	if err := r.ensureSeedEvents(ctx); err != nil {
		return err
	}
	if err := r.ensureSeedConversations(ctx); err != nil {
		return err
	}
	return r.ensureSeedEventGroupChat(ctx)
}

type seedUser struct {
	Name     string
	Email    string
	Password string
}

var seedUsers = []seedUser{
	{
		Name:     "Ava Johnson",
		Email:    "ava@example.com",
		Password: "password123",
	},
	{
		Name:     "Liam Patel",
		Email:    "liam@example.com",
		Password: "welcome123",
	},
	{
		Name:     "Sophia Chen",
		Email:    "sophia@example.com",
		Password: "secret123",
	},
	{
		Name:     "Noah Smith",
		Email:    "noah@example.com",
		Password: "sunset123",
	},
}

func (r *EventRepository) ensureSeedUsers(ctx context.Context) error {
	var count int
	if err := r.db.QueryRowContext(ctx, countUsers).Scan(&count); err != nil {
		return fmt.Errorf("count users: %w", err)
	}

	if count > 0 {
		return nil
	}

	for _, user := range seedUsers {
		if _, err := r.db.ExecContext(ctx, insertUser, user.Name, user.Email, user.Password); err != nil {
			return fmt.Errorf("seed user %q: %w", user.Email, err)
		}
	}

	return nil
}

func (r *EventRepository) ensureSeedEvents(ctx context.Context) error {
	var count int
	if err := r.db.QueryRowContext(ctx, countEvents).Scan(&count); err != nil {
		return fmt.Errorf("count events: %w", err)
	}

	if count > 0 {
		return nil
	}

	today := startOfDay(time.Now())
	todayStr := today.Format("2006-01-02")
	tomorrowStr := today.AddDate(0, 0, 1).Format("2006-01-02")

	for _, evt := range seedEvents {
		if strings.TrimSpace(evt.EventDate) == "" {
			if evt.DateLabel == "Tmrw" {
				evt.EventDate = tomorrowStr
			} else {
				evt.EventDate = todayStr
			}
		}
		if strings.TrimSpace(evt.GroupType) == "" {
			evt.GroupType = "Single"
		}
		parsedDate, err := time.Parse("2006-01-02", evt.EventDate)
		if err != nil {
			parsedDate = today
		}
		dayDiff := int(startOfDay(parsedDate).Sub(today).Hours() / 24)
		if dayDiff == 1 {
			evt.DateLabel = "Tmrw"
		} else {
			evt.DateLabel = "Today"
		}
		if _, err := r.Create(ctx, evt); err != nil {
			return fmt.Errorf("seed event %q: %w", evt.Title, err)
		}
	}

	return nil
}

func (r *EventRepository) ensureSeedConversations(ctx context.Context) error {
	var count int
	if err := r.db.QueryRowContext(ctx, countConversations).Scan(&count); err != nil {
		return fmt.Errorf("count conversations: %w", err)
	}

	alreadySeeded := count > 0

	rows, err := r.db.QueryContext(ctx, selectAllUsers)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	type seedUserRecord struct {
		ID   int64
		Name string
	}

	var users []seedUserRecord
	for rows.Next() {
		var record seedUserRecord
		if err := rows.Scan(&record.ID, &record.Name); err != nil {
			return fmt.Errorf("scan user: %w", err)
		}
		users = append(users, record)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate users: %w", err)
	}

	if len(users) < 2 {
		return nil
	}

	if !alreadySeeded {
		sampleMessages := []string{
			"Hey there! Want to sync up later?",
			"Looking forward to catching up soon.",
			"Should we plan something fun tonight?",
		}

		msgIndex := 0
		for i := 0; i < len(users); i++ {
			for j := i + 1; j < len(users); j++ {
				pair := []int64{users[i].ID, users[j].ID}
				convo, err := r.CreateConversation(ctx, nil, users[i].ID, pair, nil)
				if err != nil {
					return fmt.Errorf("seed direct conversation: %w", err)
				}

				intro := sampleMessages[msgIndex%len(sampleMessages)]
				msgIndex++
				if _, err = r.CreateMessage(ctx, CreateMessageParams{
					ConversationID: convo.ID,
					SenderID:       users[i].ID,
					Body:           intro,
					DeliveryStatus: "sent",
				}); err != nil {
					return fmt.Errorf("seed conversation message: %w", err)
				}

				reply := fmt.Sprintf("Hi %s! Count me in.", users[i].Name)
				replyMsg, err := r.CreateMessage(ctx, CreateMessageParams{
					ConversationID: convo.ID,
					SenderID:       users[j].ID,
					Body:           reply,
					DeliveryStatus: "sent",
				})
				if err != nil {
					return fmt.Errorf("seed conversation reply: %w", err)
				}

				if err := r.UpdateReadState(ctx, convo.ID, users[i].ID, replyMsg.ID); err != nil {
					return fmt.Errorf("seed read state sender: %w", err)
				}
				if err := r.UpdateReadState(ctx, convo.ID, users[j].ID, replyMsg.ID); err != nil {
					return fmt.Errorf("seed read state recipient: %w", err)
				}
			}
		}
	}

	if len(users) >= 3 {
		groupTitle := "Planning Crew"
		var existingID int64
		err := r.db.QueryRowContext(ctx, selectConversationByTitle, groupTitle).Scan(&existingID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("check existing group conversation: %w", err)
			}

			members := []int64{users[0].ID, users[1].ID, users[2].ID}
			convo, err := r.CreateConversation(ctx, &groupTitle, users[0].ID, members, nil)
			if err != nil {
				return fmt.Errorf("seed group conversation: %w", err)
			}

			seedGroupMessages := []struct {
				sender int64
				body   string
			}{
				{sender: users[0].ID, body: "Team, let's sync here about weekend ideas."},
				{sender: users[1].ID, body: "Love it. How about a hike followed by brunch?"},
				{sender: users[2].ID, body: "Count me in! I can book a table if we pick a spot."},
			}

			var lastMsgID int64
			for _, msg := range seedGroupMessages {
				created, err := r.CreateMessage(ctx, CreateMessageParams{
					ConversationID: convo.ID,
					SenderID:       msg.sender,
					Body:           msg.body,
					DeliveryStatus: "sent",
				})
				if err != nil {
					return fmt.Errorf("seed group conversation message: %w", err)
				}
				if created != nil {
					lastMsgID = created.ID
				}
			}

			if lastMsgID > 0 {
				for _, member := range members {
					if err := r.UpdateReadState(ctx, convo.ID, member, lastMsgID); err != nil {
						return fmt.Errorf("seed group conversation read state: %w", err)
					}
				}
			}
		}
	}

	return nil
}

func (r *EventRepository) ensureSeedEventGroupChat(ctx context.Context) error {
	convo, err := r.GetConversationByEventID(ctx, 1)
	if err != nil {
		if errors.Is(err, ErrConversationNotFound) || errors.Is(err, ErrEventNotFound) {
			return nil
		}
		return err
	}

	_, memberIDs, err := r.fetchConversationParticipants(ctx, convo.ID)
	if err != nil {
		return err
	}
	if len(memberIDs) >= 4 {
		return nil
	}

	memberSet := make(map[int64]struct{}, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = struct{}{}
	}

	additionalMembers := []int64{2, 3, 4}
	for _, userID := range additionalMembers {
		if _, ok := memberSet[userID]; ok {
			continue
		}
		if _, err := r.db.ExecContext(ctx, insertConversationMember, convo.ID, userID, "member"); err != nil {
			return fmt.Errorf("seed event group member %d: %w", userID, err)
		}
	}

	sampleMessages := []struct {
		sender int64
		body   string
	}{
		{sender: convo.CreatedBy, body: "Hey everyone! Use this chat to coordinate before the event."},
		{sender: 2, body: "Thanks for adding me—looking forward to it."},
		{sender: 3, body: "I'll bring snacks. Any allergy concerns?"},
		{sender: 4, body: "I’m good with anything. See you all there!"},
	}

	var lastMessageID int64
	for _, msg := range sampleMessages {
		created, err := r.CreateMessage(ctx, CreateMessageParams{
			ConversationID: convo.ID,
			SenderID:       msg.sender,
			Body:           msg.body,
			DeliveryStatus: "sent",
		})
		if err != nil {
			return fmt.Errorf("seed event group message: %w", err)
		}
		if created != nil {
			lastMessageID = created.ID
		}
	}

	if lastMessageID > 0 {
		if err := r.UpdateReadState(ctx, convo.ID, convo.CreatedBy, lastMessageID); err != nil {
			return fmt.Errorf("seed event group read state: %w", err)
		}
	}

	return nil
}
