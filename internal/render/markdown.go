package render

import (
	"bytes"
	"html/template"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

type markdownRenderer struct{ md goldmark.Markdown }

var markdown Renderer = markdownRenderer{md: goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		highlighting.NewHighlighting(
			highlighting.WithStyle("github"),
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		),
	),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)}

func (m markdownRenderer) Render(src []byte, opt Options) (Doc, error) {
	src = bytes.ToValidUTF8(src, []byte("�"))
	doc := m.md.Parser().Parse(text.NewReader(src))
	title := ""
	var images []string
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			if title == "" && n.Level == 1 {
				title = plainText(n, src)
			}
		case *ast.Image:
			if dest := string(n.Destination); isRelative(dest) {
				images = append(images, dest)
				if opt.AssetPrefix != "" {
					n.Destination = []byte(opt.AssetPrefix + dest)
				}
			}
		}
		return ast.WalkContinue, nil
	})
	var buf bytes.Buffer
	if err := m.md.Renderer().Render(&buf, src, doc); err != nil {
		return Doc{}, err
	}
	return Doc{HTML: template.HTML(buf.String()), Title: title, Images: images}, nil
}

func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if t, ok := c.(*ast.Text); ok {
				b.Write(t.Segment.Value(src))
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func isRelative(u string) bool {
	if u == "" || strings.HasPrefix(u, "/") || strings.HasPrefix(u, "#") {
		return false
	}
	if i := strings.IndexAny(u, ":/?#"); i >= 0 && u[i] == ':' {
		return false // has a scheme (http:, data:, mailto:)
	}
	return true
}
