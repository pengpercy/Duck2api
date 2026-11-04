package duckgo

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// parseHTMLFragment 按 HTML5 规范在 <div> 上下文中解析片段，返回浏览器 innerHTML
// 序列化后的字符串和其中的元素个数（等同 querySelectorAll('*').length）。
//
// 挑战里有一类检测会给 div.innerHTML 赋一段故意写错的标记（如 "<p><div></p><p></div"），
// 再用 序列化长度×元素数 参与计算，只有按 HTML5 纠错规则解析才能得到与浏览器一致的值。
func parseHTMLFragment(src string) (string, int) {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(src), ctx)
	if err != nil {
		return "", 0
	}
	var b strings.Builder
	count := 0
	for _, n := range nodes {
		serializeHTMLNode(&b, n, &count, false)
	}
	return b.String(), count
}

// voidElements 不输出子节点和结束标签（HTML 序列化算法）。
var voidElements = map[string]bool{
	"area": true, "base": true, "basefont": true, "bgsound": true, "br": true, "col": true,
	"embed": true, "frame": true, "hr": true, "img": true, "input": true, "keygen": true,
	"link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true,
}

// rawTextParents 中的文本按原样输出，不做转义。
var rawTextParents = map[string]bool{
	"style": true, "script": true, "xmp": true, "iframe": true, "noembed": true,
	"noframes": true, "plaintext": true, "noscript": true,
}

var htmlTextEscaper = strings.NewReplacer("&", "&amp;", " ", "&nbsp;", "<", "&lt;", ">", "&gt;")
var htmlAttrEscaper = strings.NewReplacer("&", "&amp;", " ", "&nbsp;", `"`, "&quot;")

// inTemplate: template 的内容属于独立的 DocumentFragment，querySelectorAll 不会计入。
func serializeHTMLNode(b *strings.Builder, n *html.Node, count *int, inTemplate bool) {
	switch n.Type {
	case html.ElementNode:
		if !inTemplate {
			*count++
		}
		b.WriteString("<" + n.Data)
		for _, a := range n.Attr {
			name := a.Key
			if a.Namespace != "" {
				name = a.Namespace + ":" + a.Key
			}
			b.WriteString(" " + name + `="` + htmlAttrEscaper.Replace(a.Val) + `"`)
		}
		b.WriteString(">")
		if voidElements[n.Data] && n.Namespace == "" {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			serializeHTMLNode(b, c, count, inTemplate || n.Data == "template")
		}
		b.WriteString("</" + n.Data + ">")
	case html.TextNode:
		if n.Parent != nil && rawTextParents[n.Parent.Data] {
			b.WriteString(n.Data)
		} else {
			b.WriteString(htmlTextEscaper.Replace(n.Data))
		}
	case html.CommentNode:
		b.WriteString("<!--" + n.Data + "-->")
	}
}
