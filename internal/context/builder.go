package repocontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	defaultMaxSymbols      = 40
	defaultMaxSnippetBytes = 1400
	defaultMaxTotalBytes   = 48 * 1024
	defaultMaxFileBytes    = 2 * 1024 * 1024
)

type Budget struct {
	MaxSymbols      int
	MaxSnippetBytes int
	MaxTotalBytes   int
	MaxFileBytes    int64
}

type Input struct {
	Repository string
	Packages   []string
	Files      []review.ChangedFile
	Budget     Budget
}

type Builder struct {
	runner analyzer.Runner
}

func NewBuilder(runner analyzer.Runner) Builder {
	return Builder{runner: runner}
}

func (b Builder) Build(ctx context.Context, input Input) (review.ContextBundle, error) {
	if strings.TrimSpace(input.Repository) == "" {
		return review.ContextBundle{}, errors.New("repository path is required")
	}
	if len(input.Packages) == 0 {
		return review.EmptyContextBundle(review.ContextComplete), nil
	}
	if _, err := b.runner.LookPath("go"); err != nil {
		return review.ContextBundle{}, errors.New("go is not installed or not available on PATH")
	}
	budget := normalizeBudget(input.Budget)
	packages, err := b.loadPackages(ctx, input.Repository, input.Packages)
	if err != nil {
		return review.ContextBundle{}, err
	}
	exports := b.loadExports(ctx, input.Repository, input.Packages)
	index := newRepositoryIndex(input.Repository, budget, exports)
	for _, packageInfo := range packages {
		if err := ctx.Err(); err != nil {
			return review.ContextBundle{}, err
		}
		index.addPackage(packageInfo)
	}
	if err := ctx.Err(); err != nil {
		return review.ContextBundle{}, err
	}
	index.buildTypeInformation()
	index.buildRelations()
	bundle := index.buildBundle(input.Files)
	if index.typeCheckFailures > 0 {
		bundle.Warnings = append(bundle.Warnings, fmt.Sprintf(
			"%d package(s) used AST fallback because complete go/types information was unavailable",
			index.typeCheckFailures,
		))
	}
	sort.Strings(bundle.Warnings)
	if len(bundle.Warnings) > 0 {
		bundle.Status = review.ContextPartial
	}
	return bundle, nil
}

func (b Builder) loadExports(ctx context.Context, repository string, patterns []string) map[string]string {
	arguments := []string{"list", "-deps", "-export", "-json"}
	arguments = append(arguments, patterns...)
	execution, err := b.runner.Run(ctx, analyzer.Command{
		Name: "go", Arguments: arguments, Directory: repository,
	})
	if err != nil {
		return map[string]string{}
	}
	exports := make(map[string]string)
	decoder := json.NewDecoder(strings.NewReader(execution.Stdout))
	for {
		var packageInfo struct {
			ImportPath string
			Export     string
		}
		if err := decoder.Decode(&packageInfo); err != nil {
			break
		}
		if packageInfo.ImportPath != "" && packageInfo.Export != "" {
			exports[packageInfo.ImportPath] = packageInfo.Export
		}
	}
	return exports
}

func (b Builder) loadPackages(ctx context.Context, repository string, patterns []string) ([]goListPackage, error) {
	arguments := []string{"list", "-json"}
	arguments = append(arguments, patterns...)
	execution, err := b.runner.Run(ctx, analyzer.Command{
		Name: "go", Arguments: arguments, Directory: repository,
	})
	if err != nil {
		return nil, fmt.Errorf("run go list: %w", err)
	}
	if execution.ExitCode != 0 {
		detail := strings.TrimSpace(execution.CombinedOutput())
		if len(detail) > 1200 {
			detail = detail[len(detail)-1200:]
		}
		return nil, fmt.Errorf("go list failed with exit code %d: %s", execution.ExitCode, detail)
	}
	decoder := json.NewDecoder(strings.NewReader(execution.Stdout))
	packages := make([]goListPackage, 0)
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return nil, fmt.Errorf("resolve repository symlinks: %w", err)
	}
	for {
		var packageInfo goListPackage
		if err := decoder.Decode(&packageInfo); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		if packageInfo.ImportPath == "" || packageInfo.Dir == "" {
			continue
		}
		resolvedDirectory, err := filepath.EvalSymlinks(packageInfo.Dir)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(resolvedRepository, resolvedDirectory)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		packages = append(packages, packageInfo)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ImportPath < packages[j].ImportPath })
	return packages, nil
}

func normalizeBudget(budget Budget) Budget {
	if budget.MaxSymbols <= 0 {
		budget.MaxSymbols = defaultMaxSymbols
	}
	if budget.MaxSnippetBytes <= 0 {
		budget.MaxSnippetBytes = defaultMaxSnippetBytes
	}
	if budget.MaxTotalBytes <= 0 {
		budget.MaxTotalBytes = defaultMaxTotalBytes
	}
	if budget.MaxFileBytes <= 0 {
		budget.MaxFileBytes = defaultMaxFileBytes
	}
	return budget
}

type goListPackage struct {
	Dir          string
	ImportPath   string
	Name         string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
}
