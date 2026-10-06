package config

import (
	"strings"
	"testing"
)

const operatorPublicationGlobal = `pr:
  appendix: none
  preserve_body: true
test:
  e2e: ci
repository_overrides:
  https://github.com/acme/attested.git:
    pr:
      appendix: full
      preserve_body: false
`

func TestLoadGlobal_OperatorPublicationAndE2EPolicy(t *testing.T) {
	t.Parallel()
	global, err := LoadGlobalFromBytes([]byte(operatorPublicationGlobal))
	if err != nil {
		t.Fatal(err)
	}
	repo := &RepoConfig{}

	got := MergeForRemote(global, repo, "git@github.com:acme/widget.git")
	if got.PR.AppendixMode() != PRAppendixNone || !got.PR.PreserveBody || !got.Test.E2EInCI {
		t.Fatalf("global defaults not applied: pr=%+v e2eInCI=%v", got.PR, got.Test.E2EInCI)
	}

	override := MergeForRemote(global, repo, "https://github.com/ACME/attested")
	if override.PR.AppendixMode() != PRAppendixFull || override.PR.PreserveBody {
		t.Fatalf("repository_overrides did not beat global: %+v", override.PR)
	}

	trusted := &RepoConfig{PR: PRRaw{Appendix: PRAppendixCollapsed}}
	if mode := MergeForRemote(global, EffectiveRepoConfig(&RepoConfig{}, trusted, false), "git@github.com:acme/attested.git").PR.AppendixMode(); mode != PRAppendixCollapsed {
		t.Fatalf("trusted repo pr.appendix = %q, want it to win over operator layers", mode)
	}
}

func TestMerge_OperatorPublicationDefaultsUnchanged(t *testing.T) {
	t.Parallel()
	got := Merge(DefaultGlobalConfig(), &RepoConfig{})
	if got.PR.AppendixMode() != PRAppendixFull || got.PR.PreserveBody || got.Test.E2EInCI {
		t.Fatalf("unset operator settings changed defaults: pr=%+v e2eInCI=%v", got.PR, got.Test.E2EInCI)
	}
	explicitLocal, err := LoadGlobalFromBytes([]byte("test:\n  e2e: local\n"))
	if err != nil {
		t.Fatal(err)
	}
	if Merge(explicitLocal, &RepoConfig{}).Test.E2EInCI {
		t.Fatal("test.e2e: local enabled the CI-only policy")
	}
}

func TestRepoConfig_CannotSetTheE2EPolicy(t *testing.T) {
	t.Parallel()
	repo, err := LoadRepoFromBytes([]byte("test:\n  e2e: ci\n"))
	if err != nil {
		t.Fatal(err)
	}
	if Merge(DefaultGlobalConfig(), EffectiveRepoConfig(repo, repo, true)).Test.E2EInCI {
		t.Fatal("a repository config relaxed the inconclusive gate")
	}
}

func TestLoadGlobal_OperatorPublicationValidation(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ yaml, want string }{
		"unknown appendix":          {"pr:\n  appendix: hidden\n", "pr.appendix"},
		"empty appendix":            {"pr:\n  appendix: \"\"\n", "pr.appendix"},
		"unknown e2e":               {"test:\n  e2e: remote\n", "test.e2e"},
		"unknown override appendix": {"repository_overrides:\n  https://github.com/acme/w.git:\n    pr:\n      appendix: hidden\n", "pr.appendix"},
		"unknown pr key":            {"pr:\n  template: x.md\n", "template"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadGlobalFromBytes([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
