package setup

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The headers every response carries (design §The setup page).
var wantHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'",
	"X-Content-Type-Options":  "nosniff",
	"Referrer-Policy":         "no-referrer",
}

// A guarded is the setup page's handler behind a real listener on
// 127.0.0.1, as httptest serves it, and the port it listens on.
type guarded struct {
	t     *testing.T
	h     *Handler
	srv   *httptest.Server
	port  string
	token string
}

func newGuarded(t *testing.T) *guarded {
	t.Helper()
	return newGuardedWith(t, Options{})
}

// newGuardedWith is newGuarded, with the handler made from opts.
func newGuardedWith(t *testing.T, opts Options) *guarded {
	t.Helper()
	h, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &guarded{t: t, h: h, srv: srv, port: u.Port(), token: h.Token()}
}

// cookie is the cookie the page's first request sets, as the browser sends
// it back.
func (g *guarded) cookie() *http.Cookie {
	return &http.Cookie{Name: "herdr-pager-setup-" + g.port, Value: g.token}
}

// do sends req, following no redirect, and checks that the response carries
// every header in wantHeaders, whatever its status.
func (g *guarded) do(req *http.Request) *http.Response {
	g.t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	for k, v := range wantHeaders {
		if got := resp.Header.Get(k); got != v {
			g.t.Errorf("%s %s (%d): %s %q; want %q", req.Method, req.URL, resp.StatusCode, k, got, v)
		}
	}
	if strings.Contains(string(body), g.token) {
		g.t.Errorf("%s %s (%d): the body holds the token", req.Method, req.URL, resp.StatusCode)
	}
	return resp
}

// request is a request for path, as the page's own tab sends it: the
// cookie, Host 127.0.0.1:<port>, and edit's changes.
func (g *guarded) request(method, path string, edit func(*http.Request)) *http.Request {
	g.t.Helper()
	req, err := http.NewRequest(method, g.srv.URL+path, nil)
	if err != nil {
		g.t.Fatal(err)
	}
	req.AddCookie(g.cookie())
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", "http://127.0.0.1:"+g.port)
	}
	if edit != nil {
		edit(req)
	}
	return req
}

func refused(t *testing.T, resp *http.Response, what string) {
	t.Helper()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("%s: status %d; want %d, refused", what, resp.StatusCode, http.StatusForbidden)
	}
}

func allowed(t *testing.T, resp *http.Response, what string) {
	t.Helper()
	if resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("%s: refused (%s); want it through the guard", what, strings.TrimSpace(string(body)))
	}
}

// TestSetupPageGuard is S5P-4's guard (requirement 2.5, design §The setup
// page, threat T18): the page's token on every request, the first as ?t=
// and then as the cookie that first response sets; its Host
// 127.0.0.1:<port> or localhost:<port>; no cross-origin Origin; the
// headers on every response; and its listener on 127.0.0.1.
func TestSetupPageGuard(t *testing.T) {
	t.Run("the token: 32 bytes from crypto/rand, new each time", func(t *testing.T) {
		g1, g2 := newGuarded(t), newGuarded(t)
		raw, err := base64.RawURLEncoding.DecodeString(g1.token)
		if err != nil || len(raw) != 32 {
			t.Errorf("token %q: %d bytes (%v); want 32, base64url", g1.token, len(raw), err)
		}
		if g1.token == g2.token {
			t.Errorf("two handlers share the token %q", g1.token)
		}
	})

	t.Run("the first request: ?t= sets the cookie and leaves the URL", func(t *testing.T) {
		g := newGuarded(t)
		req, _ := http.NewRequest(http.MethodGet, g.srv.URL+"/?t="+g.token, nil)
		resp := g.do(req)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
			t.Errorf("status %d, Location %q; want %d to /", resp.StatusCode, resp.Header.Get("Location"), http.StatusSeeOther)
		}
		var got *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == "herdr-pager-setup-"+g.port {
				got = c
			}
		}
		switch {
		case got == nil:
			t.Fatalf("no cookie herdr-pager-setup-%s in %q", g.port, resp.Header.Values("Set-Cookie"))
		case got.Value != g.token || !got.HttpOnly || got.SameSite != http.SameSiteStrictMode || got.Path != "/":
			t.Errorf("cookie %q; want the token, HttpOnly, SameSite=Strict, Path=/", resp.Header.Get("Set-Cookie"))
		case got.MaxAge != 0 || !got.Expires.IsZero():
			t.Errorf("cookie %q outlives the browser's session", resp.Header.Get("Set-Cookie"))
		}
		// And with it, the page's requests pass.
		allowed(t, g.do(g.request(http.MethodGet, "/", nil)), "GET / with the cookie")
		allowed(t, g.do(g.request(http.MethodPost, "/api/anything", nil)), "POST with the cookie and the page's Origin")
		allowed(t, g.do(g.request(http.MethodGet, "/", func(r *http.Request) { r.Host = "localhost:" + g.port })),
			"GET with Host localhost:<port>")
	})

	t.Run("no token", func(t *testing.T) {
		g := newGuarded(t)
		for _, path := range []string{"/", "/api/anything", "/firmware/manifest.json", "/nowhere"} {
			req, _ := http.NewRequest(http.MethodGet, g.srv.URL+path, nil)
			refused(t, g.do(req), "GET "+path+" with no token")
		}
		req, _ := http.NewRequest(http.MethodPost, g.srv.URL+"/api/anything", nil)
		req.Header.Set("Origin", "http://127.0.0.1:"+g.port)
		refused(t, g.do(req), "POST with no token")
	})

	t.Run("a wrong token", func(t *testing.T) {
		g := newGuarded(t)
		wrong := []string{"", "x", g.token[:len(g.token)-1], g.token + "A", strings.ToUpper(g.token), newGuarded(t).token}
		for _, tok := range wrong {
			req, _ := http.NewRequest(http.MethodGet, g.srv.URL+"/?t="+url.QueryEscape(tok), nil)
			resp := g.do(req)
			refused(t, resp, "?t="+tok)
			if len(resp.Cookies()) > 0 {
				t.Errorf("?t=%s: set cookies %q", tok, resp.Header.Values("Set-Cookie"))
			}
			refused(t, g.do(g.request(http.MethodGet, "/", func(r *http.Request) {
				r.Header.Del("Cookie")
				r.AddCookie(&http.Cookie{Name: "herdr-pager-setup-" + g.port, Value: tok})
			})), "a cookie holding "+tok)
		}
		// The right token in another port's cookie: another setup's.
		refused(t, g.do(g.request(http.MethodGet, "/", func(r *http.Request) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: "herdr-pager-setup-1", Value: g.token})
		})), "the token in another port's cookie")
		// A wrong ?t= beside the right cookie.
		refused(t, g.do(g.request(http.MethodGet, "/?t=x", nil)), "a wrong ?t= with the right cookie")
		// ?t= only on a GET: a POST carries the cookie.
		req, _ := http.NewRequest(http.MethodPost, g.srv.URL+"/api/anything?t="+g.token, nil)
		req.Header.Set("Origin", "http://127.0.0.1:"+g.port)
		refused(t, g.do(req), "POST with ?t= and no cookie")
	})

	t.Run("a Host other than 127.0.0.1:<port> or localhost:<port>", func(t *testing.T) {
		g := newGuarded(t)
		hosts := []string{
			"evil.example", "evil.example:" + g.port, "127.0.0.1", "localhost", "127.0.0.1:1",
			"localhost:1", "[::1]:" + g.port, "127.0.0.2:" + g.port, "0.0.0.0:" + g.port,
			"127.0.0.1:" + g.port + ".evil.example", "localhost:" + g.port + "x", "127.0.0.1:0" + g.port,
		}
		for _, host := range hosts {
			refused(t, g.do(g.request(http.MethodGet, "/", func(r *http.Request) { r.Host = host })), "Host "+host)
			// The first request too: a rebinding page that learnt the URL can't
			// trade it for the cookie.
			req, _ := http.NewRequest(http.MethodGet, g.srv.URL+"/?t="+g.token, nil)
			req.Host = host
			resp := g.do(req)
			refused(t, resp, "?t= with Host "+host)
			if len(resp.Cookies()) > 0 {
				t.Errorf("?t= with Host %s: set cookies %q", host, resp.Header.Values("Set-Cookie"))
			}
		}
	})

	t.Run("a cross-origin Origin", func(t *testing.T) {
		g := newGuarded(t)
		origins := []string{
			"http://evil.example", "http://127.0.0.1:1", "http://localhost:" + g.port, "https://127.0.0.1:" + g.port,
			"http://127.0.0.1:" + g.port + "/", "http://127.0.0.1", "null", "file://",
		}
		for _, origin := range origins {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
				refused(t, g.do(g.request(method, "/api/anything", func(r *http.Request) { r.Header.Set("Origin", origin) })),
					method+" with Origin "+origin)
			}
		}
		// Two Origins.
		refused(t, g.do(g.request(http.MethodPost, "/api/anything", func(r *http.Request) {
			r.Header.Add("Origin", "http://evil.example")
		})), "POST with two Origins")
		// A request that changes something must say where it's from.
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			refused(t, g.do(g.request(method, "/api/anything", func(r *http.Request) { r.Header.Del("Origin") })), method+" with no Origin")
		}
		// The page's own origin, as each Host names it.
		allowed(t, g.do(g.request(http.MethodGet, "/", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:"+g.port) })),
			"GET with the page's Origin")
		allowed(t, g.do(g.request(http.MethodPost, "/api/anything", func(r *http.Request) {
			r.Host = "localhost:" + g.port
			r.Header.Set("Origin", "http://localhost:"+g.port)
		})), "POST from localhost:<port>")
		// The fetch metadata a browser sends: only its own origin, or the
		// person typing or opening the URL.
		for _, site := range []string{"cross-site", "same-site", "bogus"} {
			refused(t, g.do(g.request(http.MethodGet, "/", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", site) })),
				"Sec-Fetch-Site "+site)
		}
		for _, site := range []string{"same-origin", "none"} {
			allowed(t, g.do(g.request(http.MethodGet, "/", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", site) })),
				"Sec-Fetch-Site "+site)
		}
		req, _ := http.NewRequest(http.MethodGet, g.srv.URL+"/?t="+g.token, nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		refused(t, g.do(req), "?t= from another site")
	})

	t.Run("the listener is on 127.0.0.1", func(t *testing.T) {
		ln, err := Listen()
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		addr, ok := ln.Addr().(*net.TCPAddr)
		if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Port == 0 {
			t.Errorf("listening on %v; want 127.0.0.1 and a port the system chose", ln.Addr())
		}
		// And the handler refuses a connection that didn't come in on
		// 127.0.0.1: one served on every address, by mistake.
		h, err := NewHandler(Options{})
		if err != nil {
			t.Fatal(err)
		}
		any, err := net.Listen("tcp", "[::1]:0")
		if err != nil {
			t.Skip("no IPv6 loopback:", err)
		}
		srv := httptest.NewUnstartedServer(h)
		srv.Listener.Close()
		srv.Listener = any
		srv.Start()
		defer srv.Close()
		_, port, _ := net.SplitHostPort(any.Addr().String())
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/?t="+h.Token(), nil)
		req.Host = "localhost:" + port
		g := &guarded{t: t, h: h, srv: srv, port: port, token: h.Token()}
		refused(t, g.do(req), "a request that came in on ::1")
	})
}
