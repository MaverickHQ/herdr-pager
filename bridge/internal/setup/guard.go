package setup

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// The page's token: 32 bytes from crypto/rand, base64url without padding,
// made fresh by each NewHandler and never stored (design §The setup page).
const tokenBytes = 32

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making the setup page's token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// The headers on every response, a refusal's too (design §The setup page):
// a content security policy that allows nothing inline and nothing from
// elsewhere, no sniffing, and no Referer. The last two keep the page out of
// caches, and keep other origins, another port on 127.0.0.1 among them,
// from loading its responses with <script> or <img> (so a GET's answer
// can't be read across origins, even with the cookie).
var pageHeaders = [][2]string{
	{"Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'"},
	{"X-Content-Type-Options", "nosniff"},
	{"Referrer-Policy", "no-referrer"},
	{"Cache-Control", "no-store"},
	{"Cross-Origin-Resource-Policy", "same-origin"},
}

// cookieName is the token's cookie for the page on port. A browser sends a
// host's cookies to every port, so each setup's cookie is named for its own.
func cookieName(port string) string { return "herdr-pager-setup-" + port }

// guard lets r through, or refuses it with 403 and says why. Every request
// must:
//   - have come in on 127.0.0.1 (the listener's address), naming it, or
//     localhost, with the listener's port as its Host: anything else is
//     another name for the address, as DNS rebinding makes;
//   - carry no Origin but the page's own, http://<its Host>, and, if it
//     changes something (any method but GET and HEAD), carry that Origin;
//   - if the browser sends Sec-Fetch-Site, come from the page itself
//     (same-origin) or from the person (none: typed, opened or bookmarked);
//   - carry the token: the first, a GET or HEAD, as ?t=, which the guard
//     trades for the cookie and a redirect to / (so the token leaves the
//     address bar); every other as that cookie, HttpOnly and SameSite=Strict.
//
// The cookie, not a header of the page's own, carries the token after the
// first request because the browser makes most of the page's requests
// itself: its scripts and styles, the event stream, and ESP Web Tools'
// fetches of its modules, the manifest and the images, none of which can
// add a header.
func (h *Handler) guard(w http.ResponseWriter, r *http.Request) bool {
	port, why := h.check(r)
	if why != "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "The setup page refused this request: %s. Open the address `pager-bridge setup` printed.\n", why)
		return false
	}
	if r.URL.Query().Has("t") { // the first request: the token checked, trade it
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName(port),
			Value:    h.token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return false
	}
	return true
}

// check is guard's verdict on r: the listener's port, or why r is refused.
func (h *Handler) check(r *http.Request) (port, why string) {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok || !local.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return "", "it didn't come in on 127.0.0.1"
	}
	port = strconv.Itoa(local.Port)
	if r.Host != "127.0.0.1:"+port && r.Host != "localhost:"+port {
		return "", "its Host isn't 127.0.0.1:" + port + " or localhost:" + port
	}

	origins := r.Header.Values("Origin")
	switch {
	case len(origins) > 1:
		return "", "it has more than one Origin"
	case len(origins) == 1 && origins[0] != "http://"+r.Host:
		return "", "it comes from another origin"
	case len(origins) == 0 && r.Method != http.MethodGet && r.Method != http.MethodHead:
		return "", "it changes something, and has no Origin"
	}
	if site := r.Header.Values("Sec-Fetch-Site"); len(site) > 1 || (len(site) == 1 && site[0] != "same-origin" && site[0] != "none") {
		return "", "it comes from another site"
	}

	if q := r.URL.Query(); q.Has("t") {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return "", "only a GET carries the token in its URL"
		}
		if !h.isToken(q["t"]...) {
			return "", "its token is wrong"
		}
		return port, ""
	}
	c, err := r.Cookie(cookieName(port))
	if err != nil {
		return "", "it has no token"
	}
	var cookies []string
	for _, c := range r.CookiesNamed(c.Name) {
		cookies = append(cookies, c.Value)
	}
	if !h.isToken(cookies...) {
		return "", "its token is wrong"
	}
	return port, ""
}

// isToken is whether vals is exactly one value, the token.
func (h *Handler) isToken(vals ...string) bool {
	return len(vals) == 1 && subtle.ConstantTimeCompare([]byte(vals[0]), []byte(h.token)) == 1
}
