package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type ProfileHandler struct {
	repo *EventRepository
	hub  *ChatHub
}

func NewProfileHandler(repo *EventRepository, hub ...*ChatHub) *ProfileHandler {
	handler := &ProfileHandler{repo: repo}
	if len(hub) > 0 {
		handler.hub = hub[0]
	}
	return handler
}

type updateProfileRequest struct {
	Name   string  `json:"name" binding:"required,min=1"`
	Gender string  `json:"gender" binding:"required,oneof=Female Male"`
	Age    int     `json:"age" binding:"required,gte=13,lte=120"`
	Avatar *string `json:"avatar"`
}

func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	claims, exists := sessionFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userID := claims.UserID

	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Fetch current user to check if gender/age are already set
	existingUser, err := h.repo.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired, please sign in again"})
			return
		}
		log.Printf("profile update: failed to fetch user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}

	// Gender and age are immutable once set
	if existingUser.Gender != nil && *existingUser.Gender != req.Gender {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gender cannot be changed once set"})
		return
	}
	if existingUser.Age != nil && *existingUser.Age != req.Age {
		c.JSON(http.StatusBadRequest, gin.H{"error": "age cannot be changed once set"})
		return
	}

	gender := req.Gender
	age := req.Age

	params := UpdateProfileParams{
		Name:   req.Name,
		Gender: &gender,
		Age:    &age,
		Avatar: req.Avatar,
	}

	user, err := h.repo.UpdateUserProfile(c.Request.Context(), userID, params)
	if err != nil {
		log.Printf("profile update: failed to update user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	userResponse := gin.H{
		"id":               user.ID,
		"name":             user.Name,
		"email":            user.Email,
		"profile_complete": user.ProfileComplete,
	}
	if user.Gender != nil {
		userResponse["gender"] = *user.Gender
	}
	if user.Age != nil {
		userResponse["age"] = *user.Age
	}
	if user.Avatar != nil {
		userResponse["avatar"] = *user.Avatar
	}

	c.JSON(http.StatusOK, gin.H{"user": userResponse})
}

func (h *ProfileHandler) DeleteProfile(c *gin.Context) {
	claims, exists := sessionFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
	defer cancel()

	result, err := h.repo.DeleteUserAccount(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired, please sign in again"})
			return
		}
		log.Printf("profile delete: failed to delete user %d: %v", claims.UserID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete account"})
		return
	}

	// Notify only after the transaction commits. If clients refresh before the
	// commit, they can briefly reload rows that are about to disappear.
	if h.hub != nil {
		for _, membership := range result.MembershipNotifications {
			h.hub.NotifyMembership(membership.ConversationID, membership.UserID, "removed")
		}
		for _, event := range result.HostedEvents {
			if len(event.RecipientIDs) == 0 {
				continue
			}
			h.hub.sendPushToUsers(event.RecipientIDs, map[string]string{
				"type":    "event.deleted",
				"eventId": strconv.FormatInt(event.ID, 10),
				"title":   event.Title,
				"body":    eventDeletedPushBody,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "account deleted"})
}
