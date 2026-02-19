package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"pi-agent/internal/chat"
	"pi-agent/internal/store"
	"pi-agent/internal/token"
)

// Config holds server configuration.
type Config struct {
	Addr           string // listen address, e.g. ":8080"
	Model          string // OpenAI model, e.g. "gpt-4o"
	SystemPrompt   string // optional system prompt
	ConversationID string // default conversation ID
}

// Server is the HTTP server for the pi-agent.
type Server struct {
	cfg Config
	ts  *token.Store
	db  *store.DB
	mux *http.ServeMux
}

// New creates a new Server.
func New(cfg Config, ts *token.Store, db *store.DB) *Server {
	s := &Server{
		cfg: cfg,
		ts:  ts,
		db:  db,
		mux: http.NewServeMux(),
	}
	s.mux.HandleFunc("POST /chat", s.handleChat)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("/", s.handleNotFound)
	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	log.Printf("listening on %s", s.cfg.Addr)
	return http.ListenAndServe(s.cfg.Addr, s.mux)
}

// ChatRequest is the JSON body for POST /chat.
type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}

	convID := req.ConversationID
	if convID == "" {
		convID = s.cfg.ConversationID
	}

	// Get a valid access token (auto-refreshes if expired).
	accessToken, err := s.ts.AccessToken(r.Context())
	if err != nil {
		log.Printf("token error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"authentication error: %s"}`, err), http.StatusUnauthorized)
		return
	}

	// Store the user message.
	if err := s.db.AddMessage(convID, store.RoleUser, req.Message); err != nil {
		log.Printf("db error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Build the messages list from conversation history.
	history, err := s.db.Messages(convID)
	if err != nil {
		log.Printf("db error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	var messages []chat.Message
	for _, m := range history {
		messages = append(messages, chat.Message{Role: string(m.Role), Content: m.Content})
	}

	// Stream the response back as SSE.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	accountID := s.ts.AccountID()
	deltaCh, errCh := chat.StreamCompletion(ctx, accessToken, accountID, s.cfg.Model, s.cfg.SystemPrompt, messages)

	var fullResponse strings.Builder
	for delta := range deltaCh {
		if delta.Done {
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			break
		}
		fullResponse.WriteString(delta.Content)

		chunk, _ := json.Marshal(map[string]string{"content": delta.Content})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
	}

	// Check for stream errors.
	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("stream error: %v", err)
			fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error())
			flusher.Flush()
			return
		}
	default:
	}

	// Store the assistant response.
	if resp := fullResponse.String(); resp != "" {
		if err := s.db.AddMessage(convID, store.RoleAssistant, resp); err != nil {
			log.Printf("db error saving response: %v", err)
		}
	}
}
