package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/liliang/roma/internal/agents"
	"github.com/liliang/roma/internal/api"
	"github.com/liliang/roma/internal/artifacts"
	"github.com/liliang/roma/internal/domain"
	"github.com/liliang/roma/internal/events"
	"github.com/liliang/roma/internal/history"
	"github.com/liliang/roma/internal/policy"
	"github.com/liliang/roma/internal/queue"
	"github.com/liliang/roma/internal/replay"
	runsvc "github.com/liliang/roma/internal/run"
	"github.com/liliang/roma/internal/scheduler"
	"github.com/liliang/roma/internal/sqliteutil"
	storepkg "github.com/liliang/roma/internal/store"
	"github.com/liliang/roma/internal/syncdb"
	"github.com/liliang/roma/internal/taskstore"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	registry, err := agents.DefaultRegistry()
	if err != nil {
		return fmt.Errorf("load default agent registry: %w", err)
	}

	switch args[0] {
	case "approve":
		return runQueueDecision(ctx, true, args[1:])
	case "agents":
		return runAgents(ctx, registry, args[1:])
	case "artifacts":
		return runArtifacts(ctx, args[1:])
	case "graph":
		return runGraph(ctx, registry, args[1:])
	case "events":
		return runEvents(ctx, args[1:])
	case "policy":
		return runPolicy(ctx, args[1:])
	case "queue":
		return runQueue(ctx, args[1:])
	case "replay":
		return runReplay(ctx, args[1:])
	case "recover":
		return runRecover(ctx, args[1:])
	case "reject":
		return runQueueDecision(ctx, false, args[1:])
	case "status":
		return runStatus(ctx)
	case "submit":
		return runSubmit(ctx, args[1:])
	case "sessions":
		return runSessions(ctx, args[1:])
	case "tasks":
		return runTasks(ctx, args[1:])
	case "run":
		return runPrompt(ctx, registry, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return runPrompt(ctx, registry, args)
	}
}

func runAgents(ctx context.Context, registry *agents.Registry, args []string) error {
	if len(args) == 0 || args[0] == "list" {
		profiles := registry.WithResolvedAvailability(ctx)
		fmt.Println("ID\tNAME\tCOMMAND\tAVAILABILITY\tCAPABILITIES")
		for _, profile := range profiles {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\n",
				profile.ID,
				profile.DisplayName,
				profile.Command,
				profile.Availability,
				strings.Join(profile.Capabilities, ","),
			)
		}
		return nil
	}

	return fmt.Errorf("unknown agents subcommand %q", args[0])
}

func runPrompt(ctx context.Context, registry *agents.Registry, args []string) error {
	req, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	if req.StarterAgent == "" {
		req.StarterAgent = "codex"
	}
	if req.WorkingDir == "" {
		req.WorkingDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	client := api.NewClient(req.WorkingDir)
	if client.Available() {
		resp, err := client.Submit(ctx, api.SubmitRequest{
			GraphFile:    "",
			Prompt:       req.Prompt,
			StarterAgent: req.StarterAgent,
			Delegates:    req.Delegates,
			WorkingDir:   req.WorkingDir,
			Continuous:   req.Continuous,
			MaxRounds:    req.MaxRounds,
		})
		if err != nil {
			return err
		}
		fmt.Printf("submitted to daemon id=%s agent=%s delegates=%s\n", resp.JobID, req.StarterAgent, strings.Join(req.Delegates, ","))
		return nil
	}

	svc := runsvc.NewService(registry)
	return svc.Run(ctx, req)
}

func runGraph(ctx context.Context, registry *agents.Registry, args []string) error {
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("unknown graph subcommand")
	}
	var filePath string
	var workingDir string
	var continuous bool
	var maxRounds int
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--file":
			i++
			if i >= len(args) {
				return fmt.Errorf("--file requires a value")
			}
			filePath = args[i]
		case "--cwd":
			i++
			if i >= len(args) {
				return fmt.Errorf("--cwd requires a value")
			}
			workingDir = args[i]
		case "--continuous":
			continuous = true
		case "--max-rounds":
			i++
			if i >= len(args) {
				return fmt.Errorf("--max-rounds requires a value")
			}
			n, convErr := strconv.Atoi(args[i])
			if convErr != nil || n <= 0 {
				return fmt.Errorf("--max-rounds requires a positive integer")
			}
			maxRounds = n
		default:
			return fmt.Errorf("unknown graph run argument %q", args[i])
		}
	}
	if filePath == "" {
		return fmt.Errorf("graph file is required")
	}
	graphReq, err := runsvc.LoadGraphRequest(filePath)
	if err != nil {
		return err
	}
	if workingDir != "" {
		graphReq.WorkingDir = workingDir
	}
	graphReq.Continuous = continuous
	graphReq.MaxRounds = maxRounds
	if graphReq.WorkingDir == "" {
		graphReq.WorkingDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	client := api.NewClient(graphReq.WorkingDir)
	if client.Available() {
		nodes := make([]api.GraphSubmitNode, 0, len(graphReq.Nodes))
		for _, node := range graphReq.Nodes {
			nodes = append(nodes, api.GraphSubmitNode{
				ID:           node.ID,
				Title:        node.Title,
				Agent:        node.Agent,
				Strategy:     string(node.Strategy),
				Dependencies: node.Dependencies,
			})
		}
		resp, err := client.Submit(ctx, api.SubmitRequest{
			GraphFile: "",
			Graph: &api.GraphSubmitRequest{
				Prompt: graphReq.Prompt,
				Nodes:  nodes,
			},
			Prompt:     graphReq.Prompt,
			WorkingDir: graphReq.WorkingDir,
			Continuous: graphReq.Continuous,
			MaxRounds:  graphReq.MaxRounds,
		})
		if err != nil {
			return err
		}
		fmt.Printf("submitted graph to daemon id=%s nodes=%d\n", resp.JobID, len(nodes))
		return nil
	}

	svc := runsvc.NewService(registry)
	return svc.RunGraph(ctx, graphReq, os.Stdout)
}

func runSubmit(ctx context.Context, args []string) error {
	req, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	if req.StarterAgent == "" {
		req.StarterAgent = "codex"
	}
	wd := req.WorkingDir
	if wd == "" {
		wd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	store := preferredQueueStore(wd)
	client := api.NewClient(wd)
	if client.Available() {
		resp, err := client.Submit(ctx, api.SubmitRequest{
			GraphFile:    "",
			Prompt:       req.Prompt,
			StarterAgent: req.StarterAgent,
			Delegates:    req.Delegates,
			WorkingDir:   wd,
			Continuous:   req.Continuous,
			MaxRounds:    req.MaxRounds,
		})
		if err != nil {
			return err
		}
		fmt.Printf("queued via daemon id=%s agent=%s delegates=%s\n", resp.JobID, req.StarterAgent, strings.Join(req.Delegates, ","))
		return nil
	}

	id := fmt.Sprintf("job_%d", time.Now().UTC().UnixNano())
	record := queue.Request{
		ID:           id,
		GraphFile:    "",
		Prompt:       req.Prompt,
		StarterAgent: req.StarterAgent,
		Delegates:    req.Delegates,
		WorkingDir:   wd,
		Continuous:   req.Continuous,
		MaxRounds:    req.MaxRounds,
	}
	if err := store.Enqueue(ctx, record); err != nil {
		return err
	}
	fmt.Printf("queued id=%s agent=%s delegates=%s\n", id, req.StarterAgent, strings.Join(req.Delegates, ","))
	return nil
}

func runQueue(ctx context.Context, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	store := preferredQueueStore(wd)
	client := api.NewClient(wd)
	statusFilter, modeFilter, subcommand, subArg, err := parseQueueArgs(args)
	if err != nil {
		return err
	}

	if client.Available() && subcommand == "list" {
		requests, err := client.QueueList(ctx)
		if err != nil {
			return err
		}
		requests = filterQueueRequests(requests, statusFilter, modeFilter)
		fmt.Println("ID\tAGENT\tSTATUS\tCREATED\tERROR")
		for _, req := range requests {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\n",
				req.ID,
				queueLabel(req),
				req.Status,
				req.CreatedAt.Format("2006-01-02T15:04:05Z"),
				req.Error,
			)
		}
		return nil
	}

	if client.Available() && subcommand == "show" {
		req, err := client.QueueGet(ctx, subArg)
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(req, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal queue request: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if client.Available() && subcommand == "inspect" {
		resp, err := client.QueueInspect(ctx, subArg)
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal queue inspect response: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if subcommand == "list" {
		requests, err := store.List(ctx)
		if err != nil {
			return err
		}
		requests = filterQueueRequests(requests, statusFilter, modeFilter)
		fmt.Println("ID\tAGENT\tSTATUS\tCREATED\tERROR")
		for _, req := range requests {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\n",
				req.ID,
				queueLabel(req),
				req.Status,
				req.CreatedAt.Format("2006-01-02T15:04:05Z"),
				req.Error,
			)
		}
		return nil
	}

	if subcommand == "show" {
		req, err := store.Get(ctx, subArg)
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(req, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal queue request: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if subcommand == "inspect" {
		req, err := store.Get(ctx, subArg)
		if err != nil {
			return err
		}
		resp := api.QueueInspectResponse{Job: req}
		if req.SessionID != "" {
			sessionStore := preferredHistoryStore(wd)
			if session, err := sessionStore.Get(ctx, req.SessionID); err == nil {
				resp.Session = &session
			}
			taskStore := preferredTaskStore(wd)
			if items, err := taskStore.ListTasksBySession(ctx, req.SessionID); err == nil {
				resp.Tasks = items
			}
			artifactStore := preferredArtifactStore(wd)
			if items, err := artifactStore.List(ctx, req.SessionID); err == nil {
				resp.Artifacts = items
			}
			eventStore := preferredEventStore(wd)
			if items, err := eventStore.ListEvents(ctx, storepkg.EventFilter{SessionID: req.SessionID}); err == nil {
				resp.Events = items
			}
		}
		raw, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal queue inspect response: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	return fmt.Errorf("unknown queue subcommand %q", subcommand)
}

func runQueueDecision(ctx context.Context, approved bool, args []string) error {
	if len(args) == 0 {
		if approved {
			return fmt.Errorf("roma approve requires a job id")
		}
		return fmt.Errorf("roma reject requires a job id")
	}
	jobID := args[0]
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)

	client := api.NewClient(wd)
	if client.Available() {
		var item queue.Request
		if approved {
			item, err = client.QueueApprove(ctx, jobID)
		} else {
			item, err = client.QueueReject(ctx, jobID)
		}
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal queue decision: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	queueStore := preferredQueueStore(wd)
	item, err := queueStore.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if approved {
		item.PolicyOverride = true
		item.Status = queue.StatusPending
		item.Error = ""
	} else {
		item.PolicyOverride = false
		item.Status = queue.StatusRejected
		item.Error = "rejected by user"
	}
	if err := queueStore.Update(ctx, item); err != nil {
		return err
	}
	if item.SessionID != "" {
		sessionStore := preferredHistoryStore(wd)
		if session, err := sessionStore.Get(ctx, item.SessionID); err == nil {
			if approved {
				session.Status = "pending"
			} else {
				session.Status = "rejected"
			}
			session.UpdatedAt = time.Now().UTC()
			_ = sessionStore.Save(ctx, session)
		}
		eventStore := preferredEventStore(wd)
		reason := "human_approved"
		if !approved {
			reason = "human_rejected"
		}
		_ = eventStore.AppendEvent(ctx, events.Record{
			ID:         "evt_" + item.ID + "_" + reason,
			SessionID:  item.SessionID,
			TaskID:     item.TaskID,
			Type:       events.TypePolicyDecisionRecorded,
			ActorType:  events.ActorTypeHuman,
			OccurredAt: time.Now().UTC(),
			ReasonCode: reason,
			Payload: map[string]any{
				"job_id":   item.ID,
				"approved": approved,
			},
		})
	}
	raw, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal queue decision: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func runArtifacts(ctx context.Context, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	store := preferredArtifactStore(wd)
	client := api.NewClient(wd)

	if client.Available() && (len(args) == 0 || args[0] == "list") {
		sessionID := ""
		if len(args) > 2 && args[1] == "--session" {
			sessionID = args[2]
		}
		envelopes, err := client.ArtifactList(ctx, sessionID)
		if err != nil {
			return err
		}
		fmt.Println("ID\tKIND\tSESSION\tTASK\tPRODUCER\tCREATED")
		for _, envelope := range envelopes {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				envelope.ID,
				envelope.Kind,
				envelope.SessionID,
				envelope.TaskID,
				envelope.Producer.AgentID,
				envelope.CreatedAt.Format("2006-01-02T15:04:05Z"),
			)
		}
		return nil
	}

	if len(args) == 0 || args[0] == "list" {
		sessionID := ""
		if len(args) > 2 && args[1] == "--session" {
			sessionID = args[2]
		}
		envelopes, err := store.List(ctx, sessionID)
		if err != nil {
			return err
		}
		fmt.Println("ID\tKIND\tSESSION\tTASK\tPRODUCER\tCREATED")
		for _, envelope := range envelopes {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				envelope.ID,
				envelope.Kind,
				envelope.SessionID,
				envelope.TaskID,
				envelope.Producer.AgentID,
				envelope.CreatedAt.Format("2006-01-02T15:04:05Z"),
			)
		}
		return nil
	}

	if client.Available() && len(args) > 1 && args[0] == "show" {
		envelope, err := client.ArtifactGet(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal artifact: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if args[0] == "show" {
		if len(args) < 2 {
			return fmt.Errorf("roma artifacts show requires an artifact id")
		}
		envelope, err := store.Get(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal artifact: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	return fmt.Errorf("unknown artifacts subcommand %q", args[0])
}

func runSessions(ctx context.Context, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	store := preferredHistoryStore(wd)
	client := api.NewClient(wd)

	if client.Available() && (len(args) == 0 || args[0] == "list") {
		records, err := client.SessionList(ctx)
		if err != nil {
			return err
		}
		fmt.Println("ID\tTASK\tSTARTER\tSTATUS\tCREATED")
		for _, record := range records {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\n",
				record.ID,
				record.TaskID,
				record.Starter,
				record.Status,
				record.CreatedAt.Format("2006-01-02T15:04:05Z"),
			)
		}
		return nil
	}

	if client.Available() && len(args) > 1 && args[0] == "show" {
		record, err := client.SessionGet(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal session: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if len(args) == 0 || args[0] == "list" {
		records, err := store.List(ctx)
		if err != nil {
			return err
		}
		fmt.Println("ID\tTASK\tSTARTER\tSTATUS\tCREATED")
		for _, record := range records {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\n",
				record.ID,
				record.TaskID,
				record.Starter,
				record.Status,
				record.CreatedAt.Format("2006-01-02T15:04:05Z"),
			)
		}
		return nil
	}

	if args[0] == "show" {
		if len(args) < 2 {
			return fmt.Errorf("roma sessions show requires a session id")
		}
		record, err := store.Get(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal session: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	return fmt.Errorf("unknown sessions subcommand %q", args[0])
}

func runStatus(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)

	client := api.NewClient(wd)
	queueStore := preferredQueueStore(wd)
	sessionStore := preferredHistoryStore(wd)
	artifactStore := preferredArtifactStore(wd)
	eventStore := preferredEventStore(wd)

	daemonMode := "filesystem-fallback"
	queueCount := 0
	sessionCount := 0
	artifactCount := 0
	eventCount := 0
	sqlitePath := sqliteutil.DBPath(wd)
	sqliteBytes := int64(0)
	sqliteEnabled := false

	if client.Available() {
		daemonMode = "daemon-api"
		if items, err := client.QueueList(ctx); err == nil {
			queueCount = len(items)
		}
		if items, err := client.SessionList(ctx); err == nil {
			sessionCount = len(items)
		}
		if items, err := client.ArtifactList(ctx, ""); err == nil {
			artifactCount = len(items)
		}
	} else {
		if items, err := queueStore.List(ctx); err == nil {
			queueCount = len(items)
		}
		if items, err := sessionStore.List(ctx); err == nil {
			sessionCount = len(items)
		}
		if items, err := artifactStore.List(ctx, ""); err == nil {
			artifactCount = len(items)
		}
	}
	if items, err := eventStore.ListEvents(ctx, storepkg.EventFilter{}); err == nil {
		eventCount = len(items)
	}
	if info, err := os.Stat(sqlitePath); err == nil {
		sqliteEnabled = true
		sqliteBytes = info.Size()
	}

	fmt.Printf("mode=%s\n", daemonMode)
	fmt.Printf("queue_items=%d\n", queueCount)
	fmt.Printf("sessions=%d\n", sessionCount)
	fmt.Printf("artifacts=%d\n", artifactCount)
	fmt.Printf("events=%d\n", eventCount)
	fmt.Printf("sqlite_enabled=%t\n", sqliteEnabled)
	fmt.Printf("sqlite_path=%s\n", filepath.Clean(sqlitePath))
	fmt.Printf("sqlite_bytes=%d\n", sqliteBytes)
	return nil
}

func runReplay(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("roma replay requires a session id")
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	client := api.NewClient(wd)
	eventStore := preferredEventStore(wd)

	var snapshot replay.SessionSnapshot
	if client.Available() {
		items, err := client.EventList(ctx, args[0], "", "")
		if err != nil {
			return err
		}
		snapshot = replay.RebuildSessionSnapshot(args[0], items)
	} else {
		snapshot, err = replay.NewService(eventStore).ReplaySession(ctx, args[0])
		if err != nil {
			return err
		}
	}

	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal replay snapshot: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func runRecover(ctx context.Context, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	items, err := scheduler.RecoverableSessions(ctx, wd)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery snapshot: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func runPolicy(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "check" {
		return fmt.Errorf("unknown policy subcommand")
	}
	req, err := parseRunArgs(args[1:])
	if err != nil {
		return err
	}
	if req.StarterAgent == "" {
		req.StarterAgent = "codex"
	}
	if req.WorkingDir == "" {
		req.WorkingDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	decision, err := policy.NewSimpleBroker(nil).Evaluate(ctx, policy.Request{
		SessionID:    "policy_check",
		TaskID:       "policy_check",
		Mode:         "direct",
		Prompt:       req.Prompt,
		WorkingDir:   req.WorkingDir,
		StarterAgent: req.StarterAgent,
		Delegates:    req.Delegates,
		NodeCount:    1 + len(req.Delegates),
	})
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(decision, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal policy decision: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func runTasks(ctx context.Context, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	taskStore := preferredTaskStore(wd)
	client := api.NewClient(wd)

	if client.Available() && (len(args) == 0 || args[0] == "list") {
		sessionID := ""
		if len(args) > 2 && args[1] == "--session" {
			sessionID = args[2]
		}
		items, err := client.TaskList(ctx, sessionID)
		if err != nil {
			return err
		}
		fmt.Println("ID\tSESSION\tSTATE\tSTRATEGY\tAGENT\tARTIFACT")
		for _, item := range items {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				item.ID,
				item.SessionID,
				item.State,
				item.Strategy,
				item.AgentID,
				item.ArtifactID,
			)
		}
		return nil
	}

	if client.Available() && len(args) > 1 && args[0] == "show" {
		item, err := client.TaskGet(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal task: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if client.Available() && len(args) > 1 && (args[0] == "approve" || args[0] == "reject") {
		var item domain.TaskRecord
		if args[0] == "approve" {
			item, err = client.TaskApprove(ctx, args[1])
		} else {
			item, err = client.TaskReject(ctx, args[1])
		}
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal task: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if len(args) == 0 || args[0] == "list" {
		sessionID := ""
		if len(args) > 2 && args[1] == "--session" {
			sessionID = args[2]
		}
		items, err := taskStore.ListTasksBySession(ctx, sessionID)
		if err != nil {
			return err
		}
		fmt.Println("ID\tSESSION\tSTATE\tSTRATEGY\tAGENT\tARTIFACT")
		for _, item := range items {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				item.ID,
				item.SessionID,
				item.State,
				item.Strategy,
				item.AgentID,
				item.ArtifactID,
			)
		}
		return nil
	}

	if args[0] == "show" {
		if len(args) < 2 {
			return fmt.Errorf("roma tasks show requires a task id")
		}
		item, err := taskStore.GetTask(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal task: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if args[0] == "approve" || args[0] == "reject" {
		if len(args) < 2 {
			return fmt.Errorf("roma tasks %s requires a task id", args[0])
		}
		lifecycle := scheduler.NewGraphLifecycle(taskStore, preferredEventStore(wd))
		if args[0] == "approve" {
			err = lifecycle.ApproveTask(ctx, args[1])
		} else {
			err = lifecycle.RejectTask(ctx, args[1])
		}
		if err != nil {
			return err
		}
		item, err := taskStore.GetTask(ctx, args[1])
		if err != nil {
			return err
		}
		sessionStore := preferredHistoryStore(wd)
		if session, err := sessionStore.Get(ctx, item.SessionID); err == nil {
			if args[0] == "approve" {
				session.Status = "running"
			} else {
				session.Status = "failed"
			}
			session.UpdatedAt = time.Now().UTC()
			_ = sessionStore.Save(ctx, session)
		}
		queueStore := preferredQueueStore(wd)
		if requests, err := queueStore.List(ctx); err == nil {
			for _, req := range requests {
				if req.SessionID != item.SessionID || req.Status != queue.StatusAwaitingApproval {
					continue
				}
				if args[0] == "approve" {
					req.Status = queue.StatusPending
					req.Error = ""
				} else {
					req.Status = queue.StatusRejected
					req.Error = "task approval rejected"
				}
				_ = queueStore.Update(ctx, req)
			}
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal task: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	return fmt.Errorf("unknown tasks subcommand %q", args[0])
}

func runEvents(ctx context.Context, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)

	eventStore := preferredEventStore(wd)
	client := api.NewClient(wd)
	filter := storepkg.EventFilter{}

	if len(args) == 0 || args[0] == "list" {
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--session":
				i++
				if i >= len(args) {
					return fmt.Errorf("--session requires a value")
				}
				filter.SessionID = args[i]
			case "--task":
				i++
				if i >= len(args) {
					return fmt.Errorf("--task requires a value")
				}
				filter.TaskID = args[i]
			case "--type":
				i++
				if i >= len(args) {
					return fmt.Errorf("--type requires a value")
				}
				filter.Type = events.Type(args[i])
			default:
				return fmt.Errorf("unknown events list argument %q", args[i])
			}
		}

		var records []events.Record
		if client.Available() {
			records, err = client.EventList(ctx, filter.SessionID, filter.TaskID, filter.Type)
		} else {
			records, err = eventStore.ListEvents(ctx, filter)
		}
		if err != nil {
			return err
		}
		fmt.Println("ID\tTYPE\tSESSION\tTASK\tACTOR\tTIME\tREASON")
		for _, record := range records {
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				record.ID,
				record.Type,
				record.SessionID,
				record.TaskID,
				record.ActorType,
				record.OccurredAt.Format("2006-01-02T15:04:05Z"),
				record.ReasonCode,
			)
		}
		return nil
	}

	if args[0] == "show" {
		if len(args) < 2 {
			return fmt.Errorf("roma events show requires an event id")
		}
		if client.Available() {
			record, err := client.EventGet(ctx, args[1])
			if err != nil {
				return err
			}
			raw, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal event: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		records, err := eventStore.ListEvents(ctx, storepkg.EventFilter{})
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.ID != args[1] {
				continue
			}
			raw, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal event: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		return fmt.Errorf("event %q not found", args[1])
	}

	return fmt.Errorf("unknown events subcommand %q", args[0])
}

func preferredHistoryStore(workDir string) history.Backend {
	sqliteStore, err := history.NewSQLiteStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return history.NewStore(workDir)
}

func preferredEventStore(workDir string) storepkg.EventStore {
	sqliteStore, err := storepkg.NewSQLiteEventStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return storepkg.NewFileEventStore(workDir)
}

func preferredTaskStore(workDir string) storepkg.TaskStore {
	sqliteStore, err := taskstore.NewSQLiteStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return taskstore.NewStore(workDir)
}

func preferredArtifactStore(workDir string) artifacts.Backend {
	sqliteStore, err := artifacts.NewSQLiteStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return artifacts.NewFileStore(workDir)
}

func preferredQueueStore(workDir string) queue.Backend {
	sqliteStore, err := queue.NewSQLiteStore(workDir)
	if err == nil {
		return sqliteStore
	}
	return queue.NewStore(workDir)
}

func syncWorkspace(ctx context.Context, workDir string) error {
	return syncdb.NewWorkspace(workDir).Run(ctx)
}

func parseRunArgs(args []string) (runsvc.Request, error) {
	req := runsvc.Request{}
	promptParts := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			i++
			if i >= len(args) {
				return runsvc.Request{}, fmt.Errorf("--agent requires a value")
			}
			req.StarterAgent = args[i]
		case "--cwd":
			i++
			if i >= len(args) {
				return runsvc.Request{}, fmt.Errorf("--cwd requires a value")
			}
			req.WorkingDir = args[i]
		case "--delegate":
			i++
			if i >= len(args) {
				return runsvc.Request{}, fmt.Errorf("--delegate requires a value")
			}
			for _, part := range strings.Split(args[i], ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					req.Delegates = append(req.Delegates, part)
				}
			}
		case "--continuous":
			req.Continuous = true
		case "--max-rounds":
			i++
			if i >= len(args) {
				return runsvc.Request{}, fmt.Errorf("--max-rounds requires a value")
			}
			n, convErr := strconv.Atoi(args[i])
			if convErr != nil || n <= 0 {
				return runsvc.Request{}, fmt.Errorf("--max-rounds requires a positive integer")
			}
			req.MaxRounds = n
		default:
			promptParts = append(promptParts, args[i])
		}
	}

	req.Prompt = strings.TrimSpace(strings.Join(promptParts, " "))
	if req.Prompt == "" {
		return runsvc.Request{}, fmt.Errorf("prompt is required")
	}
	return req, nil
}

func printUsage() {
	fmt.Println("roma usage:")
	fmt.Println("  roma agents list")
	fmt.Println("  roma status")
	fmt.Println("  roma artifacts list")
	fmt.Println("  roma artifacts show <artifact_id>")
	fmt.Println("  roma approve <job_id>")
	fmt.Println("  roma events list [--session <session_id>] [--task <task_id>] [--type <event_type>]")
	fmt.Println("  roma events show <event_id>")
	fmt.Println("  roma graph run --file examples/relay-graph.json")
	fmt.Println(`  roma policy check --agent codex "build a feature"`)
	fmt.Println("  roma queue list")
	fmt.Println("  roma queue show <job_id>")
	fmt.Println("  roma queue inspect <job_id>")
	fmt.Println("  roma recover")
	fmt.Println("  roma reject <job_id>")
	fmt.Println("  roma replay <session_id>")
	fmt.Println("  roma submit --agent codex --continuous --max-rounds 5 \"build a feature\"")
	fmt.Println("  roma sessions list")
	fmt.Println("  roma sessions show <session_id>")
	fmt.Println("  roma tasks list [--session <session_id>]")
	fmt.Println("  roma tasks show <task_id>")
	fmt.Println(`  roma run --agent codex --continuous --max-rounds 5 "build a feature"`)
	fmt.Println(`  roma --agent claude "fix the failing tests"`)
	fmt.Println(`  roma --agent codex --delegate gemini,copilot "build a feature with optional delegation"`)
}

func queueLabel(req queue.Request) string {
	if req.GraphFile != "" || req.Graph != nil {
		return "graph"
	}
	return req.StarterAgent
}

func parseQueueArgs(args []string) (statusFilter string, modeFilter string, subcommand string, subArg string, err error) {
	subcommand = "list"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "list":
			subcommand = "list"
		case "show", "inspect":
			subcommand = args[i]
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("roma queue %s requires a job id", subcommand)
			}
			subArg = args[i]
		case "--status":
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("--status requires a value")
			}
			statusFilter = args[i]
		case "--mode":
			i++
			if i >= len(args) {
				return "", "", "", "", fmt.Errorf("--mode requires a value")
			}
			modeFilter = args[i]
		default:
			return "", "", "", "", fmt.Errorf("unknown queue argument %q", args[i])
		}
	}
	return statusFilter, modeFilter, subcommand, subArg, nil
}

func filterQueueRequests(requests []queue.Request, statusFilter, modeFilter string) []queue.Request {
	if statusFilter == "" && modeFilter == "" {
		return requests
	}
	filtered := make([]queue.Request, 0, len(requests))
	for _, req := range requests {
		if statusFilter != "" && string(req.Status) != statusFilter {
			continue
		}
		if modeFilter != "" {
			mode := "direct"
			if req.GraphFile != "" || req.Graph != nil {
				mode = "graph"
			}
			if mode != modeFilter {
				continue
			}
		}
		filtered = append(filtered, req)
	}
	return filtered
}
