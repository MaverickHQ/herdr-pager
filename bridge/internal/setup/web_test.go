package setup

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The page's markup, read as the test reads it: its tags, each tag's
// attributes, and what's between a <script> and its </script>.
var (
	tagRE       = regexp.MustCompile(`(?s)<([a-zA-Z][a-zA-Z0-9-]*)((?:\s+[^\s=>/]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'>]+))?)*)\s*/?>`)
	attrRE      = regexp.MustCompile(`([^\s=>/]+)(?:\s*=\s*("[^"]*"|'[^']*'|[^\s"'>]+))?`)
	scriptRE    = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script\s*>`)
	cssURLRE    = regexp.MustCompile(`url\(\s*['"]?([^'")]+)['"]?\s*\)`)
	cssImportRE = regexp.MustCompile(`@import`)
	// A module's imports: static (`from "./x.js"`, `import "./x.js"`) and
	// dynamic (`import("./x.js")`), as the vendored chunks write them.
	jsImportRE = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s*)["'](\.{1,2}/[^"']+)["']`)
	// The page's routes its script names, as string literals.
	jsRouteRE = regexp.MustCompile(`["'` + "`" + `](/(?:api/[a-z/]+|events|firmware/[a-z.]+))["'` + "`" + `]`)
	// The page's own files its script names (the pager's screens).
	jsAssetRE = regexp.MustCompile(`["'` + "`" + `]([A-Za-z0-9_./-]+\.(?:png|svg|ttf|css|js))["'` + "`" + `]`)
	// What a script of ours must never do: run text as code, or put text
	// into the page as markup (requirement 2.5: log lines only as text).
	jsForbidden = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\b|\bFunction\s*\(|\.innerHTML\b|\.outerHTML\b|insertAdjacentHTML|createContextualFragment|document\.write|DOMParser|setAttribute\(\s*["'](?:on|style)|\bsetTimeout\s*\(\s*["'` + "`" + `]|\bsetInterval\s*\(\s*["'` + "`" + `]`)
)

func attrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range attrRE.FindAllStringSubmatch(s, -1) {
		m[strings.ToLower(a[1])] = strings.Trim(a[2], `"'`)
	}
	return m
}

// local is ref as a path in the page's files, from the file at from; ok is
// false for a reference to elsewhere (another origin, a data: URL, a
// fragment) or to one of the page's routes rather than a file.
func local(from, ref string) (string, bool) {
	switch {
	case ref == "", strings.HasPrefix(ref, "#"), strings.HasPrefix(ref, "data:"):
		return "", false
	case strings.Contains(ref, "://"), strings.HasPrefix(ref, "//"), strings.HasPrefix(ref, "mailto:"):
		return "", false
	case strings.HasPrefix(ref, "/api/"), strings.HasPrefix(ref, "/firmware/"), ref == "/events":
		return "", false
	}
	ref, _, _ = strings.Cut(ref, "?")
	ref, _, _ = strings.Cut(ref, "#")
	if strings.HasPrefix(ref, "/") {
		return strings.TrimPrefix(path.Clean(ref), "/"), true
	}
	return path.Join(path.Dir(from), ref), true
}

// TestThePageHasNoInlineScript checks the page's own files (requirement
// 2.5; design §The setup page, Headers and Assets):
//   - no inline script or style, no event-handler attribute, no
//     javascript: URL, so the CSP needs no 'unsafe-inline';
//   - nothing of ours that runs text as code or writes it as markup: every
//     line the page shows goes in as text;
//   - nothing of ours from another origin;
//   - every file the page references, its scripts' imports and its styles'
//     urls included, all the way through the vendored ESP Web Tools, is
//     embedded, and served with its type and the page's headers;
//   - every route its script names is one the page serves.
func TestThePageHasNoInlineScript(t *testing.T) {
	files, err := fs.Sub(webFiles, "web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(files, "index.html"); err != nil {
		t.Fatalf("no index.html in the page's files: %v", err)
	}

	referenced := map[string]string{} // each file referenced, and by which
	routes := map[string]string{}     // each route our scripts name, and where
	queue := []string{"index.html"}
	seen := map[string]bool{}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		data, err := fs.ReadFile(files, name)
		if err != nil {
			t.Errorf("%s isn't embedded (%s references it): %v", name, referenced[name], err)
			continue
		}
		text := string(data)
		ours := !strings.HasPrefix(name, "esp-web-tools/")
		refer := func(ref string) {
			if p, ok := local(name, ref); ok {
				if _, done := referenced[p]; !done {
					referenced[p] = name
				}
				queue = append(queue, p)
			} else if ours && !strings.HasPrefix(ref, "data:") && (strings.Contains(ref, "://") || strings.HasPrefix(ref, "//")) {
				t.Errorf("%s loads %s from another origin", name, ref)
			}
		}
		switch path.Ext(name) {
		case ".html":
			for _, m := range scriptRE.FindAllStringSubmatch(text, -1) {
				if strings.TrimSpace(m[1]) != "" {
					t.Errorf("%s: an inline script: %.60q", name, strings.TrimSpace(m[1]))
				}
			}
			for _, m := range tagRE.FindAllStringSubmatch(text, -1) {
				tag, a := strings.ToLower(m[1]), attrs(m[2])
				if tag == "style" {
					t.Errorf("%s: a <style> element", name)
				}
				if tag == "script" && a["src"] == "" {
					t.Errorf("%s: a <script> with no src", name)
				}
				for k, v := range a {
					switch {
					case k == "style":
						t.Errorf("%s: <%s style=%.40q>, an inline style", name, tag, v)
					case strings.HasPrefix(k, "on"):
						t.Errorf("%s: <%s %s=…>, an inline event handler", name, tag, k)
					case strings.Contains(strings.ToLower(v), "javascript:"):
						t.Errorf("%s: <%s %s=%q>", name, tag, k, v)
					}
				}
				for _, k := range []string{"src", "href", "manifest"} {
					if v, ok := a[k]; ok {
						if k == "manifest" {
							routes[v] = name
							continue
						}
						refer(v)
					}
				}
			}
		case ".css":
			if cssImportRE.MatchString(text) {
				t.Errorf("%s: an @import", name)
			}
			for _, m := range cssURLRE.FindAllStringSubmatch(text, -1) {
				refer(m[1])
			}
		case ".js", ".mjs":
			for _, m := range jsImportRE.FindAllStringSubmatch(text, -1) {
				refer(m[1])
			}
			if ours {
				if m := jsForbidden.FindString(text); m != "" {
					t.Errorf("%s: %q: build the page's elements with textContent, never from text as markup or code", name, m)
				}
				for _, m := range jsRouteRE.FindAllStringSubmatch(text, -1) {
					routes[m[1]] = name
				}
				for _, m := range jsAssetRE.FindAllStringSubmatch(text, -1) {
					refer(m[1])
				}
			}
		}
	}
	// The walk reached ESP Web Tools' dialog (a dynamic import), its ESP32-S3
	// stub, the page's style, its font and the pager's screens.
	for _, want := range []string{"/install-button.js", "/install-dialog-", "/stub_flasher_32s3-", "setup.js", "setup.css", ".ttf", "assets/screens/boot_unpaired.png"} {
		found := false
		for name := range seen {
			found = found || strings.Contains(name, want) || strings.HasSuffix(name, want)
		}
		if !found {
			t.Errorf("the page never loads a file %q: it reached %d files", want, len(seen))
		}
	}
	loadsESPWebTools := false
	for name := range seen {
		loadsESPWebTools = loadsESPWebTools || (strings.HasPrefix(name, "esp-web-tools/") && strings.HasSuffix(name, "/install-button.js"))
	}
	if !loadsESPWebTools {
		t.Error("the page doesn't load the vendored ESP Web Tools' install-button.js")
	}

	// Every file the page loads is served, byte for byte, with its type and
	// the page's headers.
	g := newGuarded(t)
	wantType := map[string]string{".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".png": "image/png", ".ttf": "font/ttf", ".svg": "image/svg+xml"}
	for name := range seen {
		url := "/" + name
		if name == "index.html" {
			url = "/"
		}
		resp := g.do(g.request(http.MethodGet, url, nil))
		body, _ := io.ReadAll(resp.Body)
		want, _ := fs.ReadFile(files, name)
		if resp.StatusCode != http.StatusOK || string(body) != string(want) {
			t.Errorf("GET %s: %d, %d bytes; want 200 and the embedded %s", url, resp.StatusCode, len(body), name)
		}
		if w := wantType[path.Ext(name)]; w == "" {
			t.Errorf("GET %s: the test knows no type for %s", url, path.Ext(name))
		} else if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, w) {
			t.Errorf("GET %s: Content-Type %q; want %s", url, ct, w)
		}
		for k, v := range wantHeaders {
			if got := resp.Header.Get(k); got != v {
				t.Errorf("GET %s: %s %q; want %q", url, k, got, v)
			}
		}
	}
	// A folder is no listing of its files.
	for _, dir := range []string{"/esp-web-tools/", "/esp-web-tools/10.4.0/", "/assets/"} {
		if resp := g.do(g.request(http.MethodGet, dir, nil)); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d; want 404, no listing", dir, resp.StatusCode)
		}
	}

	// Every route the page's script names is one the page has.
	h, err := NewHandler(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) == 0 {
		t.Error("the page's script names none of the page's routes")
	}
	names := make([]string, 0, len(routes))
	for r := range routes {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, route := range names {
		served := false
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if _, pattern := h.mux.Handler(httptest.NewRequest(method, route, nil)); pattern != "" && pattern != "GET /" {
				served = true
			}
		}
		if !served {
			t.Errorf("%s names %s, which isn't one of the page's routes", routes[route], route)
		}
	}
}
