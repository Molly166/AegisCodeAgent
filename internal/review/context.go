package review

type ContextStatus string

const (
	ContextNotRun   ContextStatus = "not_run"
	ContextComplete ContextStatus = "complete"
	ContextPartial  ContextStatus = "partial"
	ContextFailed   ContextStatus = "failed"
)

type SymbolKind string

const (
	SymbolFunction  SymbolKind = "function"
	SymbolMethod    SymbolKind = "method"
	SymbolType      SymbolKind = "type"
	SymbolInterface SymbolKind = "interface"
	SymbolVariable  SymbolKind = "variable"
	SymbolConstant  SymbolKind = "constant"
	SymbolTest      SymbolKind = "test"
	SymbolFile      SymbolKind = "file"
)

type ContextSymbol struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	QualifiedName  string     `json:"qualified_name"`
	Kind           SymbolKind `json:"kind"`
	Package        string     `json:"package"`
	Receiver       string     `json:"receiver,omitempty"`
	Path           string     `json:"path"`
	StartLine      int        `json:"start_line"`
	EndLine        int        `json:"end_line"`
	Signature      string     `json:"signature,omitempty"`
	Documentation  string     `json:"documentation,omitempty"`
	Snippet        string     `json:"snippet,omitempty"`
	Changed        bool       `json:"changed"`
	RelevanceScore int        `json:"relevance_score"`
	Reasons        []string   `json:"reasons"`
}

type RelationKind string

const (
	RelationCalls               RelationKind = "calls"
	RelationTests               RelationKind = "tests"
	RelationMemberOf            RelationKind = "member_of"
	RelationImplements          RelationKind = "implements"
	RelationImplementsCandidate RelationKind = "implements_candidate"
)

type ContextRelation struct {
	From       string       `json:"from"`
	To         string       `json:"to"`
	Kind       RelationKind `json:"kind"`
	Confidence float64      `json:"confidence"`
	Reason     string       `json:"reason,omitempty"`
}

type ContextStats struct {
	PackagesLoaded      int `json:"packages_loaded"`
	PackagesTypeChecked int `json:"packages_type_checked"`
	TypeCheckFailures   int `json:"type_check_failures"`
	FilesParsed         int `json:"files_parsed"`
	SymbolsIndexed      int `json:"symbols_indexed"`
	RelationsIndexed    int `json:"relations_indexed"`
	SymbolsSelected     int `json:"symbols_selected"`
	EstimatedTokens     int `json:"estimated_tokens"`
}

type IntentDocument struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ChangeIntent struct {
	Source             string           `json:"source,omitempty"`
	Title              string           `json:"title,omitempty"`
	Description        string           `json:"description,omitempty"`
	Labels             []string         `json:"labels"`
	LinkedIssues       []string         `json:"linked_issues"`
	RepositoryGuidance []IntentDocument `json:"repository_guidance"`
	Truncated          bool             `json:"truncated"`
}

type ContextBundle struct {
	Status         ContextStatus     `json:"status"`
	Intent         ChangeIntent      `json:"change_intent"`
	Packages       []string          `json:"packages"`
	ChangedSymbols []ContextSymbol   `json:"changed_symbols"`
	RelatedSymbols []ContextSymbol   `json:"related_symbols"`
	Relations      []ContextRelation `json:"relations"`
	Stats          ContextStats      `json:"stats"`
	Truncated      bool              `json:"truncated"`
	Warnings       []string          `json:"warnings"`
}

func EmptyContextBundle(status ContextStatus) ContextBundle {
	return ContextBundle{
		Status: status,
		Intent: ChangeIntent{
			Labels: []string{}, LinkedIssues: []string{}, RepositoryGuidance: []IntentDocument{},
		},
		Packages:       []string{},
		ChangedSymbols: []ContextSymbol{},
		RelatedSymbols: []ContextSymbol{},
		Relations:      []ContextRelation{},
		Warnings:       []string{},
	}
}
