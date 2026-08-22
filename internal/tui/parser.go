package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

var surfaceCommands = []string{"/help", "/tools", "/describe", "/context", "/trace", "/refresh", "/model", "/assistant", "setup"}

type Command struct {
	Raw       string
	Name      string
	Arguments map[string]any
	Position  []string
}

func ParseCommand(line string) (Command, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Command{}, fmt.Errorf("empty command")
	}
	words, err := splitWords(line)
	if err != nil {
		return Command{}, err
	}
	if len(words) == 0 {
		return Command{}, fmt.Errorf("empty command")
	}
	command := Command{Raw: line, Name: strings.TrimPrefix(words[0], "/"), Arguments: map[string]any{}}
	for _, word := range words[1:] {
		if key, value, ok := strings.Cut(word, "="); ok {
			if strings.TrimSpace(key) == "" {
				return Command{}, fmt.Errorf("argument name is empty")
			}
			parsed, err := parseTyped(value)
			if err != nil {
				return Command{}, fmt.Errorf("argument %s: %w", key, err)
			}
			command.Arguments[key] = parsed
			continue
		}
		command.Position = append(command.Position, word)
	}
	return command, nil
}

func (c Command) Subcommand() string {
	if len(c.Position) == 0 {
		return ""
	}
	return c.Position[0]
}

func parseTyped(value string) (any, error) {
	if strings.HasPrefix(value, "@") {
		return value, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err == nil {
		return parsed, nil
	}
	if strings.EqualFold(value, "null") {
		return nil, nil
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return nil, fmt.Errorf("numeric values must be valid JSON numbers")
	}
	return value, nil
}

func Complete(line string, catalog *Catalog) []string {
	line = strings.TrimSpace(line)
	if catalog == nil {
		return nil
	}
	if line == "" || strings.HasPrefix(line, "/") {
		prefix := line
		return filterSorted(surfaceCommands, prefix)
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return nil
	}
	if words[0] == "setup" {
		setup := []string{"setup validate", "setup graph", "setup apply", "setup status", "setup resume", "setup cancel"}
		return filterSorted(setup, line)
	}
	prefix := words[0]
	return catalog.Names(prefix)
}

// CompleteWithEntities keeps capability completion authoritative while making
// an explicit @ reference discoverable from the current authorized entity
// index. The returned values are complete typed references so accepting one
// cannot silently change the entity kind or canonical field.
func CompleteWithEntities(line string, catalog *Catalog, entities *EntityIndex) []string {
	trimmed := strings.TrimLeft(line, " \t")
	start := strings.LastIndexAny(trimmed, " \t") + 1
	token := trimmed[start:]
	entityToken := token
	argumentPrefix := ""
	if equals := strings.LastIndex(token, "="); equals >= 0 {
		argumentPrefix = token[:equals+1]
		entityToken = token[equals+1:]
	}
	if !strings.HasPrefix(entityToken, "@") || entities == nil {
		return Complete(line, catalog)
	}
	matches := entities.Search(entityToken)
	result := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, match := range matches {
		candidate := argumentPrefix + "@" + match.Entity.Kind + ":" + match.Entity.CanonicalValue
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		result = append(result, candidate)
		if len(result) == 64 {
			break
		}
	}
	return result
}

func filterSorted(values []string, prefix string) []string {
	result := make([]string, 0)
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			result = append(result, value)
		}
	}
	return result
}

func splitWords(line string) ([]string, error) {
	var words []string
	var builder strings.Builder
	quoted := rune(0)
	escaped := false
	flush := func() {
		if builder.Len() > 0 {
			words = append(words, builder.String())
			builder.Reset()
		}
	}
	for _, char := range line {
		if escaped {
			builder.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if quoted != 0 {
			if char == quoted {
				quoted = 0
			} else {
				builder.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quoted = char
			continue
		}
		if char == ' ' || char == '\t' {
			flush()
			continue
		}
		builder.WriteRune(char)
	}
	if escaped || quoted != 0 {
		return nil, fmt.Errorf("unterminated escape or quote")
	}
	flush()
	return words, nil
}
