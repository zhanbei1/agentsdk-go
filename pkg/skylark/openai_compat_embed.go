package skylark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type openAICompatEmbedClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func newOpenAICompatEmbedClient(baseURL, apiKey, model string) *openAICompatEmbedClient {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return &openAICompatEmbedClient{
		baseURL: base,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *openAICompatEmbedClient) CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error) {
	if c == nil || len(texts) == 0 {
		return nil, fmt.Errorf("skylark: empty embedding request")
	}
	body := map[string]any{
		"model": c.model,
		"input": texts,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("skylark: embedding HTTP %d: %s", resp.StatusCode, string(data))
	}
	var payload struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if len(payload.Data) != len(texts) {
		return nil, fmt.Errorf("skylark: embedding count mismatch")
	}
	out := make([][]float32, len(payload.Data))
	for i, d := range payload.Data {
		vec := make([]float32, len(d.Embedding))
		for j, v := range d.Embedding {
			vec[j] = float32(v)
		}
		out[i] = vec
	}
	return out, nil
}
