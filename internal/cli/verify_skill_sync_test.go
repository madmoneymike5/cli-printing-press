// Copyright 2026 trevin-chow. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestVerifySkillScriptInSync ensures the script embedded into the binary
// (internal/cli/verify_skill_bundled.py) matches the canonical script that
// the library repo's CI runs (scripts/verify-skill/verify_skill.py).
//
// The two used to drift: a bundled vendor copy, hand-maintained, eventually
// missed checks the canonical added (most recently the unknown-command
// check, ported in U2). The fix is mechanical: a lefthook pre-commit hook
// copies canonical → bundled on every commit that touches the canonical,
// and this test catches any commit that bypasses the hook (--no-verify).
//
// To regenerate the bundled copy after editing the canonical:
//
//	cp scripts/verify-skill/verify_skill.py internal/cli/verify_skill_bundled.py
//
// Or just run lefthook:
//
//	lefthook run pre-commit
func TestVerifySkillScriptInSync(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	canonical := filepath.Join(repoRoot, "scripts", "verify-skill", "verify_skill.py")
	bundled := filepath.Join(repoRoot, "internal", "cli", "verify_skill_bundled.py")

	canonicalBytes, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("read canonical script %s: %v", canonical, err)
	}
	bundledBytes, err := os.ReadFile(bundled)
	if err != nil {
		t.Fatalf("read bundled script %s: %v", bundled, err)
	}

	canonicalHash := sha256.Sum256(canonicalBytes)
	bundledHash := sha256.Sum256(bundledBytes)

	if canonicalHash != bundledHash {
		t.Fatalf(
			"verify-skill scripts have diverged. "+
				"\n  scripts/verify-skill/verify_skill.py    sha256=%s (%d bytes)"+
				"\n  internal/cli/verify_skill_bundled.py    sha256=%s (%d bytes)"+
				"\n\n"+
				"The canonical script (scripts/verify-skill/verify_skill.py) is the source of truth. "+
				"To resync, run:\n\n"+
				"  cp scripts/verify-skill/verify_skill.py internal/cli/verify_skill_bundled.py\n\n"+
				"Or run lefthook:\n\n"+
				"  lefthook run pre-commit",
			hex.EncodeToString(canonicalHash[:]), len(canonicalBytes),
			hex.EncodeToString(bundledHash[:]), len(bundledBytes),
		)
	}
}

func TestVerifySkillDriftWorkflowGuardsLibraryCopy(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "verify-skill-drift-check.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read verify-skill drift workflow %s: %v", workflowPath, err)
	}

	var workflow map[string]any
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse verify-skill drift workflow YAML: %v", err)
	}

	content := string(data)
	required := []string{
		"name: Verify Skill Drift",
		"schedule:",
		"cron:",
		"push:",
		"branches: [main]",
		"scripts/verify-skill/verify_skill.py",
		".github/workflows/verify-skill-drift-check.yml",
		"issues: write",
		"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
		"persist-credentials: false",
		"https://raw.githubusercontent.com/mvanhorn/printing-press-library/main/.github/scripts/verify-skill/verify_skill.py",
		"GH_TOKEN: ${{ github.token }}",
		"sha256sum",
		"cmp -s",
		"gh api \"repos/$GITHUB_REPOSITORY\" --jq '.has_issues'",
		"gh issue list",
		"gh issue create",
		"Issues disabled; tracking issue not created.",
		"::error::verify-skill drift detected between cli-printing-press and printing-press-library",
		"exit 1",
		"Resolve the synchronization in a separate change after reviewing which copy contains intentional fixes.",
	}
	for _, want := range required {
		if !strings.Contains(content, want) {
			t.Fatalf("verify-skill drift workflow should contain %q", want)
		}
	}
}

func TestVerifySkillDriftWorkflowFailsClearlyWhenIssuesDisabled(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("workflow shell runs on ubuntu-latest")
	}

	repoRoot := findRepoRoot(t)
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "verify-skill-drift-check.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read verify-skill drift workflow %s: %v", workflowPath, err)
	}

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse verify-skill drift workflow YAML: %v", err)
	}

	var runScript string
	for _, step := range workflow.Jobs["compare-library-copy"].Steps {
		if step.Name == "Compare verify-skill scripts" {
			runScript = step.Run
			break
		}
	}
	if runScript == "" {
		t.Fatal("verify-skill drift workflow has no comparison script")
	}

	tempDir := t.TempDir()
	fakeBin := filepath.Join(tempDir, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}
	fakeGH := `#!/bin/sh
if [ "$1" = "api" ]; then
  printf 'false\n'
  exit 0
fi
printf 'unexpected gh command: %s\n' "$*" >&2
exit 97
`
	if err := os.WriteFile(filepath.Join(fakeBin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	libraryScript := filepath.Join(tempDir, "library-verify_skill.py")
	if err := os.WriteFile(libraryScript, []byte("# intentionally different\n"), 0o600); err != nil {
		t.Fatalf("write library script: %v", err)
	}

	scriptPath := filepath.Join(tempDir, "verify-skill-drift.sh")
	if err := os.WriteFile(scriptPath, []byte(runScript), 0o600); err != nil {
		t.Fatalf("write drift check script: %v", err)
	}
	summaryPath := filepath.Join(tempDir, "summary.md")
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RUNNER_TEMP="+tempDir,
		"GITHUB_STEP_SUMMARY="+summaryPath,
		"GITHUB_REPOSITORY=madmoneymike5/cli-printing-press",
		"LIBRARY_VERIFY_SKILL_URL=file://"+libraryScript,
	)
	output, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("drift check exit = %v, want 1; output:\n%s", err, output)
	}
	if !strings.Contains(string(output), "::error::verify-skill drift detected between cli-printing-press and printing-press-library") {
		t.Fatalf("drift check did not emit explicit error; output:\n%s", output)
	}
	if strings.Contains(string(output), "unexpected gh command") {
		t.Fatalf("Issues-disabled drift check called an issue command:\n%s", output)
	}

	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read workflow summary: %v", err)
	}
	if !strings.Contains(string(summary), "Issues disabled; tracking issue not created.") {
		t.Fatalf("workflow summary did not explain skipped issue creation:\n%s", summary)
	}
}

// findRepoRoot walks up from the test file's location until it finds go.mod.
// This is more robust than relying on PWD or runtime.Caller alone, because
// `go test ./...` runs each package from its own directory.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file location")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root (no go.mod) starting from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}
