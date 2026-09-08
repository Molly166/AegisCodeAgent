package verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const sensitiveEnvToChildRule = "AEGIS-SEC-001"

type semanticEvidenceRule interface {
	Name() string
	Detect(string, []review.ChangedFile, analyzer.ChangedLineSet) ([]review.Finding, []string)
}

var defaultSemanticEvidenceRules = []semanticEvidenceRule{credentialToChildEnvironmentRule{}}

// detectSemanticFindings runs a registry of deterministic, source-aware rules
// for properties that compiler-oriented tools cannot prove.
func detectSemanticFindings(repository string, files []review.ChangedFile, changedLines analyzer.ChangedLineSet) ([]review.Finding, []string) {
	findings := make([]review.Finding, 0)
	warnings := make([]string, 0)
	for _, rule := range defaultSemanticEvidenceRules {
		ruleFindings, ruleWarnings := rule.Detect(repository, files, changedLines)
		findings = append(findings, ruleFindings...)
		for _, warning := range ruleWarnings {
			warnings = append(warnings, rule.Name()+": "+warning)
		}
	}
	return MergeFindings(nil, findings), uniqueStrings(warnings)
}

// credentialToChildEnvironmentRule follows credential values from os.Getenv
// into an exec.Cmd environment, including explicit isolation-boundary bypasses.
type credentialToChildEnvironmentRule struct{}

func (credentialToChildEnvironmentRule) Name() string { return sensitiveEnvToChildRule }

func (credentialToChildEnvironmentRule) Detect(repository string, files []review.ChangedFile, changedLines analyzer.ChangedLineSet) ([]review.Finding, []string) {
	findings := make([]review.Finding, 0)
	warnings := make([]string, 0)
	for _, changedFile := range files {
		path := normalizePath(changedFile.Path())
		if changedFile.Status == review.FileStatusDeleted || !strings.EqualFold(filepath.Ext(path), ".go") {
			continue
		}
		resolved, err := resolveSemanticSource(repository, path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("semantic verification skipped %s: %v", path, err))
			continue
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, resolved, nil, 0)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("semantic verification could not parse %s: %v", path, err))
			continue
		}
		childProcesses := collectChildProcessVariables(parsed)
		ast.Inspect(parsed, func(node ast.Node) bool {
			var values []ast.Expr
			switch expression := node.(type) {
			case *ast.AssignStmt:
				if !assignmentTargetsChildEnvironment(expression.Lhs, expression.Rhs, childProcesses) {
					return true
				}
				values = expression.Rhs
			case *ast.CompositeLit:
				if !isExecCommandType(expression.Type) {
					return true
				}
				values = compositeEnvironmentValues(expression)
				if len(values) == 0 {
					return true
				}
			default:
				return true
			}

			name, line, ok := sensitiveEnvironmentSource(fileSet, values)
			if !ok {
				return true
			}
			location := review.Location{Path: path, StartLine: line, EndLine: line}
			if !changedLines.Contains(location) {
				return true
			}
			untrusted := containsCall(values, "ForUntrustedChild")
			severity := review.SeverityHigh
			description := "A credential-shaped host environment value is copied into a child process environment. The child can read and exfiltrate that credential."
			if untrusted {
				severity = review.SeverityCritical
				description = "A credential-shaped host environment value is explicitly copied into an environment prepared for an untrusted child process, defeating the credential-isolation boundary."
			}
			fingerprint := semanticFingerprint(sensitiveEnvToChildRule, path, line, name)
			findings = append(findings, review.Finding{
				ID:          "AEGIS-S-" + strings.ToUpper(fingerprint[:12]),
				RuleID:      sensitiveEnvToChildRule,
				Title:       "Sensitive credential propagated to child process environment",
				Description: description,
				Severity:    severity,
				Category:    review.CategorySecurity,
				Location:    location,
				Evidence:    fmt.Sprintf("AST data-flow evidence: os.Getenv(%q) reaches an Env assignment%s.", name, ternary(untrusted, " after ForUntrustedChild constructed a restricted environment", "")),
				Suggestion:  "Do not forward the host credential. Pass a narrowly scoped, short-lived token through an approved IPC boundary only when the child requires it.",
				Confidence:  0.99,
				Source:      "semantic:credential-flow",
				Fingerprint: fingerprint,
			})
			return true
		})
	}
	return MergeFindings(nil, findings), uniqueStrings(warnings)
}

func resolveSemanticSource(repository, path string) (string, error) {
	if !safeRelativePath(path) || !allowedSourcePath(path) {
		return "", fmt.Errorf("path is outside the semantic verifier source allowlist")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(repository, filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(repository, resolved)
	if err != nil || !safeRelativePath(relative) || !allowedSourcePath(relative) {
		return "", fmt.Errorf("resolved path escapes the repository")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSourceFileBytes {
		return "", fmt.Errorf("source must be a bounded regular file")
	}
	return resolved, nil
}

func assignmentTargetsChildEnvironment(targets, values []ast.Expr, childProcesses map[string]struct{}) bool {
	if containsCall(values, "ForUntrustedChild") {
		for _, target := range targets {
			if environmentField(target) {
				return true
			}
		}
	}
	for _, expression := range targets {
		selector, ok := expression.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil || selector.Sel.Name != "Env" {
			continue
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok {
			if _, exists := childProcesses[identifier.Name]; exists {
				return true
			}
		}
	}
	return false
}

func collectChildProcessVariables(file *ast.File) map[string]struct{} {
	result := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for index, value := range assignment.Rhs {
			call, ok := value.(*ast.CallExpr)
			if !ok || !(isSelectorCall(call.Fun, "exec", "Command") || isSelectorCall(call.Fun, "exec", "CommandContext")) || index >= len(assignment.Lhs) {
				continue
			}
			identifier, ok := assignment.Lhs[index].(*ast.Ident)
			if ok && identifier.Name != "_" {
				result[identifier.Name] = struct{}{}
			}
		}
		return true
	})
	return result
}

func isExecCommandType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != "Cmd" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "exec"
}

func compositeEnvironmentValues(literal *ast.CompositeLit) []ast.Expr {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if ok && environmentField(field.Key) {
			return []ast.Expr{field.Value}
		}
	}
	return nil
}

func environmentField(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		return value.Sel != nil && value.Sel.Name == "Env"
	case *ast.Ident:
		return value.Name == "Env"
	default:
		return false
	}
}

func sensitiveEnvironmentSource(fileSet *token.FileSet, expressions []ast.Expr) (string, int, bool) {
	var name string
	line := 0
	for _, expression := range expressions {
		ast.Inspect(expression, func(node ast.Node) bool {
			if name != "" {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok || !isSelectorCall(call.Fun, "os", "Getenv") || len(call.Args) != 1 {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			decoded, err := strconv.Unquote(literal.Value)
			if err != nil || !credentialShapedName(decoded) {
				return true
			}
			name = decoded
			line = fileSet.Position(call.Pos()).Line
			return false
		})
	}
	return name, line, name != "" && line > 0
}

func credentialShapedName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	for _, signal := range []string{"API_KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE_KEY", "ACCESS_KEY", "AUTH", "COOKIE", "SESSION"} {
		if strings.Contains(upper, signal) {
			return true
		}
	}
	return false
}

func containsCall(expressions []ast.Expr, function string) bool {
	found := false
	for _, expression := range expressions {
		ast.Inspect(expression, func(node ast.Node) bool {
			if found {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				found = callee.Name == function
			case *ast.SelectorExpr:
				found = callee.Sel != nil && callee.Sel.Name == function
			}
			return !found
		})
	}
	return found
}

func isSelectorCall(expression ast.Expr, receiver, method string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != method {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == receiver
}

func semanticFingerprint(rule, path string, line int, name string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{rule, normalizePath(path), fmt.Sprint(line), strings.ToUpper(name)}, "|")))
	return hex.EncodeToString(digest[:])
}

func ternary(condition bool, whenTrue, whenFalse string) string {
	if condition {
		return whenTrue
	}
	return whenFalse
}
