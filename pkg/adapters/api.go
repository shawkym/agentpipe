package adapters

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shawkym/agentpipe/pkg/agent"
	"github.com/shawkym/agentpipe/pkg/client"
	"github.com/shawkym/agentpipe/pkg/log"
	"github.com/shawkym/agentpipe/pkg/utils"
)

const defaultAPIModel = "auto"

// APIAgent is a custom OpenAI-compatible API agent configured via endpoint + token.
type APIAgent struct {
	agent.BaseAgent
	client      *client.OpenAICompatClient
	apiKey      string
	apiEndpoint string
}

// NewAPIAgent creates a new API agent instance.
func NewAPIAgent() agent.Agent {
	return &APIAgent{}
}

// Initialize configures the API agent with the provided configuration.
func (a *APIAgent) Initialize(config agent.AgentConfig) error {
	if err := a.BaseAgent.Initialize(config); err != nil {
		log.WithFields(map[string]interface{}{
			"agent_id":   config.ID,
			"agent_name": config.Name,
		}).WithError(err).Error("api agent base initialization failed")
		return err
	}

	if config.APIEndpoint == "" {
		return fmt.Errorf("api_endpoint must be specified for api agent")
	}
	if config.APIKey == "" {
		return fmt.Errorf("api_key must be specified for api agent")
	}

	a.apiEndpoint = config.APIEndpoint
	a.apiKey = config.APIKey

	if a.Config.Model == "" {
		a.Config.Model = defaultAPIModel
		log.WithFields(map[string]interface{}{
			"agent_id":   a.ID,
			"agent_name": a.Name,
		}).Warn("model not specified for api agent, defaulting to 'auto'")
	}

	a.client = client.NewOpenAICompatClient(a.apiEndpoint, a.apiKey)

	log.WithFields(map[string]interface{}{
		"agent_id":   a.ID,
		"agent_name": a.Name,
		"model":      a.Config.Model,
		"endpoint":   a.apiEndpoint,
	}).Info("api agent initialized successfully")

	return nil
}

// IsAvailable checks if the API agent has the required configuration.
func (a *APIAgent) IsAvailable() bool {
	return a.apiKey != "" && a.apiEndpoint != ""
}

// GetCLIVersion returns a version string indicating this is an API-based agent.
func (a *APIAgent) GetCLIVersion() string {
	return "N/A (API)"
}

// HealthCheck performs a health check by making a test API request.
func (a *APIAgent) HealthCheck(ctx context.Context) error {
	if a.client == nil {
		log.WithField("agent_name", a.Name).Error("api agent health check failed: not initialized")
		return fmt.Errorf("api agent not initialized")
	}

	req := client.ChatCompletionRequest{
		Model: a.Config.Model,
		Messages: []client.ChatCompletionMessage{
			{Role: "user", Content: "test"},
		},
		MaxTokens: intPtr(1),
	}

	if _, err := a.client.CreateChatCompletion(ctx, req); err != nil {
		log.WithField("agent_name", a.Name).WithError(err).Error("api agent health check failed")
		return fmt.Errorf("api agent health check failed: %w", err)
	}

	log.WithField("agent_name", a.Name).Info("api agent health check passed")
	return nil
}

// SendMessage sends a message to the configured API and returns the response.
func (a *APIAgent) SendMessage(ctx context.Context, messages []agent.Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	apiMessages := a.buildConversationHistory(messages)

	req := client.ChatCompletionRequest{
		Model:    a.Config.Model,
		Messages: apiMessages,
	}

	if a.Config.Temperature > 0 {
		req.Temperature = &a.Config.Temperature
	}

	if a.Config.MaxTokens > 0 {
		req.MaxTokens = &a.Config.MaxTokens
	}

	startTime := time.Now()
	resp, err := a.client.CreateChatCompletion(ctx, req)
	duration := time.Since(startTime)
	if err != nil {
		log.WithFields(map[string]interface{}{
			"agent_name": a.Name,
			"duration":   duration.String(),
			"model":      a.Config.Model,
		}).WithError(err).Error("api agent request failed")
		return "", fmt.Errorf("api agent request failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		log.WithField("agent_name", a.Name).Error("api agent returned no choices")
		return "", fmt.Errorf("no response from api agent")
	}

	content := resp.Choices[0].Message.Content

	if resp.Usage != nil {
		cost := utils.EstimateCost(a.Config.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		log.WithFields(map[string]interface{}{
			"agent_name":        a.Name,
			"duration":          duration.String(),
			"model":             resp.Model,
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
			"cost":              fmt.Sprintf("$%.4f", cost),
		}).Info("api agent message sent successfully")
	} else {
		log.WithFields(map[string]interface{}{
			"agent_name": a.Name,
			"duration":   duration.String(),
			"model":      resp.Model,
		}).Info("api agent message sent successfully")
	}

	return strings.TrimSpace(content), nil
}

// StreamMessage sends a message to the API and streams the response.
func (a *APIAgent) StreamMessage(ctx context.Context, messages []agent.Message, writer io.Writer) error {
	if len(messages) == 0 {
		return nil
	}

	apiMessages := a.buildConversationHistory(messages)

	req := client.ChatCompletionRequest{
		Model:    a.Config.Model,
		Messages: apiMessages,
	}

	if a.Config.Temperature > 0 {
		req.Temperature = &a.Config.Temperature
	}

	if a.Config.MaxTokens > 0 {
		req.MaxTokens = &a.Config.MaxTokens
	}

	startTime := time.Now()
	usage, err := a.client.CreateChatCompletionStream(ctx, req, writer)
	duration := time.Since(startTime)
	if err != nil {
		log.WithFields(map[string]interface{}{
			"agent_name": a.Name,
			"duration":   duration.String(),
			"model":      a.Config.Model,
		}).WithError(err).Error("api agent streaming failed")
		return fmt.Errorf("api agent streaming failed: %w", err)
	}

	if usage != nil {
		cost := utils.EstimateCost(a.Config.Model, usage.PromptTokens, usage.CompletionTokens)
		log.WithFields(map[string]interface{}{
			"agent_name":        a.Name,
			"duration":          duration.String(),
			"model":             a.Config.Model,
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
			"cost":              fmt.Sprintf("$%.4f", cost),
		}).Info("api agent streaming message completed")
	} else {
		log.WithFields(map[string]interface{}{
			"agent_name": a.Name,
			"duration":   duration.String(),
			"model":      a.Config.Model,
		}).Info("api agent streaming message completed")
	}

	return nil
}

// buildConversationHistory converts AgentPipe messages to OpenAI API format.
func (a *APIAgent) buildConversationHistory(messages []agent.Message) []client.ChatCompletionMessage {
	apiMessages := make([]client.ChatCompletionMessage, 0)

	if a.Config.Prompt != "" {
		apiMessages = append(apiMessages, client.ChatCompletionMessage{
			Role:    "system",
			Content: a.Config.Prompt,
		})
	}

	for _, msg := range messages {
		if msg.AgentName == a.Name || msg.AgentID == a.ID {
			continue
		}

		var role string
		var content string

		switch msg.Role {
		case "system":
			role = "user"
			content = fmt.Sprintf("[System] %s", msg.Content)
		case "user":
			role = "user"
			content = msg.Content
		case "agent":
			role = "user"
			content = fmt.Sprintf("%s: %s", msg.AgentName, msg.Content)
		default:
			continue
		}

		apiMessages = append(apiMessages, client.ChatCompletionMessage{
			Role:    role,
			Content: content,
		})
	}

	return apiMessages
}

func init() {
	agent.RegisterFactory("api", NewAPIAgent)
}
