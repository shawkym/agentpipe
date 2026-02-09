package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client provides minimal Matrix Client-Server API operations.
type Client struct {
	baseURL     string
	accessToken string
	userID      string
	httpClient  *http.Client
}

// NewClient creates a Matrix client with the provided base URL and access token.
func NewClient(baseURL, accessToken, userID string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		baseURL:     cleanBaseURL(baseURL),
		accessToken: accessToken,
		userID:      userID,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// UserID returns the Matrix user ID for this client.
func (c *Client) UserID() string {
	return c.userID
}

// AccessToken returns the access token for this client.
func (c *Client) AccessToken() string {
	return c.accessToken
}

// LoginWithPassword logs in using a password and returns a new access token and user ID.
func LoginWithPassword(baseURL, userID, password string, timeout time.Duration) (string, string, error) {
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	payload := map[string]interface{}{
		"type": "m.login.password",
		"identifier": map[string]interface{}{
			"type": "m.id.user",
			"user": localpart(userID),
		},
		"password": password,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal login payload: %w", err)
	}

	client := &http.Client{Timeout: timeout}
	endpoint := cleanBaseURL(baseURL) + "/_matrix/client/v3/login"
	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return "", "", fmt.Errorf("failed to create login request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("login request failed: %w", err)
		} else {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var result struct {
					AccessToken string `json:"access_token"`
					UserID      string `json:"user_id"`
				}
				if err := json.Unmarshal(bodyBytes, &result); err != nil {
					return "", "", fmt.Errorf("failed to parse login response: %w", err)
				}
				if result.AccessToken == "" {
					return "", "", fmt.Errorf("login response missing access_token")
				}
				return result.AccessToken, result.UserID, nil
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				retryAfter := parseRetryAfter(bodyBytes)
				if retryAfter > 0 && attempt < maxRetries {
					time.Sleep(retryAfter)
					continue
				}
			}

			lastErr = fmt.Errorf("login failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		}

		if attempt < maxRetries {
			backoff := time.Duration(1<<attempt) * time.Second
			time.Sleep(backoff)
			continue
		}
	}

	if lastErr != nil {
		return "", "", lastErr
	}

	return "", "", fmt.Errorf("login failed after retries")
}

func parseRetryAfter(body []byte) time.Duration {
	var payload struct {
		ErrCode      string `json:"errcode"`
		RetryAfterMs int    `json:"retry_after_ms"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	if payload.ErrCode != "M_LIMIT_EXCEEDED" || payload.RetryAfterMs <= 0 {
		return 0
	}
	return time.Duration(payload.RetryAfterMs) * time.Millisecond
}

// JoinRoom joins a room by ID or alias and returns the resolved room ID.
func (c *Client) JoinRoom(room string) (string, error) {
	if room == "" {
		return "", fmt.Errorf("room is required")
	}

	endpoint := fmt.Sprintf("%s/_matrix/client/v3/join/%s", c.baseURL, url.PathEscape(room))
	if domain := extractRoomDomain(room); domain != "" {
		query := url.Values{}
		query.Add("server_name", domain)
		endpoint = endpoint + "?" + query.Encode()
	}
	req, err := http.NewRequest("POST", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create join request: %w", err)
	}
	c.addAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("join request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("join failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		RoomID string `json:"room_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse join response: %w", err)
	}
	if result.RoomID == "" {
		return "", fmt.Errorf("join response missing room_id")
	}

	return result.RoomID, nil
}

func extractRoomDomain(room string) string {
	if idx := strings.Index(room, ":"); idx != -1 && idx+1 < len(room) {
		return room[idx+1:]
	}
	return ""
}

// SendMessage sends a text message to the given room.
func (c *Client) SendMessage(roomID, body string) error {
	if roomID == "" {
		return fmt.Errorf("room ID is required")
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}

	txnID := url.PathEscape(generateTxnID())
	endpoint := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		c.baseURL, url.PathEscape(roomID), txnID)

	payload := map[string]interface{}{
		"msgtype": "m.text",
		"body":    body,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message payload: %w", err)
	}

	req, err := http.NewRequest("PUT", endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create send request: %w", err)
	}
	c.addAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// CreateRoom creates a new room and returns its room ID.
func (c *Client) CreateRoom(name string) (string, error) {
	endpoint := c.baseURL + "/_matrix/client/v3/createRoom"
	payload := map[string]interface{}{
		"name": name,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal create room payload: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to create createRoom request: %w", err)
	}
	c.addAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create room request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create room failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		RoomID string `json:"room_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse createRoom response: %w", err)
	}
	if result.RoomID == "" {
		return "", fmt.Errorf("createRoom response missing room_id")
	}

	return result.RoomID, nil
}

// Sync performs a single sync request.
func (c *Client) Sync(since string, timeout time.Duration, filter string) (*SyncResponse, error) {
	endpoint := c.baseURL + "/_matrix/client/v3/sync"
	params := url.Values{}
	if since != "" {
		params.Set("since", since)
	}
	if timeout > 0 {
		params.Set("timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
	}
	if filter != "" {
		params.Set("filter", filter)
	}
	params.Set("set_presence", "offline")

	req, err := http.NewRequest("GET", endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create sync request: %w", err)
	}
	c.addAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sync failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse sync response: %w", err)
	}

	return &result, nil
}

func (c *Client) addAuth(req *http.Request) {
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
}

func cleanBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if idx := strings.Index(trimmed, "/_matrix"); idx != -1 {
		return trimmed[:idx]
	}
	return trimmed
}

func localpart(userID string) string {
	if strings.HasPrefix(userID, "@") {
		userID = strings.TrimPrefix(userID, "@")
		if idx := strings.Index(userID, ":"); idx != -1 {
			return userID[:idx]
		}
	}
	return userID
}

func generateTxnID() string {
	return fmt.Sprintf("agentpipe-%d", time.Now().UnixNano())
}
