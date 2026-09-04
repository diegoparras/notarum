// Package htmltexto convierte el HTML ajeno en algo que se pueda mostrar y
// leer: saneado para el navegador, y en texto plano para quien sólo quiere las
// palabras.
//
// Lo usan los dos orígenes que notarum lee. El Boletín Oficial entrega los
// avisos con <html> y <body> anidados y <style> en línea; InfoLEG entrega las
// normas en ISO-8859-1 y con maquetación de los noventa. En los dos casos hace
// falta lo mismo, así que vive acá y no duplicado.
package htmltexto

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// politica deja pasar sólo lo que hace falta para leer un texto legal: los
// párrafos, el énfasis y las tablas. Nada de estilos, scripts ni atributos.
//
// Las tablas importan de verdad: en un decreto de designaciones o en un anexo
// de aranceles, la tabla es el contenido.
var politica = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"p", "br", "b", "strong", "i", "em", "u", "sup", "sub", "span", "div",
		"ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre",
		"table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption", "col", "colgroup",
	)
	p.AllowAttrs("colspan", "rowspan").OnElements("td", "th")
	// Se descartan con su contenido: si no, el CSS aparece como texto.
	p.SkipElementsContent("style", "script", "head", "title")
	return p
}()

var (
	// El tabulador queda afuera a propósito: es lo que separa las celdas de
	// una tabla en el texto plano, y colapsarlo perdería las columnas.
	reEspacios   = regexp.MustCompile(`[ \x{00a0}]+`)
	reLineasMult = regexp.MustCompile(`\n{3,}`)
)

// Sanear devuelve el HTML sin nada ejecutable ni decorativo.
func Sanear(crudo string) string {
	return strings.TrimSpace(politica.Sanitize(crudo))
}

// APlano convierte HTML en párrafos separados por una línea en blanco,
// conservando el orden de lectura y marcando las celdas con tabulaciones.
func APlano(fuente string) string {
	doc, err := html.Parse(strings.NewReader(fuente))
	if err != nil {
		return ""
	}
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			sb.WriteString(n.Data)
		case n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style):
			return
		case n.Type == html.ElementNode && n.DataAtom == atom.Br:
			sb.WriteString("\n")
		case n.Type == html.ElementNode && esBloque(n.DataAtom):
			sb.WriteString("\n\n")
		case n.Type == html.ElementNode && (n.DataAtom == atom.Td || n.DataAtom == atom.Th):
			sb.WriteString("\t")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
		if n.Type == html.ElementNode && (esBloque(n.DataAtom) || n.DataAtom == atom.Tr) {
			sb.WriteString("\n")
		}
	}
	rec(doc)

	var lineas []string
	for _, l := range strings.Split(sb.String(), "\n") {
		lineas = append(lineas, strings.TrimSpace(reEspacios.ReplaceAllString(
			strings.ReplaceAll(l, " ", " "), " ")))
	}
	return strings.TrimSpace(reLineasMult.ReplaceAllString(strings.Join(lineas, "\n"), "\n\n"))
}

func esBloque(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Li, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5,
		atom.H6, atom.Blockquote, atom.Pre, atom.Table, atom.Caption:
		return true
	}
	return false
}

// DesdeLatin1 convierte bytes en ISO-8859-1 o windows-1252 a texto.
//
// InfoLEG declara ISO-8859-1 y en la práctica no usa el tramo 0x80-0x9F, pero
// se decodifica como windows-1252 porque es lo que suele salir de un Windows y
// sólo difiere ahí: donde latin-1 pone controles inútiles, cp1252 pone las
// comillas tipográficas y el guion largo, que sí aparecen en textos legales.
func DesdeLatin1(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/8)
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c >= 0xa0:
			sb.WriteRune(rune(c)) // latin-1 y cp1252 coinciden acá
		default:
			if r := cp1252[c-0x80]; r != 0 {
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
}

// cp1252 es el tramo 0x80-0x9F, donde windows-1252 se aparta de ISO-8859-1.
// El cero significa que la posición no está definida y se descarta.
var cp1252 = [32]rune{
	'€', 0, '‚', 'ƒ', '„', '…', '†', '‡',
	'ˆ', '‰', 'Š', '‹', 'Œ', 0, 'Ž', 0,
	0, '‘', '’', '“', '”', '•', '–', '—',
	'˜', '™', 'š', '›', 'œ', 0, 'ž', 'Ÿ',
}
