package session

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

const DefaultHeartbeatInterval = 5 * time.Second

type LifecycleClient struct {
	baseURL    string
	sessionID  string
	serverID   string
	token      string
	httpClient *http.Client
}

func NewLifecycleClient(baseURL, sessionID, serverID, token string, httpClient *http.Client) (*LifecycleClient, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("lifecycle API URL must be an HTTP(S) URL")
	}
	if err := validateID(sessionID); err != nil {
		return nil, fmt.Errorf("lifecycle session: %w", err)
	}
	if strings.TrimSpace(serverID) == "" || len(serverID) > 128 || strings.ContainsAny(serverID, "{}[]\\\"'") {
		return nil, errors.New("lifecycle server id is required and bounded")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("lifecycle server token is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &LifecycleClient{
		baseURL:    parsed.String(),
		sessionID:  sessionID,
		serverID:   serverID,
		token:      token,
		httpClient: httpClient,
	}, nil
}

func (c *LifecycleClient) Start(ctx context.Context) (Session, error) {
	return c.post(ctx, "start")
}

func (c *LifecycleClient) Heartbeat(ctx context.Context) (Session, error) {
	return c.post(ctx, "heartbeat")
}

func (c *LifecycleClient) Complete(ctx context.Context) (Session, error) {
	return c.post(ctx, "complete")
}

func (c *LifecycleClient) HeartbeatLoop(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := c.Heartbeat(ctx); err != nil {
				if errors.Is(err, context.Canceled) && ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (c *LifecycleClient) post(ctx context.Context, action string) (Session, error) {
	if action != "start" && action != "heartbeat" && action != "complete" {
		return Session{}, errors.New("unsupported lifecycle action")
	}
	endpoint := c.baseURL + "/v1/sessions/" + url.PathEscape(c.sessionID) + "/" + action
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return Session{}, fmt.Errorf("create lifecycle request: %w", err)
	}
	request.Header.Set("X-Game-Server-ID", c.serverID)
	request.Header.Set("X-Game-Server-Token", c.token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Session{}, fmt.Errorf("send lifecycle request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Session{}, fmt.Errorf("lifecycle %s returned status %d: %s", action, response.StatusCode, strings.TrimSpace(string(body)))
	}
	var result Session
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return Session{}, fmt.Errorf("decode lifecycle %s response: %w", action, err)
	}
	return result, nil
}
