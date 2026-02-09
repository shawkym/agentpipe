package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAdminJoinSendsJSONBody(t *testing.T) {
	const room = "!room:example.com"
	const userID = "@agent:example.com"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/_synapse/admin/v1/join/" + room
		if r.URL.Path != expectedPath {
			if unescaped, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/_synapse/admin/v1/join/")); err == nil {
				if unescaped != room {
					t.Errorf("unexpected path: got %q want %q", r.URL.Path, expectedPath)
				}
			} else {
				t.Errorf("unexpected path: got %q want %q", r.URL.Path, expectedPath)
			}
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: got %q want %q", r.Method, http.MethodPost)
		}

		if got := r.URL.Query().Get("server_name"); got != "example.com" {
			t.Errorf("unexpected server_name: got %q want %q", got, "example.com")
		}

		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("unexpected content-type: got %q", ct)
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed reading body: %v", err)
		}
		defer r.Body.Close()

		var payload struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			t.Errorf("failed to parse JSON body: %v", err)
		}
		if payload.UserID != userID {
			t.Errorf("unexpected user_id: got %q want %q", payload.UserID, userID)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"room_id":"!joined:example.com"}`))
	}))
	defer server.Close()

	client := NewAdminClient(server.URL, "token", 0, nil)
	roomID, err := client.JoinRoomForUser(room, userID)
	if err != nil {
		t.Fatalf("JoinRoomForUser failed: %v", err)
	}
	if roomID != "!joined:example.com" {
		t.Fatalf("unexpected room_id: got %q want %q", roomID, "!joined:example.com")
	}
}
