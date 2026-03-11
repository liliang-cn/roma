package curia

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/romapath"
)

type ReputationRecord struct {
	AgentID          string    `json:"agent_id"`
	EffectiveWeight  int       `json:"effective_weight"`
	ReviewCount      int       `json:"review_count"`
	AlignmentCount   int       `json:"alignment_count"`
	VetoCount        int       `json:"veto_count"`
	ArbitrationCount int       `json:"arbitration_count"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ReputationStore struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

func NewReputationStore(workDir string) *ReputationStore {
	if workDir == "" {
		return nil
	}
	return &ReputationStore{
		path: romapath.Join(workDir, "curia-reputation.json"),
		now:  func() time.Time { return time.Now().UTC() },
	}
}

func (s *ReputationStore) EffectiveWeight(ctx context.Context, profile domain.AgentProfile) int {
	_ = ctx
	base := reviewerWeight(profile)
	if s == nil {
		return base
	}
	records, err := s.load()
	if err != nil {
		return base
	}
	record, ok := records[profile.ID]
	if !ok {
		return base
	}
	if record.EffectiveWeight <= 0 {
		return base
	}
	return record.EffectiveWeight
}

func (s *ReputationStore) Get(ctx context.Context, agentID string) (ReputationRecord, bool, error) {
	_ = ctx
	if s == nil {
		return ReputationRecord{}, false, nil
	}
	records, err := s.load()
	if err != nil {
		return ReputationRecord{}, false, err
	}
	record, ok := records[agentID]
	return record, ok, nil
}

func (s *ReputationStore) List(ctx context.Context) ([]ReputationRecord, error) {
	_ = ctx
	if s == nil {
		return nil, nil
	}
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]ReputationRecord, 0, len(records))
	for _, record := range records {
		out = append(out, record)
	}
	slices.SortFunc(out, func(a, b ReputationRecord) int {
		if a.AgentID < b.AgentID {
			return -1
		}
		if a.AgentID > b.AgentID {
			return 1
		}
		return 0
	})
	return out, nil
}

func (s *ReputationStore) RecordOutcome(ctx context.Context, senators []domain.AgentProfile, ballots []ballotEnvelope, selectedIDs []string, arbitrated bool) error {
	_ = ctx
	if s == nil {
		return nil
	}
	records, err := s.load()
	if err != nil {
		return err
	}
	profiles := make(map[string]domain.AgentProfile, len(senators))
	for _, senator := range senators {
		profiles[senator.ID] = senator
	}
	now := s.now()
	for _, ballot := range ballots {
		reviewerID := ballot.envelope.Producer.AgentID
		profile := profiles[reviewerID]
		record := records[reviewerID]
		record.AgentID = reviewerID
		record.ReviewCount++
		if containsString(selectedIDs, ballot.ballot.TargetProposalID) {
			record.AlignmentCount++
		}
		if ballot.ballot.Veto {
			record.VetoCount++
		}
		if arbitrated {
			record.ArbitrationCount++
		}
		record.EffectiveWeight = clampWeight(reviewerWeight(profile) + minInt(3, record.AlignmentCount/2) - minInt(2, record.VetoCount/3))
		record.UpdatedAt = now
		records[reviewerID] = record
	}
	return s.save(records)
}

func (s *ReputationStore) load() (map[string]ReputationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]ReputationRecord{}, nil
		}
		return nil, fmt.Errorf("read curia reputation: %w", err)
	}
	var records map[string]ReputationRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode curia reputation: %w", err)
	}
	if records == nil {
		records = map[string]ReputationRecord{}
	}
	return records, nil
}

func (s *ReputationStore) save(records map[string]ReputationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create curia reputation dir: %w", err)
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode curia reputation: %w", err)
	}
	if err := os.WriteFile(s.path, raw, 0o644); err != nil {
		return fmt.Errorf("write curia reputation: %w", err)
	}
	return nil
}

func clampWeight(value int) int {
	switch {
	case value < 1:
		return 1
	case value > 9:
		return 9
	default:
		return value
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
