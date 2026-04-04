package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAnalyzeCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		response   string
		wantErr    bool
		wantText   string
	}{
		{
			name:       "success",
			statusCode: http.StatusOK,
			response:   `{"choices":[{"message":{"content":"Go code scans files recursively."}}]}`,
			wantErr:    false,
			wantText:   "Go code scans files recursively.",
		},
		{
			name:       "api error",
			statusCode: http.StatusBadRequest,
			response:   `{"error":"bad request"}`,
			wantErr:    true,
		},
		{
			name:       "empty choices",
			statusCode: http.StatusOK,
			response:   `{"choices":[]}`,
			wantErr:    true,
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

				if r.URL.Path != "/v1/chat/completions" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}

				if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
					t.Fatalf("unexpected authorization header: %s", auth)
				}

				requestBody, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}

				if !strings.Contains(string(requestBody), "Summarize this code in one short, professional sentence.") {
					t.Fatalf("prompt not found in request: %s", string(requestBody))
				}

				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client, err := NewClient(server.URL+"/v1", "test-key", "test-model")
			if err != nil {
				t.Fatalf("new client failed: %v", err)
			}

			summary, err := client.AnalyzeCode(context.Background(), "package main")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if summary != tt.wantText {
				t.Fatalf("unexpected summary: got %q want %q", summary, tt.wantText)
			}
		})
	}
}
