package repocontext

import (
	"go/ast"
	"sort"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func (index *repositoryIndex) buildRelations() {
	symbols := index.sortedIndexedSymbols()
	seen := make(map[string]struct{})
	for _, symbol := range symbols {
		if symbol.function == nil || symbol.function.Body == nil {
			continue
		}
		ast.Inspect(symbol.function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			target := index.resolveTypedCall(symbol.file, call)
			confidence, reason := 1.0, "go/types resolved call"
			if target == nil {
				target, confidence, reason = index.resolveCall(symbol.file, call)
			}
			if target == nil {
				return true
			}
			index.addRelation(seen, review.ContextRelation{
				From: symbol.model.ID, To: target.model.ID, Kind: review.RelationCalls,
				Confidence: confidence, Reason: reason,
			})
			return true
		})
	}
	index.addTypedInterfaceRelations(seen, symbols)
	index.buildInterfaceCandidates(seen, symbols)
	index.sortRelations()
}

func (index *repositoryIndex) resolveCall(file *indexedFile, call *ast.CallExpr) (*indexedSymbol, float64, string) {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		candidates := callableSymbols(index.byPackageName[file.packagePath][function.Name])
		if len(candidates) == 1 {
			return candidates[0], 1, "package-local function call"
		}
	case *ast.SelectorExpr:
		if identifier, ok := function.X.(*ast.Ident); ok {
			if importPath, ok := file.imports[identifier.Name]; ok {
				candidates := callableSymbols(index.byPackageName[importPath][function.Sel.Name])
				if len(candidates) == 1 {
					return candidates[0], 0.98, "import-qualified function call"
				}
			}
			for _, candidate := range index.methodsByName[function.Sel.Name] {
				if candidate.model.Package == file.packagePath && baseReceiver(candidate.model.Receiver) == identifier.Name {
					return candidate, 0.95, "explicit method expression"
				}
			}
		}
		candidates := make([]*indexedSymbol, 0)
		for _, candidate := range index.methodsByName[function.Sel.Name] {
			if candidate.model.Package == file.packagePath {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) == 1 {
			return candidates[0], 0.65, "unique package method name (syntax inference)"
		}
	}
	return nil, 0, ""
}

func callableSymbols(symbols []*indexedSymbol) []*indexedSymbol {
	result := make([]*indexedSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		switch symbol.model.Kind {
		case review.SymbolFunction, review.SymbolTest:
			result = append(result, symbol)
		}
	}
	return result
}

func (index *repositoryIndex) buildInterfaceCandidates(seen map[string]struct{}, symbols []*indexedSymbol) {
	methodSets := make(map[string]map[string]string)
	methodsByType := make(map[string][]*indexedSymbol)
	types := make(map[string]*indexedSymbol)
	interfaces := make([]*indexedSymbol, 0)
	for _, symbol := range symbols {
		switch symbol.model.Kind {
		case review.SymbolType:
			types[symbol.model.Package+"::"+symbol.model.Name] = symbol
		case review.SymbolInterface:
			interfaces = append(interfaces, symbol)
		case review.SymbolMethod:
			key := symbol.model.Package + "::" + baseReceiver(symbol.model.Receiver)
			if methodSets[key] == nil {
				methodSets[key] = make(map[string]string)
			}
			methodSets[key][symbol.model.Name] = symbol.methodSignature
			methodsByType[key] = append(methodsByType[key], symbol)
		}
	}
	for typeKey, methods := range methodSets {
		typeSymbol := types[typeKey]
		if typeSymbol == nil {
			continue
		}
		for _, method := range methodsByType[typeKey] {
			index.addRelation(seen, review.ContextRelation{
				From: method.model.ID, To: typeSymbol.model.ID, Kind: review.RelationMemberOf,
				Confidence: 1, Reason: "method receiver type",
			})
		}
		for _, interfaceSymbol := range interfaces {
			exactKey := typeSymbol.model.ID + "|" + interfaceSymbol.model.ID + "|" + string(review.RelationImplements)
			if _, ok := seen[exactKey]; ok {
				continue
			}
			if interfaceSymbol.interfaceEmbedded || len(interfaceSymbol.interfaceMethods) == 0 {
				continue
			}
			if typeSymbol.model.Package != interfaceSymbol.model.Package && containsUnexportedMethod(interfaceSymbol.interfaceMethods) {
				continue
			}
			if !matchesMethodSet(methods, interfaceSymbol.interfaceMethods) {
				continue
			}
			index.addRelation(seen, review.ContextRelation{
				From:       typeSymbol.model.ID,
				To:         interfaceSymbol.model.ID,
				Kind:       review.RelationImplementsCandidate,
				Confidence: 0.72,
				Reason:     "AST method-set signature match; type-check verification pending",
			})
		}
	}
}

func matchesMethodSet(typeMethods, interfaceMethods map[string]string) bool {
	for name, signature := range interfaceMethods {
		if typeMethods[name] != signature {
			return false
		}
	}
	return true
}

func containsUnexportedMethod(methods map[string]string) bool {
	for name := range methods {
		if !ast.IsExported(name) {
			return true
		}
	}
	return false
}

func (index *repositoryIndex) addRelation(seen map[string]struct{}, relation review.ContextRelation) {
	key := relation.From + "|" + relation.To + "|" + string(relation.Kind)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	index.relations = append(index.relations, relation)
}

func (index *repositoryIndex) sortRelations() {
	sort.Slice(index.relations, func(i, j int) bool {
		left, right := index.relations[i], index.relations[j]
		if left.From != right.From {
			return left.From < right.From
		}
		if left.To != right.To {
			return left.To < right.To
		}
		return left.Kind < right.Kind
	})
}

func (index *repositoryIndex) sortedIndexedSymbols() []*indexedSymbol {
	result := make([]*indexedSymbol, 0, len(index.symbols))
	for _, symbol := range index.symbols {
		result = append(result, symbol)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].model.ID < result[j].model.ID })
	return result
}

func sameBasePackage(left, right string) bool {
	return strings.TrimSuffix(left, "_test") == strings.TrimSuffix(right, "_test")
}
