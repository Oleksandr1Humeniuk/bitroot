package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewClientValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		apiKey  string
		model   string
		wantErr bool
	}{
		{name: "valid", baseURL: "https://example.com/v1", apiKey: "k", model: "m", wantErr: false},
		{name: "missing base url", baseURL: "", apiKey: "k", model: "m", wantErr: true},
		{name: "missing api key", baseURL: "https://example.com/v1", apiKey: "", model: "m", wantErr: true},
		{name: "missing model", baseURL: "https://example.com/v1", apiKey: "k", model: "", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := NewClient(tt.baseURL, tt.apiKey, tt.model)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if client == nil {
				t.Fatal("expected client, got nil")
			}
		})
	}
}

func TestAnalyzeCodeWithContextDetailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		response string
		wantErr  bool
		errType  string
		wantText string
	}{
		{
			name:     "success",
			status:   http.StatusOK,
			response: `{"choices":[{"message":{"content":"{\"summary\":\"Go scanner summary.\",\"bugs\":[],\"suggestions\":[]}"}}]}`,
			wantText: "Go scanner summary.",
		},
		{
			name:     "auth error",
			status:   http.StatusUnauthorized,
			response: `{"error":"unauthorized"}`,
			wantErr:  true,
			errType:  "auth",
		},
		{
			name:     "api logic error status",
			status:   http.StatusBadRequest,
			response: `{"error":"bad request"}`,
			wantErr:  true,
			errType:  "logic",
		},
		{
			name:     "missing required field",
			status:   http.StatusOK,
			response: `{"choices":[{"message":{"content":"{\"summary\":\"x\",\"bugs\":[]}"}}]}`,
			wantErr:  true,
			errType:  "logic",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("expected POST, got %s", r.Method)
				}

				requestBody, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}

				if !strings.Contains(string(requestBody), "\"response_format\"") {
					t.Fatalf("expected response_format in request: %s", string(requestBody))
				}

				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client, err := NewClient(server.URL+"/v1", "test-key", "test-model")
			if err != nil {
				t.Fatalf("new client failed: %v", err)
			}

			result, err := client.AnalyzeCodeWithContextDetailed(context.Background(), "cmd/", "main.go", "package main")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				switch tt.errType {
				case "auth":
					var authErr AuthError
					if !strings.Contains(err.Error(), "auth error") && !strings.Contains(err.Error(), "unauthorized") {
						t.Fatalf("expected auth error, got %v", err)
					}
					_ = authErr
				case "logic":
					var logicErr APILogicError
					if !strings.Contains(err.Error(), "api error") && !strings.Contains(err.Error(), "missing required field") {
						t.Fatalf("expected logic error, got %v", err)
					}
					_ = logicErr
				}

				if result.Attempts != 1 {
					t.Fatalf("expected attempts=1 on failure, got %d", result.Attempts)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.Summary != tt.wantText {
				t.Fatalf("unexpected summary: got %q want %q", result.Summary, tt.wantText)
			}

			if result.Attempts != 1 {
				t.Fatalf("expected attempts=1, got %d", result.Attempts)
			}
		})
	}
}

func TestPing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"pong\",\"bugs\":[],\"suggestions\":[]}"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/v1", "test-key", "test-model")
	if err != nil {
		t.Fatalf("new client failed: %v", err)
	}

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

func TestAnalyzeCodeRetriesOnRateLimit(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := calls.Add(1)
		if count < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\",\"bugs\":[],\"suggestions\":[]}"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL+"/v1", "test-key", "test-model")
	if err != nil {
		t.Fatalf("new client failed: %v", err)
	}

	result, err := client.AnalyzeCodeWithContextDetailed(context.Background(), "", "main.go", "package main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", result.Attempts)
	}
}

func TestEmbedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		provider  string
		model     string
		handler   http.HandlerFunc
		checkBody bool
		wantErr   bool
		wantType  string
		wantLen   int
		wantCalls int32
	}{
		{
			name:      "openai provider success",
			provider:  EmbeddingProviderOpenAI,
			model:     "text-embedding-3-small",
			checkBody: true,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/embeddings" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
					t.Fatalf("missing auth header: %s", auth)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
			},
			wantLen:   3,
			wantCalls: 1,
		},
		{
			name:      "ollama provider success",
			provider:  EmbeddingProviderOllama,
			model:     "nomic-embed-text",
			checkBody: true,
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/embeddings" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				if auth := r.Header.Get("Authorization"); auth != "" {
					t.Fatalf("expected no auth header, got %s", auth)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"embedding":[1,2,3,4]}`))
			},
			wantLen:   4,
			wantCalls: 1,
		},
		{
			name:     "rate limit retries",
			provider: EmbeddingProviderOpenAI,
			model:    "text-embedding-3-small",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			},
			wantErr:   true,
			wantType:  "logic",
			wantCalls: 4,
		},
		{
			name:     "unsupported provider",
			provider: "custom",
			model:    "x",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			},
			wantErr:   true,
			wantType:  "transport",
			wantCalls: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)

				if tt.checkBody {
					rawBody, readErr := io.ReadAll(r.Body)
					if readErr != nil {
						t.Fatalf("read body failed: %v", readErr)
					}

					var payload map[string]any
					if unmarshalErr := json.Unmarshal(rawBody, &payload); unmarshalErr != nil {
						t.Fatalf("invalid json body: %v", unmarshalErr)
					}

					modelValue, ok := payload["model"]
					if !ok {
						t.Fatalf("missing model field in payload: %s", string(rawBody))
					}

					modelString, ok := modelValue.(string)
					if !ok {
						t.Fatalf("model must be string, got %T", modelValue)
					}

					if modelString != tt.model {
						t.Fatalf("model mismatch: got %q want %q", modelString, tt.model)
					}
				}

				tt.handler(w, r)
			}))
			defer server.Close()

			client, err := NewClient(server.URL+"/v1", "test-key", "test-model")
			if err != nil {
				t.Fatalf("new client failed: %v", err)
			}
			client.ConfigureEmbeddings(tt.provider, tt.model)

			embedding, err := client.EmbedText(context.Background(), "func main() {}")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				switch tt.wantType {
				case "logic":
					var logicErr APILogicError
					if !strings.Contains(err.Error(), "api error") {
						t.Fatalf("expected logic error, got: %v", err)
					}
					_ = logicErr
				case "transport":
					var transportErr TransportError
					if !strings.Contains(err.Error(), "unsupported embedding provider") {
						t.Fatalf("expected transport error, got: %v", err)
					}
					_ = transportErr
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(embedding) != tt.wantLen {
					t.Fatalf("embedding length mismatch: got %d want %d", len(embedding), tt.wantLen)
				}
			}

			if gotCalls := calls.Load(); gotCalls != tt.wantCalls {
				t.Fatalf("request call count mismatch: got %d want %d", gotCalls, tt.wantCalls)
			}
		})
	}
}
