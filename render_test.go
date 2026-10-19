package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func loadSeed(t *testing.T) *Data {
	t.Helper()
	d, err := parseData(sampleJSON)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

var ws = regexp.MustCompile(`\s+`)

func norm(s string) string { return strings.TrimSpace(ws.ReplaceAllString(s, " ")) }

var ariaRe = regexp.MustCompile(`aria-label="[^"]*"`)

// noAria blanks aria-labels: the original generator hardcoded them, this app takes them from the data.
func noAria(s string) string { return ariaRe.ReplaceAllString(s, `aria-label=""`) }

// TestMatchesPython is opt-in: set OFFICEMAP_REAL_DIR to a directory holding network.json,
// build.py and topo.py (the original generator). It runs the generator in a temp copy and
// diffs every output against this renderer.
func TestMatchesPython(t *testing.T) {
	src := os.Getenv("OFFICEMAP_REAL_DIR")
	if src == "" {
		t.Skip("OFFICEMAP_REAL_DIR not set")
	}
	tmp := t.TempDir()
	for _, f := range []string{"topo.py", "build.py"} {
		copyFile(t, filepath.Join(src, f), filepath.Join(tmp, f))
	}
	for _, f := range []string{"topo.py", "build.py"} {
		cmd := exec.Command("python3", f)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("python3 %s: %v\n%s", f, err, out)
		}
	}
	read := func(n string) string {
		b, err := os.ReadFile(filepath.Join(tmp, n))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	raw, err := os.ReadFile(filepath.Join(src, "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseData(raw)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := noAria(TopoSVG(d)), noAria(read("topo.svg")); got != want {
		t.Errorf("topo.svg differs:\n%s", firstDiff(got, want))
	}
	if got, want := noAria(SwitchSVG(d)), noAria(read("switch.svg")); got != want {
		t.Errorf("switch.svg differs:\n%s", firstDiff(got, want))
	}
	if got, want := stripLinks(SwitchSVGLinks(d)), SwitchSVG(d); got != want {
		t.Errorf("linked switch svg differs from plain once links are stripped:\n%s", firstDiff(got, want))
	}
	got, want := noAria(Body(d, Opts{})), noAria(read("office_network_map.html"))
	if norm(got) != norm(want) {
		t.Errorf("office_network_map.html differs:\n%s", firstDiff(norm(got), norm(want)))
	}
	if g, w := noAria(PrintHTML(d)), noAria(read("print.html")); norm(g) != norm(w) {
		t.Errorf("print.html differs:\n%s", firstDiff(norm(g), norm(w)))
	}
	// Edit chrome must not leak into the plain render.
	if strings.Contains(got, `class="ed`) || strings.Contains(got, "/edit/port/") {
		t.Error("plain render contains edit links")
	}
}

var linkRe = regexp.MustCompile(`(?:<a href="/edit/port/[^"]*" class="pl"><title>[^<]*</title>|</a>)\n?`)

func stripLinks(s string) string { return linkRe.ReplaceAllString(s, "") }

func TestSampleRenders(t *testing.T) {
	d := loadSeed(t)
	if errs := d.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	plain := SwitchSVG(d)
	linked := SwitchSVGLinks(d)
	body := Body(d, Opts{Edit: true})
	for n := 1; n <= 48; n++ {
		if !strings.Contains(plain, fmt.Sprintf(">%d</text>", n)) {
			t.Errorf("port %d number missing from switch svg", n)
		}
		if !strings.Contains(linked, fmt.Sprintf(`href="/edit/port/%d"`, n)) {
			t.Errorf("port %d link missing", n)
		}
	}
	for _, k := range []string{"te1", "te2"} {
		if !strings.Contains(linked, `href="/edit/port/`+k+`"`) {
			t.Errorf("%s link missing", k)
		}
	}
	if strings.Contains(plain, "<a ") {
		t.Error("plain switch svg must have no links")
	}
	if stripLinks(linked) != plain {
		t.Errorf("links changed the drawing:\n%s", firstDiff(stripLinks(linked), plain))
	}
	if n := strings.Count(body, "<svg "); n != 2 {
		t.Errorf("expected 2 svgs in page, got %d", n)
	}
	if !strings.Contains(body, `aria-label="`+d.Switch.Title+`"`) || !strings.Contains(body, `aria-label="`+d.Topology.Title+`"`) {
		t.Error("aria-labels should come from the data")
	}
	if !strings.Contains(Body(d, Opts{}), "<svg ") || strings.Contains(Body(d, Opts{}), "/edit/port/") {
		t.Error("plain body wrong")
	}
	if PrintHTML(d) == "" || TopoSVG(d) == "" {
		t.Error("empty render")
	}
}

// The embedded sample must be in the app's canonical on-disk form so a first save is a small diff.
func TestSampleCanonical(t *testing.T) {
	d := loadSeed(t)
	if string(d.marshal()) != string(sampleJSON) {
		t.Error("sample/network.json is not canonical; regenerate with the app's marshal")
	}
}

func firstDiff(a, b string) string {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	lo := max(0, i-80)
	return "got:  ..." + a[lo:min(len(a), i+120)] + "\nwant: ..." + b[lo:min(len(b), i+120)]
}

func TestValidateRejects(t *testing.T) {
	d := loadSeed(t)
	d.Switch.Ports[0].Role = "nope"
	d.Devices.Rows[0][0] = "<script>x</script>"
	d.Switch.Callouts[0].Port = 99
	if errs := d.Validate(); len(errs) < 3 {
		t.Fatalf("expected >=3 errors, got %v", errs)
	}
}

func TestEscape(t *testing.T) {
	if got := esc(`a&b<c>"d'e`); got != `a&amp;b&lt;c&gt;&quot;d&#x27;e` {
		t.Fatal(got)
	}
}
