package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const summaryPrompt = "Summarize this code in one short, professional sentence. Return strict JSON with keys: summary (string), bugs (array), suggestions (array)."
const projectContextPrompt = "You are analyzing code in the context of this project tree:\n%s"

const (
	EmbeddingProviderOpenAI = "openai"
	EmbeddingProviderOllama = "ollama"
)

const (
	maxRetryAttempts  = 4
	initialRetryDelay = 500 * time.Millisecond
)

type AnalyzeResult struct {
	Summary  string
	Attempts int
}

type AuthError struct {
	StatusCode int
	Message    string
}

func (e AuthError) Error() string {
	return fmt.Sprintf("auth error (%d): %s", e.StatusCode, e.Message)
}

type TransportError struct {
	Message string
}

func (e TransportError) Error() string {
	return e.Message
}

type APILogicError struct {
	StatusCode int
	Message    string
}

func (e APILogicError) Error() string {
	if e.StatusCode == 0 {
		return e.Message
	}

	return fmt.Sprintf("api error (%d): %s", e.StatusCode, e.Message)
}

type Client struct {
	baseURL           string
	apiKey            string
	model             string
	embeddingModel    string
	embeddingProvider string
	httpClient        *http.Client
}

func NewClient(baseURL, apiKey, model string) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("AI_BASE_URL is required")
	}

	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("AI_API_KEY is required")
	}

	if strings.TrimSpace(model) == "" {
		return nil, errors.New("AI_MODEL is required")
	}

	return &Client{
		baseURL:           strings.TrimRight(baseURL, "/"),
		apiKey:            apiKey,
		model:             model,
		embeddingModel:    model,
		embeddingProvider: EmbeddingProviderOpenAI,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}, nil
}

func (c *Client) ConfigureEmbeddings(provider, model string) {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" {
		provider = EmbeddingProviderOpenAI
	}

	model = strings.TrimSpace(model)
	if model == "" {
		model = c.model
	}

	c.embeddingProvider = provider
	c.embeddingModel = model
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.AnalyzeCodeWithContextDetailed(ctx, "", "ping.go", "package main\n\nfunc main() {}")
	return err
}

func (c *Client) AnalyzeCode(ctx context.Context, code string) (string, error) {
	result, err := c.analyzeCode(ctx, "", "", code)
	if err != nil {
		return "", err
	}

	return result.Summary, nil
}

func (c *Client) AnalyzeCodeWithContext(ctx context.Context, projectTree, filePath, code string) (string, error) {
	result, err := c.analyzeCode(ctx, projectTree, filePath, code)
	if err != nil {
		return "", err
	}

	return result.Summary, nil
}

func (c *Client) AnalyzeCodeWithContextDetailed(ctx context.Context, projectTree, filePath, code string) (AnalyzeResult, error) {
	return c.analyzeCode(ctx, projectTree, filePath, code)
}

func (c *Client) EmbedText(ctx context.Context, input string) ([]float64, error) {
	if strings.TrimSpace(input) == "" {
		return nil, TransportError{Message: "embedding input is required"}
	}

	attempts := 0
	delay := initialRetryDelay

	for {
		attempts++

		embedding, err := c.sendEmbeddingRequest(ctx, input)
		if err == nil {
			return embedding, nil
		}

		var apiErr APILogicError
		if !errors.As(err, &apiErr) || !shouldRetry(apiErr.StatusCode) || attempts >= maxRetryAttempts {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, TransportError{Message: ctx.Err().Error()}
		case <-time.After(delay):
			delay *= 2
		}
	}
}

func (c *Client) analyzeCode(ctx context.Context, projectTree, filePath, code string) (AnalyzeResult, error) {
	messages := make([]chatMessage, 0, 2)
	if strings.TrimSpace(projectTree) != "" {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: fmt.Sprintf(projectContextPrompt, projectTree),
		})
	}

	userContent := summaryPrompt + "\n\n"
	if strings.TrimSpace(filePath) != "" {
		userContent += "File: " + filePath + "\n\n"
	}
	userContent += code

	messages = append(messages, chatMessage{Role: "user", Content: userContent})

	requestBody := chatCompletionRequest{
		Model:          c.model,
		Messages:       messages,
		ResponseFormat: summaryResponseFormat(),
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return AnalyzeResult{Attempts: 1}, TransportError{Message: err.Error()}
	}

	attempts := 0
	delay := initialRetryDelay

	for {
		attempts++

		completion, err := c.sendChatRequest(ctx, body)
		if err == nil {
			summary, summaryErr := extractSummary(completion)
			if summaryErr != nil {
				return AnalyzeResult{Attempts: attempts}, summaryErr
			}

			return AnalyzeResult{Summary: summary, Attempts: attempts}, nil
		}

		var apiErr APILogicError
		if !errors.As(err, &apiErr) || !shouldRetry(apiErr.StatusCode) || attempts >= maxRetryAttempts {
			return AnalyzeResult{Attempts: attempts}, err
		}

		select {
		case <-ctx.Done():
			return AnalyzeResult{Attempts: attempts}, TransportError{Message: ctx.Err().Error()}
		case <-time.After(delay):
			delay *= 2
		}
	}
}

type chatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type responseFormat struct {
	Type       string          `json:"type"`
	JSONSchema jsonSchemaBlock `json:"json_schema"`
}

type jsonSchemaBlock struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

func (c *Client) sendChatRequest(ctx context.Context, body []byte) (chatCompletionResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return chatCompletionResponse{}, TransportError{Message: err.Error()}
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return chatCompletionResponse{}, TransportError{Message: err.Error()}
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatCompletionResponse{}, TransportError{Message: err.Error()}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return chatCompletionResponse{}, AuthError{StatusCode: resp.StatusCode, Message: message}
		}

		return chatCompletionResponse{}, APILogicError{StatusCode: resp.StatusCode, Message: message}
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(responseBody, &completion); err != nil {
		return chatCompletionResponse{}, APILogicError{Message: err.Error()}
	}

	return completion, nil
}

type openAIEmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

type ollamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

func (c *Client) sendEmbeddingRequest(ctx context.Context, input string) ([]float64, error) {
	provider := strings.TrimSpace(strings.ToLower(c.embeddingProvider))
	if provider == "" {
		provider = EmbeddingProviderOpenAI
	}

	endpoint := c.baseURL + "/embeddings"
	var body []byte
	var err error

	switch provider {
	case EmbeddingProviderOpenAI:
		body, err = json.Marshal(openAIEmbeddingRequest{Model: c.embeddingModel, Input: input})
	case EmbeddingProviderOllama:
		baseURL := c.baseURL
		if strings.HasSuffix(baseURL, "/v1") {
			baseURL = strings.TrimSuffix(baseURL, "/v1")
		}
		endpoint = baseURL + "/api/embeddings"
		body, err = json.Marshal(ollamaEmbeddingRequest{Model: c.embeddingModel, Prompt: input})
	default:
		return nil, TransportError{Message: "unsupported embedding provider: " + provider}
	}
	if err != nil {
		return nil, TransportError{Message: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, TransportError{Message: err.Error()}
	}

	req.Header.Set("Content-Type", "application/json")
	if provider == EmbeddingProviderOpenAI {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, TransportError{Message: err.Error()}
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, TransportError{Message: err.Error()}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(responseBody))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, AuthError{StatusCode: resp.StatusCode, Message: message}
		}

		return nil, APILogicError{StatusCode: resp.StatusCode, Message: message}
	}

	if provider == EmbeddingProviderOpenAI {
		var openAIResp openAIEmbeddingResponse
		if err := json.Unmarshal(responseBody, &openAIResp); err != nil {
			return nil, APILogicError{Message: err.Error()}
		}

		if len(openAIResp.Data) == 0 || len(openAIResp.Data[0].Embedding) == 0 {
			return nil, APILogicError{Message: "embedding response is empty"}
		}

		return openAIResp.Data[0].Embedding, nil
	}

	var ollamaResp ollamaEmbeddingResponse
	if err := json.Unmarshal(responseBody, &ollamaResp); err != nil {
		return nil, APILogicError{Message: err.Error()}
	}

	if len(ollamaResp.Embedding) == 0 {
		return nil, APILogicError{Message: "embedding response is empty"}
	}

	return ollamaResp.Embedding, nil
}

func extractSummary(completion chatCompletionResponse) (string, error) {
	if len(completion.Choices) == 0 {
		err := APILogicError{Message: "empty choices in response"}
		slog.Error("ai response validation failed", "error", err)
		return "", err
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		err := APILogicError{Message: "empty content in response"}
		slog.Error("ai response validation failed", "error", err)
		return "", err
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		logicErr := APILogicError{Message: "response content is not valid JSON"}
		slog.Error("ai response validation failed", "error", logicErr)
		return "", logicErr
	}

	if err := validateRequiredFields(payload, []string{"summary", "bugs", "suggestions"}); err != nil {
		slog.Error("ai response validation failed", "error", err)
		return "", err
	}

	summary, ok := payload["summary"].(string)
	if !ok || strings.TrimSpace(summary) == "" {
		err := APILogicError{Message: "summary must be a non-empty string"}
		slog.Error("ai response validation failed", "error", err)
		return "", err
	}

	return strings.TrimSpace(summary), nil
}

func validateRequiredFields(payload map[string]any, required []string) error {
	for _, field := range required {
		if _, ok := payload[field]; !ok {
			return APILogicError{Message: "missing required field: " + field}
		}
	}

	return nil
}

func summaryResponseFormat() *responseFormat {
	return &responseFormat{
		Type: "json_schema",
		JSONSchema: jsonSchemaBlock{
			Name:   "summary_response",
			Strict: true,
			Schema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"summary": map[string]any{"type": "string"},
					"bugs": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"suggestions": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required": []string{"summary", "bugs", "suggestions"},
			},
		},
	}
}

func shouldRetry(statusCode int) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}

	return statusCode >= 500 && statusCode <= 599
}
