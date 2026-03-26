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
		model = "qwen2:7b"
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

// Ping checks if Ollama is reachable.
func (c *Client) Ping() error {
	resp, err := c.HTTP.Get(c.BaseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("ollama unreachable at %s: %w", c.BaseURL, err)
	}
	resp.Body.Close()
	return nil
}
