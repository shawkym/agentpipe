package matrix

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shawkym/agentpipe/pkg/agent"
	"github.com/shawkym/agentpipe/pkg/config"
	"github.com/shawkym/agentpipe/pkg/log"
)

const (
	defaultSyncTimeout = 30 * time.Second
	sendQueueSize      = 200
)

// Bridge mirrors AgentPipe conversations to a Matrix room and ingests room input.
type Bridge struct {
	cfg          config.MatrixConfig
	roomID       string
	agentClients map[string]*Client // agent ID -> Matrix client
	listener     *Client
	knownSenders map[string]struct{}
	adminClient  *AdminClient
	createdUsers []string
	cleanup      bool
	eraseCleanup bool

	sendQueue chan agent.Message
}

// NewBridge initializes Matrix clients for all agents and joins the room.
func NewBridge(cfg config.MatrixConfig, agents []agent.AgentConfig) (*Bridge, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("matrix bridge is disabled")
	}

	bridge := &Bridge{
		cfg:          cfg,
		agentClients: make(map[string]*Client, len(agents)),
		knownSenders: make(map[string]struct{}),
		sendQueue:    make(chan agent.Message, sendQueueSize),
	}

	if cfg.AutoProvision {
		if err := bridge.autoProvision(agents); err != nil {
			return nil, err
		}
	} else {
		if cfg.Homeserver == "" {
			return nil, fmt.Errorf("matrix homeserver is required")
		}
		if cfg.Room == "" {
			return nil, fmt.Errorf("matrix room is required")
		}

		// Initialize agent clients
		for _, agentCfg := range agents {
			client, err := createClient(cfg.Homeserver, agentCfg.Matrix)
			if err != nil {
				return nil, fmt.Errorf("matrix login failed for agent %s: %w", agentCfg.ID, err)
			}

			roomID, err := client.JoinRoom(cfg.Room)
			if err != nil {
				return nil, fmt.Errorf("matrix join failed for agent %s: %w", agentCfg.ID, err)
			}

			if bridge.roomID == "" {
				bridge.roomID = roomID
			}
			bridge.agentClients[agentCfg.ID] = client
			bridge.knownSenders[client.UserID()] = struct{}{}
		}

		// Listener client (optional)
		listener, err := createListener(cfg.Homeserver, cfg.Listener, agents)
		if err != nil {
			return nil, err
		}
		if listener != nil {
			roomID, err := listener.JoinRoom(cfg.Room)
			if err != nil {
				return nil, fmt.Errorf("matrix join failed for listener: %w", err)
			}
			if bridge.roomID == "" {
				bridge.roomID = roomID
			}
			bridge.listener = listener
			bridge.knownSenders[listener.UserID()] = struct{}{}
		}

		if bridge.roomID == "" {
			return nil, fmt.Errorf("matrix room ID was not resolved")
		}
	}

	return bridge, nil
}

// Start begins the send loop and sync listener.
func (b *Bridge) Start(ctx context.Context, onMessage func(agent.Message)) {
	if b == nil {
		return
	}

	go b.sendLoop(ctx)

	if b.listener != nil && onMessage != nil {
		go b.listenLoop(ctx, onMessage)
	}
}

// Close cleans up auto-provisioned users if enabled.
func (b *Bridge) Close() {
	if b == nil {
		return
	}
	if b.adminClient == nil || !b.cleanup {
		return
	}

	for _, userID := range b.createdUsers {
		if err := b.adminClient.DeactivateUser(userID, b.eraseCleanup); err != nil {
			log.WithError(err).WithField("user_id", userID).Warn("matrix cleanup failed")
		}
	}
}

// Send enqueues a message to be sent to Matrix.
func (b *Bridge) Send(msg agent.Message) {
	if b == nil {
		return
	}

	select {
	case b.sendQueue <- msg:
	default:
		log.WithField("agent_id", msg.AgentID).Warn("matrix send queue full, dropping message")
	}
}

func (b *Bridge) sendLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-b.sendQueue:
			if err := b.sendMessage(msg); err != nil {
				log.WithError(err).WithField("agent_id", msg.AgentID).Warn("matrix send failed")
			}
		}
	}
}

func (b *Bridge) sendMessage(msg agent.Message) error {
	if msg.Content == "" {
		return nil
	}

	// Skip messages originating from Matrix
	if strings.HasPrefix(msg.AgentID, "matrix:") {
		return nil
	}

	var client *Client
	switch msg.Role {
	case "agent":
		client = b.agentClients[msg.AgentID]
	default:
		client = b.listener
		if client == nil {
			// Fallback to first agent if no listener configured
			for _, c := range b.agentClients {
				client = c
				break
			}
		}
	}

	if client == nil {
		return fmt.Errorf("no matrix client available for message from %s", msg.AgentID)
	}

	body := formatMessageBody(msg)
	return client.SendMessage(b.roomID, body)
}

func (b *Bridge) autoProvision(agents []agent.AgentConfig) error {
	homeserver := resolveHomeserver(b.cfg)
	adminToken := resolveAdminToken(b.cfg)
	if adminToken == "" {
		return fmt.Errorf("matrix admin access token is required for auto-provisioning")
	}

	adminClient := NewAdminClient(homeserver, adminToken, 15*time.Second)
	b.adminClient = adminClient

	serverName := resolveServerName(b.cfg, homeserver)
	userPrefix := b.cfg.UserPrefix
	if userPrefix == "" {
		userPrefix = "agentpipe"
	}

	b.cleanup = resolveCleanup(b.cfg)
	b.eraseCleanup = resolveEraseCleanup(b.cfg)

	// Create listener user
	listenerUserID := buildUserID(userPrefix+"-listener", serverName)
	listenerPassword := randomPassword()
	if err := adminClient.CreateOrUpdateUser(listenerUserID, listenerPassword, "AgentPipe Listener", false); err != nil {
		return fmt.Errorf("matrix listener creation failed: %w", err)
	}
	b.createdUsers = append(b.createdUsers, listenerUserID)
	listenerToken, listenerID, err := LoginWithPassword(homeserver, listenerUserID, listenerPassword, 15*time.Second)
	if err != nil {
		return fmt.Errorf("matrix listener login failed: %w", err)
	}
	b.listener = NewClient(homeserver, listenerToken, listenerID, 15*time.Second)
	b.knownSenders[listenerID] = struct{}{}

	// Create agent users
	for _, agentCfg := range agents {
		base := fmt.Sprintf("%s-%s", userPrefix, sanitizeLocalpart(agentCfg.ID))
		userID := buildUserID(base, serverName)
		password := randomPassword()
		if err := adminClient.CreateOrUpdateUser(userID, password, agentCfg.Name, false); err != nil {
			return fmt.Errorf("matrix user creation failed for agent %s: %w", agentCfg.ID, err)
		}
		b.createdUsers = append(b.createdUsers, userID)

		token, createdUserID, err := LoginWithPassword(homeserver, userID, password, 15*time.Second)
		if err != nil {
			return fmt.Errorf("matrix login failed for agent %s: %w", agentCfg.ID, err)
		}
		client := NewClient(homeserver, token, createdUserID, 15*time.Second)
		b.agentClients[agentCfg.ID] = client
		b.knownSenders[createdUserID] = struct{}{}
	}

	room := resolveRoom(b.cfg)
	if room == "" {
		roomName := fmt.Sprintf("AgentPipe %s", time.Now().Format("20060102-150405"))
		roomID, err := b.listener.CreateRoom(roomName)
		if err != nil {
			return fmt.Errorf("matrix room creation failed: %w", err)
		}
		room = roomID
	}

	// Join all users to the room
	listenerRoomID, err := b.listener.JoinRoom(room)
	if err != nil {
		return fmt.Errorf("matrix join failed for listener: %w", err)
	}
	b.roomID = listenerRoomID

	for agentID, client := range b.agentClients {
		if _, err := client.JoinRoom(room); err != nil {
			return fmt.Errorf("matrix join failed for agent %s: %w", agentID, err)
		}
	}

	return nil
}

func (b *Bridge) listenLoop(ctx context.Context, onMessage func(agent.Message)) {
	syncTimeout := defaultSyncTimeout
	if b.cfg.SyncTimeoutMs > 0 {
		syncTimeout = time.Duration(b.cfg.SyncTimeoutMs) * time.Millisecond
	}

	filter := buildSyncFilter(b.roomID, 50)

	// Initial sync to get a since token without processing backlog
	since := ""
	if resp, err := b.listener.Sync("", 0, filter); err == nil {
		since = resp.NextBatch
	} else {
		log.WithError(err).Warn("matrix initial sync failed")
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		resp, err := b.listener.Sync(since, syncTimeout, filter)
		if err != nil {
			log.WithError(err).Warn("matrix sync failed")
			time.Sleep(2 * time.Second)
			continue
		}
		if resp.NextBatch != "" {
			since = resp.NextBatch
		}

		room := resp.Rooms.Join[b.roomID]
		for _, event := range room.Timeline.Events {
			if event.Type != "m.room.message" {
				continue
			}
			if event.Content.MsgType != "m.text" && event.Content.MsgType != "m.notice" {
				continue
			}
			if _, ok := b.knownSenders[event.Sender]; ok {
				continue
			}

			content := strings.TrimSpace(event.Content.Body)
			if content == "" {
				continue
			}

			msg := agent.Message{
				AgentID:   "matrix:" + event.Sender,
				AgentName: formatSenderName(event.Sender),
				Content:   content,
				Timestamp: event.OriginServerTS / 1000,
				Role:      "user",
			}

			onMessage(msg)
		}
	}
}

func createClient(homeserver string, userCfg agent.MatrixUserConfig) (*Client, error) {
	if userCfg.UserID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	token := userCfg.AccessToken
	userID := userCfg.UserID
	if token == "" && userCfg.Password != "" {
		var err error
		token, userID, err = LoginWithPassword(homeserver, userCfg.UserID, userCfg.Password, 15*time.Second)
		if err != nil {
			return nil, err
		}
	}

	if token == "" {
		return nil, fmt.Errorf("access token is required for %s", userCfg.UserID)
	}

	return NewClient(homeserver, token, userID, 15*time.Second), nil
}

func createListener(homeserver string, listenerCfg agent.MatrixUserConfig, agents []agent.AgentConfig) (*Client, error) {
	if listenerCfg.UserID != "" {
		return createClient(homeserver, listenerCfg)
	}

	// Fallback to first agent for listening
	if len(agents) == 0 {
		return nil, nil
	}

	first := agents[0].Matrix
	if first.UserID == "" {
		return nil, nil
	}

	log.WithField("user_id", first.UserID).Warn("matrix listener not configured, using first agent for listening")
	return createClient(homeserver, first)
}

func buildSyncFilter(roomID string, limit int) string {
	if limit <= 0 {
		limit = 50
	}
	return fmt.Sprintf(`{"room":{"rooms":["%s"],"timeline":{"limit":%d}}}`, roomID, limit)
}

func formatSenderName(sender string) string {
	// Try to show localpart if possible
	if strings.HasPrefix(sender, "@") {
		trimmed := strings.TrimPrefix(sender, "@")
		if idx := strings.Index(trimmed, ":"); idx != -1 {
			return trimmed[:idx]
		}
	}
	return sender
}

func formatMessageBody(msg agent.Message) string {
	switch msg.Role {
	case "system":
		if msg.AgentID == "host" || strings.EqualFold(msg.AgentName, "host") {
			return "HOST: " + msg.Content
		}
		return "SYSTEM: " + msg.Content
	case "user":
		if msg.AgentName != "" {
			return msg.AgentName + ": " + msg.Content
		}
		return "User: " + msg.Content
	default:
		return msg.Content
	}
}

func resolveHomeserver(cfg config.MatrixConfig) string {
	if cfg.Homeserver != "" {
		return cfg.Homeserver
	}
	if env := os.Getenv("MATRIX_HOMESERVER"); env != "" {
		return env
	}
	return "http://localhost:8008"
}

func resolveRoom(cfg config.MatrixConfig) string {
	if cfg.Room != "" {
		return cfg.Room
	}
	return os.Getenv("MATRIX_ROOM")
}

func resolveAdminToken(cfg config.MatrixConfig) string {
	if cfg.AdminAccessToken != "" {
		return cfg.AdminAccessToken
	}
	return os.Getenv("MATRIX_ADMIN_TOKEN")
}

func resolveServerName(cfg config.MatrixConfig, homeserver string) string {
	if cfg.ServerName != "" {
		return cfg.ServerName
	}
	if env := os.Getenv("MATRIX_SERVER_NAME"); env != "" {
		return env
	}

	parsed, err := url.Parse(homeserver)
	if err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "localhost"
}

func resolveCleanup(cfg config.MatrixConfig) bool {
	if cfg.Cleanup != nil {
		return *cfg.Cleanup
	}
	return true
}

func resolveEraseCleanup(cfg config.MatrixConfig) bool {
	if cfg.EraseOnCleanup != nil {
		return *cfg.EraseOnCleanup
	}
	return true
}

func buildUserID(base, serverName string) string {
	localpart := sanitizeLocalpart(base)
	suffix := randomSuffix(6)
	return fmt.Sprintf("@%s-%s:%s", localpart, suffix, serverName)
}

func sanitizeLocalpart(input string) string {
	if input == "" {
		return "agent"
	}
	input = strings.ToLower(input)
	var b strings.Builder
	for _, r := range input {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func randomPassword() string {
	return randomToken(24)
}

func randomSuffix(length int) string {
	return randomToken(length)
}

func randomToken(length int) string {
	if length <= 0 {
		length = 12
	}
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("agentpipe-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
