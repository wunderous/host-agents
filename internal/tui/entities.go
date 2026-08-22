package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/wunderous/host-agents/internal/session"
)

type Entity struct {
	Kind            string
	CanonicalField  string
	CanonicalValue  string
	DisplayName     string
	Aliases         []string
	Provider        string
	Source          string
	CatalogRevision string
	ContextRevision string
}

type EntityMatch struct {
	Entity Entity
	Method string
	Score  int
}

type EntityIndex struct {
	entities []Entity
	semantic func(string, []EntityMatch) []EntityMatch
}

func NewEntityIndex(entities []Entity) *EntityIndex {
	copyEntities := append([]Entity(nil), entities...)
	sort.Slice(copyEntities, func(i, j int) bool {
		if copyEntities[i].Kind != copyEntities[j].Kind {
			return copyEntities[i].Kind < copyEntities[j].Kind
		}
		return copyEntities[i].CanonicalValue < copyEntities[j].CanonicalValue
	})
	return &EntityIndex{entities: copyEntities}
}

func (i *EntityIndex) SetSemanticRanker(ranker func(string, []EntityMatch) []EntityMatch) {
	if i != nil {
		i.semantic = ranker
	}
}

func (i *EntityIndex) Add(entity Entity) error {
	if i == nil {
		return fmt.Errorf("entity index is nil")
	}
	if strings.TrimSpace(entity.Kind) == "" || strings.TrimSpace(entity.CanonicalField) == "" || strings.TrimSpace(entity.CanonicalValue) == "" || strings.TrimSpace(entity.Source) == "" {
		return fmt.Errorf("entity kind, canonical identity, and source are required")
	}
	for _, existing := range i.entities {
		if existing.Kind == entity.Kind && existing.CanonicalField == entity.CanonicalField && existing.CanonicalValue == entity.CanonicalValue {
			return fmt.Errorf("duplicate entity %s:%s", entity.Kind, entity.CanonicalValue)
		}
	}
	i.entities = append(i.entities, entity)
	sort.Slice(i.entities, func(a, b int) bool { return i.entities[a].CanonicalValue < i.entities[b].CanonicalValue })
	return nil
}

func (i *EntityIndex) Search(token string) []EntityMatch {
	if i == nil {
		return nil
	}
	query := strings.TrimSpace(strings.TrimPrefix(token, "@"))
	kind, name := splitEntityQuery(query)
	nameLower := strings.ToLower(name)
	normalized := normalizeEntityName(name)
	result := make([]EntityMatch, 0)
	seen := make(map[string]bool)
	add := func(entity Entity, method string, score int) {
		if kind != "" && !strings.EqualFold(kind, entity.Kind) {
			return
		}
		key := entity.Kind + "\x00" + entity.CanonicalField + "\x00" + entity.CanonicalValue
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, EntityMatch{Entity: entity, Method: method, Score: score})
	}
	for _, entity := range i.entities {
		if entity.CanonicalValue == name {
			add(entity, "exact_canonical", 0)
		}
	}
	for _, entity := range i.entities {
		for _, alias := range entity.Aliases {
			if strings.EqualFold(alias, name) {
				add(entity, "exact_alias", 1)
				break
			}
		}
	}
	for _, entity := range i.entities {
		if strings.HasPrefix(strings.ToLower(entity.CanonicalValue), nameLower) || strings.HasPrefix(strings.ToLower(entity.DisplayName), nameLower) {
			add(entity, "prefix", 2)
		}
	}
	for _, entity := range i.entities {
		if normalized != "" && (normalizeEntityName(entity.CanonicalValue) == normalized || normalizeEntityName(entity.DisplayName) == normalized) {
			add(entity, "normalized", 3)
		}
	}
	if i.semantic != nil && len(result) > 0 {
		if ranked := i.semantic(query, append([]EntityMatch(nil), result...)); len(ranked) > 0 {
			result = ranked
			for index := range result {
				result[index].Method = "semantic"
			}
		}
	}
	sort.SliceStable(result, func(a, b int) bool {
		if result[a].Score != result[b].Score {
			return result[a].Score < result[b].Score
		}
		return result[a].Entity.CanonicalValue < result[b].Entity.CanonicalValue
	})
	return result
}

func (i *EntityIndex) Resolve(token string, selected int) (session.EntityReference, []EntityMatch, error) {
	matches := i.Search(token)
	if len(matches) == 0 {
		return session.EntityReference{}, nil, fmt.Errorf("no authorized entity matches %q", token)
	}
	if selected < 0 {
		if len(matches) == 1 && matches[0].Method == "exact_canonical" {
			selected = 0
		} else {
			return session.EntityReference{}, matches, fmt.Errorf("entity selection is required for %q", token)
		}
	}
	if selected >= len(matches) {
		return session.EntityReference{}, matches, fmt.Errorf("entity selection %d is outside %d matches", selected, len(matches))
	}
	match := matches[selected]
	entity := match.Entity
	return session.EntityReference{
		OriginalToken:   token,
		Kind:            entity.Kind,
		CanonicalField:  entity.CanonicalField,
		CanonicalValue:  entity.CanonicalValue,
		DisplayName:     entity.DisplayName,
		Provider:        entity.Provider,
		Source:          entity.Source,
		Selection:       match.Method,
		CatalogRevision: entity.CatalogRevision,
		ContextRevision: entity.ContextRevision,
		Evidence: []session.EvidenceItem{{
			Source: entity.Source,
			Field:  entity.CanonicalField,
			Value:  entity.CanonicalValue,
		}},
	}, matches, nil
}

func splitEntityQuery(query string) (string, string) {
	if kind, name, ok := strings.Cut(query, ":"); ok {
		return strings.TrimSpace(kind), strings.TrimSpace(name)
	}
	return "", query
}

func normalizeEntityName(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
