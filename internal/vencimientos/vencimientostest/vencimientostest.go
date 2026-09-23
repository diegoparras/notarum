// Package vencimientostest arma páginas de la agenda de ARCA y un ARCA falso,
// para probar lo que consume la agenda sin salir a la red.
//
// Las páginas tienen la misma forma que las reales —las mismas clases, la
// misma codificación ISO-8859-1— pero se escriben a medida: así un test puede
// correr una fecha y ver que se registra la prórroga, o armar una agenda del
// tamaño de una real.
package vencimientostest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Regimen es un bloque de la agenda con una obligación.
type Regimen struct {
	Impuesto   string
	Regimen    string
	Obligacion string
	Periodo    string
	// Fechas son pares terminación → fecha dd/mm/aaaa. Sin fechas, la
	// obligación es de plazo relativo.
	Fechas [][2]string
}

// Muchos arma n regímenes distintos, cada uno con tres grupos de
// terminaciones que vencen el día dado (dd/mm/aaaa). Sirve para superar el
// piso de filas que se le exige a una bajada real.
func Muchos(n int, fecha string) []Regimen {
	out := make([]Regimen, n)
	for i := range out {
		out[i] = Regimen{
			Impuesto:   fmt.Sprintf("IMPUESTO DE PRUEBA %03d", i),
			Obligacion: "Presentación de la declaración jurada",
			Periodo:    "Agosto/2026",
			Fechas:     [][2]string{{"0-1-2-3", fecha}, {"4-5-6", fecha}, {"7-8-9", fecha}},
		}
	}
	return out
}

// Pagina arma la respuesta de ARCA con esos regímenes, en ISO-8859-1.
func Pagina(rs ...Regimen) []byte {
	var b strings.Builder
	b.WriteString(`<html><body><table><tr><td class="tit">Vencimientos Seleccionados - Informaci&oacute;n actualizada al Mon Sep 21 17:39:06 ART 2026</td></tr></table>`)
	for _, r := range rs {
		b.WriteString(`<table class="tabla-form-vencimientos" cellpadding="0" cellspacing="0">`)
		fmt.Fprintf(&b, `<tr><td class="grilla-titulo"> Impuesto </td><td class="grilla-dato-titulo">%s</td>`+
			`<td class="grilla-dato-norma"><a href="http://biblioteca.afip.gob.ar/dcp/LEY" target=_blank class="av">Ley N° 1<br></a></td></tr>`, r.Impuesto)
		if r.Regimen != "" {
			fmt.Fprintf(&b, `<tr><td class="grilla-titulo">Descripci&oacute;n del R&eacute;gimen</td><td class="grilla-dato">%s</td><td class="grilla-dato-norma">&nbsp;</td></tr>`, r.Regimen)
		}
		b.WriteString(`<tr><td class="grilla-titulo">Sujeto</td><td class="grilla-dato">Responsables inscriptos</td><td class="grilla-dato-norma">&nbsp;</td></tr>`)
		fmt.Fprintf(&b, `<tr><td rowspan="2" class="grilla-titulo-inf">Tipo de obligaci&oacute;n</td>`+
			`<td class="td-obligacion-sin-borde"><div class="Estilo1">%s</div>%s</td><td class="grilla-dato-norma">&nbsp;</td></tr>`,
			r.Obligacion, r.Periodo)
		if len(r.Fechas) > 0 {
			b.WriteString(`<tr><td class="td-obligacion-sin-borde"><table class="tabla-fecha"><tr><td class="titulo-fecha">Terminaci&oacute;n de CUIT</td><td class="titulo-fecha">Fecha de Vencimiento</td></tr>`)
			for _, f := range r.Fechas {
				fmt.Fprintf(&b, `<tr><td class="td-fecha" >%s</td><td class="td-fecha" >%s</td></tr>`, f[0], f[1])
			}
			b.WriteString(`</table></td></tr>`)
		}
		b.WriteString(`</table><br><hr class="hr-vencimiento">`)
	}
	b.WriteString(`</body></html>`)
	return latin1(b.String())
}

// Formulario es el formulario de consulta con un desplegable de impuestos.
func Formulario(impuestos ...string) []byte {
	var b strings.Builder
	b.WriteString(`<html><form name="consultaVencimientoForm"><select name="impuestosASeleccionar" multiple="multiple">`)
	b.WriteString(`<option value="53" title="-">-</option>`)
	for i, imp := range impuestos {
		fmt.Fprintf(&b, `<option value="%d" title="%s">%s</option>`, i+1, imp, imp)
	}
	b.WriteString(`<option value="999" title="TODOS">TODOS</option></select></form></html>`)
	return latin1(b.String())
}

func latin1(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xff {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return out
}

// ARCA es un formulario de ARCA falso: GET devuelve el formulario y POST la
// agenda que se le haya puesto.
type ARCA struct {
	*httptest.Server
	mu         sync.Mutex
	agenda     []byte
	formulario []byte
}

// NuevaARCA levanta el ARCA falso con esa agenda. Se cierra solo al terminar
// el test.
func NuevaARCA(t testing.TB, agenda []byte) *ARCA {
	t.Helper()
	a := &ARCA{agenda: agenda, formulario: Formulario("IMPUESTO AL VALOR AGREGADO", "IMPUESTO A LAS GANANCIAS")}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		w.Header().Set("Content-Type", "text/html;charset=iso-8859-1")
		if r.Method == http.MethodGet {
			_, _ = w.Write(a.formulario)
			return
		}
		_, _ = w.Write(a.agenda)
	}))
	t.Cleanup(a.Close)
	return a
}

// Poner cambia la agenda que devuelve.
func (a *ARCA) Poner(agenda []byte) {
	a.mu.Lock()
	a.agenda = agenda
	a.mu.Unlock()
}
