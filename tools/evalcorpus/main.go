package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	evalpkg "github.com/Molly166/AegisCodeAgent/internal/eval"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const catalogSchemaVersion = "v1"

type catalog struct {
	SchemaVersion string     `json:"schema_version"`
	Cases         []scenario `json:"cases"`
}

type scenario struct {
	ID           string                  `json:"id"`
	Directory    string                  `json:"directory"`
	Title        string                  `json:"title"`
	Description  string                  `json:"description"`
	Kind         evalpkg.CaseKind        `json:"kind"`
	Layer        evalpkg.EvaluationLayer `json:"layer"`
	Provenance   evalpkg.CaseProvenance  `json:"provenance"`
	Tags         []string                `json:"tags"`
	ExpectedGate evalpkg.ExpectedGate    `json:"expected_gate"`
	Path         string                  `json:"path"`
	Finding      *finding                `json:"finding,omitempty"`
	Hypothesis   *hypothesis             `json:"hypothesis,omitempty"`
	Stages       *stageOverrides         `json:"stages,omitempty"`
}

type finding struct {
	Severity    review.Severity `json:"severity"`
	Category    review.Category `json:"category"`
	Line        int             `json:"line"`
	RuleID      string          `json:"rule_id"`
	Title       string          `json:"title"`
	Source      string          `json:"source"`
	Description string          `json:"description"`
	Suggestion  string          `json:"suggestion"`
}

type hypothesis struct {
	Severity review.Severity `json:"severity"`
	Category review.Category `json:"category"`
	Line     int             `json:"line"`
	Title    string          `json:"title"`
	Reason   string          `json:"reason"`
}

type stageOverrides struct {
	Analysis     review.AnalysisStatus     `json:"analysis,omitempty"`
	Context      review.ContextStatus      `json:"context,omitempty"`
	Agent        review.AgentStatus        `json:"agent,omitempty"`
	Verification review.VerificationStatus `json:"verification,omitempty"`
	Warnings     []string                  `json:"verification_warnings,omitempty"`
}

func main() {
	catalogPath := flag.String("catalog", "eval/catalog.json", "path to the corpus catalog")
	outputPath := flag.String("output", "eval/cases", "directory for generated cases")
	flag.Parse()
	if flag.NArg() != 0 {
		fatalf("unexpected positional arguments: %v", flag.Args())
	}

	definition, err := loadCatalog(*catalogPath)
	if err != nil {
		fatalf("load catalog: %v", err)
	}
	if err := generate(definition, *outputPath); err != nil {
		fatalf("generate corpus: %v", err)
	}
	fmt.Printf("generated %d eval cases in %s\n", len(definition.Cases), filepath.Clean(*outputPath))
}

func loadCatalog(path string) (catalog, error) {
	file, err := os.Open(filepath.Clean(path)) // #nosec G304 -- explicit local generator input
	if err != nil {
		return catalog{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var result catalog
	if err := decoder.Decode(&result); err != nil {
		return catalog{}, err
	}
	if result.SchemaVersion != catalogSchemaVersion {
		return catalog{}, fmt.Errorf("unsupported catalog schema %q", result.SchemaVersion)
	}
	if len(result.Cases) != 50 {
		return catalog{}, fmt.Errorf("catalog must contain exactly 50 cases, got %d", len(result.Cases))
	}
	return result, nil
}

func generate(definition catalog, output string) error {
	root, err := filepath.Abs(strings.TrimSpace(output))
	if err != nil || strings.TrimSpace(output) == "" {
		return errors.New("output directory is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	seenIDs := make(map[string]struct{}, len(definition.Cases))
	seenDirectories := make(map[string]struct{}, len(definition.Cases))
	for _, item := range definition.Cases {
		if err := validateScenario(item); err != nil {
			return fmt.Errorf("%s: %w", item.ID, err)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return fmt.Errorf("duplicate ID %q", item.ID)
		}
		if _, exists := seenDirectories[item.Directory]; exists {
			return fmt.Errorf("duplicate directory %q", item.Directory)
		}
		seenIDs[item.ID] = struct{}{}
		seenDirectories[item.Directory] = struct{}{}

		spec, report := buildArtifacts(item)
		if err := evalpkg.ValidateCase(spec); err != nil {
			return fmt.Errorf("%s: generated case contract is invalid: %w", item.ID, err)
		}
		directory := filepath.Join(root, item.Directory)
		if err := ensureDirectory(root, directory); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(directory, "case.json"), spec); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(directory, "report.json"), report); err != nil {
			return err
		}
	}
	return nil
}

func validateScenario(item scenario) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Directory) == "" || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Description) == "" {
		return errors.New("id, directory, title, and description are required")
	}
	if filepath.IsAbs(item.Directory) || filepath.Clean(item.Directory) != item.Directory || strings.HasPrefix(item.Directory, "..") {
		return errors.New("directory must be a clean relative path")
	}
	if item.Path == "" || filepath.IsAbs(item.Path) || strings.HasPrefix(filepath.Clean(item.Path), "..") {
		return errors.New("path must be repository-relative")
	}
	switch item.Kind {
	case evalpkg.CaseKindBug:
		if item.Finding == nil || item.Hypothesis != nil || item.Stages != nil {
			return errors.New("bug cases require exactly one finding")
		}
	case evalpkg.CaseKindClean:
		if item.Finding != nil || item.Hypothesis != nil || item.Stages != nil || item.ExpectedGate != evalpkg.GatePassed {
			return errors.New("clean cases cannot define findings, hypotheses, stage overrides, or a blocked gate")
		}
	case evalpkg.CaseKindNeedsReview:
		if item.Finding != nil || item.Hypothesis == nil || item.Stages != nil {
			return errors.New("needs_review cases require exactly one hypothesis")
		}
	case evalpkg.CaseKindResilience:
		if item.Finding != nil || item.Hypothesis != nil || item.Stages == nil {
			return errors.New("resilience cases require stage overrides only")
		}
	default:
		return fmt.Errorf("unsupported kind %q", item.Kind)
	}
	return nil
}

func buildArtifacts(item scenario) (evalpkg.CaseSpec, review.ReviewReport) {
	spec := evalpkg.CaseSpec{
		SchemaVersion: evalpkg.CaseSchemaVersion, ID: item.ID, Title: item.Title, Description: item.Description,
		Kind: item.Kind, Layer: item.Layer, Provenance: item.Provenance, Tags: append([]string(nil), item.Tags...),
		Report: "report.json", ExpectedFindings: []evalpkg.ExpectedFinding{}, ExpectedGate: item.ExpectedGate,
		ExpectedNeedsReview: 0, MaxUnexpectedFindings: 0,
	}
	report := review.NewReport(review.Comparison{
		Repository: "eval-fixture/" + strings.ToLower(item.ID), Base: "master", Head: "eval/" + strings.ToLower(item.ID),
		BaseCommit: strings.Repeat("0", 40), HeadCommit: stableDigest(item.ID)[:40],
	}, []review.ChangedFile{{
		NewPath: item.Path, Status: review.FileStatusModified, Stats: review.FileStats{Additions: 1}, Hunks: []review.Hunk{},
	}}, nil)
	report.GeneratedAt = time.Date(2026, time.August, 16, 0, 0, 0, 0, time.UTC)
	report.Context = review.EmptyContextBundle(review.ContextComplete)
	report.Agent = review.EmptyAgentRun(review.AgentSkipped)
	report.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	report.Analysis.CompletedStages = []string{"diff", "static-analysis"}

	if item.Finding != nil {
		actual := buildFinding(item, *item.Finding)
		report.Findings = []review.Finding{actual}
		spec.ExpectedFindings = []evalpkg.ExpectedFinding{{
			Severity: item.Finding.Severity, Category: item.Finding.Category, Path: item.Path, StartLine: item.Finding.Line,
			RuleID: item.Finding.RuleID, TitleContains: item.Finding.Title,
		}}
		if item.Layer == evalpkg.LayerSemantic {
			report.Verification.Summary.SemanticFindings = 1
			report.Verification.Summary.Promoted = 1
		}
		if item.Layer == evalpkg.LayerAgent || item.Layer == evalpkg.LayerMixed {
			attachVerifiedCandidate(&report, item, actual)
		}
	}
	if item.Hypothesis != nil {
		attachNeedsReview(&report, item, *item.Hypothesis)
		spec.ExpectedNeedsReview = 1
	}
	if item.Stages != nil {
		applyStageOverrides(&report, *item.Stages)
	}
	report.RecalculateSummary()
	return spec, report
}

func buildFinding(item scenario, value finding) review.Finding {
	fingerprint := stableDigest(item.ID + "|finding")
	return review.Finding{
		ID: "EVAL-" + item.ID, RuleID: value.RuleID, Title: value.Title, Description: value.Description,
		Severity: value.Severity, Category: value.Category,
		Location: review.Location{Path: item.Path, StartLine: value.Line, EndLine: value.Line},
		Evidence: "Versioned golden evidence for " + item.ID + ".", Suggestion: value.Suggestion,
		Confidence: 0.95, Source: value.Source, Fingerprint: fingerprint,
	}
}

func attachVerifiedCandidate(report *review.ReviewReport, item scenario, actual review.Finding) {
	fingerprint := stableDigest(item.ID + "|candidate")
	candidateID := "AGENT-" + strings.ToUpper(fingerprint[:10])
	report.Agent = review.EmptyAgentRun(review.AgentComplete)
	report.Agent.Provider = "recorded-fixture"
	report.Agent.Model = "golden-corpus"
	report.Agent.Steps = 1
	report.Agent.Candidates = []review.CandidateFinding{{
		ID: candidateID, Fingerprint: fingerprint, Title: actual.Title, Description: actual.Description,
		Severity: actual.Severity, Category: actual.Category, Location: actual.Location,
		Evidence: actual.Evidence, Suggestion: actual.Suggestion, Confidence: actual.Confidence,
		Verification: []string{"Replay the recorded deterministic corroboration."},
	}}
	report.Verification.Summary = review.VerificationSummary{Candidates: 1, Verified: 1, Promoted: 1}
	report.Verification.Candidates = []review.CandidateVerification{{
		CandidateID: candidateID, Title: actual.Title, Severity: actual.Severity, Location: actual.Location,
		Verdict: review.CandidateVerified, Reason: "golden corpus records independent corroboration",
		CalibratedConfidence: actual.Confidence, Checks: []review.VerificationCheck{{
			Name: "golden-corroboration", Status: review.VerificationCheckPassed, Detail: "recorded evidence matched",
		}}, MatchedFindingIDs: []string{actual.ID}, FindingID: actual.ID, Promoted: true,
	}}
}

func attachNeedsReview(report *review.ReviewReport, item scenario, value hypothesis) {
	fingerprint := stableDigest(item.ID + "|hypothesis")
	candidateID := "AGENT-" + strings.ToUpper(fingerprint[:10])
	location := review.Location{Path: item.Path, StartLine: value.Line, EndLine: value.Line}
	report.Agent = review.EmptyAgentRun(review.AgentComplete)
	report.Agent.Provider = "recorded-fixture"
	report.Agent.Model = "golden-corpus"
	report.Agent.Steps = 1
	report.Agent.Candidates = []review.CandidateFinding{{
		ID: candidateID, Fingerprint: fingerprint, Title: value.Title, Description: value.Reason,
		Severity: value.Severity, Category: value.Category, Location: location,
		Evidence: "Bounded hypothesis evidence for " + item.ID + ".", Suggestion: "Request focused human review.",
		Confidence: 0.62, Verification: []string{"Inspect the unresolved semantic boundary."},
	}}
	report.Verification.Summary = review.VerificationSummary{Candidates: 1, NeedsReview: 1}
	report.Verification.Candidates = []review.CandidateVerification{{
		CandidateID: candidateID, Title: value.Title, Severity: value.Severity, Location: location,
		Verdict: review.CandidateNeedsReview, Reason: value.Reason, CalibratedConfidence: 0.62,
		Checks:            []review.VerificationCheck{{Name: "independent-corroboration", Status: review.VerificationCheckWarning, Detail: "insufficient deterministic evidence"}},
		MatchedFindingIDs: []string{}, Promoted: false,
	}}
}

func applyStageOverrides(report *review.ReviewReport, stages stageOverrides) {
	if stages.Analysis != "" {
		report.Analysis.Status = stages.Analysis
	}
	if stages.Context != "" {
		report.Context.Status = stages.Context
	}
	if stages.Agent != "" {
		report.Agent = review.EmptyAgentRun(stages.Agent)
	}
	if stages.Verification != "" {
		report.Verification = review.EmptyVerificationRun(stages.Verification)
		report.Verification.Warnings = append([]string(nil), stages.Warnings...)
	}
}

func ensureDirectory(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("case directory escapes output root")
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(directory, 0o755)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("case path %q must be a real directory", directory)
	}
	return nil
}

func writeJSON(path string, value any) error {
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("refuse to replace non-regular file %q", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".aegis-eval-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func stableDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "evalcorpus: "+format+"\n", arguments...)
	os.Exit(1)
}
