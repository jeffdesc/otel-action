package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/go-github/v47/github"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
)

func createTraces(ctx context.Context, conf configType) error {
	var token *http.Client
	if len(conf.githubToken) != 0 {
		token = oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: conf.githubToken},
		))
	}

	client := github.NewClient(token)

	runID, err := strconv.ParseInt(conf.runID, 10, 64)
	if err != nil {
		return err
	}

	workflowData, _, err := client.Actions.GetWorkflowRunByID(ctx, conf.owner, conf.repo, runID)
	if err != nil {
		return err
	}

	jobs, _, err := client.Actions.ListWorkflowJobs(ctx, conf.owner, conf.repo, runID, &github.ListWorkflowJobsOptions{})
	if err != nil {
		return err
	}

	// Set a specific Trace ID
	if conf.traceID != "" {
		var traceID, _ = trace.TraceIDFromHex(conf.traceID)
		spanContext := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID,
		})
		ctx = trace.ContextWithSpanContext(ctx, spanContext)
	}

	var lastJobFinishesAt time.Time
	ctx, workflowSpan := tracer.Start(ctx, *workflowData.Name, trace.WithTimestamp(workflowData.GetCreatedAt().Time))
	for _, job := range jobs.Jobs {
		// job_data, _ := json.Marshal(job)
		// fmt.Println(string(job_data))
		ctx, jobSpan := tracer.Start(ctx, *job.Name, trace.WithTimestamp(job.GetStartedAt().Time))
		jobSpan.SetAttributes(
			attribute.Int64("github.run_id", *job.RunID),

			attribute.String("github.status", *job.Status),
			attribute.String("github.name", *job.Name),
			attribute.String("github.runner.name", *job.RunnerName),
			attribute.String("github.run.url", *job.HTMLURL),
		)

		for _, step := range job.Steps {
			_, stepSpan := tracer.Start(ctx, *step.Name, trace.WithTimestamp(step.GetStartedAt().Time))
			stepSpan.SetAttributes(
				attribute.String("step.name", *step.Name),
				attribute.String("step.status", *step.Status),
				attribute.String("step.conclusion", *step.Conclusion),
			)

			if *step.Conclusion == "failure" {
				stepSpan.SetStatus(codes.Error, fmt.Sprintf("Job step '%s' failed", *step.Name))
			}

			if step.CompletedAt != nil {
				stepSpan.End(trace.WithTimestamp(step.GetCompletedAt().Time))
				if step.GetCompletedAt().Time.After(lastJobFinishesAt) {
					lastJobFinishesAt = step.GetCompletedAt().Time
				}
			} else {
				stepSpan.End()
			}
		}

		if job.CompletedAt != nil {
			jobSpan.End(trace.WithTimestamp(job.GetCompletedAt().Time))
		} else {
			jobSpan.End()
		}
	}
	workflowSpan.End(trace.WithTimestamp(lastJobFinishesAt))

	return nil
}
