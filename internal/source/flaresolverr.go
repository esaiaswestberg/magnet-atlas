package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultFlareSolverrTimeout = 60 * time.Second

type flareSolverrClient struct {
	endpoint   *url.URL
	httpClient *http.Client
	timeout    time.Duration
}

type flareSolverrRequest struct {
	Cmd               string `json:"cmd"`
	URL               string `json:"url,omitempty"`
	MaxTimeout        int    `json:"maxTimeout,omitempty"`
	Session           string `json:"session,omitempty"`
	SessionTTLMinutes int    `json:"session_ttl_minutes,omitempty"`
	ReturnOnlyCookies bool   `json:"returnOnlyCookies,omitempty"`
}

type flareSolverrResponse struct {
	Status         string             `json:"status"`
	Message        string             `json:"message"`
	Solution       flareSolverrResult `json:"solution"`
	StartTimestamp int64              `json:"startTimestamp"`
	EndTimestamp   int64              `json:"endTimestamp"`
	Version        string             `json:"version"`
}

type flareSolverrResult struct {
	URL       string               `json:"url"`
	Status    int                  `json:"status"`
	Headers   map[string]string    `json:"headers"`
	Response  string               `json:"response"`
	Cookies   []flareSolverrCookie `json:"cookies"`
	UserAgent string               `json:"userAgent"`
}

type flareSolverrCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires"`
	Size     int    `json:"size"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
	Session  bool   `json:"session"`
	SameSite string `json:"sameSite"`
}

func newFlareSolverrClient(rawURL string) (*flareSolverrClient, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("flaresolverr_url is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse flaresolverr_url: %w", err)
	}
	if !parsed.IsAbs() {
		return nil, fmt.Errorf("flaresolverr_url must be absolute")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("flaresolverr_url must use http or https")
	}
	if strings.TrimSpace(parsed.Path) == "" || strings.TrimSpace(parsed.Path) == "/" {
		parsed.Path = "/v1"
	}
	parsed.Fragment = ""
	parsed.RawQuery = ""

	return &flareSolverrClient{
		endpoint:   parsed,
		httpClient: &http.Client{Timeout: 90 * time.Second},
		timeout:    defaultFlareSolverrTimeout,
	}, nil
}

func (c *flareSolverrClient) get(ctx context.Context, targetURL string) (string, int, error) {
	if c == nil {
		return "", 0, fmt.Errorf("flaresolverr client is required")
	}
	reqBody := flareSolverrRequest{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: int(c.timeout / time.Millisecond),
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}

	var parsed flareSolverrResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", resp.StatusCode, fmt.Errorf("parse flaresolverr response: %w", err)
	}
	if !strings.EqualFold(parsed.Status, "ok") {
		message := strings.TrimSpace(parsed.Message)
		if message == "" {
			message = "unexpected flaresolverr status"
		}
		return "", resp.StatusCode, fmt.Errorf("%s", message)
	}
	if parsed.Solution.Status == 0 {
		parsed.Solution.Status = resp.StatusCode
	}
	return parsed.Solution.Response, parsed.Solution.Status, nil
}
