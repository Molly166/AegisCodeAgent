package repocontext

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type rankedSymbol struct {
	symbol  *indexedSymbol
	score   int
	reasons map[string]struct{}
}

func (index *repositoryIndex) buildBundle(files []review.ChangedFile) review.ContextBundle {
	bundle := review.EmptyContextBundle(review.ContextComplete)
	bundle.Packages = make([]string, 0, len(index.packages))
	for packagePath := range index.packages {
		bundle.Packages = append(bundle.Packages, packagePath)
	}
	sort.Strings(bundle.Packages)

	changed := index.locateChangedSymbols(files)
	related, addedRelations := index.rankRelatedSymbols(changed)
	if len(addedRelations) > 0 {
		seen := make(map[string]struct{}, len(index.relations))
		for _, relation := range index.relations {
			seen[relation.From+"|"+relation.To+"|"+string(relation.Kind)] = struct{}{}
		}
		for _, relation := range addedRelations {
			index.addRelation(seen, relation)
		}
		index.sortRelations()
	}

	selected := make(map[string]struct{})
	usedBytes := 0
	for _, ranked := range changed {
		if !index.selectSymbol(&bundle.ChangedSymbols, selected, ranked, &usedBytes) {
			bundle.Truncated = true
		}
	}
	for _, ranked := range related {
		if !index.selectSymbol(&bundle.RelatedSymbols, selected, ranked, &usedBytes) {
			bundle.Truncated = true
		}
	}

	for _, relation := range index.relations {
		_, fromSelected := selected[relation.From]
		_, toSelected := selected[relation.To]
		if fromSelected && toSelected {
			bundle.Relations = append(bundle.Relations, relation)
		}
	}
	bundle.Warnings = append(bundle.Warnings, index.warnings...)
	sort.Strings(bundle.Warnings)
	bundle.Stats = review.ContextStats{
		PackagesLoaded:      len(index.packages),
		PackagesTypeChecked: index.typeCheckedPackages,
		TypeCheckFailures:   index.typeCheckFailures,
		FilesParsed:         index.filesParsed,
		SymbolsIndexed:      len(index.symbols),
		RelationsIndexed:    len(index.relations),
		SymbolsSelected:     len(bundle.ChangedSymbols) + len(bundle.RelatedSymbols),
		EstimatedTokens:     (usedBytes + 3) / 4,
	}
	return bundle
}

func (index *repositoryIndex) locateChangedSymbols(files []review.ChangedFile) []*rankedSymbol {
	changed := make(map[string]*rankedSymbol)
	for _, changedFile := range files {
		path := filepath.ToSlash(filepath.Clean(changedFile.NewPath))
		if changedFile.NewPath == "" {
			path = filepath.ToSlash(filepath.Clean(changedFile.OldPath))
		}
		if filepath.Ext(path) != ".go" {
			continue
		}
		if changedFile.Status == review.FileStatusDeleted {
			synthetic := index.fileSymbol(path, "deleted Go file")
			changed[synthetic.model.ID] = &rankedSymbol{symbol: synthetic, score: 100, reasons: reasonSet("deleted Go file")}
			continue
		}
		matched := false
		for _, symbol := range index.symbols {
			if symbol.model.Path != path || !symbolIntersectsDiff(symbol.model, changedFile) {
				continue
			}
			matched = true
			changed[symbol.model.ID] = &rankedSymbol{symbol: symbol, score: 100, reasons: reasonSet("changed declaration or body")}
		}
		if !matched {
			synthetic := index.fileSymbol(path, "file-level or import change")
			changed[synthetic.model.ID] = &rankedSymbol{symbol: synthetic, score: 100, reasons: reasonSet("file-level or import change")}
		}
	}
	result := make([]*rankedSymbol, 0, len(changed))
	for _, symbol := range changed {
		result = append(result, symbol)
	}
	sortRankedSymbols(result)
	return result
}

func symbolIntersectsDiff(symbol review.ContextSymbol, file review.ChangedFile) bool {
	for _, hunk := range file.Hunks {
		start := hunk.NewStart
		end := hunk.NewStart + hunk.NewLines - 1
		if hunk.NewLines == 0 {
			end = start
		}
		if start <= symbol.EndLine && end >= symbol.StartLine {
			return true
		}
	}
	return false
}

func (index *repositoryIndex) fileSymbol(path, reason string) *indexedSymbol {
	packagePath := ""
	if file := index.files[path]; file != nil {
		packagePath = file.packagePath
	}
	model := review.ContextSymbol{
		ID:             "file:" + path,
		Name:           filepath.Base(path),
		QualifiedName:  path,
		Kind:           review.SymbolFile,
		Package:        packagePath,
		Path:           path,
		Changed:        true,
		RelevanceScore: 100,
		Reasons:        []string{reason},
	}
	return &indexedSymbol{model: model, file: index.files[path]}
}

func (index *repositoryIndex) rankRelatedSymbols(changed []*rankedSymbol) ([]*rankedSymbol, []review.ContextRelation) {
	changedIDs := make(map[string]struct{}, len(changed))
	for _, symbol := range changed {
		changedIDs[symbol.symbol.model.ID] = struct{}{}
	}
	related := make(map[string]*rankedSymbol)
	add := func(id string, score int, reason string) {
		if _, ok := changedIDs[id]; ok {
			return
		}
		symbol := index.symbols[id]
		if symbol == nil {
			return
		}
		ranked := related[id]
		if ranked == nil {
			ranked = &rankedSymbol{symbol: symbol, reasons: make(map[string]struct{})}
			related[id] = ranked
		}
		if score > ranked.score {
			ranked.score = score
		}
		ranked.reasons[reason] = struct{}{}
	}
	for _, relation := range index.relations {
		_, fromChanged := changedIDs[relation.From]
		_, toChanged := changedIDs[relation.To]
		if fromChanged {
			score, reason := 80, "called by changed symbol"
			if relation.Kind == review.RelationImplementsCandidate || relation.Kind == review.RelationImplements {
				score, reason = 70, "interface related to changed type"
			} else if relation.Kind == review.RelationMemberOf {
				score, reason = 88, "receiver type of changed method"
			}
			add(relation.To, score, reason)
		}
		if toChanged {
			score, reason := 75, "calls changed symbol"
			if relation.Kind == review.RelationImplementsCandidate || relation.Kind == review.RelationImplements {
				score, reason = 70, "candidate implementation of changed interface"
			}
			if source := index.symbols[relation.From]; source != nil && source.model.Kind == review.SymbolTest {
				score, reason = 95, "test exercises changed symbol"
			} else if relation.Kind == review.RelationMemberOf {
				score, reason = 65, "method declared on changed type"
			}
			add(relation.From, score, reason)
		}
	}
	for _, relation := range index.relations {
		if relation.Kind != review.RelationImplementsCandidate && relation.Kind != review.RelationImplements {
			continue
		}
		if _, receiverRelated := related[relation.From]; receiverRelated {
			add(relation.To, 65, "interface matched by changed receiver type")
		}
	}

	addedRelations := make([]review.ContextRelation, 0)
	for _, changedSymbol := range changed {
		if changedSymbol.symbol.model.Kind == review.SymbolFile {
			continue
		}
		for _, candidate := range index.symbols {
			if candidate.model.Kind != review.SymbolTest || !sameBasePackage(candidate.model.Package, changedSymbol.symbol.model.Package) {
				continue
			}
			if !strings.Contains(strings.ToLower(candidate.model.Name), strings.ToLower(changedSymbol.symbol.model.Name)) {
				continue
			}
			add(candidate.model.ID, 85, "test name references changed symbol")
			addedRelations = append(addedRelations, review.ContextRelation{
				From: candidate.model.ID, To: changedSymbol.symbol.model.ID, Kind: review.RelationTests,
				Confidence: 0.75, Reason: "test name references changed symbol",
			})
		}
	}
	result := make([]*rankedSymbol, 0, len(related))
	for _, symbol := range related {
		result = append(result, symbol)
	}
	sortRankedSymbols(result)
	return result, addedRelations
}

func (index *repositoryIndex) selectSymbol(destination *[]review.ContextSymbol, selected map[string]struct{}, ranked *rankedSymbol, usedBytes *int) bool {
	if _, ok := selected[ranked.symbol.model.ID]; ok {
		return true
	}
	if len(selected) >= index.budget.MaxSymbols {
		return false
	}
	model := ranked.symbol.model
	model.Changed = ranked.score == 100
	model.RelevanceScore = ranked.score
	model.Reasons = sortedReasons(ranked.reasons)
	model.Snippet = truncateUTF8(model.Snippet, index.budget.MaxSnippetBytes)
	symbolBytes := len(model.Signature) + len(model.Documentation) + len(model.Snippet) + len(model.Path) + len(model.QualifiedName)
	if *usedBytes+symbolBytes > index.budget.MaxTotalBytes {
		return false
	}
	*usedBytes += symbolBytes
	selected[model.ID] = struct{}{}
	*destination = append(*destination, model)
	return true
}

func reasonSet(reasons ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(reasons))
	for _, reason := range reasons {
		set[reason] = struct{}{}
	}
	return set
}

func sortedReasons(reasons map[string]struct{}) []string {
	result := make([]string, 0, len(reasons))
	for reason := range reasons {
		result = append(result, reason)
	}
	sort.Strings(result)
	return result
}

func sortRankedSymbols(symbols []*rankedSymbol) {
	sort.Slice(symbols, func(i, j int) bool {
		left, right := symbols[i], symbols[j]
		if left.score != right.score {
			return left.score > right.score
		}
		if left.symbol.model.Path != right.symbol.model.Path {
			return left.symbol.model.Path < right.symbol.model.Path
		}
		if left.symbol.model.StartLine != right.symbol.model.StartLine {
			return left.symbol.model.StartLine < right.symbol.model.StartLine
		}
		return left.symbol.model.ID < right.symbol.model.ID
	})
}
