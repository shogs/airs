package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/state"
	"airs/internal/watch"
)

type fixture struct {
	h     http.Handler
	store *state.Store
	ns    *NetState
	dir   string
	hub   *watch.Hub
}

// newFixture serves dir/notes.md (with img.png, secret.txt beside it) and the
// folder dir/docs.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes\n\n![](img.png)\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "img.png"), []byte("\x89PNG"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("s"), 0o644)
	docs := filepath.Join(dir, "docs")
	mkfile(t, filepath.Join(docs, "a.md"))
	mkfile(t, filepath.Join(docs, "sub", "b.md"))
	mkfile(t, filepath.Join(docs, "My Notes", "c.md"))
	mkfile(t, filepath.Join(docs, ".git", "config"))
	os.WriteFile(filepath.Join(docs, "pic.png"), []byte("\x89PNG"), 0o644)
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.Add(filepath.Join(dir, "notes.md"), false, state.Flags{Project: "p"})
	store.Add(docs, true, state.Flags{Project: "p"})
	hub, _ := watch.New(0)
	t.Cleanup(func() { hub.Close() })
	ns := &NetState{}
	ns.Set(netinfo.Info{LocalIP: "192.168.1.20"}, true)
	return &fixture{h: NewHTTP(store, config.Config{Port: 7777, Notes: true}, ns, hub), store: store, ns: ns, dir: dir, hub: hub}
}

const remote = "192.168.1.50:5000"

// asset is the URL of image rel of the shared notes.md.
func (f *fixture) asset(rel string) string {
	it, ok := f.store.ByLink("p", "notes.md")
	if !ok {
		panic("no notes.md")
	}
	return "/_airs/asset/" + it.Key() + "/" + rel
}

func (f *fixture) get(t *testing.T, path, from string) (int, http.Header, string) {
	t.Helper()
	return f.getVia(t, path, from, "192.168.1.20:7777", "192.168.1.20:7777")
}

// getVia sends a request that arrived on local address `via` with Host `host`.
func (f *fixture) getVia(t *testing.T, path, from, via, host string) (int, http.Header, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = from
	req.Host = host
	if la, err := net.ResolveTCPAddr("tcp", via); err == nil {
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, la))
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, rec.Header(), string(b)
}

func TestFileRender(t *testing.T) {
	f := newFixture(t)
	code, _, body := f.get(t, "/p/notes.md", remote)
	if code != 200 || !strings.Contains(body, "<h1") || !strings.Contains(body, "airs-doc") {
		t.Fatalf("%d %s", code, body)
	}
	if !strings.Contains(body, `src="`+f.asset("img.png")+`"`) {
		t.Fatalf("asset prefix missing: %s", body)
	}
	if code, _, _ := f.get(t, f.asset("img.png"), remote); code != 200 {
		t.Fatalf("asset %d", code)
	}
}

func TestSlugWithSpaces(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.dir, "My Notes.md")
	os.WriteFile(p, []byte("# x\n"), 0o644)
	f.store.Add(p, false, state.Flags{Project: "p"})
	if code, _, body := f.get(t, "/p/My%20Notes.md", remote); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
}

func TestDirListing(t *testing.T) {
	f := newFixture(t)
	code, h, _ := f.get(t, "/p/docs", remote)
	if code != 301 || h.Get("Location") != "/p/docs/" {
		t.Fatalf("%d %v", code, h)
	}
	code, _, body := f.get(t, "/p/docs/", remote)
	if code != 200 {
		t.Fatalf("%d", code)
	}
	iSub, iMy, iA := strings.Index(body, `href="sub/"`), strings.Index(body, `href="My%20Notes/"`), strings.Index(body, `href="a.md"`)
	if iSub < 0 || iMy < 0 || iA < 0 || iA < iSub || iA < iMy {
		t.Fatalf("listing order/links wrong: %s", body)
	}
	if strings.Contains(body, ".git") || strings.Contains(body, "pic.png") {
		t.Fatalf("hidden or non-markdown listed: %s", body)
	}
	if code, _, body := f.get(t, "/p/docs/sub/b.md", remote); code != 200 || !strings.Contains(body, "<h1") {
		t.Fatalf("nested page %d", code)
	}
	if code, _, _ := f.get(t, "/p/docs/pic.png", remote); code != 200 {
		t.Fatalf("raw file %d", code)
	}
	if code, h, _ := f.get(t, "/p/docs/sub", remote); code != 301 || h.Get("Location") != "/p/docs/sub/" {
		t.Fatalf("subdir redirect %d %v", code, h)
	}
}

func TestTraversalHTTP(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/p/docs/..%2f..%2fetc%2fpasswd", "/p/docs/%2e%2e/notes.md", f.asset("secret.txt"), "/p/notes.md/x", "/p/docs/.git/config"} {
		if code, _, _ := f.get(t, p, remote); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestUntrustedRemoteForbidden(t *testing.T) {
	f := newFixture(t)
	f.ns.Set(netinfo.Info{LocalIP: "192.168.1.20"}, false)
	if code, _, _ := f.get(t, "/p/notes.md", remote); code != 403 {
		t.Fatalf("remote %d", code)
	}
	if code, _, _ := f.get(t, f.asset("img.png"), remote); code != 403 {
		t.Fatalf("asset remote %d", code)
	}
	if code, _, _ := f.get(t, "/p/notes.md", "127.0.0.1:5000"); code != 200 {
		t.Fatalf("loopback %d", code)
	}
	_, _, idx := f.get(t, "/", remote)
	if strings.Contains(idx, "notes.md") {
		t.Fatalf("index lists blocked item: %s", idx)
	}
	f.store.Add(filepath.Join(f.dir, "notes.md"), false, state.Flags{AnyNetwork: true})
	if code, _, _ := f.get(t, "/p/notes.md", remote); code != 200 {
		t.Fatalf("any-network item %d", code)
	}
}

func TestIndex(t *testing.T) {
	f := newFixture(t)
	code, _, body := f.get(t, "/", remote)
	if code != 200 || !strings.Contains(body, `href="/p/notes.md"`) || !strings.Contains(body, `href="/p/docs/"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestUnknownName(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.get(t, "/p/nope.md", remote); code != 404 {
		t.Fatalf("%d", code)
	}
}

func TestFileMissingOnDisk(t *testing.T) {
	f := newFixture(t)
	os.Remove(filepath.Join(f.dir, "notes.md"))
	if code, _, _ := f.get(t, "/p/notes.md", remote); code != 404 {
		t.Fatalf("%d", code)
	}
}

func TestItemURL(t *testing.T) {
	base := BaseURL(netinfo.Info{LocalIP: "10.0.0.2"}, 7777)
	if base != "http://10.0.0.2:7777" || BaseURL(netinfo.Info{}, 7777) != "http://localhost:7777" {
		t.Fatal(base)
	}
	if u := ItemURL(base, state.Item{Project: "my proj", Slug: "a b.md"}); u != "http://10.0.0.2:7777/my%20proj/a%20b.md" {
		t.Fatal(u)
	}
	if u := ItemURL(base, state.Item{Project: "p#1", Slug: "docs", IsDir: true}); u != "http://10.0.0.2:7777/p%231/docs/" {
		t.Fatal(u)
	}
}

func TestStaticAppJS(t *testing.T) {
	f := newFixture(t)
	code, h, body := f.get(t, "/_airs/static/app.js", remote)
	if code != 200 || !strings.Contains(h.Get("Content-Type"), "javascript") || !strings.Contains(body, "No longer shared — this is the last version") {
		t.Fatalf("%d %s", code, h.Get("Content-Type"))
	}
}

func TestQRRoute(t *testing.T) {
	f := newFixture(t)
	code, h, body := f.get(t, "/_airs/qr?u=/notes.md", remote)
	if code != 200 || h.Get("Content-Type") != "image/png" || !strings.HasPrefix(body, "\x89PNG") || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", code, h)
	}
	for _, u := range []string{"https://evil.com", "//evil.com/x", ""} {
		if code, _, _ := f.get(t, "/_airs/qr?u="+u, remote); code != 400 {
			t.Errorf("%q: %d", u, code)
		}
	}
}

// A shared single file exposes only the images it references, not every
// image under its folder.
func TestAssetOnlyReferencedImages(t *testing.T) {
	f := newFixture(t)
	mkfile(t, filepath.Join(f.dir, "images", "a.png"))
	mkfile(t, filepath.Join(f.dir, "Private", "deep", "passport.jpg"))
	mkfile(t, filepath.Join(f.dir, "other.png"))
	os.WriteFile(filepath.Join(f.dir, "notes.md"), []byte("# N\n\n![](img.png)\n\n![a](images/a.png)\n"), 0o644)
	for p, want := range map[string]int{
		f.asset("img.png"):                   200,
		f.asset("images/a.png"):              200,
		f.asset("Private/deep/passport.jpg"): 404,
		f.asset("other.png"):                 404,
	} {
		if code, _, _ := f.get(t, p, remote); code != want {
			t.Errorf("%s: %d want %d", p, code, want)
		}
	}
}

// Trust applies to the trusted network only, not to other interfaces such as
// a VPN or Tailscale.
func TestTrustOnlyOnTrustedInterface(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.getVia(t, "/p/notes.md", "100.64.0.9:5000", "100.64.0.5:7777", "100.64.0.5:7777"); code != 403 {
		t.Fatalf("other interface: %d", code)
	}
	if code, _, _ := f.getVia(t, "/p/notes.md", remote, "192.168.1.20:7777", "192.168.1.20:7777"); code != 200 {
		t.Fatalf("trusted interface: %d", code)
	}
	_, _, idx := f.getVia(t, "/", "100.64.0.9:5000", "100.64.0.5:7777", "100.64.0.5:7777")
	if strings.Contains(idx, "notes.md") {
		t.Fatal("index lists items on another interface")
	}
}

// Opening the printed LAN link on the Mac itself works on untrusted networks.
func TestOwnAddressAllowedWhenUntrusted(t *testing.T) {
	own := ""
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			own = n.IP.String()
		}
	}
	if own == "" {
		t.Skip("no non-loopback IPv4 address")
	}
	f := newFixture(t)
	f.ns.Set(netinfo.Info{LocalIP: own}, false)
	if code, _, _ := f.getVia(t, "/p/notes.md", own+":5000", own+":7777", own+":7777"); code != 200 {
		t.Fatalf("%d", code)
	}
}

// DNS rebinding: a web page cannot reach airs through its own domain name.
func TestRejectsForeignHost(t *testing.T) {
	f := newFixture(t)
	for host, want := range map[string]int{
		"evil.example:7777": 403, "evil.example": 403,
		"localhost:7777": 200, "127.0.0.1:7777": 200, "mymac.local:7777": 200, "[::1]:7777": 200,
	} {
		if code, _, _ := f.getVia(t, "/p/notes.md", "127.0.0.1:5000", "127.0.0.1:7777", host); code != want {
			t.Errorf("host %s: %d want %d", host, code, want)
		}
	}
}

func TestProjectWithSpecialCharacters(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.dir, "x.md")
	os.WriteFile(p, []byte("# x\n"), 0o644)
	it, _, _ := f.store.Add(p, false, state.Flags{Project: "my proj#1 日本"})
	if code, _, body := f.get(t, ItemPath(it), remote); code != 200 {
		t.Fatalf("%s: %d %s", ItemPath(it), code, body)
	}
}

func TestOldLinkNotFound(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/notes.md", "/docs/", "/docs/a.md"} {
		if code, _, _ := f.get(t, p, remote); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestAirsProjectSegmentNotFound(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/_airs/nothing", "/_airs/x/y", "/_airs/"} {
		if code, _, _ := f.get(t, p, remote); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	for _, p := range []string{"/_airs/static/app.js", "/_airs/qr?u=/p/notes.md"} {
		if code, _, _ := f.get(t, p, remote); code != 200 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestAssetUnknownKey(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.get(t, "/_airs/asset/nope-00000000/img.png", remote); code != 404 {
		t.Fatalf("%d", code)
	}
}

func TestIndexGroupsByProject(t *testing.T) {
	f := newFixture(t)
	q := filepath.Join(f.dir, "q.md")
	os.WriteFile(q, []byte("# q\n"), 0o644)
	f.store.Add(q, false, state.Flags{Project: "another"})
	_, _, body := f.get(t, "/", remote)
	ia, ip := strings.Index(body, ">another<"), strings.Index(body, ">p<")
	iq, in := strings.Index(body, `href="/another/q.md"`), strings.Index(body, `href="/p/notes.md"`)
	if ia < 0 || ip < 0 || iq < 0 || in < 0 || !(ia < iq && iq < ip && ip < in) {
		t.Fatalf("%d %d %d %d\n%s", ia, iq, ip, in, body)
	}
}
