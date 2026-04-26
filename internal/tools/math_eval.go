package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

type MathEvalTool struct{}

func (t *MathEvalTool) Name() string { return "math_eval" }
func (t *MathEvalTool) Description() string {
	return "Evaluate a mathematical expression safely. Supports +, -, *, /, %, parentheses, and functions (sqrt, abs, floor, ceil, round, log, log10, pow, min, max)."
}
func (t *MathEvalTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expression": map[string]any{"type": "string", "description": "Mathematical expression to evaluate"},
		},
		"required": []string{"expression"},
	}
}

type mathEvalInput struct {
	Expression string `json:"expression"`
}

func (t *MathEvalTool) Execute(ctx context.Context, input string) (string, error) {
	var in mathEvalInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Expression == "" {
		return "", fmt.Errorf("expression is required")
	}

	result, err := evalExpr(in.Expression)
	if err != nil {
		return "", fmt.Errorf("evaluation error: %w", err)
	}

	out := map[string]any{
		"expression": in.Expression,
		"result":     result,
	}
	data, _ := json.Marshal(out) //nolint:errcheck // marshal of known struct
	return string(data), nil
}

// Simple recursive descent parser for arithmetic expressions.

type mathParser struct {
	input string
	pos   int
}

func evalExpr(expr string) (float64, error) {
	p := &mathParser{input: strings.TrimSpace(expr)}
	result, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipWhitespace()
	if p.pos < len(p.input) {
		return 0, fmt.Errorf("unexpected character at position %d: %q", p.pos, string(p.input[p.pos]))
	}
	return result, nil
}

func (p *mathParser) skipWhitespace() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *mathParser) parseExpr() (float64, error) {
	return p.parseAddSub()
}

func (p *mathParser) parseAddSub() (float64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWhitespace()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		if op != '+' && op != '-' {
			break
		}
		p.pos++
		right, err := p.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *mathParser) parseMulDiv() (float64, error) {
	left, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWhitespace()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' && op != '%' {
			break
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			left *= right
		case '/':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		case '%':
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			left = math.Mod(left, right)
		}
	}
	return left, nil
}

func (p *mathParser) parseUnary() (float64, error) {
	p.skipWhitespace()
	if p.pos < len(p.input) && p.input[p.pos] == '-' {
		p.pos++
		val, err := p.parsePrimary()
		if err != nil {
			return 0, err
		}
		return -val, nil
	}
	if p.pos < len(p.input) && p.input[p.pos] == '+' {
		p.pos++
	}
	return p.parsePrimary()
}

func (p *mathParser) parsePrimary() (float64, error) {
	p.skipWhitespace()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	// Parenthesized expression.
	if p.input[p.pos] == '(' {
		p.pos++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipWhitespace()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return val, nil
	}

	// Function call or constant.
	if unicode.IsLetter(rune(p.input[p.pos])) {
		return p.parseFunc()
	}

	// Number.
	return p.parseNumber()
}

func (p *mathParser) parseFunc() (float64, error) {
	start := p.pos
	for p.pos < len(p.input) && (unicode.IsLetter(rune(p.input[p.pos])) || unicode.IsDigit(rune(p.input[p.pos]))) {
		p.pos++
	}
	name := strings.ToLower(p.input[start:p.pos])

	// Constants.
	switch name {
	case "pi":
		return math.Pi, nil
	case "e":
		return math.E, nil
	}

	// Functions require parentheses.
	p.skipWhitespace()
	if p.pos >= len(p.input) || p.input[p.pos] != '(' {
		return 0, fmt.Errorf("unknown identifier: %q", name)
	}
	p.pos++ // skip '('

	// Parse arguments.
	args, err := p.parseArgs()
	if err != nil {
		return 0, err
	}

	switch name {
	case "sqrt":
		if len(args) != 1 {
			return 0, fmt.Errorf("sqrt requires 1 argument")
		}
		return math.Sqrt(args[0]), nil
	case "abs":
		if len(args) != 1 {
			return 0, fmt.Errorf("abs requires 1 argument")
		}
		return math.Abs(args[0]), nil
	case "floor":
		if len(args) != 1 {
			return 0, fmt.Errorf("floor requires 1 argument")
		}
		return math.Floor(args[0]), nil
	case "ceil":
		if len(args) != 1 {
			return 0, fmt.Errorf("ceil requires 1 argument")
		}
		return math.Ceil(args[0]), nil
	case "round":
		if len(args) != 1 {
			return 0, fmt.Errorf("round requires 1 argument")
		}
		return math.Round(args[0]), nil
	case "log":
		if len(args) != 1 {
			return 0, fmt.Errorf("log requires 1 argument")
		}
		return math.Log(args[0]), nil
	case "log10":
		if len(args) != 1 {
			return 0, fmt.Errorf("log10 requires 1 argument")
		}
		return math.Log10(args[0]), nil
	case "pow":
		if len(args) != 2 {
			return 0, fmt.Errorf("pow requires 2 arguments")
		}
		return math.Pow(args[0], args[1]), nil
	case "min":
		if len(args) != 2 {
			return 0, fmt.Errorf("min requires 2 arguments")
		}
		return math.Min(args[0], args[1]), nil
	case "max":
		if len(args) != 2 {
			return 0, fmt.Errorf("max requires 2 arguments")
		}
		return math.Max(args[0], args[1]), nil
	default:
		return 0, fmt.Errorf("unknown function: %q", name)
	}
}

func (p *mathParser) parseArgs() ([]float64, error) {
	var args []float64
	p.skipWhitespace()
	if p.pos < len(p.input) && p.input[p.pos] == ')' {
		p.pos++
		return args, nil
	}
	for {
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, val)
		p.skipWhitespace()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("missing closing parenthesis in function call")
		}
		if p.input[p.pos] == ')' {
			p.pos++
			return args, nil
		}
		if p.input[p.pos] != ',' {
			return nil, fmt.Errorf("expected ',' or ')' in function arguments")
		}
		p.pos++ // skip ','
	}
}

func (p *mathParser) parseNumber() (float64, error) {
	p.skipWhitespace()
	start := p.pos
	if p.pos < len(p.input) && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
		p.pos++
	}
	hasDigit := false
	for p.pos < len(p.input) && unicode.IsDigit(rune(p.input[p.pos])) {
		hasDigit = true
		p.pos++
	}
	if p.pos < len(p.input) && p.input[p.pos] == '.' {
		p.pos++
		for p.pos < len(p.input) && unicode.IsDigit(rune(p.input[p.pos])) {
			hasDigit = true
			p.pos++
		}
	}
	// Scientific notation.
	if p.pos < len(p.input) && (p.input[p.pos] == 'e' || p.input[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.input) && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
			p.pos++
		}
		for p.pos < len(p.input) && unicode.IsDigit(rune(p.input[p.pos])) {
			p.pos++
		}
	}
	if !hasDigit {
		return 0, fmt.Errorf("expected number at position %d", start)
	}
	return strconv.ParseFloat(p.input[start:p.pos], 64)
}
