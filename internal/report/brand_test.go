package report

import (
	"bytes"
	"encoding/base64"
	"html"
	"image/png"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Only the exact renderer-owned image is exempted from injection assertions.
// Additional images or attributes still fail rather than widening the allowlist.
func stripTrustedReportLogo(t *testing.T, document string) string {
	t.Helper()
	images := regexp.MustCompile(`(?i)<img\b[^>]*>`).FindAllString(document, -1)
	want := `<img class="brand-mark" src="` + htmlReportLogoDataURL + `" width="48" height="48" alt="" aria-hidden="true">`
	if len(images) != 1 || html.UnescapeString(images[0]) != want {
		t.Fatal("report must contain exactly the trusted, decorative inline Aegis logo")
	}
	return strings.Replace(document, images[0], "", 1)
}

func TestReportLogoMatchesREADMEAsset(t *testing.T) {
	approved, err := os.ReadFile("../../docs/assets/aegis-pr-gate-harmony.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(htmlReportLogoPNG, approved) {
		t.Fatal("report logo differs from the approved README logo; update the embedded copy together")
	}
	config, err := png.DecodeConfig(bytes.NewReader(htmlReportLogoPNG))
	if err != nil || config.Width == 0 || config.Width != config.Height {
		t.Fatalf("report logo must be a valid square PNG: config=%+v error=%v", config, err)
	}
}

func TestReportLogoIsSelfContainedOutsideRepository(t *testing.T) {
	// Rendering must not read assets from the working directory at runtime.
	t.Chdir(t.TempDir())
	document := renderDaylight(t, daylightReport(), daylightPolicy())
	stripTrustedReportLogo(t, document)
	if strings.Contains(document, "#ZgotmplZ") {
		t.Fatal("template URL filtering removed the trusted embedded logo")
	}
	if !strings.Contains(document, "img-src data:; base-uri 'none'; form-action 'none'") {
		t.Fatal("report must keep its offline, data-image-only security policy")
	}
	if strings.Contains(document, `<svg class="brand-mark"`) {
		t.Fatal("report still renders the retired shield logo")
	}
	encoded := strings.TrimPrefix(htmlReportLogoDataURL, "data:image/png;base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !bytes.Equal(decoded, htmlReportLogoPNG) {
		t.Fatal("inline image must preserve the approved PNG bytes")
	}
}
