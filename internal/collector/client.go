package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultRequestTimeout = 3 * time.Second
	maxJSONResponseBytes  = 1 << 20
	DefaultProfileLimit   = 2 << 20
)

type HTTPError struct {
	Endpoint   string
	StatusCode int
	Message    string
	Cause      error
}

func (e *HTTPError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s returned HTTP %d: %s", e.Endpoint, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("request %s failed: %v", e.Endpoint, e.Cause)
}

func (e *HTTPError) Unwrap() error {
	return e.Cause
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse demo service URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("demo service URL must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("demo service URL must contain only scheme, host, and optional base path")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultRequestTimeout}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}, nil
}

func (c *Client) Health(ctx context.Context) (Health, RequestMeta, error) {
	var result Health
	meta, err := c.getJSON(ctx, "/healthz", &result)
	return result, meta, err
}

func (c *Client) Runtime(ctx context.Context) (RuntimeSample, RequestMeta, error) {
	var result RuntimeSample
	meta, err := c.getJSON(ctx, "/api/runtime", &result)
	return result, meta, err
}

func (c *Client) LeakStatus(ctx context.Context) (LeakStatus, RequestMeta, error) {
	var result LeakStatus
	meta, err := c.getJSON(ctx, "/api/leak/status", &result)
	return result, meta, err
}

func (c *Client) GoroutineProfile(ctx context.Context, limitBytes int) (ProfilePayload, error) {
	if limitBytes < 1 {
		limitBytes = DefaultProfileLimit
	}
	endpoint := c.baseURL + "/debug/pprof/goroutine?debug=2"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ProfilePayload{}, fmt.Errorf("create profile request: %w", err)
	}

	started := time.Now()
	response, err := c.httpClient.Do(request)
	latency := time.Since(started)
	if err != nil {
		return ProfilePayload{}, &HTTPError{Endpoint: "/debug/pprof/goroutine", Cause: err}
	}
	defer response.Body.Close()
	meta := RequestMeta{HTTPStatus: response.StatusCode, Latency: latency}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return ProfilePayload{}, &HTTPError{Endpoint: "/debug/pprof/goroutine", StatusCode: response.StatusCode, Message: strings.TrimSpace(string(message))}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, int64(limitBytes)+1))
	if err != nil {
		return ProfilePayload{}, &HTTPError{Endpoint: "/debug/pprof/goroutine", Cause: err}
	}
	truncated := len(body) > limitBytes
	if truncated {
		body = body[:limitBytes]
	}
	return ProfilePayload{
		Body:           body,
		RawBytes:       len(body),
		LimitBytes:     limitBytes,
		InputTruncated: truncated,
		Meta:           meta,
	}, nil
}

func (c *Client) getJSON(ctx context.Context, path string, destination any) (RequestMeta, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return RequestMeta{}, fmt.Errorf("create request: %w", err)
	}
	started := time.Now()
	response, err := c.httpClient.Do(request)
	latency := time.Since(started)
	if err != nil {
		return RequestMeta{}, &HTTPError{Endpoint: path, Cause: err}
	}
	defer response.Body.Close()
	meta := RequestMeta{HTTPStatus: response.StatusCode, Latency: latency}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return meta, &HTTPError{Endpoint: path, StatusCode: response.StatusCode, Message: strings.TrimSpace(string(message))}
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, maxJSONResponseBytes))
	if err := decoder.Decode(destination); err != nil {
		return meta, &HTTPError{Endpoint: path, StatusCode: response.StatusCode, Cause: fmt.Errorf("decode JSON: %w", err)}
	}
	return meta, nil
}
