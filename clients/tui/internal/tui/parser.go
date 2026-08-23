package tui

import (
	"fmt"
	"strings"
)

// Command is the presentation-neutral parsed form of a typed command. The
// parser does not infer operations from prose; completion and validation use
// the current catalog after parsing.
type Command struct {
	Operation string
	Arguments map[string]DraftValue
}

func ParseCommand(input string) (Command, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return Command{}, err
	}
	if len(tokens) == 0 || strings.TrimSpace(tokens[0]) == "" {
		return Command{}, fmt.Errorf("command is empty")
	}
	command := Command{Operation: tokens[0], Arguments: map[string]DraftValue{}}
	for _, token := range tokens[1:] {
		key, value, ok := strings.Cut(token, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return Command{}, fmt.Errorf("argument %q must use key=value", token)
		}
		command.Arguments[key] = ParseValue(value)
	}
	return command, nil
}

func tokenize(input string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, char := range input {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' {
			flush()
			continue
		}
		current.WriteRune(char)
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated quoted or escaped command")
	}
	flush()
	return tokens, nil
}
