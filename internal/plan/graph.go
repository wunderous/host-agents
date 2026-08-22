package plan

import (
	"fmt"
	"sort"
)

func ValidateGraph(doc Document) error {
	graph, err := buildGraph(doc)
	if err != nil {
		return err
	}
	indegree := cloneIndegree(graph.indegree)
	queue := make([]string, 0, len(graph.nodes))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	processed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		processed++
		for _, dependent := range graph.dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if processed != len(graph.nodes) {
		return fmt.Errorf("plan graph contains a cycle")
	}
	return nil
}

func TopologicalLevels(doc Document) ([][]Node, error) {
	graph, err := buildGraph(doc)
	if err != nil {
		return nil, err
	}
	nodes := graph.nodes
	indegree := graph.indegree
	dependents := graph.dependents
	levels := make([][]Node, 0)
	remaining := len(nodes)
	for remaining > 0 {
		levelIDs := make([]string, 0)
		for id, degree := range indegree {
			if degree == 0 {
				levelIDs = append(levelIDs, id)
			}
		}
		if len(levelIDs) == 0 {
			return nil, fmt.Errorf("plan graph contains a cycle")
		}
		sort.Strings(levelIDs)
		level := make([]Node, 0, len(levelIDs))
		for _, id := range levelIDs {
			level = append(level, nodes[id])
			delete(indegree, id)
			remaining--
		}
		for _, id := range levelIDs {
			for _, dependent := range dependents[id] {
				indegree[dependent]--
			}
		}
		levels = append(levels, level)
	}
	return levels, nil
}

type graphData struct {
	nodes      map[string]Node
	indegree   map[string]int
	dependents map[string][]string
}

func buildGraph(doc Document) (graphData, error) {
	graph := graphData{
		nodes:      make(map[string]Node, len(doc.Nodes)),
		indegree:   make(map[string]int, len(doc.Nodes)),
		dependents: make(map[string][]string, len(doc.Nodes)),
	}
	for _, node := range doc.Nodes {
		if _, exists := graph.nodes[node.ID]; exists {
			return graphData{}, fmt.Errorf("duplicate node id %q", node.ID)
		}
		graph.nodes[node.ID] = node
		graph.indegree[node.ID] = 0
	}
	for _, node := range doc.Nodes {
		seen := make(map[string]bool, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			if _, exists := graph.nodes[dependency]; !exists {
				return graphData{}, fmt.Errorf("node %q depends on unknown node %q", node.ID, dependency)
			}
			if seen[dependency] {
				return graphData{}, fmt.Errorf("node %q repeats dependency %q", node.ID, dependency)
			}
			seen[dependency] = true
			graph.indegree[node.ID]++
			graph.dependents[dependency] = append(graph.dependents[dependency], node.ID)
		}
	}
	return graph, nil
}

func cloneIndegree(indegree map[string]int) map[string]int {
	clone := make(map[string]int, len(indegree))
	for id, degree := range indegree {
		clone[id] = degree
	}
	return clone
}

func ReverseTopologicalNodes(doc Document) ([]Node, error) {
	levels, err := TopologicalLevels(doc)
	if err != nil {
		return nil, err
	}
	result := make([]Node, 0, len(doc.Nodes))
	for level := len(levels) - 1; level >= 0; level-- {
		for index := len(levels[level]) - 1; index >= 0; index-- {
			result = append(result, levels[level][index])
		}
	}
	return result, nil
}
