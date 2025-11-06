package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type HTTP struct{}

type Request struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
	Timeout int               `json:"timeout"` // seconds
}

type Response struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
	Error      string            `json:"error,omitempty"`
}

func (h HTTP) Handle(ctx context.Context, payload json.RawMessage) error {
	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("invalid HTTP request payload: %w", err)
	}

	if req.URL == "" {
		return fmt.Errorf("URL is required")
	}

	if req.Method == "" {
		req.Method = "GET"
	}

	if req.Timeout <= 0 {
		req.Timeout = 30 // default 30 seconds
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: time.Duration(req.Timeout) * time.Second,
	}

	// Create request
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Make request
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTTP errors (4xx, 5xx)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d error: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
