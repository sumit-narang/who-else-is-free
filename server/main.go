package main

import (
	"context"
	"log"
	"os"
	"time"
)

// Use /data for Fly.io volume mount, fallback to local for development
func getDatabasePath() string {
	if _, err := os.Stat("/data"); err == nil {
		return "/data/event.sqlite"
	}
	return "event.sqlite"
}

var databasePath = getDatabasePath()

func main() {
	database, err := openDB(databasePath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("error closing database: %v", err)
		}
	}()

	// Load optional server/.env so local dev can configure secrets easily.
	loadServerEnv()

	repo := NewEventRepository(database)

	signer, err := newTokenSignerFromEnv()
	if err != nil {
		log.Fatalf("failed to load session signer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := repo.Init(ctx); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	if err := repo.EnsureSeedData(ctx); err != nil {
		log.Printf("failed to seed database: %v", err)
	}

	pushSender := InitPushSender(ctx)
	chatHub := NewChatHub(repo, signer, pushSender)

	eventHandler := NewEventHandler(repo, chatHub)
	authHandler := NewAuthHandler(repo, signer)
	profileHandler := NewProfileHandler(repo, chatHub)
	pushHandler := NewPushHandler(repo, pushSender)
	go chatHub.Run()
	srv := setupRouter(eventHandler, authHandler, profileHandler, chatHub, pushHandler, signer)

	if err := srv.Run(); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
