package tokens

import (
	"strings"
	"testing"
)

func TestEstimateTokens_Empty(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", got)
	}
}

func TestEstimateTokens_WhitespaceOnly(t *testing.T) {
	if got := EstimateTokens("   \t\n"); got != 4 {
		t.Errorf("EstimateTokens(whitespace) = %d, want 4", got)
	}
}

func TestEstimateTokens_ShortInput(t *testing.T) {
	// "hi" is 1 word -> 1*1.3 = 1, clamped to minimum 4
	if got := EstimateTokens("hi"); got != 4 {
		t.Errorf("EstimateTokens(\"hi\") = %d, want 4", got)
	}
	// "hello world" is 2 words -> 2*1.3 = 2, clamped to minimum 4
	if got := EstimateTokens("hello world"); got != 4 {
		t.Errorf("EstimateTokens(\"hello world\") = %d, want 4", got)
	}
}

func TestEstimateTokens_LongerInput(t *testing.T) {
	// 10 words -> 10 * 1.3 = 13
	text := "the quick brown fox jumps over the lazy dog today"
	got := EstimateTokens(text)
	if got != 13 {
		t.Errorf("EstimateTokens(%q) = %d, want 13", text, got)
	}
}

func TestEstimateTokens_LargeInput(t *testing.T) {
	// 100 words -> 100 * 1.3 = 130
	words := make([]string, 100)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")
	got := EstimateTokens(text)
	if got != 130 {
		t.Errorf("EstimateTokens(100 words) = %d, want 130", got)
	}
}

func TestEstimateTokensFromMessages_Empty(t *testing.T) {
	if got := EstimateTokensFromMessages(nil); got != 0 {
		t.Errorf("EstimateTokensFromMessages(nil) = %d, want 0", got)
	}
}

func TestEstimateTokensFromMessages_Single(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
	}
	// "hello" = 1 word -> min 4 tokens + 4 overhead = 8
	got := EstimateTokensFromMessages(msgs)
	if got != 8 {
		t.Errorf("EstimateTokensFromMessages = %d, want 8", got)
	}
}

func TestEstimateTokensFromMessages_Multiple(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful"},     // 3 words -> min 4 + 4 = 8
		{Role: "user", Content: "Tell me about tokens"},  // 4 words -> 4*1.3=5 + 4 = 9
		{Role: "assistant", Content: "Sure, here you go"}, // 4 words -> 5 + 4 = 9
	}
	// Total: 8 + 9 + 9 = 26
	got := EstimateTokensFromMessages(msgs)
	if got != 26 {
		t.Errorf("EstimateTokensFromMessages = %d, want 26", got)
	}
}

func TestEstimateTokensFromMessages_EmptyContent(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: ""},
	}
	// empty content = 0 tokens + 4 overhead = 4
	got := EstimateTokensFromMessages(msgs)
	if got != 4 {
		t.Errorf("EstimateTokensFromMessages(empty content) = %d, want 4", got)
	}
}
