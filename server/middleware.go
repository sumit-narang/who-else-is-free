package main

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type contextKey string

const sessionContextKey contextKey = "chatSession"

func bearerTokenFromHeader(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func sessionMiddleware(signer *tokenSigner, repos ...*EventRepository) gin.HandlerFunc {
	var repo *EventRepository
	if len(repos) > 0 {
		repo = repos[0]
	}

	// sessionMiddleware is applied to REST routes that require authentication.
	// It pulls the bearer token, validates it, and stashes the claims on the context
	// so handlers can trust the user identity. When a repository is provided,
	// we also verify the user row still exists so deleted accounts cannot keep
	// using unexpired stateless JWTs from another device.
	return func(c *gin.Context) {
		token := bearerTokenFromHeader(c.GetHeader("Authorization"))
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization"})
			return
		}

		claims, err := signer.verify(token)
		if err != nil {
			status := http.StatusUnauthorized
			if err == errExpiredToken {
				status = http.StatusUnauthorized
			}
			c.AbortWithStatusJSON(status, gin.H{"error": "invalid or expired token"})
			return
		}

		if repo != nil {
			if _, err := repo.GetUserByID(c.Request.Context(), claims.UserID); err != nil {
				if errors.Is(err, ErrUserNotFound) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session expired, please sign in again"})
					return
				}
				log.Printf("session middleware: failed to validate user %d: %v", claims.UserID, err)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to validate session"})
				return
			}
		}

		c.Set(string(sessionContextKey), claims)
		c.Next()
	}
}

func sessionFromContext(c *gin.Context) (*sessionClaims, bool) {
	// Helpers return the claims previously injected by sessionMiddleware.
	value, ok := c.Get(string(sessionContextKey))
	if !ok {
		return nil, false
	}
	claims, ok := value.(*sessionClaims)
	return claims, ok
}
