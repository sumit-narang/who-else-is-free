package main

import (
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func setupRouter(eventHandler *EventHandler, authHandler *AuthHandler, profileHandler *ProfileHandler, chatHub *ChatHub, pushHandler *PushHandler, signer *tokenSigner) *gin.Engine {
	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
		MaxAge:        12 * time.Hour,
	}))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	authHandler.RegisterRoutes(api)
	eventHandler.RegisterRoutes(api)

	protected := api.Group("")
	protected.Use(sessionMiddleware(signer, eventHandler.repo))
	eventHandler.RegisterProtectedRoutes(protected)
	protected.POST("/events/:id/report", eventHandler.reportEvent)
	protected.PUT("/profile", profileHandler.UpdateProfile)
	protected.DELETE("/profile", profileHandler.DeleteProfile)
	RegisterChatRoutes(protected, eventHandler.repo, chatHub)
	protected.POST("/push-tokens", pushHandler.registerPushToken)
	protected.DELETE("/push-tokens", pushHandler.deletePushToken)
	if os.Getenv("PUSH_ENABLED") == "true" {
		protected.POST("/push-tokens/test", pushHandler.testPush)
	}

	api.GET("/ws", chatHub.handleWebSocket)

	return r
}
