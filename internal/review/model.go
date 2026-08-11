package review

import "time"

const SchemaVersion = "v5"

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type Category string

const (
	CategoryBug             Category = "bug"
	CategorySecurity        Category = "security"
	CategoryPerformance     Category = "performance"
	CategoryMaintainability Category = "maintainability"
	CategoryTesting         Category = "testing"
)

type Finding struct {
	ID          string   `json:"id"`
	RuleID      string   `json:"rule_id,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Severity    Severity `json:"severity"`
	Category    Category `json:"category"`
	Location    Location `json:"location"`
	Evidence    string   `json:"evidence,omitempty"`
	Suggestion  string   `json:"suggestion,omitempty"`
	Confidence  float64  `json:"confidence"`
	Source      string   `json:"source"`
	Fingerprint string   `json:"fingerprint,omitempty"`
}

type Location struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line,omitempty"`
}

type FileStatus string

const (
	FileStatusModified FileStatus = "modified"
	FileStatusAdded    FileStatus = "added"
	FileStatusDeleted  FileStatus = "deleted"
	FileStatusRenamed  FileStatus = "renamed"
)

type LineKind string

const (
	LineContext  LineKind = "context"
	LineAddition LineKind = "addition"
	LineDeletion LineKind = "deletion"
)

type DiffLine struct {
	Kind    LineKind `json:"kind"`
	Content string   `json:"content"`
	OldLine int      `json:"old_line,omitempty"`
	NewLine int      `json:"new_line,omitempty"`
}

type Hunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Section  string     `json:"section,omitempty"`
	Lines    []DiffLine `json:"lines"`
}

type FileStats struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type ChangedFile struct {
	OldPath string     `json:"old_path,omitempty"`
	NewPath string     `json:"new_path,omitempty"`
	Status  FileStatus `json:"status"`
	Binary  bool       `json:"binary"`
	Stats   FileStats  `json:"stats"`
	Hunks   []Hunk     `json:"hunks"`
}

func (f ChangedFile) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

type Comparison struct {
	Repository string `json:"repository"`
	Base       string `json:"base"`
	Head       string `json:"head"`
	BaseCommit string `json:"base_commit"`
	HeadCommit string `json:"head_commit"`
}

type AnalysisStatus string

const (
	AnalysisScopeOnly AnalysisStatus = "scope_only"
	AnalysisComplete  AnalysisStatus = "complete"
	AnalysisPartial   AnalysisStatus = "partial"
	AnalysisFailed    AnalysisStatus = "failed"
)

type Analysis struct {
	Status          AnalysisStatus  `json:"status"`
	CompletedStages []string        `json:"completed_stages"`
	Tools           []ToolExecution `json:"tools"`
}

type ToolStatus string

const (
	ToolPassed      ToolStatus = "passed"
	ToolFindings    ToolStatus = "findings"
	ToolSkipped     ToolStatus = "skipped"
	ToolUnavailable ToolStatus = "unavailable"
	ToolFailed      ToolStatus = "failed"
	ToolTimedOut    ToolStatus = "timed_out"
)

type ToolExecution struct {
	Name           string     `json:"name"`
	Status         ToolStatus `json:"status"`
	DurationMillis int64      `json:"duration_ms"`
	Findings       int        `json:"findings"`
	Suppressed     int        `json:"suppressed"`
	Detail         string     `json:"detail,omitempty"`
}

type Summary struct {
	ChangedFiles int `json:"changed_files"`
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	Findings     int `json:"findings"`
	Critical     int `json:"critical"`
	High         int `json:"high"`
	Medium       int `json:"medium"`
	Low          int `json:"low"`
	Info         int `json:"info"`
}

type ReviewReport struct {
	SchemaVersion string          `json:"schema_version"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Comparison    Comparison      `json:"comparison"`
	Analysis      Analysis        `json:"analysis"`
	Context       ContextBundle   `json:"context"`
	Agent         AgentRun        `json:"agent"`
	Verification  VerificationRun `json:"verification"`
	Summary       Summary         `json:"summary"`
	Files         []ChangedFile   `json:"files"`
	Findings      []Finding       `json:"findings"`
}

func NewReport(comparison Comparison, files []ChangedFile, findings []Finding) ReviewReport {
	report := ReviewReport{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Comparison:    comparison,
		Analysis: Analysis{
			Status:          AnalysisComplete,
			CompletedStages: []string{},
			Tools:           []ToolExecution{},
		},
		Context:      EmptyContextBundle(ContextNotRun),
		Agent:        EmptyAgentRun(AgentNotRun),
		Verification: EmptyVerificationRun(VerificationNotRun),
		Files:        append([]ChangedFile(nil), files...),
		Findings:     append([]Finding(nil), findings...),
	}
	if report.Files == nil {
		report.Files = []ChangedFile{}
	}
	if report.Findings == nil {
		report.Findings = []Finding{}
	}
	report.RecalculateSummary()
	return report
}

func NewScopeReport(comparison Comparison, files []ChangedFile) ReviewReport {
	report := NewReport(comparison, files, nil)
	report.Analysis = Analysis{
		Status:          AnalysisScopeOnly,
		CompletedStages: []string{"diff"},
		Tools:           []ToolExecution{},
	}
	return report
}

func (r *ReviewReport) RecalculateSummary() {
	summary := Summary{ChangedFiles: len(r.Files), Findings: len(r.Findings)}
	for _, file := range r.Files {
		summary.Additions += file.Stats.Additions
		summary.Deletions += file.Stats.Deletions
	}
	for _, finding := range r.Findings {
		switch finding.Severity {
		case SeverityCritical:
			summary.Critical++
		case SeverityHigh:
			summary.High++
		case SeverityMedium:
			summary.Medium++
		case SeverityLow:
			summary.Low++
		case SeverityInfo:
			summary.Info++
		}
	}
	r.Summary = summary
}
