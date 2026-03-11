package domain

import "fmt"

// TaskGraph is the scheduler input graph for a session.
type TaskGraph struct {
	Nodes []TaskNodeSpec `json:"nodes"`
}

// Validate checks the graph for minimal correctness.
func (g TaskGraph) Validate() error {
	if len(g.Nodes) == 0 {
		return fmt.Errorf("task graph must contain at least one node")
	}

	seen := make(map[string]struct{}, len(g.Nodes))
	for _, node := range g.Nodes {
		if node.ID == "" {
			return fmt.Errorf("task node id is required")
		}
		if _, exists := seen[node.ID]; exists {
			return fmt.Errorf("duplicate task node id %q", node.ID)
		}
		seen[node.ID] = struct{}{}
	}

	for _, node := range g.Nodes {
		for _, dep := range node.Dependencies {
			if _, exists := seen[dep]; !exists {
				return fmt.Errorf("task %q depends on unknown task %q", node.ID, dep)
			}
		}
	}

	return nil
}
