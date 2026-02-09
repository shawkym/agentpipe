package matrix

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/shawkym/agentpipe/pkg/ratelimit"
)

// AdminClient provides access to Synapse admin APIs.
type AdminClient struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
	limiter     *ratelimit.Limiter
}

// ErrInvalidAdminToken indicates the admin token is invalid.
var ErrInvalidAdminToken = errors.New("matrix admin access token invalid")

// NewAdminClient creates a new admin client for the given homeserver.
func NewAdminClient(baseURL, accessToken string, timeout time.Duration, limiter *ratelimit.Limiter) *AdminClient {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &AdminClient{
		baseURL:     cleanBaseURL(baseURL),
		accessToken: accessToken,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		limiter: limiter,
	}
}

// CreateOrUpdateUser creates or updates a user via the admin API.
func (a *AdminClient) CreateOrUpdateUser(userID, password, displayName string, admin bool) error {
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}

	endpoint := fmt.Sprintf("%s/_synapse/admin/v2/users/%s", a.baseURL, url.PathEscape(userID))
	payload := map[string]interface{}{
		"password":     password,
		"displayname":  displayName,
		"admin":        admin,
		"deactivated":  false,
		"user_type":    nil,
		"threepids":    []interface{}{},
		"external_ids": []interface{}{},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal create user payload: %w", err)
	}

	const maxRetries = defaultRateLimitRetries
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		waitForLimiter(a.limiter, "admin_create_user")
		req, err := http.NewRequest("PUT", endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create admin request: %w", err)
		}
		a.addAuth(req)
		req.Header.Set("Content-Type", "application/json")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("create user request failed: %w", err)
		} else {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}

			if resp.StatusCode == http.StatusUnauthorized && isUnknownToken(bodyBytes) {
				return ErrInvalidAdminToken
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				retryAfter := capRetryAfter(parseRetryAfter(bodyBytes))
				if retryAfter > 0 && attempt < maxRetries {
					sleepWithLog("admin_create_user", "retry_after", retryAfter)
					continue
				}
			}

			lastErr = fmt.Errorf("create user failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		}

		if attempt < maxRetries {
			backoff := time.Duration(1<<attempt) * time.Second
			sleepWithLog("admin_create_user", "backoff", backoff)
			continue
		}
	}

	if lastErr != nil {
		return lastErr
	}

	return fmt.Errorf("create user failed after retries")
}

// DeactivateUser deactivates a user via the admin API.
func (a *AdminClient) DeactivateUser(userID string, erase bool) error {
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}

	endpoint := fmt.Sprintf("%s/_synapse/admin/v1/deactivate/%s", a.baseURL, url.PathEscape(userID))
	payload := map[string]interface{}{
		"erase": erase,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal deactivate payload: %w", err)
	}

	const maxRetries = defaultRateLimitRetries
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		waitForLimiter(a.limiter, "admin_deactivate_user")
		req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create deactivate request: %w", err)
		}
		a.addAuth(req)
		req.Header.Set("Content-Type", "application/json")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("deactivate request failed: %w", err)
		} else {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}

			if resp.StatusCode == http.StatusUnauthorized && isUnknownToken(bodyBytes) {
				return ErrInvalidAdminToken
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				retryAfter := capRetryAfter(parseRetryAfter(bodyBytes))
				if retryAfter > 0 && attempt < maxRetries {
					sleepWithLog("admin_deactivate_user", "retry_after", retryAfter)
					continue
				}
			}

			lastErr = fmt.Errorf("deactivate failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		}

		if attempt < maxRetries {
			backoff := time.Duration(1<<attempt) * time.Second
			sleepWithLog("admin_deactivate_user", "backoff", backoff)
			continue
		}
	}

	if lastErr != nil {
		return lastErr
	}

	return fmt.Errorf("deactivate failed after retries")
}

// JoinRoomForUser forces a user to join a room via the admin API.
func (a *AdminClient) JoinRoomForUser(roomIDOrAlias, userID string) (string, error) {
	if roomIDOrAlias == "" {
		return "", fmt.Errorf("room is required")
	}
	if userID == "" {
		return "", fmt.Errorf("user_id is required")
	}

	endpoint := fmt.Sprintf("%s/_synapse/admin/v1/join/%s", a.baseURL, url.PathEscape(roomIDOrAlias))
	query := url.Values{}
	query.Set("user_id", userID)
	endpoint = endpoint + "?" + query.Encode()

	const maxRetries = defaultRateLimitRetries
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		waitForLimiter(a.limiter, "admin_join")
		req, err := http.NewRequest("POST", endpoint, nil)
		if err != nil {
			return "", fmt.Errorf("failed to create admin join request: %w", err)
		}
		a.addAuth(req)

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("admin join request failed: %w", err)
		} else {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var result struct {
					RoomID string `json:"room_id"`
				}
				if err := json.Unmarshal(bodyBytes, &result); err == nil && result.RoomID != "" {
					return result.RoomID, nil
				}
				return "", nil
			}

			if resp.StatusCode == http.StatusUnauthorized && isUnknownToken(bodyBytes) {
				return "", ErrInvalidAdminToken
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				retryAfter := capRetryAfter(parseRetryAfter(bodyBytes))
				if retryAfter > 0 && attempt < maxRetries {
					sleepWithLog("admin_join", "retry_after", retryAfter)
					continue
				}
			}

			lastErr = fmt.Errorf("admin join failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		}

		if attempt < maxRetries {
			backoff := time.Duration(1<<attempt) * time.Second
			sleepWithLog("admin_join", "backoff", backoff)
			continue
		}
	}

	if lastErr != nil {
		return "", lastErr
	}

	return "", fmt.Errorf("admin join failed after retries")
}

func (a *AdminClient) addAuth(req *http.Request) {
	if a.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.accessToken)
	}
}

// SetAccessToken updates the admin access token.
func (a *AdminClient) SetAccessToken(token string) {
	a.accessToken = token
}

func isUnknownToken(body []byte) bool {
	var payload struct {
		ErrCode string `json:"errcode"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.ErrCode == "M_UNKNOWN_TOKEN"
}
