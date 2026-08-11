package repocontext

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func (index *repositoryIndex) buildTypeInformation() {
	groups := make(map[string][]*indexedFile)
	for _, file := range index.files {
		if file.isTest {
			continue
		}
		groups[file.packagePath] = append(groups[file.packagePath], file)
	}
	packagePaths := make([]string, 0, len(groups))
	for packagePath := range groups {
		packagePaths = append(packagePaths, packagePath)
	}
	sort.Strings(packagePaths)
	for _, packagePath := range packagePaths {
		files := groups[packagePath]
		sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
		syntax := make([]*ast.File, 0, len(files))
		for _, file := range files {
			syntax = append(syntax, file.syntax)
		}
		info := &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
		}
		errorCount := 0
		configuration := types.Config{
			Importer: index.importer,
			Error:    func(error) { errorCount++ },
		}
		_, checkError := configuration.Check(packagePath, index.fset, syntax, info)
		for _, file := range files {
			file.typeInfo = info
		}
		if checkError == nil && errorCount == 0 {
			index.typeCheckedPackages++
		} else {
			index.typeCheckFailures++
		}
		for _, symbol := range index.symbols {
			if symbol.model.Package != packagePath {
				continue
			}
			typeSpec, ok := symbol.node.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if object, ok := info.Defs[typeSpec.Name].(*types.TypeName); ok {
				symbol.goType = object.Type()
			}
		}
	}
}

type exportImporter struct {
	compiler types.Importer
	fallback types.Importer
	exports  map[string]string
}

func newExportImporter(fset *token.FileSet, exports map[string]string) types.Importer {
	lookup := func(path string) (io.ReadCloser, error) {
		exportPath := exports[path]
		if exportPath == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(exportPath)
	}
	return &exportImporter{
		compiler: importer.ForCompiler(fset, "gc", lookup),
		fallback: importer.Default(),
		exports:  exports,
	}
}

func (i *exportImporter) Import(path string) (*types.Package, error) {
	if i.exports[path] != "" {
		if imported, err := i.compiler.Import(path); err == nil {
			return imported, nil
		}
	}
	return i.fallback.Import(path)
}

func (index *repositoryIndex) resolveTypedCall(file *indexedFile, call *ast.CallExpr) *indexedSymbol {
	if file.typeInfo == nil {
		return nil
	}
	var object types.Object
	switch function := call.Fun.(type) {
	case *ast.Ident:
		object = file.typeInfo.Uses[function]
	case *ast.SelectorExpr:
		if selection := file.typeInfo.Selections[function]; selection != nil {
			object = selection.Obj()
		} else {
			object = file.typeInfo.Uses[function.Sel]
		}
	}
	function, ok := object.(*types.Func)
	if !ok || function.Pkg() == nil {
		return nil
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil {
		return uniqueSymbol(index.byPackageName[function.Pkg().Path()][function.Name()], review.SymbolFunction, review.SymbolTest)
	}
	receiver := namedTypeName(signature.Recv().Type())
	for _, candidate := range index.methodsByName[function.Name()] {
		if candidate.model.Package == function.Pkg().Path() && baseReceiver(candidate.model.Receiver) == receiver {
			return candidate
		}
	}
	return nil
}

func uniqueSymbol(symbols []*indexedSymbol, kinds ...review.SymbolKind) *indexedSymbol {
	allowed := make(map[review.SymbolKind]struct{}, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = struct{}{}
	}
	var match *indexedSymbol
	for _, symbol := range symbols {
		if _, ok := allowed[symbol.model.Kind]; !ok {
			continue
		}
		if match != nil {
			return nil
		}
		match = symbol
	}
	return match
}

func namedTypeName(value types.Type) string {
	if pointer, ok := value.(*types.Pointer); ok {
		value = pointer.Elem()
	}
	if named, ok := value.(*types.Named); ok {
		return named.Obj().Name()
	}
	return strings.TrimPrefix(types.TypeString(value, nil), "*")
}

func (index *repositoryIndex) addTypedInterfaceRelations(seen map[string]struct{}, symbols []*indexedSymbol) {
	typeSymbols := make([]*indexedSymbol, 0)
	interfaces := make([]*indexedSymbol, 0)
	for _, symbol := range symbols {
		if symbol.goType == nil {
			continue
		}
		switch symbol.model.Kind {
		case review.SymbolType:
			typeSymbols = append(typeSymbols, symbol)
		case review.SymbolInterface:
			interfaces = append(interfaces, symbol)
		}
	}
	for _, typeSymbol := range typeSymbols {
		for _, interfaceSymbol := range interfaces {
			interfaceType, ok := interfaceSymbol.goType.Underlying().(*types.Interface)
			if !ok {
				continue
			}
			implemented := types.Implements(typeSymbol.goType, interfaceType)
			if !implemented {
				implemented = types.Implements(types.NewPointer(typeSymbol.goType), interfaceType)
			}
			if !implemented {
				continue
			}
			index.addRelation(seen, review.ContextRelation{
				From:       typeSymbol.model.ID,
				To:         interfaceSymbol.model.ID,
				Kind:       review.RelationImplements,
				Confidence: 1,
				Reason:     "go/types verified method-set implementation",
			})
		}
	}
}
