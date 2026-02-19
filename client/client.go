package client

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

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type ChatOptions struct {
	Message        string
	ConversationID string
}

type ChatDelta struct {
	Content string
}

type HealthStatus struct {
	Status string `json:"status"`
}

func New(baseURL string) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return &Client{baseURL: trimmed, httpClient: http.DefaultClient}
}

func (c *Client) Health(ctx context.Context) (*HealthStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("creating health request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("health request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("health check failed %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var status HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decoding health response: %w", err)
	}
	return &status, nil
}

func (c *Client) ChatStream(ctx context.Context, options ChatOptions) (<-chan ChatDelta, <-chan error) {
	deltaCh := make(chan ChatDelta, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(deltaCh)
		defer close(errCh)

		if strings.TrimSpace(options.Message) == "" {
			errCh <- fmt.Errorf("message is required")
			return
		}

		bodyMap := map[string]string{"message": options.Message}
		if strings.TrimSpace(options.ConversationID) != "" {
			bodyMap["conversation_id"] = options.ConversationID
		}

		body, err := json.Marshal(bodyMap)
		if err != nil {
			errCh <- fmt.Errorf("marshaling chat request: %w", err)
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat", bytes.NewReader(body))
		if err != nil {
			errCh <- fmt.Errorf("creating chat request: %w", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			errCh <- fmt.Errorf("chat request: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			errCh <- fmt.Errorf("chat request failed %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
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
				return
			}

			var chunk struct {
				Content string `json:"content"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if chunk.Error != "" {
				errCh <- fmt.Errorf("%s", chunk.Error)
				return
			}
			if chunk.Content != "" {
				deltaCh <- ChatDelta{Content: chunk.Content}
			}
		}

		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("reading stream: %w", err)
		}
	}()

	return deltaCh, errCh
}

func (c *Client) ChatText(ctx context.Context, options ChatOptions) (string, error) {
	deltaCh, errCh := c.ChatStream(ctx, options)

	var full strings.Builder
	for delta := range deltaCh {
		full.WriteString(delta.Content)
	}

	select {
	case err := <-errCh:
		if err != nil {
			return "", err
		}
	default:
	}

	return full.String(), nil
}
