package agentorchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NexixAI/nexixai-agentos/internal/audit"
	"github.com/NexixAI/nexixai-agentos/internal/id"
	"github.com/NexixAI/nexixai-agentos/internal/metrics"
	"github.com/NexixAI/nexixai-agentos/internal/pii"
	"github.com/NexixAI/nexixai-agentos/internal/tools"
	"github.com/NexixAI/nexixai-agentos/internal/types"
	"github.com/NexixAI/nexixai-agentos/internal/webhook"
	"github.com/NexixAI/nexixai-agentos/modelpolicy"
)

// executeRun is the core agent loop. It runs within a worker goroutine.
// It orchestrates the run lifecycle by delegating to phase-specific helpers.
func (e *Executor) executeRun(job *runJob) {
	run := job.run

	e.transitionToRunning(&run, job)
	defer metrics.DecRunsActive(run.TenantID)

	// Determine limits.
	maxSteps := e.defaultMaxSteps
	if run.RunOptions.MaxSteps > 0 {
		maxSteps = run.RunOptions.MaxSteps
	}
	if job.agent.Config != nil && job.agent.Config.MaxSteps > 0 && run.RunOptions.MaxSteps == 0 {
		maxSteps = job.agent.Config.MaxSteps
	}

	// Load conversation memory.
	history, err := e.memory.GetRecent(job.ctx, run.TenantID, run.AgentID, e.memMaxMessages, e.memMaxTokens)
	if err != nil {
		slog.Error("memory load error", "run_id", run.RunID, "tenant_id", run.TenantID, "error", err)
		// Continue with empty history on error.
		history = nil
	}

	// Build initial prompt.
	messages := buildPrompt(job.agent, run, history)

	runToolRegistry, toolDefs := e.loadAgentToolsAndDefs(job, &run)

	// Determine model ID: agent config > executor default > fail.
	var modelID string
	if job.agent.Config != nil && job.agent.Config.ModelID != "" {
		modelID = job.agent.Config.ModelID
	} else if e.defaultModel != "" {
		modelID = e.defaultModel
	} else {
		e.failRun(&run, job, "no_model", "no model configured for agent and no default model set")
		return
	}

	var totalUsage modelpolicy.ChatUsage
	step := 0
	for step < maxSteps {
		step++

		// Check context cancellation before model call.
		if err := job.ctx.Err(); err != nil {
			e.failRun(&run, job, "canceled", "run canceled")
			return
		}

		// PII scan: scan the latest user message before model invocation.
		blocked := e.scanInputForPII(&run, job, messages)
		if blocked {
			return
		}

		resp, err := e.invokeModel(job, &run, modelID, messages, toolDefs, step)
		if err != nil {
			if job.ctx.Err() != nil {
				e.failRun(&run, job, "canceled", "run canceled")
				return
			}
			e.failRun(&run, job, "provider_error", err.Error())
			return
		}

		if len(resp.Choices) == 0 {
			e.failRun(&run, job, "provider_error", "no choices in model response")
			return
		}

		choice := resp.Choices[0]

		// Record token usage.
		totalUsage.PromptTokens += resp.Usage.PromptTokens
		totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens
		metrics.AddModelTokens(run.TenantID, modelID, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		if e.tokenBudget != nil {
			e.tokenBudget.Record(run.TenantID, resp.Usage.TotalTokens)
		}

		job.events.Emit("step.model_response", map[string]any{
			"step":          step,
			"finish_reason": choice.FinishReason,
			"usage":         resp.Usage,
		})

		// PII scan: scan model output in warn mode.
		if e.piiDetector != nil {
			if outText := modelpolicy.MessageText(choice.Message); outText != "" {
				detections := e.piiDetector.Scan(outText)
				if len(detections) > 0 {
					slog.Warn("PII detected in model output", "run_id", run.RunID, "tenant_id", run.TenantID, "detections", len(detections))
				}
			}
		}

		// Check for tool calls.
		if choice.FinishReason == "tool_calls" && len(choice.Message.ToolCalls) > 0 {
			messages = append(messages, choice.Message)

			// Execute tool calls concurrently.
			type toolResult struct {
				tc         modelpolicy.ToolCall
				result     string
				durationMs int64
			}
			results := make([]toolResult, len(choice.Message.ToolCalls))
			var wg sync.WaitGroup

			for i, tc := range choice.Message.ToolCalls {
				job.events.Emit("step.tool_call", map[string]any{
					"step":      step,
					"tool_name": tc.Function.Name,
					"tool_id":   tc.ID,
				})

				results[i].tc = tc
				wg.Add(1)
				go func(idx int, tc modelpolicy.ToolCall) {
					defer wg.Done()
					toolStart := time.Now()
					result := runToolRegistry.Execute(job.ctx, tc.Function.Name, tc.Function.Arguments)
					result = e.truncateToolOutput(result)
					toolDur := time.Since(toolStart)
					toolStatus := "ok"
					if strings.HasPrefix(result, "error:") {
						toolStatus = "error"
					}
					metrics.ObserveToolExec(tc.Function.Name, toolStatus, toolDur.Seconds())
					results[idx].result = result
					results[idx].durationMs = toolDur.Milliseconds()
				}(i, tc)
			}
			wg.Wait()

			// Emit results and append messages in order.
			for _, tr := range results {
				job.events.Emit("step.tool_result", map[string]any{
					"step":    step,
					"tool_id": tr.tc.ID,
					"result":  truncate(tr.result, 500),
				})
				messages = append(messages, modelpolicy.ChatMessage{
					Role:       "tool",
					Content:    tr.result,
					ToolCallID: tr.tc.ID,
				})

				// Emit webhook step event for each tool execution.
				e.webhook.Send(webhook.Event{
					Event:     webhook.EventRunStep,
					EventID:   id.New("evt"),
					TenantID:  run.TenantID,
					RunID:     run.RunID,
					AgentID:   run.AgentID,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
					Data: map[string]any{
						"step_id":     tr.tc.ID,
						"tool_name":   tr.tc.Function.Name,
						"tool_input":  truncate(tr.tc.Function.Arguments, 512),
						"tool_output": truncate(tr.result, 512),
						"duration_ms": tr.durationMs,
					},
				})
			}
			continue
		}

		// Final response (finish_reason == "stop" or anything without tool calls).
		run.Status = "completed"
		run.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		run.Output = &types.RunOutput{Type: "text", Text: modelpolicy.MessageText(choice.Message)}

		if err := e.runs.Save(context.Background(), run); err != nil {
			slog.Error("failed to save completed state", "run_id", run.RunID, "tenant_id", run.TenantID, "error", err)
		}

		metrics.IncRunLifecycle(run.TenantID, "completed")
		metrics.ObserveRunSteps(run.TenantID, step)

		job.events.Emit("run.completed", map[string]any{
			"run_id":      run.RunID,
			"total_steps": step,
			"usage":       totalUsage,
		})
		e.webhook.Send(webhook.Event{
			Event:     webhook.EventRunCompleted,
			EventID:   id.New("evt"),
			TenantID:  run.TenantID,
			RunID:     run.RunID,
			AgentID:   run.AgentID,
			Timestamp: run.CompletedAt,
			Data:      map[string]any{"total_steps": step},
		})

		// Persist conversation to memory (include user input + assistant response).
		e.saveConversation(job.ctx, run.TenantID, run.AgentID, messages)
		return
	}

	// Max steps exceeded.
	e.failRun(&run, job, "max_steps_exceeded",
		fmt.Sprintf("run exceeded maximum of %d steps", maxSteps))
}

// transitionToRunning moves a run from queued to running, persists the state,
// emits events/webhooks, and increments active-run metrics.
func (e *Executor) transitionToRunning(run *types.Run, job *runJob) {
	run.Status = "running"
	run.StartedAt = time.Now().UTC().Format(time.RFC3339)
	if err := e.runs.Save(job.ctx, *run); err != nil {
		slog.Error("failed to save running state", "run_id", run.RunID, "tenant_id", run.TenantID, "error", err)
	}
	job.events.Emit("run.started", map[string]any{"run_id": run.RunID})
	e.webhook.Send(webhook.Event{
		Event:     webhook.EventRunStarted,
		EventID:   id.New("evt"),
		TenantID:  run.TenantID,
		RunID:     run.RunID,
		AgentID:   run.AgentID,
		Timestamp: run.StartedAt,
	})
	metrics.IncRunLifecycle(run.TenantID, "started")
	metrics.IncRunsActive(run.TenantID)
}

// loadAgentToolsAndDefs resolves the tool registry (including custom tools) and
// builds the model-compatible tool definitions for the run's agent.
func (e *Executor) loadAgentToolsAndDefs(job *runJob, run *types.Run) (*tools.Registry, []modelpolicy.ToolDef) {
	var agentTools []string
	runToolRegistry := e.tools
	if job.agent.Config != nil {
		agentTools = job.agent.Config.Tools
		if len(job.agent.Config.CustomTools) > 0 {
			cloned, err := e.tools.RegisterCustomTools(job.agent.Config.CustomTools)
			if err != nil {
				slog.Error("custom tool registration error", "run_id", run.RunID, "tenant_id", run.TenantID, "error", err)
			} else {
				runToolRegistry = cloned
			}
		}
	}
	toolDefs := e.toolDefsForModelRegistry(runToolRegistry, agentTools)
	return runToolRegistry, toolDefs
}

// scanInputForPII checks the latest user message for PII and applies the
// configured action (block, redact, or warn). Returns true if the run was
// blocked and should not continue.
func (e *Executor) scanInputForPII(run *types.Run, job *runJob, messages []modelpolicy.ChatMessage) bool {
	if e.piiDetector == nil || len(messages) == 0 {
		return false
	}
	lastMsg := messages[len(messages)-1]
	lastText := modelpolicy.MessageText(lastMsg)
	if lastMsg.Role != "user" || lastText == "" {
		return false
	}
	detections := e.piiDetector.Scan(lastText)
	if len(detections) == 0 {
		return false
	}
	switch e.piiMode {
	case "block":
		e.failRun(run, job, "pii_blocked", "PII detected in input")
		return true
	case "redact":
		messages[len(messages)-1].Content = pii.Redact(lastText, detections)
	default: // "warn"
		slog.Warn("PII detected in input", "run_id", run.RunID, "tenant_id", run.TenantID, "detections", len(detections))
	}
	return false
}

// invokeModel calls the model provider (streaming or buffered) and records
// metrics. Returns the model response or an error.
func (e *Executor) invokeModel(
	job *runJob,
	run *types.Run,
	modelID string,
	messages []modelpolicy.ChatMessage,
	toolDefs []modelpolicy.ToolDef,
	step int,
) (*modelpolicy.ChatResponse, error) {
	job.events.Emit("step.model_invoke", map[string]any{"step": step, "model": modelID})

	useStream := run.RunOptions.StreamEvents
	streamProvider, canStream := e.provider.(ModelStreamProvider)

	var resp *modelpolicy.ChatResponse
	var err error
	modelStart := time.Now()
	if useStream && canStream {
		resp, err = e.streamModelCall(job, streamProvider, modelID, messages, toolDefs, step)
	} else {
		resp, err = e.provider.ChatComplete(job.ctx, modelpolicy.ChatRequest{
			Model:    modelID,
			Messages: messages,
			Tools:    toolDefs,
		})
	}
	modelDur := time.Since(modelStart).Seconds()
	if err != nil {
		metrics.ObserveModelCall(modelID, "", "error", modelDur)
		return nil, err
	}
	metrics.ObserveModelCall(modelID, "", "ok", modelDur)
	return resp, nil
}

// streamModelCall invokes the model via streaming and emits step.token events
// for each text delta. It accumulates the full response and returns a synthetic
// ChatResponse matching the buffered format.
func (e *Executor) streamModelCall(
	job *runJob,
	sp ModelStreamProvider,
	modelID string,
	messages []modelpolicy.ChatMessage,
	toolDefs []modelpolicy.ToolDef,
	step int,
) (*modelpolicy.ChatResponse, error) {
	ch, err := sp.ChatCompleteStream(job.ctx, modelpolicy.ChatRequest{
		Model:    modelID,
		Messages: messages,
		Tools:    toolDefs,
	})
	if err != nil {
		return nil, err
	}

	var content strings.Builder
	var allToolCalls []modelpolicy.ToolCall
	var finishReason string
	var usage modelpolicy.ChatUsage

	for chunk := range ch {
		if chunk.Err != nil {
			return nil, chunk.Err
		}

		// Emit token event for text deltas.
		if chunk.Delta != "" {
			content.WriteString(chunk.Delta)
			job.events.Emit("step.token", map[string]any{
				"delta": chunk.Delta,
				"step":  step,
			})
		}

		// Accumulate tool calls.
		if len(chunk.ToolCalls) > 0 {
			allToolCalls = append(allToolCalls, chunk.ToolCalls...)
		}

		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
	}

	if finishReason == "" {
		finishReason = "stop"
	}

	// Emit tool_call events for accumulated tool calls.
	for _, tc := range allToolCalls {
		job.events.Emit("step.tool_call", map[string]any{
			"step":      step,
			"tool_name": tc.Function.Name,
			"tool_id":   tc.ID,
		})
	}

	return &modelpolicy.ChatResponse{
		Model: modelID,
		Choices: []modelpolicy.ChatChoice{{
			Message: modelpolicy.ChatMessage{
				Role:      "assistant",
				Content:   content.String(),
				ToolCalls: allToolCalls,
			},
			FinishReason: finishReason,
		}},
		Usage: usage,
	}, nil
}

// failRun marks a run as failed, persists it, and emits a failure event.
// If the run has already been moved to a terminal state (e.g. by a cancel
// handler), the save is skipped to avoid overwriting.
func (e *Executor) failRun(run *types.Run, job *runJob, code, message string) {
	// Use a background context so that cancellation does not prevent
	// us from reading/persisting the terminal state.
	saveCtx := context.Background()

	// Re-read the run to check if it was already moved to a terminal state
	// (e.g. canceled via the HTTP handler while we were executing).
	if current, ok, err := e.runs.Get(saveCtx, run.TenantID, run.RunID); err == nil && ok {
		switch current.Status {
		case "canceled", "completed", "failed":
			// Already terminal; don't overwrite.
			job.events.Emit("run.failed", map[string]any{
				"run_id":  run.RunID,
				"code":    code,
				"message": message,
				"note":    "run already in terminal state: " + current.Status,
			})
			return
		}
	}

	run.Status = "failed"
	run.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	run.Error = &types.RunError{Code: code, Message: message}
	metrics.IncRunLifecycle(run.TenantID, "failed")

	if err := e.runs.Save(saveCtx, *run); err != nil {
		slog.Error("failed to save failed state", "run_id", run.RunID, "tenant_id", run.TenantID, "error", err)
	}

	job.events.Emit("run.failed", map[string]any{
		"run_id":  run.RunID,
		"code":    code,
		"message": message,
	})
	e.webhook.Send(webhook.Event{
		Event:     webhook.EventRunFailed,
		EventID:   id.New("evt"),
		TenantID:  run.TenantID,
		RunID:     run.RunID,
		AgentID:   run.AgentID,
		Timestamp: run.CompletedAt,
		Data:      map[string]any{"code": code, "message": message},
	})

	// Auto-retry: if enabled and this is a provider error on a non-retry run,
	// create a single automatic retry after a short delay.
	if code == "provider_error" && run.RetryOf == "" && os.Getenv("AGENTOS_AUTO_RETRY_ON_PROVIDER_ERROR") == "true" {
		e.scheduleAutoRetry(run, job)
	}
}

// scheduleAutoRetry creates a new retry run after a 5-second delay.
// It runs in a separate goroutine tracked by the executor's WaitGroup and
// derived from the executor's shutdown context so it is cancelled on
// shutdown (v9.0 #13 / M-6).
func (e *Executor) scheduleAutoRetry(run *types.Run, job *runJob) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()

		// Wait 5 seconds or until the executor shuts down.
		select {
		case <-time.After(5 * time.Second):
		case <-e.shutdownCtx.Done():
			slog.Info("auto-retry: cancelled by shutdown",
				"original_run_id", run.RunID,
				"tenant_id", run.TenantID,
			)
			return
		}

		now := time.Now().UTC().Format(time.RFC3339)
		newRunID := id.New("run")

		retryRun := types.Run{
			TenantID:   run.TenantID,
			AgentID:    run.AgentID,
			RunID:      newRunID,
			Status:     "queued",
			CreatedAt:  now,
			EventsURL:  "/v1/runs/" + newRunID + "/events",
			Input:      run.Input,
			RunOptions: run.RunOptions,
			RetryOf:    run.RunID,
		}

		saveCtx, saveCancel := context.WithTimeout(e.shutdownCtx, 10*time.Second)
		defer saveCancel()
		if err := e.runs.Create(saveCtx, retryRun); err != nil {
			slog.Error("auto-retry: failed to persist retry run",
				"run_id", newRunID,
				"original_run_id", run.RunID,
				"tenant_id", run.TenantID,
				"error", err,
			)
			return
		}

		e.audit.Log(audit.Entry{
			TenantID:  run.TenantID,
			Action:    "run.auto_retry",
			Resource:  "run/" + newRunID,
			Outcome:   "allowed",
			Meta:      map[string]any{"original_run_id": run.RunID, "agent_id": run.AgentID},
		})

		slog.Info("auto-retry: created retry run",
			"run_id", newRunID,
			"original_run_id", run.RunID,
			"tenant_id", run.TenantID,
		)

		// Resolve agent for config.
		agent := job.agent

		timeout := 300 * time.Second
		if retryRun.RunOptions.TimeoutMs > 0 {
			timeout = time.Duration(retryRun.RunOptions.TimeoutMs) * time.Millisecond
		} else if agent.Config != nil && agent.Config.TimeoutMs > 0 {
			timeout = time.Duration(agent.Config.TimeoutMs) * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(e.shutdownCtx, timeout)

		var events *EventSink
		if e.eventLog != nil {
			events = NewEventSinkWithLog(run.TenantID, run.AgentID, newRunID, e.eventLog)
		} else {
			events = NewEventSink(run.TenantID, run.AgentID, newRunID)
		}

		retryJob := &runJob{
			run:    retryRun,
			agent:  agent,
			ctx:    ctx,
			cancel: cancel,
			events: events,
		}
		e.Submit(retryJob)
	}()
}

// saveConversation persists the messages from a completed run to memory.
func (e *Executor) saveConversation(ctx context.Context, tenantID, agentID string, messages []modelpolicy.ChatMessage) {
	storageMsgs := modelMsgsToStorage(messages)
	if err := e.memory.Append(ctx, tenantID, agentID, storageMsgs); err != nil {
		slog.Error("failed to save conversation", "tenant_id", tenantID, "agent_id", agentID, "error", err)
	}
}

// toolDefsForModel converts tool registry definitions to modelpolicy format.
func (e *Executor) toolDefsForModel(agentTools []string) []modelpolicy.ToolDef {
	return e.toolDefsForModelRegistry(e.tools, agentTools)
}

// toolDefsForModelRegistry converts tool definitions from a specific registry.
func (e *Executor) toolDefsForModelRegistry(reg *tools.Registry, agentTools []string) []modelpolicy.ToolDef {
	if reg == nil {
		return nil
	}
	defs := reg.GetToolDefs(agentTools)
	out := make([]modelpolicy.ToolDef, len(defs))
	for i, d := range defs {
		out[i] = modelpolicy.ToolDef{
			Type: d.Type,
			Function: modelpolicy.ToolDefFunction{
				Name:        d.Function.Name,
				Description: d.Function.Description,
				Parameters:  d.Function.Parameters,
			},
		}
	}
	return out
}

// truncate limits a string to maxLen characters for event payloads.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
