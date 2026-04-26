package pii

import "regexp"

// PatternDef defines a named PII pattern with its regex and replacement placeholder.
type PatternDef struct {
	Name        string
	Regex       *regexp.Regexp
	Placeholder string
	Priority    int // lower = higher priority for overlap resolution
	Validate    func(match string) bool
}

// Pattern name constants.
const (
	PatternEmail      = "email"
	PatternPhone      = "phone"
	PatternSSN        = "ssn"
	PatternCreditCard = "credit_card"
	PatternIPAddress  = "ip_address"
)

// builtinPatterns contains all supported PII patterns, keyed by name.
// Regexes are compiled once at package init time.
var builtinPatterns = map[string]PatternDef{
	PatternEmail: {
		Name:        PatternEmail,
		Regex:       regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
		Placeholder: "[EMAIL]",
		Priority:    3,
	},
	PatternPhone: {
		Name:        PatternPhone,
		Regex:       regexp.MustCompile(`\b\d{3}[-.]?\d{3}[-.]?\d{4}\b`),
		Placeholder: "[PHONE]",
		Priority:    4,
	},
	PatternSSN: {
		Name:        PatternSSN,
		Regex:       regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		Placeholder: "[SSN]",
		Priority:    1, // SSN takes priority over phone
	},
	PatternCreditCard: {
		Name:        PatternCreditCard,
		Regex:       regexp.MustCompile(`\b\d{4}[\s\-]?\d{4}[\s\-]?\d{4}[\s\-]?\d{1,7}\b`),
		Placeholder: "[CREDIT_CARD]",
		Priority:    2,
		Validate:    luhnValid,
	},
	PatternIPAddress: {
		Name:        PatternIPAddress,
		Regex:       regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\b`),
		Placeholder: "[IP_ADDRESS]",
		Priority:    5,
	},
}

// patternOrder defines the order patterns are checked. SSN before phone is
// critical so that SSN-format strings are claimed before the phone regex.
var patternOrder = []string{
	PatternSSN,
	PatternCreditCard,
	PatternEmail,
	PatternPhone,
	PatternIPAddress,
}

// luhnValid performs the Luhn algorithm on a string that may contain spaces
// or hyphens. Returns true if the digit sequence passes.
func luhnValid(s string) bool {
	// Strip non-digit characters.
	var digits []int
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			digits = append(digits, int(ch-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}

	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := digits[i]
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
