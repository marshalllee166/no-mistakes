package steps

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

const inconclusiveNotDrivenLiveJSON = `{"findings":[],"summary":"","tested":["go test ./internal/checkout/..."],"testing_summary":"targeted tests passed; nothing driven live","artifacts":[],
	"scenarios":[{"name":"user reaches the success screen","result":"untested","live":false,"evidence":"","reason":"end-to-end validation runs in CI under operator policy"}],
	"verdict":"inconclusive"}`

const noSurfaceJSON = `{"findings":[],"summary":"","tested":["inspected .github/workflows/ci.yml"],"testing_summary":"CI workflow has no running product","artifacts":[],
	"scenarios":[{"name":"CI shard runs","result":"untested","live":false,"evidence":"","reason":"CI workflow YAML has no running product"}],
	"verdict":"no-surface"}`

const ciOnlyE2EPolicyMarker = "Operator policy (this machine's global config, test.e2e: ci)"

func runTestStepWithOutput(t *testing.T, output string, e2eInCI, fixing bool) (*mockAgent, *types.Findings, bool) {
	t.Helper()
	dir, baseSHA, headSHA := setupGitRepo(t)
	gitCmd(t, dir, "checkout", "--detach", headSHA)
	ag := &mockAgent{name: "test", runFn: func(context.Context, agent.RunOpts) (*agent.Result, error) {
		return &agent.Result{Output: json.RawMessage(output)}, nil
	}}
	sctx := newTestContextWithDBRecords(t, ag, dir, baseSHA, headSHA, config.Commands{})
	sctx.UserIntent = "Show users a success screen after checkout"
	sctx.Config.Test.E2EInCI = e2eInCI
	sctx.Fixing = fixing
	outcome, err := (&TestStep{}).Execute(sctx)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := types.ParseFindingsJSON(outcome.Findings)
	if err != nil {
		t.Fatal(err)
	}
	return ag, &findings, outcome.NeedsApproval
}

func TestTestStep_CIOnlyE2E_InconclusiveDoesNotPark(t *testing.T) {
	t.Parallel()
	_, findings, parked := runTestStepWithOutput(t, inconclusiveNotDrivenLiveJSON, true, false)
	if parked {
		t.Fatalf("inconclusive parked under test.e2e: ci: %+v", findings.Items)
	}
	if findings.Verdict != types.TestVerdictInconclusive {
		t.Fatalf("verdict = %q, want it recorded as inconclusive", findings.Verdict)
	}
	var recorded bool
	for _, item := range findings.Items {
		if strings.Contains(item.Description, "live validation verdict: inconclusive") {
			recorded = true
			if item.Severity != types.FindingSeverityInfo || item.Action != types.ActionNoOp || !strings.Contains(item.Description, "deferred to CI") {
				t.Fatalf("policy verdict finding = %+v, want informational no-op naming CI", item)
			}
		}
	}
	if !recorded {
		t.Fatalf("inconclusive verdict not recorded on the step: %+v", findings.Items)
	}
	// Completed, the step still reports a Test exception, never a clean pass.
	raw, _ := json.Marshal(findings)
	rawStr := string(raw)
	step := &db.StepResult{StepName: types.StepTest, Status: types.StepStatusCompleted, FindingsJSON: &rawStr}
	if got := step.TestOverrideReason(); !strings.Contains(got, "inconclusive") {
		t.Fatalf("TestOverrideReason = %q, want the inconclusive verdict surfaced", got)
	}
}

func TestTestStep_CIOnlyE2E_OffStillParksInconclusive(t *testing.T) {
	t.Parallel()
	_, findings, parked := runTestStepWithOutput(t, inconclusiveNotDrivenLiveJSON, false, false)
	if !parked {
		t.Fatalf("inconclusive no longer parks with the policy off: %+v", findings.Items)
	}
}

func TestTestStep_CIOnlyE2E_NoSurfaceStillParks(t *testing.T) {
	t.Parallel()
	_, findings, parked := runTestStepWithOutput(t, noSurfaceJSON, true, false)
	if !parked {
		t.Fatalf("no-surface stopped parking under test.e2e: ci: %+v", findings.Items)
	}
}

func TestTestStep_CIOnlyE2E_PolicyReachesEvidenceAndRepairPrompts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		e2eInCI bool
		fixing  bool
	}{
		{"evidence on", true, false},
		{"evidence off", false, false},
		{"repair on", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ag, _, _ := runTestStepWithOutput(t, passingScenarioFindingsJSON, tc.e2eInCI, tc.fixing)
			if len(ag.calls) == 0 {
				t.Fatal("no agent call")
			}
			for i, call := range ag.calls {
				has := strings.Contains(call.Prompt, ciOnlyE2EPolicyMarker)
				if has != tc.e2eInCI {
					t.Fatalf("call %d policy present = %v, want %v", i, has, tc.e2eInCI)
				}
				if has {
					for _, want := range []string{"Do NOT run Playwright, hermetic, or any other end-to-end suite locally", "Do NOT start dev servers, docker or compose stacks", "targeted unit, integration, or story tests"} {
						if !strings.Contains(call.Prompt, want) {
							t.Fatalf("call %d missing %q", i, want)
						}
					}
				}
			}
			if tc.fixing && len(ag.calls) < 2 {
				t.Fatalf("repair run made %d calls, want repair plus evidence", len(ag.calls))
			}
		})
	}
}
