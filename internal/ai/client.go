package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const summaryPrompt = "Summarize this code in one short, professional sentence."
const projectContextPrompt = "You are analyzing code in the context of this project tree:\n%s"

type Client struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
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
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}, nil
}

func (c *Client) AnalyzeCode(ctx context.Context, code string) (string, error) {
	return c.analyzeCode(ctx, "", "", code)
}

func (c *Client) AnalyzeCodeWithContext(ctx context.Context, projectTree, filePath, code string) (string, error) {
	return c.analyzeCode(ctx, projectTree, filePath, code)
}

func (c *Client) analyzeCode(ctx context.Context, projectTree, filePath, code string) (string, error) {
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
		Model:    c.model,
		Messages: messages,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(responseBody, &completion); err != nil {
		return "", err
	}

	if len(completion.Choices) == 0 {
		return "", errors.New("empty choices in response")
	}

	summary := strings.TrimSpace(completion.Choices[0].Message.Content)
	if summary == "" {
		return "", errors.New("empty summary in response")
	}

	return summary, nil
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
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
