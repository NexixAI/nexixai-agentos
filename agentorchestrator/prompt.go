package agentorchestrator

import (
	"github.com/NexixAI/nexixai-agentos/internal/types"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

// buildPrompt constructs the message list for a model invocation from the
// agent's system prompt, conversation history, and the current run input.
func buildPrompt(agent types.Agent, run types.Run, history []types.ChatMessage) []modelpolicy.ChatMessage {
	var messages []modelpolicy.ChatMessage

	// System prompt from agent config.
	if agent.Config != nil && agent.Config.SystemPrompt != "" {
		messages = append(messages, modelpolicy.ChatMessage{
			Role:    "system",
			Content: agent.Config.SystemPrompt,
		})
	}

	// Conversation history (converted from storage type).
	for _, m := range history {
		messages = append(messages, storageMsgToModel(m))
	}

	// Current run input.
	if run.Input.Text != "" {
		messages = append(messages, modelpolicy.ChatMessage{
			Role:    "user",
			Content: run.Input.Text,
		})
	}

	return messages
}

// storageMsgToModel converts a types.ChatMessage to a modelpolicy.ChatMessage.
func storageMsgToModel(m types.ChatMessage) modelpolicy.ChatMessage {
	msg := modelpolicy.ChatMessage{
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	// ToolCalls is stored as raw JSON in storage; decode if present.
	if len(m.ToolCalls) > 0 {
		var tcs []modelpolicy.ToolCall
		if err := jsonUnmarshal(m.ToolCalls, &tcs); err == nil {
			msg.ToolCalls = tcs
		}
	}
	return msg
}

// modelMsgToStorage converts a modelpolicy.ChatMessage to a types.ChatMessage.
func modelMsgToStorage(m modelpolicy.ChatMessage) types.ChatMessage {
	// Storage schema holds Content as a string; flatten multipart via
	// MessageText so conversation history is readable after v11.1. The
	// persisted form loses image URLs — acceptable tradeoff for v11.1
	// since conversation storage is a text-only record.
	msg := types.ChatMessage{
		Role:       m.Role,
		Content:    modelpolicy.MessageText(m),
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	if len(m.ToolCalls) > 0 {
		if b, err := jsonMarshal(m.ToolCalls); err == nil {
			msg.ToolCalls = b
		}
	}
	return msg
}

// modelMsgsToStorage converts a slice of model messages to storage messages.
func modelMsgsToStorage(msgs []modelpolicy.ChatMessage) []types.ChatMessage {
	out := make([]types.ChatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = modelMsgToStorage(m)
	}
	return out
}
