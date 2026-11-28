package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// requestLogger is gin's default request log line without the query string.
// The chat socket sends its session token as ?token=, and place searches send
// what the user typed, so neither should reach the server logs.
func requestLogger(out io.Writer) gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		Output: out,
		Formatter: func(param gin.LogFormatterParams) string {
			path, _, _ := strings.Cut(param.Path, "?")
			var statusColor, methodColor, resetColor string
			if param.IsOutputColor() {
				statusColor = param.StatusCodeColor()
				methodColor = param.MethodColor()
				resetColor = param.ResetColor()
			}
			if param.Latency > time.Minute {
				param.Latency = param.Latency.Truncate(time.Second)
			}
			return fmt.Sprintf("[GIN] %v |%s %3d %s| %13v | %15s |%s %-7s %s %#v\n%s",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				statusColor, param.StatusCode, resetColor,
				param.Latency,
				param.ClientIP,
				methodColor, param.Method, resetColor,
				path,
				param.ErrorMessage,
			)
		},
	})
}

// newEngine returns a bare engine whose ClientIP comes from Fly's proxy header.
// Forwarding headers are never trusted, so a client cannot choose its own IP
// for the per-IP rate limits (email sign-in, anonymous help).
func newEngine() *gin.Engine {
	r := gin.New()
	r.TrustedPlatform = gin.PlatformFlyIO
	if err := r.SetTrustedProxies(nil); err != nil {
		panic(err)
	}
	return r
}

func setupRouter(eventHandler *EventHandler, authHandler *AuthHandler, profileHandler *ProfileHandler, chatHub *ChatHub, pushHandler *PushHandler, signer *tokenSigner) *gin.Engine {
	r := newEngine()
	r.Use(requestLogger(gin.DefaultWriter), gin.Recovery())
	collector := newAnalyticsCollector()

	r.Use(cors.New(cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
		MaxAge:        12 * time.Hour,
	}))
	r.Use(collector.middleware())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	authHandler.RegisterRoutes(api)
	eventHandler.RegisterRoutes(api)
	NewHelpHandler(eventHandler.repo, signer).RegisterRoutes(api)
	r.Static("/assets/covers", coverAssetsDir())
	coverHandler := newEventCoverHandler(eventHandler.repo)
	api.GET("/event-covers/:id", coverHandler.serve)

	placesHandler := NewPlacesHandler()
	api.GET("/places/autocomplete", placesHandler.autocomplete)
	api.GET("/places/details", placesHandler.details)
	api.GET("/location/approximate", NewApproximateLocationHandler().approximate)

	protected := api.Group("")
	protected.Use(sessionMiddleware(signer, eventHandler.repo))
	eventHandler.RegisterProtectedRoutes(protected)
	protected.POST("/event-covers", coverHandler.upload)
	protected.POST("/events/:id/report", eventHandler.reportEvent)
	protected.PUT("/profile", profileHandler.UpdateProfile)
	protected.DELETE("/profile", profileHandler.DeleteProfile)
	RegisterChatRoutes(protected, eventHandler.repo, chatHub)
	protected.POST("/push-tokens", pushHandler.registerPushToken)
	protected.DELETE("/push-tokens", pushHandler.deletePushToken)
	NewNotificationHandler(eventHandler.repo).RegisterRoutes(protected)
	protected.GET("/admin-access", NewAdminAccessHandler(eventHandler.repo).access)
	if os.Getenv("PUSH_ENABLED") == "true" {
		protected.POST("/push-tokens/test", pushHandler.testPush)
	}

	admin := api.Group("/admin")
	admin.Use(sessionMiddleware(signer, eventHandler.repo), adminMiddleware(eventHandler.repo))
	NewAnalyticsHandler(eventHandler.repo, collector).RegisterRoutes(admin)
	NewAdminHelpHandler(eventHandler.repo).RegisterRoutes(admin)

	api.GET("/ws", chatHub.handleWebSocket)

	return r
}
