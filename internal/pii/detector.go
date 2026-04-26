package pii

import "sort"

// Detection represents a single PII match found in text.
type Detection struct {
	Pattern string `json:"pattern"` // pattern name, e.g. "email"
	Match   string `json:"match"`   // the matched text
	Start   int    `json:"start"`   // byte offset (inclusive)
	End     int    `json:"end"`     // byte offset (exclusive)
}

// Detector scans text for PII using compiled regex patterns.
// It is safe for concurrent use once constructed.
type Detector struct {
	enabled []PatternDef // patterns to check, in priority order
}

// NewDetector creates a Detector with only the specified patterns enabled.
// If patterns is nil or empty, all built-in patterns are enabled.
func NewDetector(patterns []string) *Detector {
	d := &Detector{}
	if len(patterns) == 0 {
		// Enable all in priority order.
		for _, name := range patternOrder {
			d.enabled = append(d.enabled, builtinPatterns[name])
		}
	} else {
		// Enable only requested patterns, but maintain priority order.
		wanted := make(map[string]bool, len(patterns))
		for _, p := range patterns {
			wanted[p] = true
		}
		for _, name := range patternOrder {
			if wanted[name] {
				d.enabled = append(d.enabled, builtinPatterns[name])
			}
		}
	}
	return d
}

// Scan returns all PII detections in text, sorted by start position.
// When patterns overlap, SSN takes priority over phone (handled by
// scanning SSN first and marking claimed byte ranges).
func (d *Detector) Scan(text string) []Detection {
	if text == "" {
		return nil
	}

	// Track which byte offsets are already claimed by a higher-priority pattern.
	claimed := make([]bool, len(text))
	var results []Detection

	for _, pat := range d.enabled {
		matches := pat.Regex.FindAllStringIndex(text, -1)
		for _, loc := range matches {
			start, end := loc[0], loc[1]

			// Skip if any byte in this range is already claimed.
			overlap := false
			for i := start; i < end; i++ {
				if claimed[i] {
					overlap = true
					break
				}
			}
			if overlap {
				continue
			}

			matched := text[start:end]

			// Run validation function if present (e.g. Luhn for credit cards).
			if pat.Validate != nil && !pat.Validate(matched) {
				continue
			}

			// Claim these bytes.
			for i := start; i < end; i++ {
				claimed[i] = true
			}

			results = append(results, Detection{
				Pattern: pat.Name,
				Match:   matched,
				Start:   start,
				End:     end,
			})
		}
	}

	// Sort by start position for deterministic output.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Start < results[j].Start
	})

	return results
}
