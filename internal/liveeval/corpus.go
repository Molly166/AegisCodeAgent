package liveeval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

var caseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

func loadCorpus(path string) (Corpus, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Corpus{}, "", err
	}
	if info.IsDir() {
		path = filepath.Join(path, "corpus.json")
	}
	data, err := readRegularFile(path, 4*1024*1024)
	if err != nil {
		return Corpus{}, "", err
	}
	var corpus Corpus
	if err := decode(data, &corpus); err != nil {
		return Corpus{}, "", err
	}
	if corpus.SchemaVersion != SchemaVersion || len(corpus.Cases) == 0 || len(corpus.Cases) > 100 {
		return Corpus{}, "", errors.New("live corpus requires live-v1 schema and 1..100 cases")
	}
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if !caseIDPattern.MatchString(c.ID) || seen[c.ID] {
			return Corpus{}, "", errors.New("case IDs must be unique safe identifiers")
		}
		seen[c.ID] = true
		if err := validateCase(c); err != nil {
			return Corpus{}, "", fmt.Errorf("case %s: %w", c.ID, err)
		}
	}
	digest := sha256.Sum256(data)
	return corpus, hex.EncodeToString(digest[:]), nil
}

func validateCase(c Case) error {
	if c.Title == "" || c.Description == "" || len(c.Base) == 0 || len(c.Head) == 0 {
		return errors.New("title, description, base and head files are required")
	}
	if c.Kind != "bug" && c.Kind != "clean" {
		return errors.New("kind must be bug or clean")
	}
	if c.ExpectedGate != "blocked" && c.ExpectedGate != "passed" {
		return errors.New("expected_gate must be blocked or passed")
	}
	if (c.Kind == "bug") != (len(c.Expected) > 0) || (c.Kind == "clean" && c.ExpectedGate != "passed") {
		return errors.New("bug cases need independent labels; clean cases must expect no findings and a passed gate")
	}
	changed := false
	for _, files := range []map[string]string{c.Base, c.Head} {
		if len(files) > 20 {
			return errors.New("a fixture may contain at most 20 files per revision")
		}
		for path, source := range files {
			if !safeSourcePath(path) || len(source) > 128*1024 || strings.ContainsRune(source, 0) {
				return fmt.Errorf("invalid source file %q", path)
			}
		}
	}
	for path, source := range c.Head {
		if original, ok := c.Base[path]; !ok || source != original {
			changed = true
		}
	}
	if !changed {
		return errors.New("head must change source content")
	}
	seen := map[string]bool{}
	for _, expected := range c.Expected {
		p, err := githubreport.ParsePriority(string(expected.Priority))
		if err != nil || p == githubreport.PriorityNone || !caseIDPattern.MatchString(expected.ID) || seen[expected.ID] || !safeSourcePath(expected.Path) || expected.StartLine < 1 || expected.EndLine < expected.StartLine || len(expected.EvidenceTermsAny) == 0 {
			return errors.New("invalid expected finding identity, priority, location, or evidence terms")
		}
		seen[expected.ID] = true
		switch expected.Category {
		case review.CategoryBug, review.CategorySecurity, review.CategoryPerformance, review.CategoryMaintainability, review.CategoryTesting:
		default:
			return errors.New("unsupported expected category")
		}
		source, ok := c.Head[expected.Path]
		if !ok {
			source, ok = c.Base[expected.Path]
		}
		if !ok || expected.EndLine > len(strings.Split(source, "\n")) {
			return errors.New("expected location must exist in head")
		}
		for _, term := range expected.EvidenceTermsAny {
			if len(strings.TrimSpace(term)) < 3 {
				return errors.New("evidence terms must contain at least three characters")
			}
		}
	}
	return nil
}

func safeSourcePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "\\\x00:") || filepath.ToSlash(filepath.Clean(path)) != path {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." || strings.HasPrefix(segment, ".") {
			return false
		}
	}
	return strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".md")
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular non-symlink file smaller than %d bytes", path, limit)
	}
	// #nosec G304 -- paths are explicit local corpus/report files, bounded and
	// checked with Lstat; generated reports live in an owned temporary directory.
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > int(limit) {
		return nil, errors.New("file exceeded size limit")
	}
	return data, err
}

func decode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON content is not allowed")
	}
	return nil
}
