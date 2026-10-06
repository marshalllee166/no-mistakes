package steps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const preserveDraftNarrative = "## What Changed\n\n- add a Bar helper"

// preserveBodyTestContext is an ordinary (no pr.template) PR context with the
// operator's pr.preserve_body and pr.appendix: none set, as global config
// resolves them.
func preserveBodyTestContext(t *testing.T) (*pipeline.StepContext, *mockAgent) {
	t.Helper()
	dir, base, head := setupGitRepo(t)
	ag := &mockAgent{name: "test", runFn: func(context.Context, agent.RunOpts) (*agent.Result, error) {
		data, _ := json.Marshal(prContent{Title: "feat: add bar helper", Body: preserveDraftNarrative})
		return &agent.Result{Output: data}, nil
	}}
	sctx := newTestContextWithDBRecords(t, ag, dir, base, head, config.Commands{})
	sctx.Config.PR.PreserveBody = true
	sctx.Config.PR.Appendix = config.PRAppendixNone
	no := false
	sctx.Config.PR.PublishIntent = &no
	sr, err := sctx.DB.InsertStepResult(sctx.Run.ID, types.StepReview)
	if err != nil {
		t.Fatal(err)
	}
	if err := sctx.DB.UpdateStepStatus(sr.ID, types.StepStatusCompleted); err != nil {
		t.Fatal(err)
	}
	return sctx, ag
}

func assertNoVisibleAppendix(t *testing.T, body string) {
	t.Helper()
	for _, leak := range []string{"## Pipeline", "## Risk Assessment", "## Testing", "## Intent", noMistakesPRSignature} {
		if strings.Contains(body, leak) {
			t.Fatalf("body published %q:\n%s", leak, body)
		}
	}
}

func TestPRPreserveBody_CreateIsOwnedFromTheStart(t *testing.T) {
	t.Parallel()
	sctx, ag := preserveBodyTestContext(t)
	env, _ := fakeGH(t, "")
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	sctx.Env = append(env, "FAKE_CLI_PR_BODY_FILE="+bodyFile)

	out, err := (&PRStep{}).Execute(sctx)
	if err != nil || out == nil || out.PRURL == "" {
		t.Fatalf("create: %+v, %v", out, err)
	}
	body, err := os.ReadFile(bodyFile)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := parsePROwnedBody(string(body))
	if err != nil || !parts.managed {
		t.Fatalf("created body is not owned: %+v, %v\n%s", parts, err, body)
	}
	if !strings.HasPrefix(string(body), preserveDraftNarrative) {
		t.Fatalf("narrative missing from created body:\n%s", body)
	}
	assertNoVisibleAppendix(t, string(body))
	if got := parsePipelineAttestationForTest(t, string(body)).HeadSHA; got != sctx.Run.HeadSHA {
		t.Fatalf("attestation head = %q, want %s", got, sctx.Run.HeadSHA)
	}
	if len(ag.calls) != 1 {
		t.Fatalf("agent calls = %d, want one narrative draft", len(ag.calls))
	}
}

func TestPRPreserveBody_RerunKeepsTheCuratedDescription(t *testing.T) {
	t.Parallel()
	sctx, ag := preserveBodyTestContext(t)
	// Operator-curated body re-applied with gh pr edit: no ownership markers,
	// no attestation.
	curated := "## Problem\n\nRe-runs overwrite curated PR text.\n\n## Proposal\n\nKeep it.\n\n## What changed\n\n- preserve_body\n"
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte(curated), 0o644); err != nil {
		t.Fatal(err)
	}
	env, logFile := fakeGH(t, "https://github.com/test/repo/pull/42")
	sctx.Env = append(env, "FAKE_CLI_PR_BODY_FILE="+bodyFile)
	step := &PRStep{}
	if _, err := step.Execute(sctx); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(bodyFile)
	if !strings.HasPrefix(string(first), curated) || len(ag.calls) != 0 {
		t.Fatalf("curated description re-drafted: calls=%d, body=%s", len(ag.calls), first)
	}
	assertNoVisibleAppendix(t, string(first))
	if got := parsePipelineAttestationForTest(t, string(first)).HeadSHA; got != sctx.Run.HeadSHA {
		t.Fatalf("attestation head = %q, want %s", got, sctx.Run.HeadSHA)
	}

	// A later run refreshes only the owned appendix around an edited body.
	later := strings.Replace(string(first), "Keep it.", "Keep it verbatim.", 1)
	if err := os.WriteFile(bodyFile, []byte(later), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := step.Execute(sctx); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(bodyFile)
	if string(got) != later || len(ag.calls) != 0 {
		t.Fatalf("rerun changed the curated description: %s", got)
	}
	logs, _ := os.ReadFile(logFile)
	for _, line := range strings.Split(string(logs), "\n") {
		if strings.Contains(line, "pr edit") && strings.Contains(line, "--title") {
			t.Fatal("preserve_body must not replace the author's title")
		}
	}
}

func TestPRPreserveBody_OffStillRedraftsAnExistingBody(t *testing.T) {
	t.Parallel()
	sctx, ag := preserveBodyTestContext(t)
	sctx.Config.PR.PreserveBody = false
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte("## Problem\n\nCurated.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _ := fakeGH(t, "https://github.com/test/repo/pull/42")
	sctx.Env = append(env, "FAKE_CLI_PR_BODY_FILE="+bodyFile)
	if _, err := (&PRStep{}).Execute(sctx); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(bodyFile)
	if len(ag.calls) != 1 || strings.Contains(string(got), "Curated.") || hasPRAppendixMarkers(string(got)) {
		t.Fatalf("default publication changed: calls=%d body=%s", len(ag.calls), got)
	}
}

func TestPRPreserveBody_EmptyExistingBodyIsDrafted(t *testing.T) {
	t.Parallel()
	sctx, ag := preserveBodyTestContext(t)
	env, _ := fakeGH(t, "https://github.com/test/repo/pull/42")
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sctx.Env = append(env, "FAKE_CLI_PR_BODY_FILE="+bodyFile)
	if _, err := (&PRStep{}).Execute(sctx); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(bodyFile)
	if len(ag.calls) != 1 || !strings.HasPrefix(string(got), preserveDraftNarrative) {
		t.Fatalf("empty body not drafted: calls=%d body=%s", len(ag.calls), got)
	}
	if parts, err := parsePROwnedBody(string(got)); err != nil || !parts.managed {
		t.Fatalf("drafted body is not owned: %v\n%s", err, got)
	}
}

func TestPRAppendixNone_RendersOnlyTheAttestation(t *testing.T) {
	t.Parallel()
	const narrative = "## What Changed\n\n- hide the generated appendix"
	steps := []*db.StepResult{
		{StepName: types.StepReview, Status: types.StepStatusCompleted},
		{StepName: types.StepTest, Status: types.StepStatusCompleted},
	}
	attestation := buildPipelineAttestation(steps, nil, testPipelineHeadSHA)
	pipelineMD := strings.Replace(pipelineMarkdownForTest("review round kept"), noMistakesPRSignature+"\n\n", noMistakesPRSignature+"\n\n"+attestation+"\n\n", 1)
	testingMD := "## Testing\n\nran /Users/someone/worktrees/x/e2e"
	sctx := &pipeline.StepContext{Config: &config.Config{PR: config.PR{Appendix: config.PRAppendixNone}}}

	for name, body := range map[string]string{
		"assemble": assemblePRBody(sctx, narrative, "⚠️ Medium: risky", testingMD, pipelineMD, 0, scm.ProviderGitHub),
		"build":    buildPRBody(narrative, "⚠️ Medium: risky", testingMD, pipelineMD, sctx, scm.ProviderGitHub),
	} {
		if body != narrative+"\n\n"+attestation {
			t.Fatalf("%s: none body = %q, want narrative plus attestation only", name, body)
		}
	}
	if got := appendixEvidence(config.PRAppendixNone, prBodyHTML, "risk", testingMD, pipelineMD); got != attestation {
		t.Fatalf("owned none appendix = %q, want the attestation only", got)
	}
}
