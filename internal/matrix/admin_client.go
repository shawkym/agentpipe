package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// AdminClient provides access to Synapse admin APIs.
type AdminClient struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
}

// NewAdminClient creates a new admin client for the given homeserver.
func NewAdminClient(baseURL, accessToken string, timeout time.Duration) *AdminClient {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &AdminClient{
		baseURL:     cleanBaseURL(baseURL),
		accessToken: accessToken,
		httpClient: &http.Client{
			Timeout: timeout,
		},
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

	req, err := http.NewRequest("PUT", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create admin request: %w", err)
	}
	a.addAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("create user request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create user failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
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

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create deactivate request: %w", err)
	}
	a.addAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("deactivate request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deactivate failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

func (a *AdminClient) addAuth(req *http.Request) {
	if a.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.accessToken)
	}
}
