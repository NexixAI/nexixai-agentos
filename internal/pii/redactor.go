package pii

import "sort"

// placeholders maps pattern names to their replacement strings.
var placeholders = map[string]string{
	PatternEmail:      "[EMAIL]",
	PatternPhone:      "[PHONE]",
	PatternSSN:        "[SSN]",
	PatternCreditCard: "[CREDIT_CARD]",
	PatternIPAddress:  "[IP_ADDRESS]",
}

// Redact replaces each detection in text with its placeholder.
// Overlapping detections are resolved: longer match wins; if same length,
// the pattern with higher priority (lower Priority value) wins.
// The original text is not mutated; a new string is returned.
func Redact(text string, detections []Detection) string {
	if len(detections) == 0 {
		return text
	}

	// Resolve overlaps: keep only non-overlapping detections.
	resolved := resolveOverlaps(detections)

	// Sort by start position descending so we can replace from end to start
	// without invalidating earlier byte offsets.
	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].Start > resolved[j].Start
	})

	result := text
	for _, det := range resolved {
		placeholder := placeholders[det.Pattern]
		if placeholder == "" {
			placeholder = "[PII]"
		}
		result = result[:det.Start] + placeholder + result[det.End:]
	}

	return result
}

// resolveOverlaps filters detections to remove overlapping entries.
// When two detections overlap, the longer one wins. If same length,
// the one with higher priority (looked up from builtinPatterns) wins.
func resolveOverlaps(detections []Detection) []Detection {
	if len(detections) <= 1 {
		return detections
	}

	// Sort by start position, then by length descending, then by priority.
	sorted := make([]Detection, len(detections))
	copy(sorted, detections)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		lenI := sorted[i].End - sorted[i].Start
		lenJ := sorted[j].End - sorted[j].Start
		if lenI != lenJ {
			return lenI > lenJ // longer first
		}
		return priorityOf(sorted[i].Pattern) < priorityOf(sorted[j].Pattern)
	})

	var result []Detection
	lastEnd := 0
	for _, det := range sorted {
		if det.Start >= lastEnd {
			result = append(result, det)
			lastEnd = det.End
		}
	}
	return result
}

// priorityOf returns the priority value for a pattern name.
func priorityOf(name string) int {
	if p, ok := builtinPatterns[name]; ok {
		return p.Priority
	}
	return 999
}
