// Package tokens provides shared token-count estimation utilities.
package tokens

import "strings"

// Message is a minimal interface satisfied by both types.ChatMessage and
// modelpolicy.ChatMessage, allowing callers from different packages to
// reuse the same estimation logic.
type Message struct {
	Role    string
	Content string
}

// EstimateTokens returns an approximate token count for a piece of text.
//
// The heuristic splits on whitespace and multiplies the word count by 1.3
// (the average number of BPE tokens per English word across common LLM
// tokenizers).  A minimum of 4 tokens is returned for any non-empty input
// to account for short strings and message framing overhead.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	words := len(strings.Fields(text))
	if words == 0 {
		// Non-empty but only whitespace.
		return 4
	}
	est := int(float64(words) * 1.3)
	if est < 4 {
		est = 4
	}
	return est
}

// EstimateTokensFromMessages sums the token estimate for a slice of
// messages.  Each message adds a small fixed overhead (4 tokens) to
// approximate the special tokens that real tokenizers insert for role
// markers and message boundaries.
func EstimateTokensFromMessages(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateTokens(m.Content) + 4 // +4 for role/boundary overhead
	}
	return total
}
