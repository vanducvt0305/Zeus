package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vanducvt0305/zeus/internal/httpx"
)

// Anthropic calls the Claude Messages API. A fast, cheap model (e.g.
// claude-haiku-4-5) is a good default for enrichment, which is a high-volume
// batch job.
type Anthropic struct {
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	client    *http.Client
}

// NewAnthropic builds a Claude Messages client. An empty baseURL defaults to
// the public API.
func NewAnthropic(baseURL, apiKey, model string, maxTokens int) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	return &Anthropic{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (a *Anthropic) Name() string { return a.model }

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Anthropic) Complete(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(anthropicRequest{
		Model:     a.model,
		MaxTokens: a.maxTokens,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", err
	}
	resp, err := httpx.Do(ctx, a.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		return req, nil
	}, httpx.DefaultRetries)
	if err != nil {
		return "", fmt.Errorf("messages request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decoding anthropic response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("anthropic error: %s", parsed.Error.Message)
	}
	var sb strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}
