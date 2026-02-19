package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ChatGPT backend endpoint for OAuth-authenticated requests.
// OAuth tokens from ChatGPT subscriptions are scoped to this backend,
// not the standard api.openai.com which requires a separate API key.
const responsesURL = "https://chatgpt.com/backend-api/codex/responses"

// Message is the OpenAI chat message format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamDelta is a single token or content fragment from a streaming response.
type StreamDelta struct {
	Content string
	Done    bool
}

// responsesRequest is the request body for the Responses API.
type responsesRequest struct {
	Model        string    `json:"model"`
	Store        bool      `json:"store"`
	Instructions string    `json:"instructions"`
	Input        []Message `json:"input"`
	Stream       bool      `json:"stream"`
}

// StreamCompletion calls the ChatGPT backend Responses API in streaming mode
// and sends content deltas to the returned channel. The channel is closed
// when the stream finishes or an error occurs.
//
// The accountID is the ChatGPT account ID extracted from the OAuth JWT,
// required for the ChatGPT-Account-Id header.
func StreamCompletion(ctx context.Context, token, accountID, model, instructions string, messages []Message) (<-chan StreamDelta, <-chan error) {
	deltaCh := make(chan StreamDelta, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(deltaCh)
		defer close(errCh)

		if strings.TrimSpace(instructions) == "" {
			instructions = "You are a helpful assistant."
		}

		body, err := json.Marshal(responsesRequest{
			Model:        model,
			Store:        false,
			Instructions: instructions,
			Input:        messages,
			Stream:       true,
		})
		if err != nil {
			errCh <- fmt.Errorf("marshaling request: %w", err)
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", responsesURL, bytes.NewReader(body))
		if err != nil {
			errCh <- fmt.Errorf("creating request: %w", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- fmt.Errorf("API request: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			errCh <- fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
			return
		}

		// The Responses API uses SSE with typed events:
		//   event: response.output_text.delta
		//   data: {"type":"response.output_text.delta","delta":"..."}
		//
		//   event: response.completed
		//   data: {"type":"response.completed","response":{...}}
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var event struct {
				Type     string `json:"type"`
				Delta    string `json:"delta"`
				Response *struct {
					Output []struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"output"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue // skip malformed chunks
			}

			switch event.Type {
			case "response.output_text.delta":
				if event.Delta != "" {
					deltaCh <- StreamDelta{Content: event.Delta}
				}
			case "response.completed":
				deltaCh <- StreamDelta{Done: true}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("reading stream: %w", err)
		}
	}()

	return deltaCh, errCh
}
