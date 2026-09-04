package boletin

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/diegoparras/notarum/internal/htmltexto"
)

// BaseSitio es el origen del que se leen las páginas.
const BaseSitio = "https://www.boletinoficial.gob.ar"

var (
	reHrefAviso  = regexp.MustCompile(`^/detalleAviso/([a-z]+)/([A-Za-z0-9._-]+)/(\d{8})`)
	reFechaPie   = regexp.MustCompile(`Fecha de publicaci[^\s]*\s+(\d{2}/\d{2}/\d{4})`)
	reAnexoJS    = regexp.MustCompile(`descargarPDFAnexo\(\s*"([^"]*)"\s*,\s*"([^"]*)"\s*,\s*"([^"]*)"\s*,\s*"([^"]*)"`)
	reEspacios   = regexp.MustCompile(`[ \t\x{00a0}]+`)
	reLineasMult = regexp.MustCompile(`\n{3,}`)
)

// ---------------------------------------------------------------- utilidades

func atributo(n *html.Node, clave string) string {
	for _, a := range n.Attr {
		if a.Key == clave {
			return a.Val
		}
	}
	return ""
}

func tieneClase(n *html.Node, clase string) bool {
	for _, c := range strings.Fields(atributo(n, "class")) {
		if c == clase {
			return true
		}
	}
	return false
}

// limpiar normaliza espacios, incluidos los no separables que trae el sitio.
func limpiar(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(reEspacios.ReplaceAllString(s, " "))
}

func textoDe(n *html.Node) string {
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
			return
		}
		if x.Type == html.ElementNode && (x.DataAtom == atom.Script || x.DataAtom == atom.Style) {
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return limpiar(sb.String())
}

// recorrer visita el árbol en orden de documento.
func recorrer(n *html.Node, visita func(*html.Node)) {
	visita(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		recorrer(c, visita)
	}
}

func buscarPrimero(n *html.Node, pred func(*html.Node) bool) *html.Node {
	var hallado *html.Node
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if hallado != nil {
			return
		}
		if pred(x) {
			hallado = x
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return hallado
}

func porID(id string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		return n.Type == html.ElementNode && atributo(n, "id") == id
	}
}

func renderInterior(n *html.Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&sb, c)
	}
	return sb.String()
}

// ------------------------------------------------------------------ portadas

// ParsearPortada extrae el sumario de una edición. Si la página trae avisos de
// una fecha distinta a la pedida, devuelve error: es un error del sitio, no
// una edición.
func ParsearPortada(cuerpo []byte, sec Seccion, fecha Fecha) (*Edicion, error) {
	avisos, err := extraerAvisos(cuerpo, sec)
	if err != nil {
		return nil, err
	}
	ed := &Edicion{
		Seccion:  sec,
		Fecha:    fecha,
		PorRubro: map[string]int{},
		Avisos:   make([]Aviso, 0, len(avisos)),
	}
	for _, a := range avisos {
		if a.Fecha.API() != fecha.API() {
			return nil, fmt.Errorf("la portada de %s del %s trae el aviso %s fechado el %s",
				sec, fecha.API(), a.ID, a.Fecha.API())
		}
		if a.Suplemento {
			ed.ConSuplemento = true
		}
		ed.PorRubro[a.Rubro]++
		ed.Avisos = append(ed.Avisos, a)
	}
	ed.Cantidad = len(ed.Avisos)
	return ed, nil
}

// extraerAvisos recorre una página de sumario (portada o resultados de
// búsqueda) y arma un aviso por cada enlace que envuelve un div.linea-aviso.
func extraerAvisos(cuerpo []byte, secEsperada Seccion) ([]Aviso, error) {
	doc, err := html.Parse(strings.NewReader(string(cuerpo)))
	if err != nil {
		return nil, fmt.Errorf("no se pudo parsear el HTML: %w", err)
	}

	// Primera pasada: qué avisos tienen anexos. El clip es un enlace aparte,
	// al mismo id, con ?anexos=1.
	conAnexos := map[string]bool{}
	recorrer(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.DataAtom != atom.A {
			return
		}
		href := atributo(n, "href")
		if !strings.Contains(href, "anexos=1") {
			return
		}
		if m := reHrefAviso.FindStringSubmatch(href); m != nil {
			conAnexos[m[2]] = true
		}
	})

	var (
		avisos []Aviso
		rubro  string
	)
	recorrer(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.DataAtom == atom.H5 && tieneClase(n, "seccion-rubro") {
			rubro = limpiar(textoDe(n))
			return
		}
		if n.DataAtom != atom.A {
			return
		}
		linea := buscarPrimero(n, func(x *html.Node) bool {
			return x.Type == html.ElementNode && tieneClase(x, "linea-aviso")
		})
		if linea == nil {
			return // es el clip de anexos o cualquier otro enlace
		}
		href := atributo(n, "href")
		m := reHrefAviso.FindStringSubmatch(href)
		if m == nil {
			return
		}
		sec := Seccion(m[1])
		if secEsperada != "" && sec != secEsperada {
			sec = secEsperada
		}
		fecha, err := ParseFechaSitio(m[3])
		if err != nil {
			return
		}
		a := Aviso{
			ID:          m[2],
			Seccion:     sec,
			Fecha:       fecha,
			Rubro:       rubro,
			TieneAnexos: conAnexos[m[2]],
			Repetido:    esRubroAnterior(rubro),
			Suplemento:  strings.Contains(href, "suplemento=1") || esRubroSuplemento(rubro),
			URL:         BaseSitio + "/detalleAviso/" + string(sec) + "/" + m[2] + "/" + m[3],
		}
		if a.Suplemento {
			a.URL += "?suplemento=1"
		}
		completarDesdeLinea(&a, linea)
		avisos = append(avisos, a)
	})
	return avisos, nil
}

func esRubroAnterior(rubro string) bool {
	u := strings.ToUpper(limpiar(rubro))
	return strings.HasSuffix(u, "- ANTERIOR") || strings.HasSuffix(u, "(ANTERIOR)")
}

func esRubroSuplemento(rubro string) bool {
	return strings.Contains(strings.ToUpper(rubro), "(SUPLEMENTO)")
}

// completarDesdeLinea llena organismo, norma, referencia y síntesis a partir
// de los párrafos del bloque. La primera sección trae los tres; la segunda,
// sólo el organismo.
func completarDesdeLinea(a *Aviso, linea *html.Node) {
	var detalles []string
	recorrer(linea, func(n *html.Node) {
		if n.Type != html.ElementNode || n.DataAtom != atom.P {
			return
		}
		txt := textoDe(n)
		if txt == "" {
			return
		}
		switch {
		case tieneClase(n, "item"):
			if a.Organismo == "" {
				a.Organismo = txt
			}
		case tieneClase(n, "item-detalle"):
			detalles = append(detalles, txt)
		}
	})
	if len(detalles) > 0 {
		a.Norma = detalles[0]
	}
	if len(detalles) > 1 {
		a.Referencia, a.Sintesis = partirReferencia(detalles[1])
	}
}

// partirReferencia separa "DECTO-2026-845-APN-PTE - Disposiciones." en su
// código y su síntesis. Si la parte izquierda no parece un código (tiene
// espacios), todo es síntesis.
func partirReferencia(s string) (referencia, sintesis string) {
	i := strings.Index(s, " - ")
	if i < 0 {
		return "", s
	}
	izq, der := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:])
	if izq == "" || strings.Contains(izq, " ") {
		return "", s
	}
	return izq, der
}

// ------------------------------------------------------------------- detalle

// ParsearDetalle extrae un aviso completo con su texto, su HTML limpio y sus
// anexos.
func ParsearDetalle(cuerpo []byte, sec Seccion, id string, fecha Fecha) (*Detalle, error) {
	doc, err := html.Parse(strings.NewReader(string(cuerpo)))
	if err != nil {
		return nil, fmt.Errorf("no se pudo parsear el HTML: %w", err)
	}

	titulo := buscarPrimero(doc, porID("tituloDetalleAviso"))
	if titulo == nil {
		return nil, fmt.Errorf("la página del aviso %s/%s no tiene el bloque de título: cambió la forma del sitio", sec, id)
	}

	d := &Detalle{Aviso: Aviso{
		ID:      id,
		Seccion: sec,
		Fecha:   fecha,
		URL:     BaseSitio + "/detalleAviso/" + string(sec) + "/" + id + "/" + fecha.Sitio(),
	}}

	recorrer(titulo, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		switch n.DataAtom {
		case atom.H1:
			if d.Organismo == "" {
				d.Organismo = textoDe(n)
			}
		case atom.H2:
			if d.Norma == "" {
				d.Norma = textoDe(n)
			}
		case atom.H6:
			if d.Referencia == "" && d.Sintesis == "" {
				d.Referencia, d.Sintesis = partirReferencia(textoDe(n))
			}
		}
	})

	if cuerpoNodo := buscarPrimero(doc, porID("cuerpoDetalleAviso")); cuerpoNodo != nil {
		crudo := renderInterior(cuerpoNodo)
		d.HTML = htmltexto.Sanear(crudo)
		d.Texto = htmltexto.APlano(d.HTML)
	}

	d.Anexos = extraerAnexos(doc, sec, fecha)
	d.TieneAnexos = len(d.Anexos) > 0

	if m := reFechaPie.FindSubmatch(cuerpo); m != nil {
		if f, err := parsearFechaPie(string(m[1])); err == nil {
			d.FechaPublicacion = f.API()
		}
	}
	if d.Organismo == "" && d.Texto == "" {
		return nil, fmt.Errorf("el aviso %s/%s vino vacío: cambió la forma del sitio", sec, id)
	}
	return d, nil
}

func parsearFechaPie(s string) (Fecha, error) {
	t, err := timeParsePie(s)
	if err != nil {
		return Fecha{}, err
	}
	return Fecha{t}, nil
}

// extraerAnexos lee los botones de descarga que el sitio arma con JavaScript:
// descargarPDFAnexo(seccion, nroAnexo, idAnexo, fechaPublicacion, url).
func extraerAnexos(doc *html.Node, sec Seccion, fecha Fecha) []Anexo {
	var anexos []Anexo
	vistos := map[string]bool{}
	recorrer(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		onclick := atributo(n, "onclick")
		m := reAnexoJS.FindStringSubmatch(onclick)
		if m == nil {
			return
		}
		// Todos los anexos de un aviso comparten idAnexo y se distinguen por
		// nroAnexo: la clave es el par, no el id solo.
		nro, idAnexo := m[2], m[3]
		clave := idAnexo + "/" + nro
		if vistos[clave] {
			return
		}
		vistos[clave] = true
		nombre := textoDe(n)
		if nombre == "" {
			nombre = "Anexo - " + nro
		}
		// El sitio pone la fecha en AAAAMMDD; la API la expone en AAAA-MM-DD.
		fechaAnexo := fecha
		if f, err := ParseFechaSitio(m[4]); err == nil {
			fechaAnexo = f
		}
		anexos = append(anexos, Anexo{
			ID:     idAnexo,
			Numero: nro,
			Nombre: nombre,
			URL:    fmt.Sprintf("/v1/anexos/%s/%s/%s/%s.pdf", sec, nro, idAnexo, fechaAnexo.API()),
		})
	})
	return anexos
}

// ---------------------------------------------------------------- calendario

// ParsearCalendario decodifica la respuesta de /calendario/dias_publicacion,
// que llega como un string JSON adentro de otro JSON.
func ParsearCalendario(cuerpo []byte, sec Seccion, anio int) (*Calendario, error) {
	var interno string
	if err := json.Unmarshal(cuerpo, &interno); err != nil {
		// Algunas respuestas podrían venir ya como objeto.
		interno = string(cuerpo)
	}
	var datos struct {
		Fechas              []string `json:"fechas"`
		FechasConSuplemento []string `json:"fechas_con_suplemento"`
	}
	if err := json.Unmarshal([]byte(interno), &datos); err != nil {
		return nil, fmt.Errorf("el calendario de %s %d no vino como se esperaba: %w", sec, anio, err)
	}
	cal := &Calendario{Anio: anio, Seccion: sec}
	for _, s := range datos.Fechas {
		if f, err := ParseFechaSitio(s); err == nil {
			cal.Fechas = append(cal.Fechas, f)
		}
	}
	for _, s := range datos.FechasConSuplemento {
		if f, err := ParseFechaSitio(s); err == nil {
			cal.ConSuplemento = append(cal.ConSuplemento, f)
		}
	}
	if len(cal.Fechas) == 0 {
		return nil, fmt.Errorf("el calendario de %s %d vino sin fechas", sec, anio)
	}
	return cal, nil
}

// -------------------------------------------------------------------- rubros

// ParsearRubros decodifica el catálogo que sirve /busquedaAvanzada/{sec}/rubros.
func ParsearRubros(cuerpo []byte) ([]Rubro, error) {
	var crudos []struct {
		ID     string `json:"id"`
		Nombre string `json:"name"`
	}
	if err := json.Unmarshal(cuerpo, &crudos); err != nil {
		return nil, fmt.Errorf("el catálogo de rubros no vino como se esperaba: %w", err)
	}
	rubros := make([]Rubro, 0, len(crudos))
	for _, c := range crudos {
		id, nombre := limpiar(c.ID), limpiar(c.Nombre)
		if id == "" && nombre == "" {
			continue
		}
		rubros = append(rubros, Rubro{ID: id, Nombre: nombre})
	}
	return rubros, nil
}

// ------------------------------------------------------------------ búsqueda

// ParsearBusqueda decodifica la respuesta del POST de búsqueda avanzada, que
// devuelve los resultados como un fragmento de HTML con la misma forma que
// una portada.
func ParsearBusqueda(cuerpo []byte, sec Seccion, pagina int) (*ResultadoBusqueda, error) {
	var resp struct {
		Error     int      `json:"error"`
		Mensajes  []string `json:"mensajes"`
		Contenido *struct {
			HTML     string `json:"html"`
			SigPag   any    `json:"sig_pag"`
			Cantidad any    `json:"cantidad_result_seccion"`
		} `json:"content"`
	}
	if err := json.Unmarshal(cuerpo, &resp); err != nil {
		return nil, fmt.Errorf("la búsqueda no vino como se esperaba: %w", err)
	}
	if resp.Error != 0 {
		return nil, fmt.Errorf("el sitio rechazó la búsqueda: %s", strings.Join(resp.Mensajes, " "))
	}
	if resp.Contenido == nil {
		return &ResultadoBusqueda{Pagina: pagina}, nil
	}
	avisos, err := extraerAvisos([]byte(resp.Contenido.HTML), "")
	if err != nil {
		return nil, err
	}
	if sec != "" {
		for i := range avisos {
			if avisos[i].Seccion == "" {
				avisos[i].Seccion = sec
			}
		}
	}
	return &ResultadoBusqueda{
		Pagina:   pagina,
		Cantidad: len(avisos),
		HayMas:   len(avisos) > 0 && resp.Contenido.SigPag != nil && fmt.Sprint(resp.Contenido.SigPag) != "0",
		Avisos:   avisos,
	}, nil
}
