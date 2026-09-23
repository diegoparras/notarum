package vencimientos

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
)

// El parseo se ancla en los nombres de clase de la página y no en la posición
// de las tablas: el HTML tiene tablas anidadas, atributos sin comillas y
// etiquetas sin cerrar, pero las clases son estables y dicen qué es cada cosa
// (grilla-dato-titulo es el impuesto; td-fecha, una fecha).
//
// Si ARCA las cambia, Parsear falla diciéndolo, en vez de adivinar y terminar
// leyendo el menú como si fueran vencimientos.
const (
	marcaBloque = `class="tabla-form-vencimientos"`
	marcaError  = `Lista de Errores`

	// textoRangoNoCargado es lo que contesta ARCA cuando se le pide una fecha
	// más allá de lo que tiene cargado.
	textoRangoNoCargado = "no está cargado"
)

// ErrRangoNoCargado es ARCA diciendo que su agenda no llega hasta la fecha
// pedida. No es una falla: la agenda del año siguiente aparece en algún
// momento del año, sin fecha anunciada. Quien consulta se repliega a una
// ventana más corta (ver VentanaDeRespaldo).
var ErrRangoNoCargado = errors.New("ARCA no tiene cargada esa ventana de fechas")

var (
	// reCampo toma los pares rótulo/valor del encabezado de cada régimen.
	reCampo = regexp.MustCompile(`(?s)<td[^>]*class="grilla-titulo"[^>]*>(.*?)</td>\s*<td[^>]*class="(?:grilla-dato-titulo|grilla-dato)"[^>]*>(.*?)</td>`)
	// reMarcador recorre el bloque en orden y devuelve, mezclados, los
	// encabezados de obligación (con su período y sus normas) y las tablas de
	// fechas.
	reMarcador = regexp.MustCompile(`(?s)<div class="Estilo1">(.*?)</div>(.*?)</td>\s*(?:<td[^>]*class="grilla-dato-norma[^"]*"[^>]*>(.*?)</td>)?|<table[^>]*class="tabla-fecha".*?</table>`)
	// reFila toma terminación de CUIT y fecha de una fila de la tabla.
	reFila       = regexp.MustCompile(`(?s)<td[^>]*class="td-fecha"[^>]*>(.*?)</td>\s*<td[^>]*class="td-fecha"[^>]*>(.*?)</td>`)
	reNormaCelda = regexp.MustCompile(`(?s)<td[^>]*class="grilla-dato-norma[^"]*"[^>]*>(.*?)</td>`)
	reEnlace     = regexp.MustCompile(`(?s)<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	reItem       = regexp.MustCompile(`(?s)<li>(.*?)</li>`)
	reEtiqueta   = regexp.MustCompile(`(?s)<[^>]*>`)
	reEspacios   = regexp.MustCompile(`\s+`)
	reSelect     = regexp.MustCompile(`(?s)<select[^>]*name="impuestosASeleccionar".*?</select>`)
	reOpcion     = regexp.MustCompile(`(?s)<option value="([^"]*)"[^>]*>(.*?)</option>`)
)

// formatoARCA es el dd/mm/aaaa que usa la página.
const formatoARCA = "02/01/2006"

// Parsear interpreta la página de resultados.
//
// La página contesta 200 tanto con la agenda como con una lista de errores, y
// tanto con seis mil vencimientos como con cero. Acá se distinguen, y sólo el
// primer caso da datos.
func Parsear(cuerpo []byte, desde, hasta time.Time) (Agenda, error) {
	s := DeLatin1(cuerpo)

	if i := strings.Index(s, marcaError); i >= 0 {
		motivos := listaDeErrores(s[i:])
		if strings.Contains(motivos, textoRangoNoCargado) {
			return Agenda{}, fmt.Errorf("%w: %s", ErrRangoNoCargado, motivos)
		}
		return Agenda{}, &ErrDeARCA{Que: "rechazó la consulta: " + motivos}
	}

	bloques := strings.Split(s, marcaBloque)[1:]
	if len(bloques) == 0 {
		return Agenda{}, &ErrDeARCA{Que: "la respuesta no trae ningún régimen; la página cambió de forma"}
	}

	ag := Agenda{Desde: desde, Hasta: hasta}
	for i, b := range bloques {
		if fin := strings.LastIndex(b, "</table>"); fin > 0 {
			b = b[:fin]
		}
		vs, avisos := parsearBloque(b)
		ag.Vencimientos = append(ag.Vencimientos, vs...)
		for _, a := range avisos {
			ag.Avisos = append(ag.Avisos, fmt.Sprintf("régimen %d de %d: %s", i+1, len(bloques), a))
		}
	}
	return ag, nil
}

// parsearBloque interpreta un régimen: su encabezado y sus obligaciones.
func parsearBloque(b string) ([]Vencimiento, []string) {
	var avisos []string

	campos := map[string]string{}
	for _, m := range reCampo.FindAllStringSubmatch(b, -1) {
		campos[strings.ToLower(limpiar(m[1]))] = limpiar(m[2])
	}
	base := Vencimiento{
		Impuesto:    campos["impuesto"],
		Regimen:     campos["descripción del régimen"],
		Sujeto:      campos["sujeto"],
		TipoRegimen: campos["tipo de régimen"],
		Formularios: campos["formulario/s"],
		Aplicativos: campos["aplicativo/s"],
	}
	if base.Impuesto == "" {
		return nil, []string{"no trae el nombre del impuesto y se descarta"}
	}

	// Las normas del régimen están antes de la primera obligación; las que
	// vienen después son de una obligación en particular.
	encabezado := b
	if i := strings.Index(b, `<div class="Estilo1">`); i > 0 {
		encabezado = b[:i]
	}
	delRegimen := normasDe(encabezado)

	obligaciones, avisosObl := armarObligaciones(b)
	avisos = append(avisos, avisosObl...)

	var out []Vencimiento
	for _, o := range obligaciones {
		normas := unirNormas(delRegimen, o.normas)
		if len(o.fechas) == 0 {
			v := base
			v.Obligacion, v.Periodo = o.obligacion, o.periodo
			v.SinFechaFija, v.Normas = true, normas
			out = append(out, v)
			continue
		}
		for _, f := range o.fechas {
			fecha, err := time.Parse(formatoARCA, f.fecha)
			if err != nil {
				avisos = append(avisos, fmt.Sprintf("la fecha %q de %q no se pudo leer y esa fila se descarta", f.fecha, o.obligacion))
				continue
			}
			v := base
			v.Obligacion, v.Periodo = o.obligacion, o.periodo
			v.TerminacionCUIT, v.Fecha, v.Normas = f.terminacion, fecha.Format(FormatoISO), normas
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		avisos = append(avisos, fmt.Sprintf("%q no trajo ninguna obligación", base.Impuesto))
	}
	return out, avisos
}

type filaDeFecha struct{ terminacion, fecha string }

type obligacion struct {
	obligacion, periodo string
	normas              []Norma
	fechas              []filaDeFecha
}

// armarObligaciones aparea cada encabezado de obligación con su tabla de
// fechas.
//
// Una tabla puede servir a varios encabezados seguidos: "transferencia de
// fondos" y "presentación del formulario" que vencen el mismo día se publican
// como dos encabezados y una tabla. En la agenda de septiembre de 2026 hay 220
// encabezados y 188 tablas; aparearlos uno a uno perdería obligaciones o les
// pondría la fecha de otra. Así que los encabezados se acumulan y la tabla que
// aparece los cierra a todos. Los que quedan abiertos al final no tienen fecha
// fija.
func armarObligaciones(b string) ([]obligacion, []string) {
	var (
		out, pendientes []obligacion
		avisos          []string
	)
	for _, m := range reMarcador.FindAllStringSubmatch(b, -1) {
		if strings.HasPrefix(m[0], "<div") {
			pendientes = append(pendientes, obligacion{
				obligacion: limpiar(m[1]),
				periodo:    limpiar(m[2]),
				normas:     normasDe(m[3]),
			})
			continue
		}
		filas := filasDe(m[0])
		if len(filas) == 0 {
			avisos = append(avisos, "una tabla de fechas vino vacía")
			continue
		}
		if len(pendientes) == 0 {
			avisos = append(avisos, fmt.Sprintf("una tabla con %d fecha(s) no tiene obligación a la que pertenecer y se descarta", len(filas)))
			continue
		}
		for i := range pendientes {
			pendientes[i].fechas = filas
		}
		out = append(out, pendientes...)
		pendientes = nil
	}
	return append(out, pendientes...), avisos
}

func filasDe(tabla string) []filaDeFecha {
	var out []filaDeFecha
	for _, m := range reFila.FindAllStringSubmatch(tabla, -1) {
		t, f := limpiar(m[1]), limpiar(m[2])
		if t == "" && f == "" {
			continue
		}
		out = append(out, filaDeFecha{terminacion: t, fecha: f})
	}
	return out
}

// normasDe junta las citas legales de un fragmento. Sólo cuentan las que
// tienen enlace: las celdas vacías traen un &nbsp;.
func normasDe(fragmento string) []Norma {
	if fragmento == "" {
		return nil
	}
	celdas := reNormaCelda.FindAllStringSubmatch(fragmento, -1)
	if len(celdas) == 0 {
		// El fragmento ya es el contenido de la celda.
		celdas = [][]string{{fragmento, fragmento}}
	}
	var out []Norma
	for _, c := range celdas {
		for _, m := range reEnlace.FindAllStringSubmatch(c[1], -1) {
			if t := limpiar(m[2]); t != "" {
				out = append(out, Norma{Texto: t, URL: strings.TrimSpace(html.UnescapeString(m[1]))})
			}
		}
	}
	return out
}

func unirNormas(listas ...[]Norma) []Norma {
	var out []Norma
	visto := map[Norma]bool{}
	for _, l := range listas {
		for _, n := range l {
			if !visto[n] {
				visto[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func listaDeErrores(s string) string {
	var motivos []string
	for _, m := range reItem.FindAllStringSubmatch(s, -1) {
		if t := limpiar(m[1]); t != "" {
			motivos = append(motivos, t)
		}
	}
	if len(motivos) == 0 {
		return "no dijo por qué"
	}
	return strings.Join(motivos, "; ")
}

// ParsearCatalogo saca del formulario la lista de impuestos del desplegable.
// Sirve para filtrar por un nombre exacto y para enterarse cuando ARCA suma un
// impuesto, antes de que tenga vencimientos cargados.
func ParsearCatalogo(cuerpo []byte) ([]Impuesto, error) {
	sel := reSelect.FindString(DeLatin1(cuerpo))
	if sel == "" {
		return nil, &ErrDeARCA{Que: "el formulario no trae el desplegable de impuestos; la página cambió de forma"}
	}
	var out []Impuesto
	for _, m := range reOpcion.FindAllStringSubmatch(sel, -1) {
		codigo, nombre := strings.TrimSpace(m[1]), limpiar(m[2])
		// "-" es el separador y 999 es "TODOS": ninguno es un impuesto.
		if nombre == "" || nombre == "-" || codigo == CodigoTodos {
			continue
		}
		out = append(out, Impuesto{Codigo: codigo, Nombre: nombre})
	}
	if len(out) == 0 {
		return nil, &ErrDeARCA{Que: "el desplegable de impuestos vino vacío"}
	}
	return out, nil
}

// limpiar deja el texto visible de una celda. Primero se sacan las etiquetas y
// después se resuelven las entidades: al revés, un "&lt;b&gt;" del dato se
// borraría como si fuera marcado.
func limpiar(s string) string {
	if s == "" {
		return ""
	}
	s = reEtiqueta.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(reEspacios.ReplaceAllString(s, " "))
}
