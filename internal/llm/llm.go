package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatClient interface {
	Chat(ctx context.Context, messages []ChatMessage) (string, error)
}

type EmbedClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type Config struct {
	Chat      ModelConfig
	Embedding ModelConfig
}

type ModelConfig struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
}

type openAICompat struct {
	cfg    ModelConfig
	client *http.Client
	logger *slog.Logger
}

func NewChatClient(cfg ModelConfig, logger *slog.Logger) ChatClient {
	return &openAICompat{
		cfg:    cfg,
		client: &http.Client{Timeout: 120 * time.Second},
		logger: logger,
	}
}

func NewEmbedClient(cfg ModelConfig, logger *slog.Logger) EmbedClient {
	return &openAICompat{
		cfg:    cfg,
		client: &http.Client{Timeout: 120 * time.Second},
		logger: logger,
	}
}

func (c *openAICompat) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.cfg.BaseURL + path
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http call %s: %w", url, err)
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode >= 400 {
			retryable := resp.StatusCode == 429 || resp.StatusCode >= 500
			lastErr = fmt.Errorf("llm api %s returned %d: %s", path, resp.StatusCode, truncate(string(data), 300))
			if retryable {
				continue
			}
			return lastErr
		}

		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		return nil
	}
	return lastErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *openAICompat) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	payload := map[string]any{
		"model":    c.cfg.Model,
		"messages": messages,
	}

	var out chatResponse
	if err := c.post(ctx, "/chat/completions", payload, &out); err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("chat: empty response")
	}
	return out.Choices[0].Message.Content, nil
}

type embedResponse struct {
	Data []struct {
		Index int             `json:"index"`
		Embedding []float32   `json:"embedding"`
	} `json:"data"`
}

func (c *openAICompat) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	payload := map[string]any{
		"model": c.cfg.Model,
		"input": texts,
	}

	var out embedResponse
	if err := c.post(ctx, "/embeddings", payload, &out); err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embed: got %d vectors for %d inputs", len(out.Data), len(texts))
	}

	vectors := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return nil, fmt.Errorf("embed: invalid index %d", d.Index)
		}
		vectors[d.Index] = d.Embedding
	}
	return vectors, nil
}
