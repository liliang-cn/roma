package domain

import (
	"testing"
	"time"
)

func TestValidateSessionRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		record  SessionRecord
		wantErr bool
	}{
		{
			name: "valid",
			record: SessionRecord{
				ID:        "sess_1",
				State:     SessionStatePending,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
		},
		{
			name: "missing id",
			record: SessionRecord{
				State: SessionStatePending,
			},
			wantErr: true,
		},
		{
			name: "invalid state",
			record: SessionRecord{
				ID:    "sess_1",
				State: SessionState("bad"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateSessionRecord(tt.record)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSessionRecord() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTaskRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		record  TaskRecord
		wantErr bool
	}{
		{
			name: "valid",
			record: TaskRecord{
				ID:        "task_1",
				SessionID: "sess_1",
				Title:     "bootstrap",
				Strategy:  TaskStrategyDirect,
				State:     TaskStatePending,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
		},
		{
			name: "missing title",
			record: TaskRecord{
				ID:        "task_1",
				SessionID: "sess_1",
				State:     TaskStatePending,
			},
			wantErr: true,
		},
		{
			name: "invalid state",
			record: TaskRecord{
				ID:        "task_1",
				SessionID: "sess_1",
				Title:     "bootstrap",
				State:     TaskState("bad"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateTaskRecord(tt.record)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTaskRecord() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
