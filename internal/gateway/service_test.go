package gateway

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/plans"
	"github.com/liliang/roma/internal/store"
	workspacepkg "github.com/liliang/roma/internal/workspace"
)

type fakeAdapter struct {
	typ       domain.GatewayEndpointType
	delivered []string
	fail      bool
}

func (f *fakeAdapter) Type() domain.GatewayEndpointType {
	return f.typ
}

func (f *fakeAdapter) Deliver(_ context.Context, _ domain.GatewayEndpoint, notification domain.NotificationEnvelope) error {
	if f.fail {
		return fmt.Errorf("delivery failed")
	}
	f.delivered = append(f.delivered, notification.ID)
	return nil
}

func TestServiceDeliverAndAudit(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	adapter := &fakeAdapter{typ: domain.GatewayEndpointTypeTelegram}
	svc := NewService(mem, nil, adapter)
	ctx := context.Background()

	err := svc.RegisterEndpoint(ctx, domain.GatewayEndpoint{
		ID:             "gw_1",
		Type:           domain.GatewayEndpointTypeTelegram,
		Enabled:        true,
		Target:         "chat:1",
		AllowedActions: []domain.RemoteCommandAction{domain.RemoteCommandActionApprove},
	}, domain.RemoteSubscription{
		EndpointID:        "gw_1",
		EventTypes:        []string{"approval_required"},
		SeverityThreshold: domain.NotificationSeverityMedium,
	})
	if err != nil {
		t.Fatalf("RegisterEndpoint() error = %v", err)
	}

	err = svc.Deliver(ctx, domain.NotificationEnvelope{
		ID:        "notif_1",
		Type:      "approval_required",
		Severity:  domain.NotificationSeverityHigh,
		SessionID: "sess_1",
		Title:     "Approval",
		Summary:   "Need approval",
		Actions:   []domain.RemoteCommandAction{domain.RemoteCommandActionApprove},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if len(adapter.delivered) != 1 {
		t.Fatalf("delivered count = %d, want 1", len(adapter.delivered))
	}

	events, err := mem.ListEvents(ctx, store.EventFilter{SessionID: "sess_1"})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("ListEvents() returned no events")
	}
}

func TestServiceRejectsDisallowedRemoteCommand(t *testing.T) {
	t.Parallel()

	mem := store.NewMemoryStore()
	svc := NewService(mem, nil)
	ctx := context.Background()

	err := svc.RegisterEndpoint(ctx, domain.GatewayEndpoint{
		ID:             "gw_1",
		Type:           domain.GatewayEndpointTypeTelegram,
		Enabled:        true,
		Target:         "chat:1",
		AllowedActions: []domain.RemoteCommandAction{domain.RemoteCommandActionApprove},
	}, domain.RemoteSubscription{
		EndpointID:        "gw_1",
		SeverityThreshold: domain.NotificationSeverityLow,
	})
	if err != nil {
		t.Fatalf("RegisterEndpoint() error = %v", err)
	}

	err = svc.SubmitRemoteCommand(ctx, domain.RemoteCommand{
		CommandID:        "rcmd_1",
		SourceEndpointID: "gw_1",
		Actor:            "user:leo",
		SessionID:        "sess_1",
		Action:           domain.RemoteCommandActionCancel,
		IssuedAt:         time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("SubmitRemoteCommand() error = nil, want error")
	}
}

func TestServicePlanRemoteCommandBridgesToPlanApproval(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initGatewayGitRepo(t, root)

	mem := store.NewMemoryStore()
	manager := workspacepkg.NewManager(root, mem)
	prepared, err := manager.Prepare(context.Background(), "sess_plan", "art_plan_task", root, domain.TaskStrategyDirect)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if err := os.WriteFile(prepared.EffectiveDir+"/README.md", []byte("approved\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	artifactStore := artifacts.NewFileStore(root)
	artifactSvc := artifacts.NewService()
	envelope, err := artifactSvc.BuildExecutionPlan(context.Background(), artifacts.BuildExecutionPlanRequest{
		SessionID: "sess_plan",
		TaskID:    "art_plan_task",
		RunID:     "art_plan_task",
		Goal:      "approve remotely",
		Proposal: artifacts.ProposalPayload{
			ProposalID:     "prop_remote",
			Summary:        "Change README",
			EstimatedSteps: []string{"Edit README"},
			AffectedFiles:  []string{"README.md"},
		},
		HumanApprovalRequired: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionPlan() error = %v", err)
	}
	payload, _ := artifacts.ExecutionPlanFromEnvelope(envelope)
	payload.RequiredChecks = nil
	envelope.Payload = payload
	if err := artifactStore.Save(context.Background(), envelope); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	planSvc := plans.NewService(artifactStore, manager, mem)
	svc := NewService(mem, planSvc)
	ctx := context.Background()
	err = svc.RegisterEndpoint(ctx, domain.GatewayEndpoint{
		ID:             "gw_1",
		Type:           domain.GatewayEndpointTypeTelegram,
		Enabled:        true,
		Target:         "chat:1",
		AllowedActions: []domain.RemoteCommandAction{domain.RemoteCommandActionPlanApprove},
	}, domain.RemoteSubscription{
		EndpointID:        "gw_1",
		SeverityThreshold: domain.NotificationSeverityLow,
	})
	if err != nil {
		t.Fatalf("RegisterEndpoint() error = %v", err)
	}

	if err := svc.SubmitRemoteCommand(ctx, domain.RemoteCommand{
		CommandID:        "rcmd_plan_1",
		SourceEndpointID: "gw_1",
		Actor:            "local_owner",
		SessionID:        "sess_plan",
		TaskID:           envelope.ID,
		Action:           domain.RemoteCommandActionPlanApprove,
		IssuedAt:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SubmitRemoteCommand() error = %v", err)
	}

	result, err := planSvc.Apply(ctx, "sess_plan", "art_plan_task", envelope.ID, plans.ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply() after remote approval error = %v", err)
	}
	if !result.Applied {
		t.Fatalf("result = %#v, want applied", result)
	}
}

func initGatewayGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGatewayGit(t, dir, "init")
	runGatewayGit(t, dir, "config", "user.email", "roma@example.com")
	runGatewayGit(t, dir, "config", "user.name", "ROMA")
	if err := os.WriteFile(dir+"/README.md", []byte("roma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGatewayGit(t, dir, "add", "README.md")
	runGatewayGit(t, dir, "commit", "-m", "init")
}

func runGatewayGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v (%s)", args, err, string(output))
	}
}
