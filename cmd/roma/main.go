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

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/artifacts"
	"github.com/liliang-cn/roma/internal/curia"
	"github.com/liliang-cn/roma/internal/domain"
	"github.com/liliang-cn/roma/internal/events"
	"github.com/liliang-cn/roma/internal/history"
	"github.com/liliang-cn/roma/internal/plans"
	"github.com/liliang-cn/roma/internal/policy"
	"github.com/liliang-cn/roma/internal/queue"
	"github.com/liliang-cn/roma/internal/replay"
	"github.com/liliang-cn/roma/internal/romapath"
	runsvc "github.com/liliang-cn/roma/internal/run"
	"github.com/liliang-cn/roma/internal/scheduler"
	"github.com/liliang-cn/roma/internal/sqliteutil"
	storepkg "github.com/liliang-cn/roma/internal/store"
	"github.com/liliang-cn/roma/internal/syncdb"
	"github.com/liliang-cn/roma/internal/taskstore"
	workspacepkg "github.com/liliang-cn/roma/internal/workspace"
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
	registry.SetUserConfigPath(agents.DefaultUserConfigPath())
	if err := registry.LoadUserConfig(registry.UserConfigPath()); err != nil {
		return fmt.Errorf("load user agent config: %w", err)
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 2 && strings.EqualFold(strings.TrimSpace(args[1]), "debug") {
			printTopicUsage("debug " + args[2])
			return nil
		}
		if len(args) > 1 {
			printTopicUsage(args[1])
			return nil
		}
		printUsage()
		return nil
	case "approve":
		return runQueueDecision(ctx, true, args[1:])
	case "cancel":
		return runQueueCancel(ctx, args[1:])
	case "debug":
		return runDebug(ctx, registry, args[1:])
	case "agent", "agents":
		return runAgents(ctx, registry, args[1:])
	case "artifact", "artifacts":
		return runArtifacts(ctx, args[1:])
	case "curia":
		return runCuria(ctx, args[1:])
	case "graph":
		return runGraph(ctx, registry, args[1:])
	case "event", "events":
		return runEvents(ctx, args[1:])
	case "policy":
		return runPolicy(ctx, args[1:])
	case "plan", "plans":
		return runPlans(ctx, args[1:])
	case "queue":
		return runQueue(ctx, args[1:])
	case "result", "results":
		return runResults(ctx, args[1:])
	case "replay":
		return runReplay(ctx, args[1:])
	case "recover":
		return runRecover(ctx, args[1:])
	case "reject":
		return runQueueDecision(ctx, false, args[1:])
	case "acp":
		return runAcp(ctx, args[1:])
	case "start":
		return runStart(args[1:])
	case "stop":
		return runStop()
	case "status":
		return runStatus(ctx)
	case "submit", "tell", "ask":
		return fmt.Errorf("%q has been removed; use \"roma run\" instead", args[0])
	case "tui":
		return runTUI(ctx, args[1:])
	case "session", "sessions":
		return runSessions(ctx, args[1:])
	case "task", "tasks":
		return runTasks(ctx, args[1:])
	case "workspace", "workspaces":
		return runWorkspaces(ctx, args[1:])
	case "run":
		return runPrompt(ctx, registry, args[1:])
	default:
		return runPrompt(ctx, registry, args)
	}
}

func runDebug(ctx context.Context, registry *agents.Registry, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printTopicUsage("debug")
		return nil
	}
	switch args[0] {
	case "agent", "agents":
		return runAgents(ctx, registry, args[1:])
	case "artifact", "artifacts":
		return runArtifacts(ctx, args[1:])
	case "curia":
		return runCuria(ctx, args[1:])
	case "event", "events":
		return runEvents(ctx, args[1:])
	case "graph":
		return runGraph(ctx, registry, args[1:])
	case "plan", "plans":
		return runPlans(ctx, args[1:])
	case "policy":
		return runPolicy(ctx, args[1:])
	case "replay":
		return runReplay(ctx, args[1:])
	case "recover":
		return runRecover(ctx, args[1:])
	case "session", "sessions":
		return runSessions(ctx, args[1:])
	case "task", "tasks":
		return runTasks(ctx, args[1:])
	case "workspace", "workspaces":
		return runWorkspaces(ctx, args[1:])
	default:
		return fmt.Errorf("unknown debug topic %q", args[0])
	}
}

func runAgents(ctx context.Context, registry *agents.Registry, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("agent")
		return nil
	}
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

	switch args[0] {
	case "add":
		if len(args) < 4 {
			return fmt.Errorf("usage: roma agent add <id> <display_name> <path> [--arg <arg>] [--alias <alias>] [--pty] [--mcp] [--json]")
		}
		profile := domain.AgentProfile{
			ID:                 args[1],
			DisplayName:        args[2],
			Command:            args[3],
			Availability:       domain.AgentAvailabilityPlanned,
			SupportsMCP:        false,
			SupportsJSONOutput: false,
		}
		for i := 4; i < len(args); i++ {
			switch args[i] {
			case "--arg":
				i++
				if i >= len(args) {
					return fmt.Errorf("--arg requires a value")
				}
				profile.Args = append(profile.Args, args[i])
			case "--alias":
				i++
				if i >= len(args) {
					return fmt.Errorf("--alias requires a value")
				}
				profile.Aliases = strings.Split(args[i], ",")
			case "--pty":
				profile.UsePTY = true
			case "--mcp":
				profile.SupportsMCP = true
			case "--json":
				profile.SupportsJSONOutput = true
			default:
				return fmt.Errorf("unknown argument %q", args[i])
			}
		}
		if err := registry.AddUserProfile(profile); err != nil {
			return err
		}
		fmt.Printf("agent %s added to %s\n", profile.ID, registry.UserConfigPath())
		return nil

	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: roma agent remove <id>")
		}
		if err := registry.RemoveUserProfile(args[1]); err != nil {
			return err
		}
		fmt.Printf("agent %s removed from %s\n", args[1], registry.UserConfigPath())
		return nil

	case "inspect":
		if len(args) < 2 {
			return fmt.Errorf("usage: roma agent inspect <id>")
		}
		profile, ok := registry.Get(args[1])
		if !ok {
			return fmt.Errorf("agent %s not found", args[1])
		}
		payload := map[string]any{
			"profile":     profile,
			"config_path": registry.UserConfigPath(),
			"builtin":     registry.IsBuiltin(profile.ID),
		}
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(raw))
		return nil
	}

	return fmt.Errorf("unknown agent subcommand %q", args[0])
}

func runPrompt(ctx context.Context, registry *agents.Registry, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("run")
		return nil
	}
	req, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	if req.StarterAgent == "" {
		profile, err := registry.DefaultProfile(ctx)
		if err != nil {
			return err
		}
		req.StarterAgent = profile.ID
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
			GraphFile:           "",
			Prompt:              req.Prompt,
			StarterAgent:        req.StarterAgent,
			Delegates:           req.Delegates,
			WorkingDir:          req.WorkingDir,
			PolicyOverride:      req.PolicyOverride,
			PolicyOverrideActor: req.OverrideActor,
			Continuous:          req.Continuous,
			MaxRounds:           req.MaxRounds,
		})
		if err != nil {
			return err
		}
		fmt.Printf("submitted to daemon id=%s agent=%s with=%s\n", resp.JobID, req.StarterAgent, strings.Join(req.Delegates, ","))
		return nil
	}

	svc := runsvc.NewService(registry)
	svc.SetControlDir(romapath.HomeDir())
	return svc.Run(ctx, req)
}

func runGraph(ctx context.Context, registry *agents.Registry, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("graph")
		return nil
	}
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
				ID:              node.ID,
				Title:           node.Title,
				Agent:           node.Agent,
				Strategy:        string(node.Strategy),
				Dependencies:    node.Dependencies,
				Senators:        node.Senators,
				Quorum:          node.Quorum,
				ArbitrationMode: node.ArbitrationMode,
				Arbitrator:      node.Arbitrator,
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
	svc.SetControlDir(romapath.HomeDir())
	return svc.RunGraph(ctx, graphReq, os.Stdout)
}

func runCuria(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("curia")
		return nil
	}
	if len(args) == 0 || args[0] != "reputation" {
		return fmt.Errorf("unknown curia subcommand")
	}
	reviewerID := ""
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--reviewer":
			i++
			if i >= len(args) {
				return fmt.Errorf("--reviewer requires a value")
			}
			reviewerID = args[i]
		case strings.HasPrefix(args[i], "--reviewer="):
			reviewerID = strings.TrimPrefix(args[i], "--reviewer=")
		default:
			return fmt.Errorf("unknown curia reputation argument %q", args[i])
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	client := api.NewClient(wd)
	var items []curia.ReputationRecord
	if client.Available() {
		items, err = client.CuriaReputation(ctx, reviewerID)
		if err != nil {
			return err
		}
	} else {
		store := curia.NewReputationStore(romapath.HomeDir())
		items, err = store.List(ctx)
		if err != nil {
			return err
		}
		if reviewerID != "" {
			filtered := make([]curia.ReputationRecord, 0, 1)
			for _, item := range items {
				if item.AgentID == reviewerID {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
	}
	raw, err := json.MarshalIndent(api.CuriaReputationResponse{Items: items}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal curia reputation: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func runResults(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("result")
		return nil
	}
	if len(args) < 2 || args[0] != "show" {
		return fmt.Errorf("usage: roma result show <session_id>")
	}
	sessionID := args[1]
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	client := api.NewClient(wd)
	if client.Available() {
		resp, err := client.ResultShow(ctx, sessionID)
		if err == nil {
			return printResultShow(resp)
		}
	}
	sessionStore := preferredHistoryStore(wd)
	session, err := sessionStore.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.Status == "running" || session.Status == "pending" || session.Status == "awaiting_approval" {
		return printResultShow(api.ResultShowResponse{
			Session: session,
			Pending: true,
			Message: fmt.Sprintf("result is not ready yet; session status is %s", session.Status),
		})
	}
	inspectDir := wd
	if strings.TrimSpace(session.WorkingDir) != "" {
		inspectDir = session.WorkingDir
	}
	artifact, err := resolveFinalAnswerEnvelopeLocal(ctx, preferredArtifactStore(inspectDir), session)
	if err != nil {
		return err
	}
	return printResultShow(api.ResultShowResponse{Session: session, Artifact: artifact})
}

func printResultShow(resp api.ResultShowResponse) error {
	if resp.Pending {
		fmt.Printf("session=%s\n", resp.Session.ID)
		fmt.Printf("status=%s\n", resp.Session.Status)
		fmt.Println("pending=true")
		if resp.Message != "" {
			fmt.Printf("message=%s\n", resp.Message)
		}
		return nil
	}
	payload, ok := artifacts.FinalAnswerFromEnvelope(resp.Artifact)
	if !ok {
		raw, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	fmt.Printf("session=%s\n", resp.Session.ID)
	fmt.Printf("status=%s\n", resp.Session.Status)
	fmt.Printf("outcome=%s\n", payload.OutcomeType)
	fmt.Printf("summary=%s\n", payload.Summary)
	if payload.Answer != "" {
		fmt.Printf("\n%s\n", payload.Answer)
	}
	if len(payload.ChangedFiles) > 0 {
		fmt.Println("\nchanged_files:")
		for _, item := range payload.ChangedFiles {
			fmt.Printf("- %s\n", item)
		}
	}
	if payload.ApprovalRequired {
		fmt.Println("\napproval_required=true")
	}
	if len(payload.NextActions) > 0 {
		fmt.Println("\nnext_actions:")
		for _, item := range payload.NextActions {
			fmt.Printf("- %s\n", item)
		}
	}
	fmt.Printf("\nartifact=%s\n", resp.Artifact.ID)
	return nil
}

func runSubmit(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("submit")
		return nil
	}
	req, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	registry, err := agents.DefaultRegistry()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	registry.SetUserConfigPath(agents.DefaultUserConfigPath())
	if err := registry.LoadUserConfig(registry.UserConfigPath()); err != nil {
		return fmt.Errorf("load user agent config: %w", err)
	}
	if req.StarterAgent == "" {
		profile, err := registry.DefaultProfile(ctx)
		if err != nil {
			return err
		}
		req.StarterAgent = profile.ID
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
			GraphFile:           "",
			Prompt:              req.Prompt,
			StarterAgent:        req.StarterAgent,
			Delegates:           req.Delegates,
			WorkingDir:          wd,
			PolicyOverride:      req.PolicyOverride,
			PolicyOverrideActor: req.OverrideActor,
			Continuous:          req.Continuous,
			MaxRounds:           req.MaxRounds,
		})
		if err != nil {
			return err
		}
		fmt.Printf("queued via daemon id=%s agent=%s with=%s\n", resp.JobID, req.StarterAgent, strings.Join(req.Delegates, ","))
		return nil
	}

	id := fmt.Sprintf("job_%d", time.Now().UTC().UnixNano())
	record := queue.Request{
		ID:                  id,
		GraphFile:           "",
		Prompt:              req.Prompt,
		StarterAgent:        req.StarterAgent,
		Delegates:           req.Delegates,
		WorkingDir:          wd,
		PolicyOverride:      req.PolicyOverride,
		PolicyOverrideActor: req.OverrideActor,
		Continuous:          req.Continuous,
		MaxRounds:           req.MaxRounds,
	}
	if err := store.Enqueue(ctx, record); err != nil {
		return err
	}
	fmt.Printf("queued id=%s agent=%s with=%s\n", id, req.StarterAgent, strings.Join(req.Delegates, ","))
	return nil
}

func runQueue(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("queue")
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	store := preferredQueueStore(wd)
	client := api.NewClient(wd)
	statusFilter, modeFilter, subcommand, subArg, rawTail, err := parseQueueArgs(args)
	if err != nil {
		return err
	}

	if client.Available() && subcommand == "list" {
		requests, err := client.QueueList(ctx)
		if err != nil {
			return err
		}
		requests = filterQueueRequests(requests, statusFilter, modeFilter)
		fmt.Println("ID\tTARGET\tSTATUS\tSUMMARY\tCREATED\tERROR")
		for _, req := range requests {
			summary := queueNodeSummary(ctx, wd, req)
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				req.ID,
				queueLabel(req),
				req.Status,
				summary,
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
		resp, err := client.QueueInspect(ctx, subArg, rawTail)
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

	if client.Available() && subcommand == "tail" {
		return runQueueTail(ctx, func(ctx context.Context, id string) (api.QueueInspectResponse, error) {
			return client.QueueInspect(ctx, id, true)
		}, subArg, rawTail)
	}

	if client.Available() && subcommand == "attach" {
		return runQueueTail(ctx, func(ctx context.Context, id string) (api.QueueInspectResponse, error) {
			return client.QueueInspect(ctx, id, true)
		}, subArg, rawTail)
	}

	if client.Available() && subcommand == "cancel" {
		item, err := client.QueueCancel(ctx, subArg)
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal queue cancel response: %w", err)
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
		fmt.Println("ID\tTARGET\tSTATUS\tSUMMARY\tCREATED\tERROR")
		for _, req := range requests {
			summary := queueNodeSummary(ctx, wd, req)
			fmt.Printf(
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				req.ID,
				queueLabel(req),
				req.Status,
				summary,
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
		resp, err := inspectQueueLocal(ctx, wd, subArg, rawTail)
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

	if subcommand == "tail" {
		return runQueueTail(ctx, func(ctx context.Context, id string) (api.QueueInspectResponse, error) {
			return inspectQueueLocal(ctx, wd, id, true)
		}, subArg, rawTail)
	}

	if subcommand == "attach" {
		return runQueueTail(ctx, func(ctx context.Context, id string) (api.QueueInspectResponse, error) {
			return inspectQueueLocal(ctx, wd, id, true)
		}, subArg, rawTail)
	}

	if subcommand == "cancel" {
		return runQueueCancel(ctx, []string{subArg})
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
	handled := false
	actor := policy.OverrideActor()
	if delegated, updated, err := applyQueueDecisionLocal(ctx, wd, item, approved); delegated {
		if err != nil {
			return err
		}
		handled = true
		item = updated
	} else {
		if approved {
			item.PolicyOverride = true
			item.PolicyOverrideActor = actor
			item.Status = queue.StatusPending
			item.Error = ""
		} else {
			item.PolicyOverride = false
			item.PolicyOverrideActor = ""
			item.Status = queue.StatusRejected
			item.Error = "rejected by user"
		}
		if err := queueStore.Update(ctx, item); err != nil {
			return err
		}
	}
	if !handled && item.SessionID != "" {
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
				"actor":    actor,
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

func runQueueTail(ctx context.Context, inspect func(context.Context, string) (api.QueueInspectResponse, error), jobID string, raw bool) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastLine string
	var lastHeartbeat string
	seenEvents := map[string]struct{}{}
	for {
		resp, err := inspect(ctx, jobID)
		if err != nil {
			return err
		}
		printQueueTailEvents(resp.Events, seenEvents, raw)
		if line := formatQueueHeartbeatLine(resp); line != "" && line != lastHeartbeat {
			fmt.Println(line)
			lastHeartbeat = line
		}
		line := formatQueueTailLine(resp)
		if line != lastLine {
			fmt.Println(line)
			lastLine = line
		}
		if resp.Job.Status != queue.StatusPending && resp.Job.Status != queue.StatusRunning {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func printQueueTailEvents(records []events.Record, seen map[string]struct{}, raw bool) {
	for _, line := range queueTailEventLines(records, seen, raw) {
		fmt.Println(line)
	}
}

func queueTailEventLines(records []events.Record, seen map[string]struct{}, raw bool) []string {
	lines := make([]string, 0)
	for _, record := range records {
		if _, ok := seen[record.ID]; ok {
			continue
		}
		seen[record.ID] = struct{}{}
		prefix := fmt.Sprintf("[%s] time=%s task=%s", queueTailEventLabel(record.Type), record.OccurredAt.Format(time.RFC3339), record.TaskID)
		switch record.Type {
		case events.TypeRelayNodeStarted:
			lines = append(lines, fmt.Sprintf("%s agent=%s", prefix, payloadString(record.Payload, "agent")))
		case events.TypeRelayNodeCompleted:
			lines = append(lines, fmt.Sprintf("%s reason=%s artifact=%s", prefix, record.ReasonCode, payloadString(record.Payload, "artifact_id")))
		case events.TypeWorkspacePrepared:
			lines = append(lines, fmt.Sprintf("%s dir=%s provider=%s", prefix, payloadString(record.Payload, "effective_dir"), payloadString(record.Payload, "provider")))
		case events.TypeRuntimeStarted:
			line := fmt.Sprintf("%s exec=%s agent=%s", prefix, payloadString(record.Payload, "execution_id"), payloadString(record.Payload, "agent"))
			if pid := payloadInt(record.Payload, "pid"); pid > 0 {
				line += " pid=" + strconv.Itoa(pid)
			}
			lines = append(lines, line)
		case events.TypeRuntimeStdoutCaptured:
			chunk := strings.TrimSpace(payloadString(record.Payload, "stdout"))
			if chunk == "" {
				continue
			}
			for _, line := range strings.Split(chunk, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if raw {
					lines = append(lines, line)
					continue
				}
				lines = append(lines, fmt.Sprintf("%s agent=%s text=%s", prefix, payloadString(record.Payload, "agent"), strconv.Quote(line)))
			}
		case events.TypeRuntimeExited:
			lines = append(lines, fmt.Sprintf("%s exec=%s state=%s", prefix, payloadString(record.Payload, "execution_id"), record.ReasonCode))
		case events.TypeApprovalRequested, events.TypeDangerousCommandDetected, events.TypeHighRiskChangeDetected, events.TypeDelegationRequested, events.TypeExecutionCompletedDetected, events.TypeParseWarning:
			line := fmt.Sprintf("%s confidence=%s reason=%s", prefix, payloadString(record.Payload, "confidence"), record.ReasonCode)
			if text := strings.TrimSpace(payloadString(record.Payload, "text")); text != "" {
				line += " text=" + strconv.Quote(text)
			}
			lines = append(lines, line)
		case events.TypeSemanticReportProduced, events.TypeSemanticApprovalRecommended, events.TypeCuriaPromotionRecommended:
			line := fmt.Sprintf("%s classifier=%s risk=%s", prefix, payloadString(record.Payload, "classifier_agent_id"), payloadString(record.Payload, "risk"))
			if intent := strings.TrimSpace(record.ReasonCode); intent != "" {
				line += " intent=" + intent
			}
			if summary := strings.TrimSpace(payloadString(record.Payload, "summary")); summary != "" {
				line += " summary=" + strconv.Quote(summary)
			}
			lines = append(lines, line)
		case events.TypePlanApplyRejected, events.TypePlanApplied, events.TypePlanRolledBack:
			lines = append(lines, fmt.Sprintf("%s reason=%s artifact=%s", prefix, record.ReasonCode, payloadString(record.Payload, "artifact_id")))
		case events.TypeTaskStateChanged:
			lines = append(lines, fmt.Sprintf("%s state=%s", prefix, record.ReasonCode))
		}
	}
	return lines
}

func formatQueueTailLine(resp api.QueueInspectResponse) string {
	parts := []string{
		fmt.Sprintf("job=%s", resp.Job.ID),
		fmt.Sprintf("status=%s", resp.Job.Status),
	}
	if resp.Live != nil {
		if resp.Live.Phase != "" {
			parts = append(parts, "phase="+resp.Live.Phase)
		}
		if resp.Live.CurrentRound > 0 {
			parts = append(parts, "round="+strconv.Itoa(resp.Live.CurrentRound))
		}
		if resp.Live.ParticipantCount > 1 {
			parts = append(parts, "agents="+strconv.Itoa(resp.Live.ParticipantCount))
		}
		if resp.Live.CurrentTaskID != "" {
			parts = append(parts, "task="+resp.Live.CurrentTaskID)
		}
		if resp.Live.CurrentAgentID != "" {
			parts = append(parts, "agent="+resp.Live.CurrentAgentID)
		}
		if resp.Live.ExecutionID != "" {
			parts = append(parts, "exec="+resp.Live.ExecutionID)
		}
		if resp.Live.ProcessPID > 0 {
			parts = append(parts, "pid="+strconv.Itoa(resp.Live.ProcessPID))
		}
		if resp.Live.WorkspacePath != "" {
			parts = append(parts, "workspace="+resp.Live.WorkspacePath)
		}
		if resp.Live.WorkspaceMode != "" {
			parts = append(parts, "workspace_mode="+resp.Live.WorkspaceMode)
		}
		if resp.Live.LastOutputAt != nil {
			parts = append(parts, "last_output="+resp.Live.LastOutputAt.Format(time.RFC3339))
		}
		if resp.Live.LastOutputPreview != "" {
			parts = append(parts, "output="+strconv.Quote(resp.Live.LastOutputPreview))
		}
	}
	if resp.ArtifactCount > 0 {
		parts = append(parts, "artifacts="+strconv.Itoa(resp.ArtifactCount))
	}
	if resp.EventCount > 0 {
		parts = append(parts, "events="+strconv.Itoa(resp.EventCount))
	}
	return strings.Join(parts, " ")
}

func formatQueueHeartbeatLine(resp api.QueueInspectResponse) string {
	if resp.Live == nil || resp.Live.LastHeartbeatAt == nil {
		return ""
	}
	parts := []string{
		"[heartbeat]",
		"at=" + resp.Live.LastHeartbeatAt.Format(time.RFC3339),
		"job=" + resp.Job.ID,
		"status=" + string(resp.Job.Status),
	}
	if resp.Live.CurrentTaskID != "" {
		parts = append(parts, "task="+resp.Live.CurrentTaskID)
	}
	if resp.Live.CurrentAgentID != "" {
		parts = append(parts, "agent="+resp.Live.CurrentAgentID)
	}
	if resp.Live.ProcessPID > 0 {
		parts = append(parts, "pid="+strconv.Itoa(resp.Live.ProcessPID))
	}
	return strings.Join(parts, " ")
}

func inspectQueueLocal(ctx context.Context, wd, jobID string, raw bool) (api.QueueInspectResponse, error) {
	queueStore := preferredQueueStore(wd)
	req, err := queueStore.Get(ctx, jobID)
	if err != nil {
		return api.QueueInspectResponse{}, err
	}
	resp := api.QueueInspectResponse{Job: req, ApprovalResumeReady: true}
	if req.SessionID == "" {
		return resp, nil
	}

	controlDir := romapath.HomeDir()
	workspaceDir := req.WorkingDir
	var eventItems []events.Record
	sessionStore := preferredHistoryStore(controlDir)
	if session, err := sessionStore.Get(ctx, req.SessionID); err == nil {
		resp.Session = &session
		if session.WorkingDir != "" {
			workspaceDir = session.WorkingDir
		}
	}
	if leaseStore, err := scheduler.NewLeaseStore(controlDir); err == nil {
		if lease, err := leaseStore.Get(ctx, req.SessionID); err == nil {
			resp.Lease = &lease
			resp.PendingApprovalTaskIDs = append(resp.PendingApprovalTaskIDs, lease.PendingApprovalTaskIDs...)
			resp.ApprovalResumeReady = len(lease.PendingApprovalTaskIDs) == 0
		}
	}
	taskStore := preferredTaskStore(controlDir)
	if items, err := taskStore.ListTasksBySession(ctx, req.SessionID); err == nil {
		resp.Tasks = items
	}
	artifactStore := preferredArtifactStore(controlDir)
	if items, err := artifactStore.List(ctx, req.SessionID); err == nil {
		resp.ArtifactCount = len(items)
		if raw {
			resp.Artifacts = items
		}
		resp.Curia = summarizeCuriaArtifactsCLI(controlDir, items)
		resp.Semantic = summarizeSemanticArtifactsCLI(items)
	}
	eventStore := preferredEventStore(controlDir)
	if items, err := eventStore.ListEvents(ctx, storepkg.EventFilter{SessionID: req.SessionID}); err == nil {
		eventItems = items
		resp.EventCount = len(items)
		if raw {
			resp.Events = items
		}
		resp.Plans = summarizePlanActions(items)
	}
	if workspaceDir != "" {
		manager := workspacepkg.NewManager(workspaceDir, nil)
		if items, err := manager.List(ctx); err == nil {
			for _, item := range items {
				if item.SessionID == req.SessionID {
					resp.Workspaces = append(resp.Workspaces, item)
				}
			}
		}
	}
	sessionStatus := string(req.Status)
	if resp.Session != nil && resp.Session.Status != "" {
		sessionStatus = resp.Session.Status
	}
	resp.Live = api.EnrichRuntimeLive(api.SummarizeRuntimeLive(sessionStatus, resp.Tasks, eventItems, resp.Workspaces, resp.Lease, req.UpdatedAt), req.StarterAgent, req.Delegates)
	return resp, nil
}

func runQueueCancel(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("roma cancel requires a job id")
	}
	jobID := args[0]
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)

	clientRoot := resolveQueueClientRoot(ctx, wd, jobID)

	client := api.NewClient(clientRoot)
	if client.Available() {
		item, err := client.QueueCancel(ctx, jobID)
		if err == nil {
			raw, err := json.MarshalIndent(item, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal queue cancel response: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		item, err = cancelQueueLocal(ctx, wd, jobID)
		if err == nil {
			raw, err := json.MarshalIndent(item, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal queue cancel response: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		return err
	}

	item, err := cancelQueueLocal(ctx, clientRoot, jobID)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal queue cancel: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func cancelQueueLocal(ctx context.Context, wd, jobID string) (queue.Request, error) {
	item, queueRoot, err := findQueueRequestAcrossRoots(ctx, wd, jobID)
	if err != nil {
		return queue.Request{}, err
	}
	queueStore := preferredQueueStore(queueRoot)
	item.Status = queue.StatusCancelled
	item.Error = "cancelled by user"
	item.PolicyOverride = false
	item.PolicyOverrideActor = ""
	if err := queueStore.Update(ctx, item); err != nil {
		return queue.Request{}, err
	}
	executionRoot := queueRoot
	if strings.TrimSpace(item.WorkingDir) != "" {
		executionRoot = item.WorkingDir
	}
	if item.SessionID != "" {
		sessionStore := preferredHistoryStore(executionRoot)
		if session, err := sessionStore.Get(ctx, item.SessionID); err == nil {
			session.Status = "cancelled"
			session.UpdatedAt = time.Now().UTC()
			_ = sessionStore.Save(ctx, session)
		}
		taskStore := preferredTaskStore(executionRoot)
		if tasks, err := taskStore.ListTasksBySession(ctx, item.SessionID); err == nil {
			for _, task := range tasks {
				switch task.State {
				case domain.TaskStateSucceeded, domain.TaskStateFailedRecoverable, domain.TaskStateFailedTerminal, domain.TaskStateCancelled:
					continue
				default:
					_ = taskStore.UpdateTaskState(ctx, storepkg.TaskStateUpdate{
						TaskID: task.ID,
						State:  domain.TaskStateCancelled,
					})
				}
			}
		}
		eventStore := preferredEventStore(executionRoot)
		_ = eventStore.AppendEvent(ctx, events.Record{
			ID:         "evt_" + item.ID + "_cancelled",
			SessionID:  item.SessionID,
			TaskID:     item.TaskID,
			Type:       events.TypeQueueCancelled,
			ActorType:  events.ActorTypeHuman,
			OccurredAt: time.Now().UTC(),
			ReasonCode: "manual_cancel",
			Payload: map[string]any{
				"job_id": item.ID,
			},
		})
	}
	return item, nil
}

func resolveQueueClientRoot(ctx context.Context, wd, jobID string) string {
	if _, queueRoot, err := findQueueRequestAcrossRoots(ctx, wd, jobID); err == nil && strings.TrimSpace(queueRoot) != "" {
		return queueRoot
	}
	return wd
}

func findQueueRequestAcrossRoots(ctx context.Context, wd, jobID string) (queue.Request, string, error) {
	var lastErr error
	for _, root := range candidateQueueRoots(wd) {
		item, err := preferredQueueStore(root).Get(ctx, jobID)
		if err == nil {
			return item, root, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("queue request %s not found", jobID)
	}
	return queue.Request{}, "", lastErr
}

func candidateQueueRoots(wd string) []string {
	_ = wd
	return []string{filepath.Clean(romapath.HomeDir())}
}

func applyQueueDecisionLocal(ctx context.Context, wd string, item queue.Request, approved bool) (bool, queue.Request, error) {
	if item.SessionID == "" {
		return false, item, nil
	}
	leaseStore, err := scheduler.NewLeaseStore(wd)
	if err != nil {
		return false, item, nil
	}
	lease, err := leaseStore.Get(ctx, item.SessionID)
	if err != nil || len(lease.PendingApprovalTaskIDs) == 0 {
		return false, item, nil
	}
	eventStore := preferredEventStore(wd)
	taskStore := preferredTaskStore(wd)
	lifecycle := scheduler.NewGraphLifecycle(taskStore, eventStore)
	for _, taskID := range lease.PendingApprovalTaskIDs {
		if approved {
			if err := lifecycle.ApproveTask(ctx, taskID); err != nil {
				return true, item, err
			}
		} else {
			if err := lifecycle.RejectTask(ctx, taskID); err != nil {
				return true, item, err
			}
		}
	}
	if err := leaseStore.UpdatePendingApprovalTaskIDs(ctx, item.SessionID, nil); err != nil {
		return true, item, err
	}
	reason := "human_approved"
	if !approved {
		reason = "human_rejected"
	}
	actor := policy.OverrideActor()
	_ = eventStore.AppendEvent(ctx, events.Record{
		ID:         "evt_" + item.ID + "_" + reason,
		SessionID:  item.SessionID,
		TaskID:     item.TaskID,
		Type:       events.TypePolicyDecisionRecorded,
		ActorType:  events.ActorTypeHuman,
		OccurredAt: time.Now().UTC(),
		ReasonCode: reason,
		Payload: map[string]any{
			"job_id":                    item.ID,
			"approved":                  approved,
			"actor":                     actor,
			"pending_approval_task_ids": lease.PendingApprovalTaskIDs,
		},
	})
	_ = eventStore.AppendEvent(ctx, events.Record{
		ID:         "evt_" + item.SessionID + "_lease_" + fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
		SessionID:  item.SessionID,
		Type:       events.TypeSchedulerLeaseRecorded,
		ActorType:  events.ActorTypeScheduler,
		OccurredAt: time.Now().UTC(),
		ReasonCode: string(lease.Status),
		Payload: map[string]any{
			"owner_id":                  lease.OwnerID,
			"status":                    lease.Status,
			"ready_task_ids":            lease.ReadyTaskIDs,
			"workspace_refs":            lease.WorkspaceRefs,
			"pending_approval_task_ids": []string{},
			"completed_task_ids":        lease.CompletedTaskIDs,
		},
	})
	sessionStore := preferredHistoryStore(wd)
	if session, err := sessionStore.Get(ctx, item.SessionID); err == nil {
		if approved {
			session.Status = "running"
		} else {
			session.Status = "rejected"
		}
		session.UpdatedAt = time.Now().UTC()
		_ = sessionStore.Save(ctx, session)
	}
	if approved {
		item.Status = queue.StatusPending
		item.Error = ""
	} else {
		item.Status = queue.StatusRejected
		item.Error = "task approval rejected"
	}
	item.PolicyOverride = false
	item.PolicyOverrideActor = ""
	if err := preferredQueueStore(wd).Update(ctx, item); err != nil {
		return true, item, err
	}
	return true, item, nil
}

func runArtifacts(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("artifact")
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	store := preferredArtifactStore(wd)
	client := api.NewClient(wd)

	if client.Available() && (len(args) == 0 || args[0] == "list") {
		sessionID := ""
		kindFilter := ""
		if len(args) > 2 && args[1] == "--session" {
			sessionID = args[2]
		}
		if len(args) > 2 && args[1] == "--kind" {
			kindFilter = args[2]
		}
		if len(args) > 4 && args[3] == "--kind" {
			kindFilter = args[4]
		}
		envelopes, err := client.ArtifactList(ctx, sessionID)
		if err != nil {
			return err
		}
		envelopes = filterArtifactsByKind(envelopes, kindFilter)
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
		kindFilter := ""
		if len(args) > 2 && args[1] == "--session" {
			sessionID = args[2]
		}
		if len(args) > 2 && args[1] == "--kind" {
			kindFilter = args[2]
		}
		if len(args) > 4 && args[3] == "--kind" {
			kindFilter = args[4]
		}
		envelopes, err := store.List(ctx, sessionID)
		if err != nil {
			return err
		}
		envelopes = filterArtifactsByKind(envelopes, kindFilter)
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

func resolveFinalAnswerEnvelopeLocal(ctx context.Context, artifactStore artifacts.Backend, session history.SessionRecord) (domain.ArtifactEnvelope, error) {
	if session.FinalArtifactID != "" {
		return artifactStore.Get(ctx, session.FinalArtifactID)
	}
	items, err := artifactStore.List(ctx, session.ID)
	if err != nil {
		return domain.ArtifactEnvelope{}, err
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Kind == domain.ArtifactKindFinalAnswer {
			return items[i], nil
		}
	}
	if len(items) == 0 {
		return domain.ArtifactEnvelope{}, fmt.Errorf("session %s has no final answer", session.ID)
	}
	return artifacts.NewService().BuildFinalAnswer(ctx, artifacts.BuildFinalAnswerRequest{
		SessionID:    session.ID,
		TaskID:       session.TaskID,
		RunID:        session.TaskID,
		Status:       session.Status,
		Prompt:       session.Prompt,
		StarterAgent: session.Starter,
		Artifacts:    items,
	})
}

func runSessions(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("session")
		return nil
	}
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

	if client.Available() && len(args) > 1 && args[0] == "inspect" {
		record, err := client.SessionInspect(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal session inspect: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if client.Available() && len(args) > 1 && args[0] == "curia" {
		record, err := client.SessionInspect(ctx, args[1])
		if err != nil {
			return err
		}
		printCuriaSummary(record)
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

	if args[0] == "inspect" {
		if len(args) < 2 {
			return fmt.Errorf("roma sessions inspect requires a session id")
		}
		record, err := store.Get(ctx, args[1])
		if err != nil {
			return err
		}
		controlDir := romapath.HomeDir()
		workspaceDir := record.WorkingDir
		resp := api.SessionInspectResponse{Session: record, ApprovalResumeReady: true}
		if leaseStore, err := scheduler.NewLeaseStore(controlDir); err == nil {
			if lease, err := leaseStore.Get(ctx, args[1]); err == nil {
				resp.Lease = &lease
				resp.PendingApprovalTaskIDs = append(resp.PendingApprovalTaskIDs, lease.PendingApprovalTaskIDs...)
				resp.ApprovalResumeReady = len(lease.PendingApprovalTaskIDs) == 0
			}
		}
		taskStore := preferredTaskStore(controlDir)
		if items, err := taskStore.ListTasksBySession(ctx, args[1]); err == nil {
			resp.Tasks = items
		}
		eventStore := preferredEventStore(controlDir)
		if items, err := eventStore.ListEvents(ctx, storepkg.EventFilter{SessionID: args[1]}); err == nil {
			resp.Events = items
			resp.Plans = summarizePlanActions(items)
		}
		artifactStore := preferredArtifactStore(controlDir)
		if items, err := artifactStore.List(ctx, args[1]); err == nil {
			resp.Artifacts = items
			resp.Curia = summarizeCuriaArtifactsCLI(controlDir, items)
			resp.Semantic = summarizeSemanticArtifactsCLI(items)
		}
		if workspaceDir != "" {
			manager := workspacepkg.NewManager(workspaceDir, nil)
			if items, err := manager.List(ctx); err == nil {
				for _, item := range items {
					if item.SessionID == args[1] {
						resp.Workspaces = append(resp.Workspaces, item)
					}
				}
			}
		}
		resp.Live = api.EnrichRuntimeLive(api.SummarizeRuntimeLive(resp.Session.Status, resp.Tasks, resp.Events, resp.Workspaces, resp.Lease, resp.Session.UpdatedAt), resp.Session.Starter, resp.Session.Delegates)
		raw, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal session inspect: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if args[0] == "curia" {
		if len(args) < 2 {
			return fmt.Errorf("roma sessions curia requires a session id")
		}
		record, err := store.Get(ctx, args[1])
		if err != nil {
			return err
		}
		controlDir := romapath.HomeDir()
		workspaceDir := record.WorkingDir
		resp := api.SessionInspectResponse{Session: record, ApprovalResumeReady: true}
		if leaseStore, err := scheduler.NewLeaseStore(controlDir); err == nil {
			if lease, err := leaseStore.Get(ctx, args[1]); err == nil {
				resp.Lease = &lease
				resp.PendingApprovalTaskIDs = append(resp.PendingApprovalTaskIDs, lease.PendingApprovalTaskIDs...)
				resp.ApprovalResumeReady = len(lease.PendingApprovalTaskIDs) == 0
			}
		}
		taskStore := preferredTaskStore(controlDir)
		if items, err := taskStore.ListTasksBySession(ctx, args[1]); err == nil {
			resp.Tasks = items
		}
		eventStore := preferredEventStore(controlDir)
		if items, err := eventStore.ListEvents(ctx, storepkg.EventFilter{SessionID: args[1]}); err == nil {
			resp.Events = items
			resp.Plans = summarizePlanActions(items)
		}
		artifactStore := preferredArtifactStore(controlDir)
		if items, err := artifactStore.List(ctx, args[1]); err == nil {
			resp.Artifacts = items
			resp.Curia = summarizeCuriaArtifactsCLI(controlDir, items)
			resp.Semantic = summarizeSemanticArtifactsCLI(items)
		}
		if workspaceDir != "" {
			manager := workspacepkg.NewManager(workspaceDir, nil)
			if items, err := manager.List(ctx); err == nil {
				for _, item := range items {
					if item.SessionID == args[1] {
						resp.Workspaces = append(resp.Workspaces, item)
					}
				}
			}
		}
		resp.Live = api.EnrichRuntimeLive(api.SummarizeRuntimeLive(resp.Session.Status, resp.Tasks, resp.Events, resp.Workspaces, resp.Lease, resp.Session.UpdatedAt), resp.Session.Starter, resp.Session.Delegates)
		printCuriaSummary(resp)
		return nil
	}

	return fmt.Errorf("unknown sessions subcommand %q", args[0])
}

func filterArtifactsByKind(envelopes []domain.ArtifactEnvelope, kind string) []domain.ArtifactEnvelope {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return envelopes
	}
	out := make([]domain.ArtifactEnvelope, 0, len(envelopes))
	for _, envelope := range envelopes {
		if string(envelope.Kind) == kind {
			out = append(out, envelope)
		}
	}
	return out
}

func printCuriaSummary(resp api.SessionInspectResponse) {
	counts := map[domain.ArtifactKind]int{}
	var latestDebate *artifacts.DebateLogPayload
	var latestDecision *artifacts.DecisionPackPayload
	for _, artifact := range resp.Artifacts {
		counts[artifact.Kind]++
		switch artifact.Kind {
		case domain.ArtifactKindDebateLog:
			if payload, ok := artifacts.DebateLogFromEnvelope(artifact); ok {
				item := payload
				latestDebate = &item
			}
		case domain.ArtifactKindDecisionPack:
			if payload, ok := artifacts.DecisionPackFromEnvelope(artifact); ok {
				item := payload
				latestDecision = &item
			}
		}
	}
	fmt.Printf("session=%s\n", resp.Session.ID)
	fmt.Printf("status=%s\n", resp.Session.Status)
	fmt.Printf("approval_resume_ready=%t\n", resp.ApprovalResumeReady)
	fmt.Printf("tasks=%d\n", len(resp.Tasks))
	fmt.Printf("workspaces=%d\n", len(resp.Workspaces))
	fmt.Printf("proposals=%d\n", counts[domain.ArtifactKindProposal])
	fmt.Printf("ballots=%d\n", counts[domain.ArtifactKindBallot])
	fmt.Printf("debate_logs=%d\n", counts[domain.ArtifactKindDebateLog])
	fmt.Printf("decision_packs=%d\n", counts[domain.ArtifactKindDecisionPack])
	fmt.Printf("execution_plans=%d\n", counts[domain.ArtifactKindExecutionPlan])
	for _, artifact := range resp.Artifacts {
		switch artifact.Kind {
		case domain.ArtifactKindDecisionPack, domain.ArtifactKindExecutionPlan:
			fmt.Printf("%s=%s\n", artifact.Kind, artifact.ID)
		}
	}
	if latestDebate != nil {
		fmt.Printf("curia_dispute=%t\n", latestDebate.DisputeDetected)
		if latestDebate.DisputeClass != "" {
			fmt.Printf("curia_dispute_class=%s\n", latestDebate.DisputeClass)
		}
		if latestDebate.ArbitrationStrategy != "" {
			fmt.Printf("curia_arbitration_strategy=%s\n", latestDebate.ArbitrationStrategy)
		}
		if latestDebate.ArbitrationConfidence != "" {
			fmt.Printf("curia_arbitration_confidence=%s\n", latestDebate.ArbitrationConfidence)
		}
		if latestDebate.ConsensusStrength != "" {
			fmt.Printf("curia_consensus_strength=%s\n", latestDebate.ConsensusStrength)
		}
		fmt.Printf("curia_critical_veto=%t\n", latestDebate.CriticalVeto)
		fmt.Printf("curia_top_score_gap=%d\n", latestDebate.TopScoreGap)
		if len(latestDebate.DisputeReasons) > 0 {
			fmt.Printf("curia_dispute_reasons=%s\n", strings.Join(latestDebate.DisputeReasons, " | "))
		}
		if len(latestDebate.EscalationReasons) > 0 {
			fmt.Printf("curia_escalation_reasons=%s\n", strings.Join(latestDebate.EscalationReasons, " | "))
		}
		if len(latestDebate.CompetingProposalIDs) > 0 {
			fmt.Printf("curia_competing=%s\n", strings.Join(latestDebate.CompetingProposalIDs, ","))
		}
		for _, item := range latestDebate.Scoreboard {
			fmt.Printf("scoreboard[%s]=raw:%d weighted:%d veto:%d reviewers:%d\n", item.ProposalID, item.RawScore, item.WeightedScore, item.VetoCount, item.ReviewerCount)
		}
	}
	if latestDecision != nil {
		fmt.Printf("curia_winning_mode=%s\n", latestDecision.WinningMode)
		if latestDecision.ArbitrationStrategy != "" {
			fmt.Printf("curia_arbitration_strategy=%s\n", latestDecision.ArbitrationStrategy)
		}
		if latestDecision.ArbitrationConfidence != "" {
			fmt.Printf("curia_arbitration_confidence=%s\n", latestDecision.ArbitrationConfidence)
		}
		if latestDecision.ConsensusStrength != "" {
			fmt.Printf("curia_consensus_strength=%s\n", latestDecision.ConsensusStrength)
		}
		if latestDecision.Arbitrated {
			fmt.Printf("curia_arbitrated=true\n")
		}
		if latestDecision.ArbitratorID != "" {
			fmt.Printf("curia_arbitrator=%s\n", latestDecision.ArbitratorID)
		}
		if len(latestDecision.SelectedProposalIDs) > 0 {
			fmt.Printf("curia_selected=%s\n", strings.Join(latestDecision.SelectedProposalIDs, ","))
		}
		if len(latestDecision.CompetingProposalIDs) > 0 {
			fmt.Printf("curia_competing=%s\n", strings.Join(latestDecision.CompetingProposalIDs, ","))
		}
		if latestDecision.ApprovalReason != "" {
			fmt.Printf("curia_approval_reason=%s\n", latestDecision.ApprovalReason)
		}
		if len(latestDecision.EscalationReasons) > 0 {
			fmt.Printf("curia_escalation_reasons=%s\n", strings.Join(latestDecision.EscalationReasons, " | "))
		}
		if len(latestDecision.RiskFlags) > 0 {
			fmt.Printf("curia_risk_flags=%s\n", strings.Join(latestDecision.RiskFlags, " | "))
		}
		if len(latestDecision.ReviewQuestions) > 0 {
			fmt.Printf("curia_review_questions=%s\n", strings.Join(latestDecision.ReviewQuestions, " | "))
		}
		if len(latestDecision.DissentSummary) > 0 {
			fmt.Printf("curia_dissent=%s\n", strings.Join(latestDecision.DissentSummary, " | "))
		}
		for _, item := range latestDecision.CandidateSummaries {
			fmt.Printf("candidate[%s]=weighted:%d raw:%d veto:%d summary:%s\n", item.ProposalID, item.WeightedScore, item.RawScore, item.VetoCount, item.Summary)
		}
		for _, item := range latestDecision.ReviewerBreakdown {
			fmt.Printf("reviewer[%s]=proposal:%s raw:%d weight:%d weighted:%d veto:%t\n", item.ReviewerID, item.TargetProposalID, item.RawScore, item.ReviewerWeight, item.WeightedScore, item.Veto)
		}
		weights := []api.CuriaReviewerSummary(nil)
		if resp.Curia != nil {
			weights = resp.Curia.ReviewerWeights
		}
		for _, item := range weights {
			fmt.Printf("reputation[%s]=weight:%d reviews:%d aligned:%d vetoes:%d arbitrations:%d\n", item.ReviewerID, item.EffectiveWeight, item.ReviewCount, item.AlignmentCount, item.VetoCount, item.ArbitrationCount)
		}
	}
}

func runAcp(ctx context.Context, args []string) error {
	if len(args) < 1 || args[0] != "status" {
		return fmt.Errorf("usage: roma acp status")
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	client := api.NewClient(wd)
	if !client.Available() {
		// Daemon not running, so ACP is not enabled.
		fmt.Println(`{"enabled": false, "port": 0}`)
		return nil
	}

	status, err := client.AcpStatus(ctx)
	if err != nil {
		return fmt.Errorf("get acp status: %w", err)
	}

	raw, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal acp status: %w", err)
	}

	fmt.Println(string(raw))
	return nil
}

func summarizeCuriaArtifactsCLI(workDir string, items []domain.ArtifactEnvelope) *api.CuriaSummary {
	var latestDebate *artifacts.DebateLogPayload
	var latestDecision *artifacts.DecisionPackPayload
	for _, item := range items {
		switch item.Kind {
		case domain.ArtifactKindDebateLog:
			if payload, ok := artifacts.DebateLogFromEnvelope(item); ok {
				value := payload
				latestDebate = &value
			}
		case domain.ArtifactKindDecisionPack:
			if payload, ok := artifacts.DecisionPackFromEnvelope(item); ok {
				value := payload
				latestDecision = &value
			}
		}
	}
	if latestDebate == nil && latestDecision == nil {
		return nil
	}
	out := &api.CuriaSummary{}
	if latestDebate != nil {
		out.Dispute = latestDebate.DisputeDetected
		out.DisputeClass = latestDebate.DisputeClass
		out.ArbitrationStrategy = latestDebate.ArbitrationStrategy
		out.ArbitrationConfidence = latestDebate.ArbitrationConfidence
		out.ConsensusStrength = latestDebate.ConsensusStrength
		out.CriticalVeto = latestDebate.CriticalVeto
		out.TopScoreGap = latestDebate.TopScoreGap
		out.DisputeReasons = append([]string(nil), latestDebate.DisputeReasons...)
		out.EscalationReasons = append([]string(nil), latestDebate.EscalationReasons...)
		out.CompetingProposalIDs = append([]string(nil), latestDebate.CompetingProposalIDs...)
		for _, item := range latestDebate.Scoreboard {
			out.Scoreboard = append(out.Scoreboard, api.CuriaScoreSummary{
				ProposalID:    item.ProposalID,
				RawScore:      item.RawScore,
				WeightedScore: item.WeightedScore,
				VetoCount:     item.VetoCount,
				ReviewerCount: item.ReviewerCount,
			})
		}
	}
	if latestDecision != nil {
		out.WinningMode = latestDecision.WinningMode
		out.ArbitrationStrategy = latestDecision.ArbitrationStrategy
		out.ArbitrationConfidence = latestDecision.ArbitrationConfidence
		out.ConsensusStrength = latestDecision.ConsensusStrength
		out.Arbitrated = latestDecision.Arbitrated
		out.ArbitratorID = latestDecision.ArbitratorID
		out.SelectedProposalIDs = append([]string(nil), latestDecision.SelectedProposalIDs...)
		out.CompetingProposalIDs = append([]string(nil), latestDecision.CompetingProposalIDs...)
		out.ApprovalReason = latestDecision.ApprovalReason
		if len(out.EscalationReasons) == 0 {
			out.EscalationReasons = append([]string(nil), latestDecision.EscalationReasons...)
		}
		out.RiskFlags = append([]string(nil), latestDecision.RiskFlags...)
		out.ReviewQuestions = append([]string(nil), latestDecision.ReviewQuestions...)
		out.DissentSummary = append([]string(nil), latestDecision.DissentSummary...)
		out.CandidateSummaries = append([]artifacts.CuriaCandidateSummary(nil), latestDecision.CandidateSummaries...)
		out.ReviewerBreakdown = append([]artifacts.CuriaReviewContribution(nil), latestDecision.ReviewerBreakdown...)
		out.ReviewerWeights = summarizeCuriaReviewerWeightsCLI(workDir, latestDecision.ReviewerBreakdown)
		if len(out.Scoreboard) == 0 {
			for _, item := range latestDecision.Scoreboard {
				out.Scoreboard = append(out.Scoreboard, api.CuriaScoreSummary{
					ProposalID:    item.ProposalID,
					RawScore:      item.RawScore,
					WeightedScore: item.WeightedScore,
					VetoCount:     item.VetoCount,
					ReviewerCount: item.ReviewerCount,
				})
			}
		}
	}
	return out
}

func summarizeSemanticArtifactsCLI(items []domain.ArtifactEnvelope) *api.SemanticSummary {
	var latest *artifacts.SemanticReportPayload
	var artifactID string
	for _, item := range items {
		if item.Kind != domain.ArtifactKindSemanticReport {
			continue
		}
		if payload, ok := artifacts.SemanticReportFromEnvelope(item); ok {
			value := payload
			latest = &value
			artifactID = item.ID
		}
	}
	if latest == nil {
		return nil
	}
	return &api.SemanticSummary{
		Intent:           latest.Intent,
		Risk:             latest.Risk,
		NeedsApproval:    latest.NeedsApproval,
		RecommendCuria:   latest.RecommendCuria,
		Summary:          latest.Summary,
		ClassifierAgent:  latest.ClassifierAgentID,
		SourceSignal:     latest.SourceSignal,
		SourceReason:     latest.SourceReason,
		SourceConfidence: latest.SourceConfidence,
		ArtifactID:       artifactID,
	}
}

func summarizeCuriaReviewerWeightsCLI(workDir string, items []artifacts.CuriaReviewContribution) []api.CuriaReviewerSummary {
	if workDir == "" || len(items) == 0 {
		return nil
	}
	store := curia.NewReputationStore(romapath.HomeDir())
	if store == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]api.CuriaReviewerSummary, 0, len(items))
	for _, item := range items {
		if item.ReviewerID == "" {
			continue
		}
		if _, ok := seen[item.ReviewerID]; ok {
			continue
		}
		seen[item.ReviewerID] = struct{}{}
		record, ok, err := store.Get(context.Background(), item.ReviewerID)
		if err != nil {
			continue
		}
		if ok {
			out = append(out, api.CuriaReviewerSummary{
				ReviewerID:       item.ReviewerID,
				EffectiveWeight:  record.EffectiveWeight,
				ReviewCount:      record.ReviewCount,
				AlignmentCount:   record.AlignmentCount,
				VetoCount:        record.VetoCount,
				ArbitrationCount: record.ArbitrationCount,
			})
			continue
		}
		out = append(out, api.CuriaReviewerSummary{
			ReviewerID:      item.ReviewerID,
			EffectiveWeight: store.EffectiveWeight(context.Background(), domain.AgentProfile{ID: item.ReviewerID}),
		})
	}
	return out
}

func summarizePlanActions(items []events.Record) []api.PlanActionSummary {
	out := make([]api.PlanActionSummary, 0)
	for _, item := range items {
		switch item.Type {
		case events.TypePlanApplied, events.TypePlanRolledBack, events.TypePlanApplyRejected:
		default:
			continue
		}
		summary := api.PlanActionSummary{
			EventType:  string(item.Type),
			TaskID:     item.TaskID,
			Reason:     item.ReasonCode,
			OccurredAt: item.OccurredAt.Format(time.RFC3339),
		}
		if value, ok := item.Payload["artifact_id"].(string); ok {
			summary.ArtifactID = value
		}
		if values, ok := payloadStrings(item.Payload, "changed_paths"); ok {
			summary.ChangedPaths = values
		}
		if values, ok := payloadStrings(item.Payload, "violations"); ok {
			summary.Violations = values
		}
		if values, ok := payloadStrings(item.Payload, "required_checks"); ok {
			summary.RequiredChecks = values
		}
		if value, ok := item.Payload["conflict"].(bool); ok {
			summary.Conflict = value
		}
		if value, ok := item.Payload["conflict_detail"].(string); ok {
			summary.ConflictDetail = value
		}
		if values, ok := payloadStrings(item.Payload, "conflict_paths"); ok {
			summary.ConflictPaths = values
		}
		if items, ok := payloadConflictContext(item.Payload, "conflict_context"); ok {
			summary.ConflictContext = items
		}
		out = append(out, summary)
	}
	return out
}

func payloadStrings(payload map[string]any, key string) ([]string, bool) {
	if payload == nil {
		return nil, false
	}
	value, ok := payload[key]
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok {
				out = append(out, text)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func payloadConflictContext(payload map[string]any, key string) ([]workspacepkg.ConflictSnippet, bool) {
	if payload == nil {
		return nil, false
	}
	value, ok := payload[key]
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []workspacepkg.ConflictSnippet:
		return append([]workspacepkg.ConflictSnippet(nil), typed...), true
	case []any:
		out := make([]workspacepkg.ConflictSnippet, 0, len(typed))
		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			path, _ := entry["path"].(string)
			snippet, _ := entry["snippet"].(string)
			if path == "" && snippet == "" {
				continue
			}
			out = append(out, workspacepkg.ConflictSnippet{Path: path, Snippet: snippet})
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func printPlanInbox(items []api.PlanInboxEntry) error {
	fmt.Println("ARTIFACT\tSESSION\tTASK\tSTATUS\tAPPROVAL\tLAST\tDETAIL")
	for _, item := range items {
		detail := item.LastReason
		if item.ConflictDetail != "" {
			detail = item.ConflictDetail
		} else if item.ConflictSummary != "" {
			detail = item.ConflictSummary
		} else if item.RemediationHint != "" {
			detail = item.RemediationHint
		} else if len(item.Violations) > 0 {
			detail = item.Violations[0]
		}
		if item.ConflictKind != "" {
			detail = item.ConflictKind + ": " + detail
		}
		fmt.Printf(
			"%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			item.ArtifactID,
			item.SessionID,
			item.TaskID,
			item.Status,
			item.HumanApprovalRequired,
			item.LastEventType,
			detail,
		)
	}
	return nil
}

func runStatus(ctx context.Context) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	controlDir := romapath.HomeDir()

	client := api.NewClient(wd)
	queueStore := preferredQueueStore(controlDir)
	sessionStore := preferredHistoryStore(controlDir)
	artifactStore := preferredArtifactStore(controlDir)
	eventStore := preferredEventStore(controlDir)

	daemonMode := "control-plane-local"
	queueCount := 0
	sessionCount := 0
	artifactCount := 0
	eventCount := 0
	activeLeaseCount := 0
	releasedLeaseCount := 0
	recoveredLeaseCount := 0
	pendingApprovalCount := 0
	recoverableSessionCount := 0
	preparedWorkspaceCount := 0
	releasedWorkspaceCount := 0
	reclaimedWorkspaceCount := 0
	mergedWorkspaceCount := 0
	sqlitePath := sqliteutil.DBPath(controlDir)
	sqliteBytes := int64(0)
	sqliteEnabled := false

	if client.Available() {
		daemonMode = "daemon-api"
		if status, err := client.Status(ctx); err == nil {
			queueCount = status.QueueItems
			sessionCount = status.Sessions
			artifactCount = status.Artifacts
			eventCount = status.Events
			activeLeaseCount = status.ActiveLeases
			releasedLeaseCount = status.ReleasedLeases
			recoveredLeaseCount = status.RecoveredLeases
			pendingApprovalCount = status.PendingApprovalTasks
			recoverableSessionCount = status.RecoverableSessions
			preparedWorkspaceCount = status.PreparedWorkspaces
			releasedWorkspaceCount = status.ReleasedWorkspaces
			reclaimedWorkspaceCount = status.ReclaimedWorkspaces
			mergedWorkspaceCount = status.MergedWorkspaces
			sqliteEnabled = status.SQLiteEnabled
			sqlitePath = status.SQLitePath
			sqliteBytes = status.SQLiteBytes
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
	if !client.Available() {
		if items, err := eventStore.ListEvents(ctx, storepkg.EventFilter{}); err == nil {
			eventCount = len(items)
		}
		if info, err := os.Stat(sqlitePath); err == nil {
			sqliteEnabled = true
			sqliteBytes = info.Size()
		}
		if leaseStore, err := scheduler.NewLeaseStore(controlDir); err == nil {
			if items, err := leaseStore.ListByStatus(ctx, scheduler.LeaseStatusActive); err == nil {
				activeLeaseCount = len(items)
				for _, item := range items {
					pendingApprovalCount += len(item.PendingApprovalTaskIDs)
				}
			}
			if items, err := leaseStore.ListByStatus(ctx, scheduler.LeaseStatusReleased); err == nil {
				releasedLeaseCount = len(items)
			}
			if items, err := leaseStore.ListByStatus(ctx, scheduler.LeaseStatusRecovered); err == nil {
				recoveredLeaseCount = len(items)
				for _, item := range items {
					pendingApprovalCount += len(item.PendingApprovalTaskIDs)
				}
			}
		}
		if items, err := listControlPlaneWorkspaces(ctx, sessionStore); err == nil {
			for _, item := range items {
				switch item.Status {
				case "prepared":
					preparedWorkspaceCount++
				case "released":
					releasedWorkspaceCount++
				case "reclaimed":
					reclaimedWorkspaceCount++
				case "merged":
					mergedWorkspaceCount++
				}
			}
		}
		if items, err := scheduler.RecoverableSessions(ctx, controlDir); err == nil {
			recoverableSessionCount = len(items)
		}
	}

	daemonRunning, daemonPID := daemonStatus()
	fmt.Printf("daemon_running=%t\n", daemonRunning)
	fmt.Printf("daemon_pid=%d\n", daemonPID)
	fmt.Printf("mode=%s\n", daemonMode)
	fmt.Printf("queue_items=%d\n", queueCount)
	fmt.Printf("sessions=%d\n", sessionCount)
	fmt.Printf("artifacts=%d\n", artifactCount)
	fmt.Printf("events=%d\n", eventCount)
	fmt.Printf("active_leases=%d\n", activeLeaseCount)
	fmt.Printf("released_leases=%d\n", releasedLeaseCount)
	fmt.Printf("recovered_leases=%d\n", recoveredLeaseCount)
	fmt.Printf("pending_approval_tasks=%d\n", pendingApprovalCount)
	fmt.Printf("recoverable_sessions=%d\n", recoverableSessionCount)
	fmt.Printf("prepared_workspaces=%d\n", preparedWorkspaceCount)
	fmt.Printf("released_workspaces=%d\n", releasedWorkspaceCount)
	fmt.Printf("reclaimed_workspaces=%d\n", reclaimedWorkspaceCount)
	fmt.Printf("merged_workspaces=%d\n", mergedWorkspaceCount)
	fmt.Printf("sqlite_enabled=%t\n", sqliteEnabled)
	fmt.Printf("sqlite_path=%s\n", filepath.Clean(sqlitePath))
	fmt.Printf("sqlite_bytes=%d\n", sqliteBytes)
	return nil
}

func listControlPlaneWorkspaces(ctx context.Context, sessionStore history.Backend) ([]workspacepkg.Prepared, error) {
	if sessionStore == nil {
		return nil, nil
	}
	sessions, err := sessionStore.List(ctx)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0)
	seen := make(map[string]struct{})
	for _, session := range sessions {
		root := strings.TrimSpace(session.WorkingDir)
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	items := make([]workspacepkg.Prepared, 0)
	for _, root := range roots {
		manager := workspacepkg.NewManager(root, nil)
		records, err := manager.List(ctx)
		if err != nil {
			continue
		}
		items = append(items, records...)
	}
	return items, nil
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
	client := api.NewClient(wd)
	var items []scheduler.RecoverySnapshot
	if client.Available() {
		items, err = client.RecoveryList(ctx)
		if err != nil {
			return err
		}
	} else {
		items, err = scheduler.RecoverableSessions(ctx, wd)
		if err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery snapshot: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

func runPolicy(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("policy")
		return nil
	}
	if len(args) == 0 || args[0] != "check" {
		return fmt.Errorf("unknown policy subcommand")
	}
	req, err := parseRunArgs(args[1:])
	if err != nil {
		return err
	}
	registry, err := agents.DefaultRegistry()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	registry.SetUserConfigPath(agents.DefaultUserConfigPath())
	if err := registry.LoadUserConfig(registry.UserConfigPath()); err != nil {
		return fmt.Errorf("load user agent config: %w", err)
	}
	if req.StarterAgent == "" {
		profile, err := registry.DefaultProfile(ctx)
		if err != nil {
			return err
		}
		req.StarterAgent = profile.ID
	}
	if req.WorkingDir == "" {
		req.WorkingDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	decision, err := policy.NewSimpleBroker(nil).Evaluate(ctx, policy.Request{
		SessionID:      "policy_check",
		TaskID:         "policy_check",
		Mode:           "direct",
		Prompt:         req.Prompt,
		WorkingDir:     req.WorkingDir,
		EffectiveDir:   req.WorkingDir,
		StarterAgent:   req.StarterAgent,
		Delegates:      req.Delegates,
		NodeCount:      1 + len(req.Delegates),
		PolicyOverride: req.PolicyOverride,
		OverrideActor:  req.OverrideActor,
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

func runPlans(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("plan")
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = syncWorkspace(ctx, wd)
	if len(args) == 0 {
		return fmt.Errorf("unknown plans subcommand")
	}
	service := plans.NewService(preferredArtifactStore(wd), workspacepkg.NewManager(wd, preferredEventStore(wd)), preferredEventStore(wd))
	client := api.NewClient(wd)
	switch args[0] {
	case "approve":
		if len(args) < 2 {
			return fmt.Errorf("roma plans approve requires an artifact id")
		}
		actor := policy.OverrideActor()
		if len(args) > 2 && strings.HasPrefix(args[2], "--actor=") {
			actor = strings.TrimPrefix(args[2], "--actor=")
		}
		if client.Available() {
			return client.PlanApprove(ctx, args[1], actor)
		}
		return service.Approve(ctx, args[1], actor)
	case "reject":
		if len(args) < 2 {
			return fmt.Errorf("roma plans reject requires an artifact id")
		}
		actor := policy.OverrideActor()
		if len(args) > 2 && strings.HasPrefix(args[2], "--actor=") {
			actor = strings.TrimPrefix(args[2], "--actor=")
		}
		if client.Available() {
			return client.PlanReject(ctx, args[1], actor)
		}
		return service.Reject(ctx, args[1], actor)
	case "inbox":
		sessionID := ""
		if len(args) > 2 && args[1] == "--session" {
			sessionID = args[2]
		}
		if client.Available() {
			items, err := client.PlanInbox(ctx, sessionID)
			if err != nil {
				return err
			}
			return printPlanInbox(items)
		}
		items, err := service.Inbox(ctx, sessionID)
		if err != nil {
			return err
		}
		apiItems := make([]api.PlanInboxEntry, 0, len(items))
		for _, item := range items {
			apiItems = append(apiItems, api.PlanInboxEntry{
				ArtifactID:            item.ArtifactID,
				SessionID:             item.SessionID,
				TaskID:                item.TaskID,
				Goal:                  item.Goal,
				Status:                item.Status,
				HumanApprovalRequired: item.HumanApprovalRequired,
				ExpectedFiles:         item.ExpectedFiles,
				ForbiddenPaths:        item.ForbiddenPaths,
				LastEventType:         item.LastEventType,
				LastReason:            item.LastReason,
				LastOccurredAt:        item.LastOccurredAt,
				Violations:            item.Violations,
				Conflict:              item.Conflict,
				ConflictKind:          item.ConflictKind,
				ConflictDetail:        item.ConflictDetail,
				ConflictSummary:       item.ConflictSummary,
				ConflictPaths:         item.ConflictPaths,
				ConflictContext:       item.ConflictContext,
				RemediationHint:       item.RemediationHint,
				ResolutionOptions:     item.ResolutionOptions,
				ResolutionSteps:       item.ResolutionSteps,
			})
		}
		return printPlanInbox(apiItems)
	case "inspect":
		if len(args) < 2 {
			return fmt.Errorf("roma plans inspect requires an artifact id")
		}
		if client.Available() {
			envelope, err := client.PlanInspect(ctx, args[1])
			if err != nil {
				return err
			}
			_, plan, err := service.Inspect(ctx, args[1])
			if err != nil {
				return err
			}
			raw, err := json.MarshalIndent(map[string]any{
				"artifact": envelope,
				"plan":     plan,
			}, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal plan inspect: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		envelope, plan, err := service.Inspect(ctx, args[1])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(map[string]any{
			"artifact": envelope,
			"plan":     plan,
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal plan inspect: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	case "apply":
		if len(args) < 4 {
			return fmt.Errorf("roma plans apply requires <session_id> <task_id> <artifact_id>")
		}
		dryRun := false
		policyOverride := false
		overrideActor := ""
		for _, arg := range args[4:] {
			switch {
			case arg == "--dry-run":
				dryRun = true
			case arg == "--policy-override":
				policyOverride = true
			case strings.HasPrefix(arg, "--override-actor="):
				overrideActor = strings.TrimPrefix(arg, "--override-actor=")
			}
		}
		if policyOverride && overrideActor == "" {
			overrideActor = policy.OverrideActor()
		}
		if client.Available() {
			result, err := client.PlanApply(ctx, api.PlanApplyRequest{
				SessionID:           args[1],
				TaskID:              args[2],
				ArtifactID:          args[3],
				DryRun:              dryRun,
				PolicyOverride:      policyOverride,
				PolicyOverrideActor: overrideActor,
			})
			if err != nil {
				return err
			}
			raw, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal plan apply: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		result, err := service.Apply(ctx, args[1], args[2], args[3], plans.ApplyOptions{
			DryRun:              dryRun,
			PolicyOverride:      policyOverride,
			PolicyOverrideActor: overrideActor,
		})
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal plan apply: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	case "preview":
		if len(args) < 4 {
			return fmt.Errorf("roma plans preview requires <session_id> <task_id> <artifact_id>")
		}
		if client.Available() {
			result, err := client.PlanPreview(ctx, api.PlanApplyRequest{
				SessionID:  args[1],
				TaskID:     args[2],
				ArtifactID: args[3],
			})
			if err != nil {
				return err
			}
			raw, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal plan preview: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		result, err := service.Preview(ctx, args[1], args[2], args[3])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal plan preview: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	case "rollback":
		if len(args) < 4 {
			return fmt.Errorf("roma plans rollback requires <session_id> <task_id> <artifact_id>")
		}
		if client.Available() {
			result, err := client.PlanRollback(ctx, api.PlanApplyRequest{
				SessionID:  args[1],
				TaskID:     args[2],
				ArtifactID: args[3],
			})
			if err != nil {
				return err
			}
			raw, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal plan rollback: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		}
		result, err := service.Rollback(ctx, args[1], args[2], args[3])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal plan rollback: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	default:
		return fmt.Errorf("unknown plans subcommand %q", args[0])
	}
}

func runTasks(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("task")
		return nil
	}
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

func runWorkspaces(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("workspace")
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	client := api.NewClient(wd)
	manager := workspacepkg.NewManager(wd, preferredEventStore(wd))

	if client.Available() && (len(args) == 0 || args[0] == "list") {
		items, err := client.WorkspaceList(ctx)
		if err != nil {
			return err
		}
		fmt.Println("SESSION\tTASK\tSTATUS\tPROVIDER\tMODE\tDIR")
		for _, item := range items {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", item.SessionID, item.TaskID, item.Status, item.Provider, item.Effective, item.EffectiveDir)
		}
		return nil
	}
	if client.Available() && len(args) > 2 && args[0] == "show" {
		item, err := client.WorkspaceGet(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal workspace: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	if client.Available() && len(args) > 0 && args[0] == "cleanup" {
		items, err := client.WorkspaceCleanup(ctx)
		if err != nil {
			return err
		}
		fmt.Println("SESSION\tTASK\tSTATUS\tPROVIDER\tMODE\tDIR")
		for _, item := range items {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", item.SessionID, item.TaskID, item.Status, item.Provider, item.Effective, item.EffectiveDir)
		}
		return nil
	}
	if client.Available() && len(args) > 2 && args[0] == "merge" {
		item, err := client.WorkspaceMerge(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal workspace: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}

	if len(args) == 0 || args[0] == "list" {
		items, err := manager.List(ctx)
		if err != nil {
			return err
		}
		fmt.Println("SESSION\tTASK\tSTATUS\tPROVIDER\tMODE\tDIR")
		for _, item := range items {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", item.SessionID, item.TaskID, item.Status, item.Provider, item.Effective, item.EffectiveDir)
		}
		return nil
	}
	if args[0] == "show" {
		if len(args) < 3 {
			return fmt.Errorf("roma workspaces show requires a session id and task id")
		}
		item, err := manager.Get(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(item, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal workspace: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	if args[0] == "cleanup" {
		if err := manager.ReclaimStale(ctx); err != nil {
			return err
		}
		items, err := manager.List(ctx)
		if err != nil {
			return err
		}
		fmt.Println("SESSION\tTASK\tSTATUS\tPROVIDER\tMODE\tDIR")
		for _, item := range items {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", item.SessionID, item.TaskID, item.Status, item.Provider, item.Effective, item.EffectiveDir)
		}
		return nil
	}
	if args[0] == "merge" {
		if len(args) < 3 {
			return fmt.Errorf("roma workspaces merge requires a session id and task id")
		}
		item, err := manager.Get(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		if err := manager.MergeBack(ctx, item); err != nil {
			return err
		}
		updated, err := manager.Get(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(updated, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal workspace: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	return fmt.Errorf("unknown workspaces subcommand %q", args[0])
}

func runEvents(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printTopicUsage("event")
		return nil
	}
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
	controlDir := romapath.HomeDir()
	sqliteStore, err := history.NewSQLiteStore(controlDir)
	if err == nil {
		return sqliteStore
	}
	return history.NewStore(controlDir)
}

func preferredEventStore(workDir string) storepkg.EventStore {
	controlDir := romapath.HomeDir()
	sqliteStore, err := storepkg.NewSQLiteEventStore(controlDir)
	if err == nil {
		return sqliteStore
	}
	return storepkg.NewFileEventStore(controlDir)
}

func preferredTaskStore(workDir string) storepkg.TaskStore {
	controlDir := romapath.HomeDir()
	sqliteStore, err := taskstore.NewSQLiteStore(controlDir)
	if err == nil {
		return sqliteStore
	}
	return taskstore.NewStore(controlDir)
}

func preferredArtifactStore(workDir string) artifacts.Backend {
	controlDir := romapath.HomeDir()
	sqliteStore, err := artifacts.NewSQLiteStore(controlDir)
	if err == nil {
		return sqliteStore
	}
	return artifacts.NewFileStore(controlDir)
}

func preferredQueueStore(workDir string) queue.Backend {
	controlDir := romapath.HomeDir()
	fileStore := queue.NewStore(controlDir)
	sqliteStore, err := queue.NewSQLiteStore(controlDir)
	if err == nil {
		return queue.NewMirrorStore(sqliteStore, fileStore)
	}
	return fileStore
}

func syncWorkspace(ctx context.Context, workDir string) error {
	_ = workDir
	return syncdb.NewWorkspace(romapath.HomeDir()).Run(ctx)
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
		case "--with", "--delegate":
			i++
			if i >= len(args) {
				return runsvc.Request{}, fmt.Errorf("%s requires a value", args[i-1])
			}
			for _, part := range strings.Split(args[i], ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					req.Delegates = append(req.Delegates, part)
				}
			}
		case "--continuous":
			req.Continuous = true
		case "--policy-override":
			req.PolicyOverride = true
		case "--override-actor":
			i++
			if i >= len(args) {
				return runsvc.Request{}, fmt.Errorf("--override-actor requires a value")
			}
			req.OverrideActor = args[i]
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
	if req.PolicyOverride && req.OverrideActor == "" {
		req.OverrideActor = policy.OverrideActor()
	}
	return req, nil
}

func printUsage() {
	fmt.Println("roma usage:")
	fmt.Println("  roma <command> [subcommand] [flags]")
	fmt.Println("")
	fmt.Println("Core:")
	fmt.Println("  roma --help")
	fmt.Println("  roma tui [--cwd <dir>]")
	fmt.Println(`  roma run [--agent <id>] [--with <id,...>] [--cwd <dir>] [--continuous] [--max-rounds <n>] [--policy-override] [--override-actor <id>] "<prompt>"`)
	fmt.Println("  roma status")
	fmt.Println("  roma result show <session_id>")
	fmt.Println("  roma help <topic>")
	fmt.Println("")
	fmt.Println("Daemon Control:")
	fmt.Println("  roma start [--acp-port <port>]")
	fmt.Println("  roma stop")
	fmt.Println("  roma acp status")
	fmt.Println("")
	fmt.Println("Management:")
	fmt.Println("  agent       manage coding-agent profiles")
	fmt.Println("  queue       inspect and control daemon jobs")
	fmt.Println("  artifact    inspect stored artifacts")
	fmt.Println("  debug       inspect sessions, tasks, artifacts, events, plans, and workspaces")
	fmt.Println("")
	fmt.Println("Debug:")
	fmt.Println("  graph                    run multi-agent graph")
	fmt.Println("  replay                   replay a session")
	fmt.Println("  recover                  recover a session")
	fmt.Println("")
	fmt.Println("Shortcuts:")
	fmt.Println("  roma approve <job_id>")
	fmt.Println("  roma reject <job_id>")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  roma")
	fmt.Println("  roma help queue")
	fmt.Println("  roma help agent")
	fmt.Println("  roma tui")
	fmt.Println(`  roma agent add my-codex "My Codex" /usr/bin/codex --arg exec --arg --full-auto --arg {prompt} --pty`)
	fmt.Println(`  roma run --agent my-codex --with my-gemini,my-copilot "build a feature"`)
}

func printTopicUsage(topic string) {
	switch strings.ToLower(strings.TrimSpace(topic)) {
	case "acp":
		fmt.Println("roma acp usage:")
		fmt.Println("  roma acp status")
	case "agent", "agents":
		fmt.Println("roma agent usage:")
		fmt.Println("  roma agent list")
		fmt.Println("  roma agent add <id> <name> <path> [--arg <arg>] [--alias <a1,a2>] [--pty] [--mcp] [--json]")
		fmt.Println("  roma agent remove <id>")
		fmt.Println("  roma agent inspect <id>")
	case "artifact", "artifacts":
		fmt.Println("roma artifact usage:")
		fmt.Println("  roma artifact list [--session <session_id>] [--kind <kind>]")
		fmt.Println("  roma artifact show <artifact_id>")
	case "event", "events":
		fmt.Println("roma event usage:")
		fmt.Println("  roma event list [--session <session_id>] [--task <task_id>] [--type <event_type>]")
		fmt.Println("  roma event show <event_id>")
	case "plan", "plans":
		fmt.Println("roma plan usage:")
		fmt.Println("  roma plan inbox [--session <session_id>]")
		fmt.Println("  roma plan inspect <artifact_id>")
		fmt.Println("  roma plan preview <session_id> <task_id> <artifact_id>")
		fmt.Println("  roma plan apply <session_id> <task_id> <artifact_id> [--dry-run] [--policy-override] [--override-actor <name>]")
		fmt.Println("  roma plan rollback <session_id> <task_id> <artifact_id>")
		fmt.Println("  roma plan approve <artifact_id>")
		fmt.Println("  roma plan reject <artifact_id>")
	case "queue":
		fmt.Println("roma queue usage:")
		fmt.Println("  roma queue list [--status <status>] [--mode <direct|graph>]")
		fmt.Println("  roma queue show <job_id>")
		fmt.Println("  roma queue inspect <job_id>")
		fmt.Println("  roma queue attach <job_id> [--raw]")
		fmt.Println("  roma queue tail <job_id> [--raw]")
		fmt.Println("  roma queue cancel <job_id>")
	case "result", "results":
		fmt.Println("roma result usage:")
		fmt.Println("  roma result show <session_id>")
	case "tui":
		fmt.Println("roma tui usage:")
		fmt.Println("  roma tui [--cwd <dir>]")
		fmt.Println("")
		fmt.Println("The TUI starts an embedded romad for the selected working directory and stops it when the TUI exits.")
	case "debug":
		fmt.Println("roma debug usage:")
		fmt.Println("  roma debug session <subcommand>")
		fmt.Println("  roma debug task <subcommand>")
		fmt.Println("  roma debug artifact <subcommand>")
		fmt.Println("  roma debug event <subcommand>")
		fmt.Println("  roma debug plan <subcommand>")
		fmt.Println("  roma debug workspace <subcommand>")
		fmt.Println("  roma debug graph run --file <graph.json>")
		fmt.Println("  roma debug curia reputation [--reviewer <agent_id>]")
		fmt.Println("  roma debug policy check --agent <id> \"<prompt>\"")
		fmt.Println("  roma debug replay <session_id>")
		fmt.Println("  roma debug recover")
	case "debug session":
		printTopicUsage("session")
	case "debug task":
		printTopicUsage("task")
	case "debug artifact":
		printTopicUsage("artifact")
	case "debug event":
		printTopicUsage("event")
	case "debug plan":
		printTopicUsage("plan")
	case "debug workspace":
		printTopicUsage("workspace")
	case "debug graph":
		printTopicUsage("graph")
	case "debug curia":
		printTopicUsage("curia")
	case "debug policy":
		printTopicUsage("policy")
	case "debug replay":
		printTopicUsage("replay")
	case "debug recover":
		printTopicUsage("recover")
	case "session", "sessions":
		fmt.Println("roma session usage:")
		fmt.Println("  roma session list")
		fmt.Println("  roma session show <session_id>")
		fmt.Println("  roma session inspect <session_id>")
		fmt.Println("  roma session curia <session_id>")
	case "task", "tasks":
		fmt.Println("roma task usage:")
		fmt.Println("  roma task list [--session <session_id>]")
		fmt.Println("  roma task show <task_id>")
		fmt.Println("  roma task approve <task_id>")
		fmt.Println("  roma task reject <task_id>")
	case "workspace", "workspaces":
		fmt.Println("roma workspace usage:")
		fmt.Println("  roma workspace list")
		fmt.Println("  roma workspace show <session_id> <task_id>")
		fmt.Println("  roma workspace cleanup")
		fmt.Println("  roma workspace merge <session_id> <task_id>")
	case "graph":
		fmt.Println("roma graph usage:")
		fmt.Println("  roma debug graph run --file <graph.json> [--cwd <dir>] [--continuous] [--max-rounds <n>]")
	case "curia":
		fmt.Println("roma curia usage:")
		fmt.Println("  roma debug curia reputation [--reviewer <agent_id>]")
	case "policy":
		fmt.Println("roma policy usage:")
		fmt.Println(`  roma policy check --agent <id> [--with <id,...>] [--cwd <dir>] "<prompt>"`)
	case "run":
		fmt.Println("roma run usage:")
		fmt.Println(`  roma run [--agent <id>] [--with <id,...>] [--cwd <dir>] [--continuous] [--max-rounds <n>] [--policy-override] [--override-actor <name>] "<prompt>"`)
		fmt.Println("")
		fmt.Println("Flags:")
		fmt.Println("  --agent <id>         starter agent ID (default: first available)")
		fmt.Println("  --with <id,...>      delegate agent IDs (comma-separated)")
		fmt.Println("  --cwd <dir>          working directory (default: current directory)")
		fmt.Println("  --continuous         run until completion or max rounds")
		fmt.Println("  --max-rounds <n>     maximum number of refinement rounds")
		fmt.Println("  --policy-override    override safety policies")
		fmt.Println("  --override-actor <n> name of the actor performing the override")
	case "submit", "tell", "ask":
		fmt.Println("roma run usage:")
		fmt.Println(`  "submit", "tell", and "ask" were removed.`)
		fmt.Println(`  Use: roma run [--agent <id>] [--with <id,...>] [--cwd <dir>] [--continuous] [--max-rounds <n>] [--policy-override] [--override-actor <name>] "<prompt>"`)
	case "replay":
		fmt.Println("roma replay usage:")
		fmt.Println("  roma replay <session_id>")
	case "recover":
		fmt.Println("roma recover usage:")
		fmt.Println("  roma recover")
	case "status":
		fmt.Println("roma status usage:")
		fmt.Println("  roma status")
	default:
		printUsage()
	}
}

func isHelpArg(value string) bool {
	switch strings.TrimSpace(value) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func queueLabel(req queue.Request) string {
	if req.GraphFile != "" || req.Graph != nil {
		return "graph"
	}
	if req.StarterAgent == "" {
		return "direct"
	}
	return req.StarterAgent
}

func queueNodeSummary(ctx context.Context, wd string, req queue.Request) string {
	if req.SessionID == "" {
		return "-"
	}
	controlDir := romapath.HomeDir()
	workspaceDir := req.WorkingDir
	sessionStore := preferredHistoryStore(controlDir)
	if session, err := sessionStore.Get(ctx, req.SessionID); err == nil && session.WorkingDir != "" {
		workspaceDir = session.WorkingDir
	}
	taskStore := preferredTaskStore(controlDir)
	items, err := taskStore.ListTasksBySession(ctx, req.SessionID)
	if err != nil || len(items) == 0 {
		return "-"
	}
	total := len(items)
	succeeded := 0
	running := 0
	waiting := 0
	failed := 0
	for _, item := range items {
		switch item.State {
		case domain.TaskStateSucceeded:
			succeeded++
		case domain.TaskStateRunning, domain.TaskStateReady:
			running++
		case domain.TaskStateAwaitingApproval:
			waiting++
		case domain.TaskStateFailedRecoverable, domain.TaskStateFailedTerminal, domain.TaskStateCancelled:
			failed++
		}
	}
	summary := fmt.Sprintf("nodes=%d ok=%d run=%d wait=%d fail=%d", total, succeeded, running, waiting, failed)
	eventStore := preferredEventStore(controlDir)
	var eventItems []events.Record
	if items, err := eventStore.ListEvents(ctx, storepkg.EventFilter{SessionID: req.SessionID}); err == nil {
		eventItems = items
	}
	var workspaceItems []workspacepkg.Prepared
	if workspaceDir != "" {
		manager := workspacepkg.NewManager(workspaceDir, nil)
		if items, err := manager.List(ctx); err == nil {
			for _, item := range items {
				if item.SessionID == req.SessionID {
					workspaceItems = append(workspaceItems, item)
				}
			}
		}
	}
	var lease *scheduler.LeaseRecord
	if leaseStore, err := scheduler.NewLeaseStore(controlDir); err == nil {
		if item, err := leaseStore.Get(ctx, req.SessionID); err == nil {
			lease = &item
		}
	}
	if live := api.EnrichRuntimeLive(api.SummarizeRuntimeLive(string(req.Status), items, eventItems, workspaceItems, lease, req.UpdatedAt), req.StarterAgent, req.Delegates); live != nil && req.Status == queue.StatusRunning {
		if live.CurrentTaskID != "" {
			summary += " current=" + live.CurrentTaskID
		}
		if live.CurrentAgentID != "" {
			summary += " agent=" + live.CurrentAgentID
		}
		if live.ProcessPID > 0 {
			summary += " pid=" + strconv.Itoa(live.ProcessPID)
		}
		if live.Phase != "" {
			summary += " phase=" + live.Phase
		}
		if live.CurrentRound > 0 {
			summary += " round=" + strconv.Itoa(live.CurrentRound)
		}
		if live.ParticipantCount > 1 {
			summary += " agents=" + strconv.Itoa(live.ParticipantCount)
		}
	}
	artifactStore := preferredArtifactStore(controlDir)
	artifactsForSession, err := artifactStore.List(ctx, req.SessionID)
	if err != nil || len(artifactsForSession) == 0 {
		return summary
	}
	if curia := queueCuriaSuffix(artifactsForSession); curia != "" {
		return summary + " " + curia
	}
	return summary
}

func queueCuriaSuffix(items []domain.ArtifactEnvelope) string {
	var latestDebate *artifacts.DebateLogPayload
	var latestDecision *artifacts.DecisionPackPayload
	for _, envelope := range items {
		switch envelope.Kind {
		case domain.ArtifactKindDebateLog:
			if payload, ok := artifacts.DebateLogFromEnvelope(envelope); ok {
				value := payload
				latestDebate = &value
			}
		case domain.ArtifactKindDecisionPack:
			if payload, ok := artifacts.DecisionPackFromEnvelope(envelope); ok {
				value := payload
				latestDecision = &value
			}
		}
	}
	if latestDebate == nil && latestDecision == nil {
		return ""
	}
	parts := []string{"curia"}
	if latestDecision != nil && latestDecision.WinningMode != "" {
		parts = append(parts, "mode="+latestDecision.WinningMode)
	}
	if latestDecision != nil && latestDecision.Arbitrated {
		if latestDecision.ArbitratorID != "" {
			parts = append(parts, "arbitrated="+latestDecision.ArbitratorID)
		} else {
			parts = append(parts, "arbitrated=true")
		}
	}
	if latestDebate != nil && latestDebate.DisputeClass != "" && latestDebate.DisputeClass != "none" {
		parts = append(parts, "dispute="+latestDebate.DisputeClass)
	}
	return strings.Join(parts, " ")
}

func parseQueueArgs(args []string) (statusFilter string, modeFilter string, subcommand string, subArg string, raw bool, err error) {
	subcommand = "list"
	expectJobID := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "list":
			subcommand = "list"
			expectJobID = false
		case "show", "inspect", "cancel", "tail", "attach":
			subcommand = args[i]
			expectJobID = true
		case "--status":
			i++
			if i >= len(args) {
				return "", "", "", "", false, fmt.Errorf("--status requires a value")
			}
			statusFilter = args[i]
		case "--mode":
			i++
			if i >= len(args) {
				return "", "", "", "", false, fmt.Errorf("--mode requires a value")
			}
			modeFilter = args[i]
		case "--raw":
			raw = true
		default:
			if expectJobID && subArg == "" {
				subArg = args[i]
				expectJobID = false
				continue
			}
			return "", "", "", "", false, fmt.Errorf("unknown queue argument %q", args[i])
		}
	}
	if expectJobID || ((subcommand == "show" || subcommand == "inspect" || subcommand == "cancel" || subcommand == "tail" || subcommand == "attach") && subArg == "") {
		return "", "", "", "", false, fmt.Errorf("roma queue %s requires a job id", subcommand)
	}
	return statusFilter, modeFilter, subcommand, subArg, raw, nil
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

func queueTailEventLabel(typ events.Type) string {
	switch typ {
	case events.TypeRelayNodeStarted:
		return "node-start"
	case events.TypeRelayNodeCompleted:
		return "node-done"
	case events.TypeWorkspacePrepared:
		return "workspace"
	case events.TypeRuntimeStarted:
		return "runtime-start"
	case events.TypeRuntimeStdoutCaptured:
		return "output"
	case events.TypeRuntimeExited:
		return "runtime-exit"
	case events.TypeApprovalRequested:
		return "approval"
	case events.TypeDangerousCommandDetected:
		return "dangerous"
	case events.TypeHighRiskChangeDetected:
		return "high-risk"
	case events.TypeDelegationRequested:
		return "delegate"
	case events.TypeExecutionCompletedDetected:
		return "done"
	case events.TypeParseWarning:
		return "parse-warning"
	case events.TypeSemanticReportProduced:
		return "semantic"
	case events.TypeSemanticApprovalRecommended:
		return "approval-recommend"
	case events.TypeCuriaPromotionRecommended:
		return "curia-recommend"
	case events.TypePlanApplyRejected, events.TypePlanApplied, events.TypePlanRolledBack:
		return "plan"
	case events.TypeTaskStateChanged:
		return "task"
	default:
		return string(typ)
	}
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func payloadInt(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return n
	default:
		return 0
	}
}
