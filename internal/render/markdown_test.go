package render

import (
	"strings"
	"testing"
)

func md(t *testing.T, src string, opt Options) Doc {
	t.Helper()
	r, ok := ForPath("x.md")
	if !ok {
		t.Fatal("no renderer for .md")
	}
	d, err := r.Render([]byte(src), opt)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func has(t *testing.T, d Doc, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(string(d.HTML), s) {
			t.Errorf("missing %q in:\n%s", s, d.HTML)
		}
	}
}

func TestGFM(t *testing.T) {
	d := md(t, "| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n\n~~s~~\n\nsee https://example.com\n", Options{})
	has(t, d, "<table>", `type="checkbox"`, "checked", "<del>", `<a href="https://example.com"`)
}

func TestHeadingIDs(t *testing.T) {
	d := md(t, "# Hello World\n\ntext", Options{})
	has(t, d, `id="hello-world"`)
	if d.Title != "Hello World" {
		t.Fatalf("title %q", d.Title)
	}
}

func TestFootnote(t *testing.T) {
	has(t, md(t, "a[^1]\n\n[^1]: n\n", Options{}), `class="footnotes"`)
}

func TestHighlight(t *testing.T) {
	has(t, md(t, "```go\nfunc main() {}\n```\n", Options{}), "chroma")
}

func TestRawHTML(t *testing.T) {
	has(t, md(t, "<details>x</details>\n", Options{}), "<details>x</details>")
}

func TestInvalidUTF8(t *testing.T) {
	r, _ := ForPath("x.md")
	if _, err := r.Render([]byte{0xff, 0xfe, '#', ' ', 'a'}, Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestAssetPrefix(t *testing.T) {
	opt := Options{AssetPrefix: "/_airs/asset/notes.md/"}
	d := md(t, "![](img/a.png)\n\n![](https://x/y.png)\n\n![](/abs.png)\n", opt)
	has(t, d, `src="/_airs/asset/notes.md/img/a.png"`, `src="https://x/y.png"`, `src="/abs.png"`)
	has(t, md(t, "![](img/a.png)\n", Options{}), `src="img/a.png"`)
}

func TestForPath(t *testing.T) {
	if _, ok := ForPath("A.MD"); !ok {
		t.Error("A.MD")
	}
	if _, ok := ForPath("x.markdown"); !ok {
		t.Error("x.markdown")
	}
	if _, ok := ForPath("x.txt"); ok {
		t.Error("x.txt")
	}
}

func TestIsImage(t *testing.T) {
	for _, p := range []string{"a.png", "b.JPG", "c.jpeg", "d.gif", "e.svg", "f.webp", "g.avif"} {
		if !IsImage(p) {
			t.Error(p)
		}
	}
	if IsImage("x.txt") || IsImage("x.md") {
		t.Error("non-image")
	}
}

func TestImagesListed(t *testing.T) {
	d := md(t, "![](img/a.png)\n\n![](https://x/y.png)\n\n![](b.png)\n", Options{AssetPrefix: "/p/"})
	if len(d.Images) != 2 || d.Images[0] != "img/a.png" || d.Images[1] != "b.png" {
		t.Fatalf("%v", d.Images)
	}
}
