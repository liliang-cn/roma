package domain

import "testing"

func TestTaskGraphValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		graph   TaskGraph
		wantErr bool
	}{
		{
			name: "valid",
			graph: TaskGraph{
				Nodes: []TaskNodeSpec{
					{ID: "task_a", Title: "A", Strategy: TaskStrategyDirect},
					{ID: "task_b", Title: "B", Strategy: TaskStrategyRelay, Dependencies: []string{"task_a"}},
				},
			},
		},
		{
			name:    "empty",
			graph:   TaskGraph{},
			wantErr: true,
		},
		{
			name: "missing dependency",
			graph: TaskGraph{
				Nodes: []TaskNodeSpec{
					{ID: "task_b", Title: "B", Strategy: TaskStrategyRelay, Dependencies: []string{"task_a"}},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.graph.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
