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

const completionsURL = "https://api.openai.com/v1/chat/completions"

// Message is the OpenAI chat message format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// StreamDelta is a single token or content fragment from a streaming response.
type StreamDelta struct {
	Content string
	Done    bool
}

// StreamCompletion calls the OpenAI chat completions API in streaming mode
// and sends content deltas to the returned channel. The channel is closed
// when the stream finishes or an error occurs.
func StreamCompletion(ctx context.Context, token string, model string, messages []Message) (<-chan StreamDelta, <-chan error) {
	deltaCh := make(chan StreamDelta, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(deltaCh)
		defer close(errCh)

		body, err := json.Marshal(request{
			Model:    model,
			Messages: messages,
			Stream:   true,
		})
		if err != nil {
			errCh <- fmt.Errorf("marshaling request: %w", err)
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", completionsURL, bytes.NewReader(body))
		if err != nil {
			errCh <- fmt.Errorf("creating request: %w", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

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

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				deltaCh <- StreamDelta{Done: true}
				return
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // skip malformed chunks
			}
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				deltaCh <- StreamDelta{Content: chunk.Choices[0].Delta.Content}
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("reading stream: %w", err)
		}
	}()

	return deltaCh, errCh
}
