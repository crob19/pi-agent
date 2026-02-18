package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"pi-agent/internal/oauth"
	"pi-agent/internal/server"
	"pi-agent/internal/store"
	"pi-agent/internal/token"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	model := flag.String("model", "gpt-4o", "OpenAI model to use")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for persistent data (tokens, database)")
	systemPrompt := flag.String("system-prompt", "You are a helpful assistant running on a Raspberry Pi.", "system prompt for conversations")
	conversationID := flag.String("conversation", "default", "default conversation ID")
	flag.Parse()

	tokenPath := filepath.Join(*dataDir, "token.json")
	dbPath := filepath.Join(*dataDir, "conversations.db")

	// Initialize token store.
	ts, err := token.NewStore(tokenPath)
	if err != nil {
		log.Fatalf("initializing token store: %v", err)
	}

	// If no credentials on disk, run the OAuth flow.
	if !ts.HasCredentials() {
		fmt.Println("No saved credentials found. Starting authentication...")
		cred, err := oauth.Authenticate(context.Background())
		if err != nil {
			log.Fatalf("authentication failed: %v", err)
		}
		if err := ts.Save(cred); err != nil {
			log.Fatalf("saving credentials: %v", err)
		}
		fmt.Println("Authentication successful!")
	}

	// Open SQLite database.
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	// Start the HTTP server.
	srv := server.New(server.Config{
		Addr:           *addr,
		Model:          *model,
		SystemPrompt:   *systemPrompt,
		ConversationID: *conversationID,
	}, ts, db)

	log.Fatal(srv.ListenAndServe())
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pi-agent"
	}
	return filepath.Join(home, ".pi-agent")
}
