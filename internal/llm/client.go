package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Client struct {
	BaseURL string
	Model   string
	HTTP    *http.Client
}

type GenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
	Stream bool   `json:"stream"`
	Options *Options `json:"options,omitempty"`
}

type Options struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type GenerateResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
	TotalDuration   int64 `json:"total_duration"`
	PromptEvalCount int   `json:"prompt_eval_count"`
	EvalCount       int   `json:"eval_count"`
}

func NewClient() *Client {
	baseURL := os.Getenv("AIPROD_OLLAMA_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := os.Getenv("AIPROD_OLLAMA_MODEL")
	if model == "" {
		model = "gemma4"
	}
	return &Client{
		BaseURL: baseURL,
		Model:   model,
		HTTP: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Generate sends a request to Ollama using the client's default model.
func (c *Client) Generate(system, prompt string, temperature float64, maxTokens int) (*GenerateResponse, error) {
	return c.GenerateWith(c.Model, system, prompt, temperature, maxTokens)
}

// GenerateWith sends a request using a specific model (for per-feature overrides).
func (c *Client) GenerateWith(model, system, prompt string, temperature float64, maxTokens int) (*GenerateResponse, error) {
	if model == "" { model = c.Model }
	req := GenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: system,
		Stream: false,
		Options: &Options{
			Temperature: temperature,
			NumPredict:  maxTokens,
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	resp, err := c.HTTP.Post(c.BaseURL+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(b))
	}

	var result GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// --- Embeddings ---

type EmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type EmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// EmbeddingModel returns the model used for embeddings.
// Defaults to nomic-embed-text which is small, fast, and good quality.
func (c *Client) EmbeddingModel() string {
	model := os.Getenv("AIPROD_EMBEDDING_MODEL")
	if model != "" {
		return model
	}
	return "nomic-embed-text"
}

// Embed generates an embedding vector for the given text using a local model.
func (c *Client) Embed(text string) ([]float64, error) {
	return c.EmbedWith(c.EmbeddingModel(), text)
}

// EmbedWith generates an embedding using a specific model.
func (c *Client) EmbedWith(model, text string) ([]float64, error) {
	req := EmbedRequest{Model: model, Input: text}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling embed request: %w", err)
	}

	resp, err := c.HTTP.Post(c.BaseURL+"/api/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed returned %d: %s", resp.StatusCode, string(b))
	}

	var result EmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding embed response: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return result.Embeddings[0], nil
}

// CosineSimilarity computes the cosine similarity between two vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (sqrt(normA) * sqrt(normB))
}

func sqrt(x float64) float64 {
	// Newton's method — avoids importing math for one function
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}

// Ping checks if Ollama is reachable.
func (c *Client) Ping() error {
	resp, err := c.HTTP.Get(c.BaseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("ollama unreachable at %s: %w", c.BaseURL, err)
	}
	resp.Body.Close()
	return nil
}

// EnsureModel pulls a model if it's not already available locally.
func (c *Client) EnsureModel(model string) error {
	// Check if model exists
	resp, err := c.HTTP.Post(c.BaseURL+"/api/show", "application/json",
		bytes.NewReader([]byte(fmt.Sprintf(`{"model":"%s"}`, model))))
	if err != nil {
		return fmt.Errorf("checking model %s: %w", model, err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil // already available
	}

	// Pull the model
	fmt.Printf("[llm] Pulling model %s (this may take a few minutes on first run)...\n", model)
	pullReq := fmt.Sprintf(`{"model":"%s","stream":false}`, model)
	pullResp, err := c.HTTP.Post(c.BaseURL+"/api/pull", "application/json",
		bytes.NewReader([]byte(pullReq)))
	if err != nil {
		return fmt.Errorf("pulling model %s: %w", model, err)
	}
	defer pullResp.Body.Close()
	io.ReadAll(pullResp.Body) // consume response

	if pullResp.StatusCode != http.StatusOK {
		return fmt.Errorf("pull %s returned status %d", model, pullResp.StatusCode)
	}
	fmt.Printf("[llm] Model %s ready\n", model)
	return nil
}
