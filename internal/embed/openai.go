package embed

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

// OpenAIEmbedder calls any OpenAI-compatible /embeddings endpoint. The same
// implementation works against several backends just by changing BaseURL and
// Model:
//
//   - OpenAI:  BaseURL=https://api.openai.com/v1            Model=text-embedding-3-small
//   - Voyage:  BaseURL=https://api.voyageai.com/v1          Model=voyage-3
//   - Ollama:  BaseURL=http://localhost:11434/v1            Model=nomic-embed-text   (local, free)
//   - TEI:     BaseURL=http://localhost:8080/v1             Model=<served model>     (local, free)
type OpenAIEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	client  *http.Client
}

// NewOpenAI builds an OpenAI-compatible embedder. dim must match the model's
// real output dimension; it is used to size the Qdrant collection.
func NewOpenAI(baseURL, apiKey, model string, dim int) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		dim:     dim,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *OpenAIEmbedder) Dim() int     { return e.dim }
func (e *OpenAIEmbedder) Name() string { return e.model }

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embeddingsRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, err
	}
	resp, err := httpx.Do(ctx, e.client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if e.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.apiKey)
		}
		return req, nil
	}, httpx.DefaultRetries)
	if err != nil {
		return nil, fmt.Errorf("embeddings request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var parsed embeddingsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decoding embeddings response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("embeddings error: %s", parsed.Error.Message)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(parsed.Data))
	}

	// The API does not guarantee order; place each vector at its index.
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("missing embedding for input %d", i)
		}
	}
	return out, nil
}
