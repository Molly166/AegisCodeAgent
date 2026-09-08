package repocontext

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type repositoryIndex struct {
	repository          string
	budget              Budget
	fset                *token.FileSet
	packages            map[string]*indexedPackage
	files               map[string]*indexedFile
	symbols             map[string]*indexedSymbol
	byPackageName       map[string]map[string][]*indexedSymbol
	methodsByName       map[string][]*indexedSymbol
	importer            types.Importer
	relations           []review.ContextRelation
	warnings            []string
	filesParsed         int
	typeCheckedPackages int
	typeCheckFailures   int
}

type indexedPackage struct {
	path  string
	name  string
	files []*indexedFile
}

type indexedFile struct {
	path        string
	absolute    string
	packagePath string
	syntax      *ast.File
	source      []byte
	imports     map[string]string
	typeInfo    *types.Info
	isTest      bool
}

type indexedSymbol struct {
	model             review.ContextSymbol
	node              ast.Node
	file              *indexedFile
	function          *ast.FuncDecl
	interfaceMethods  map[string]string
	interfaceEmbedded bool
	methodSignature   string
	goType            types.Type
}

func newRepositoryIndex(repository string, budget Budget, exports map[string]string) *repositoryIndex {
	fset := token.NewFileSet()
	return &repositoryIndex{
		repository:    repository,
		budget:        budget,
		fset:          fset,
		packages:      make(map[string]*indexedPackage),
		files:         make(map[string]*indexedFile),
		symbols:       make(map[string]*indexedSymbol),
		byPackageName: make(map[string]map[string][]*indexedSymbol),
		methodsByName: make(map[string][]*indexedSymbol),
		importer:      newExportImporter(fset, exports),
	}
}

func (index *repositoryIndex) addPackage(info goListPackage) {
	packageIndex := &indexedPackage{path: info.ImportPath, name: info.Name}
	index.packages[info.ImportPath] = packageIndex
	externalTests := make(map[string]struct{}, len(info.XTestGoFiles))
	for _, name := range info.XTestGoFiles {
		externalTests[name] = struct{}{}
	}
	fileNames := append([]string{}, info.GoFiles...)
	fileNames = append(fileNames, info.CgoFiles...)
	fileNames = append(fileNames, info.TestGoFiles...)
	fileNames = append(fileNames, info.XTestGoFiles...)
	sort.Strings(fileNames)
	seen := make(map[string]struct{})
	for _, fileName := range fileNames {
		if filepath.Base(fileName) != fileName {
			index.warnings = append(index.warnings, fmt.Sprintf("skip unsafe Go filename %q", fileName))
			continue
		}
		if _, ok := seen[fileName]; ok {
			continue
		}
		seen[fileName] = struct{}{}
		absolutePath := filepath.Join(info.Dir, fileName)
		resolvedPath, resolveErr := filepath.EvalSymlinks(absolutePath)
		relativeResolved, relativeErr := filepath.Rel(index.repository, resolvedPath)
		if resolveErr != nil || relativeErr != nil || relativeResolved == ".." || strings.HasPrefix(relativeResolved, ".."+string(filepath.Separator)) {
			index.warnings = append(index.warnings, fmt.Sprintf("skip Go source outside repository: %s", fileName))
			continue
		}
		relativeOriginal, originalErr := filepath.Rel(index.repository, absolutePath)
		if originalErr != nil || !allowedContextSource(relativeOriginal) || !allowedContextSource(relativeResolved) {
			index.warnings = append(index.warnings, fmt.Sprintf("skip hidden or unsupported Go source: %s", fileName))
			continue
		}
		fileInfo, err := os.Stat(absolutePath)
		if err != nil {
			index.warnings = append(index.warnings, fmt.Sprintf("inspect %s: %v", fileName, err))
			continue
		}
		if !fileInfo.Mode().IsRegular() {
			index.warnings = append(index.warnings, fmt.Sprintf("skip non-regular Go source: %s", fileName))
			continue
		}
		if fileInfo.Size() > index.budget.MaxFileBytes {
			index.warnings = append(index.warnings, fmt.Sprintf("skip oversized Go file %s (%d bytes)", fileName, fileInfo.Size()))
			continue
		}
		source, err := os.ReadFile(absolutePath)
		if err != nil {
			index.warnings = append(index.warnings, fmt.Sprintf("read %s: %v", fileName, err))
			continue
		}
		syntax, parseErr := parser.ParseFile(index.fset, absolutePath, source, parser.ParseComments|parser.SkipObjectResolution)
		if parseErr != nil {
			index.warnings = append(index.warnings, fmt.Sprintf("parse %s: %v", fileName, parseErr))
		}
		if syntax == nil {
			continue
		}
		relativePath, err := filepath.Rel(index.repository, absolutePath)
		if err != nil {
			index.warnings = append(index.warnings, fmt.Sprintf("resolve %s: %v", fileName, err))
			continue
		}
		packagePath := info.ImportPath
		if _, ok := externalTests[fileName]; ok {
			packagePath += "_test"
		}
		file := &indexedFile{
			path:        filepath.ToSlash(relativePath),
			absolute:    absolutePath,
			packagePath: packagePath,
			syntax:      syntax,
			source:      source,
			imports:     parseImports(syntax),
			isTest:      strings.HasSuffix(fileName, "_test.go"),
		}
		packageIndex.files = append(packageIndex.files, file)
		index.files[file.path] = file
		index.filesParsed++
		index.indexDeclarations(file)
	}
}

func allowedContextSource(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".go") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != ".github" {
			return false
		}
	}
	return true
}

func (index *repositoryIndex) indexDeclarations(file *indexedFile) {
	for _, declaration := range file.syntax.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			index.addFunction(file, declaration)
		case *ast.GenDecl:
			index.addGeneralDeclaration(file, declaration)
		}
	}
}

func (index *repositoryIndex) addFunction(file *indexedFile, declaration *ast.FuncDecl) {
	receiver := ""
	kind := review.SymbolFunction
	if declaration.Recv != nil && len(declaration.Recv.List) > 0 {
		receiver = receiverName(declaration.Recv.List[0].Type)
		kind = review.SymbolMethod
	} else if isTestFunction(file.path, declaration.Name.Name) {
		kind = review.SymbolTest
	}
	qualifiedName := declaration.Name.Name
	if receiver != "" {
		qualifiedName = receiver + "." + declaration.Name.Name
	}
	symbol := &indexedSymbol{
		model: index.newSymbol(
			file,
			kind,
			declaration.Name.Name,
			qualifiedName,
			receiver,
			declaration,
			functionSignature(index.fset, declaration),
			commentText(declaration.Doc),
		),
		node:            declaration,
		file:            file,
		function:        declaration,
		methodSignature: functionTypeKey(index.fset, declaration.Type),
	}
	index.registerSymbol(symbol)
}

func (index *repositoryIndex) addGeneralDeclaration(file *indexedFile, declaration *ast.GenDecl) {
	for _, specification := range declaration.Specs {
		switch specification := specification.(type) {
		case *ast.TypeSpec:
			kind := review.SymbolType
			methods := map[string]string(nil)
			embedded := false
			if interfaceType, ok := specification.Type.(*ast.InterfaceType); ok {
				kind = review.SymbolInterface
				methods, embedded = interfaceMethodSet(index.fset, interfaceType)
			}
			documentation := commentText(specification.Doc)
			if documentation == "" {
				documentation = commentText(declaration.Doc)
			}
			symbol := &indexedSymbol{
				model: index.newSymbol(
					file,
					kind,
					specification.Name.Name,
					specification.Name.Name,
					"",
					specification,
					"type "+specification.Name.Name+" "+renderNode(index.fset, specification.Type),
					documentation,
				),
				node:              specification,
				file:              file,
				interfaceMethods:  methods,
				interfaceEmbedded: embedded,
			}
			index.registerSymbol(symbol)
		case *ast.ValueSpec:
			kind := review.SymbolVariable
			if declaration.Tok == token.CONST {
				kind = review.SymbolConstant
			}
			documentation := commentText(specification.Doc)
			if documentation == "" {
				documentation = commentText(declaration.Doc)
			}
			for _, name := range specification.Names {
				symbol := &indexedSymbol{
					model: index.newSymbol(
						file, kind, name.Name, name.Name, "", specification,
						declaration.Tok.String()+" "+renderNode(index.fset, specification),
						documentation,
					),
					node: specification,
					file: file,
				}
				index.registerSymbol(symbol)
			}
		}
	}
}

func (index *repositoryIndex) newSymbol(file *indexedFile, kind review.SymbolKind, name, qualifiedName, receiver string, node ast.Node, signature, documentation string) review.ContextSymbol {
	start := index.fset.Position(node.Pos())
	end := index.fset.Position(node.End())
	return review.ContextSymbol{
		ID:            symbolID(file.packagePath, kind, qualifiedName),
		Name:          name,
		QualifiedName: qualifiedName,
		Kind:          kind,
		Package:       file.packagePath,
		Receiver:      receiver,
		Path:          file.path,
		StartLine:     start.Line,
		EndLine:       end.Line,
		Signature:     signature,
		Documentation: documentation,
		Snippet:       sourceSnippet(index.fset, file.source, node, index.budget.MaxSnippetBytes),
		Reasons:       []string{},
	}
}

func (index *repositoryIndex) registerSymbol(symbol *indexedSymbol) {
	index.symbols[symbol.model.ID] = symbol
	if index.byPackageName[symbol.model.Package] == nil {
		index.byPackageName[symbol.model.Package] = make(map[string][]*indexedSymbol)
	}
	index.byPackageName[symbol.model.Package][symbol.model.Name] = append(index.byPackageName[symbol.model.Package][symbol.model.Name], symbol)
	if symbol.model.Kind == review.SymbolMethod {
		index.methodsByName[symbol.model.Name] = append(index.methodsByName[symbol.model.Name], symbol)
	}
}

func parseImports(file *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, importSpec := range file.Imports {
		path, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if importSpec.Name != nil {
			name = importSpec.Name.Name
		}
		if name != "_" && name != "." {
			imports[name] = path
		}
	}
	return imports
}

func symbolID(packagePath string, kind review.SymbolKind, qualifiedName string) string {
	return packagePath + "#" + string(kind) + ":" + qualifiedName
}

func receiverName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return "*" + receiverName(expression.X)
	case *ast.IndexExpr:
		return receiverName(expression.X)
	case *ast.IndexListExpr:
		return receiverName(expression.X)
	case *ast.SelectorExpr:
		return receiverName(expression.X) + "." + expression.Sel.Name
	default:
		return "?"
	}
}

func baseReceiver(receiver string) string {
	return strings.TrimPrefix(receiver, "*")
}

func isTestFunction(path, name string) bool {
	if !strings.HasSuffix(path, "_test.go") {
		return false
	}
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func functionSignature(fset *token.FileSet, declaration *ast.FuncDecl) string {
	functionType := renderNode(fset, declaration.Type)
	functionType = strings.TrimPrefix(functionType, "func")
	if declaration.Recv == nil {
		return "func " + declaration.Name.Name + functionType
	}
	receiver := receiverName(declaration.Recv.List[0].Type)
	return "func (" + receiver + ") " + declaration.Name.Name + functionType
}

func functionTypeKey(fset *token.FileSet, functionType *ast.FuncType) string {
	return fieldListKey(fset, functionType.Params) + "->" + fieldListKey(fset, functionType.Results)
}

func fieldListKey(fset *token.FileSet, fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return "()"
	}
	parts := make([]string, 0)
	for _, field := range fields.List {
		value := renderNode(fset, field.Type)
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			parts = append(parts, value)
		}
	}
	return "(" + strings.Join(parts, ",") + ")"
}

func interfaceMethodSet(fset *token.FileSet, interfaceType *ast.InterfaceType) (map[string]string, bool) {
	methods := make(map[string]string)
	embedded := false
	for _, field := range interfaceType.Methods.List {
		if len(field.Names) == 0 {
			embedded = true
			continue
		}
		functionType, ok := field.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			methods[name.Name] = functionTypeKey(fset, functionType)
		}
	}
	return methods, embedded
}

func commentText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return strings.TrimSpace(group.Text())
}

func renderNode(fset *token.FileSet, node any) string {
	var output bytes.Buffer
	if err := printer.Fprint(&output, fset, node); err != nil {
		return ""
	}
	return output.String()
}

func sourceSnippet(fset *token.FileSet, source []byte, node ast.Node, limit int) string {
	start := fset.Position(node.Pos()).Offset
	end := fset.Position(node.End()).Offset
	if start < 0 || end < start || end > len(source) {
		return ""
	}
	return truncateUTF8(strings.TrimSpace(string(source[start:end])), limit)
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit - len("\n…")
	if cut <= 0 {
		return "…"
	}
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "\n…"
}
