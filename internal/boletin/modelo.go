// Package boletin lee el Boletín Oficial de la República Argentina y lo
// convierte en estructuras tipadas. No opina sobre el contenido: extrae.
package boletin

import (
	"fmt"
	"strings"
	"time"
)

// Seccion es una de las secciones publicables del Boletín.
type Seccion string

const (
	Primera Seccion = "primera"
	Segunda Seccion = "segunda"
	Tercera Seccion = "tercera"
)

// SeccionesValidas son las que esta API sabe leer.
//
// La cuarta queda afuera a propósito, y no es lo que dice su fama: no son
// contrataciones sino "DOMINIOS PUBLICADOS ar", el listado diario de altas de
// dominios de internet con su titular. No es normativa.
//
// Medido contra el sitio:
//
//   - /seccion/cuarta/<fecha> contesta con exactamente 100 entradas, en orden
//     alfabético y cortadas en la "g".
//   - ?pagina, ?page y ?offset devuelven los mismos 100. El resto llega, como
//     en las otras secciones, por /seccion/actualizar/cuarta?pag=N (ver
//     TamanoDePagina): el 19/11/2026 la página 2 siguió el listado donde lo
//     dejaba la portada. Cuando se escribió esta nota eso no se sabía, y se
//     creyó que la única fuente completa era el PDF.
//   - El buscador avanzado del sitio no la conoce: pedirle la sección 4
//     redirige a la página de error.
//   - El PDF de la sección tiene 63 páginas, o sea unos 2.500 dominios por
//     día: unas 25 páginas de 100 por edición.
//
// Sigue afuera porque el modelo no encaja: no hay avisos con identificador ni
// páginas de detalle, que es sobre lo que está armado todo lo demás. Lo que
// no puede pasar es lo que pasaba con las otras tres antes de seguir las
// páginas: servir los primeros 100 sin que se note.
var SeccionesValidas = []Seccion{Primera, Segunda, Tercera}

// ParseSeccion valida el nombre de una sección.
func ParseSeccion(s string) (Seccion, error) {
	sec := Seccion(strings.ToLower(strings.TrimSpace(s)))
	for _, v := range SeccionesValidas {
		if v == sec {
			return v, nil
		}
	}
	return "", fmt.Errorf("sección %q inválida: se esperaba primera, segunda o tercera", s)
}

// ID numérico que usa la búsqueda avanzada del sitio para cada sección.
func (s Seccion) IDBusqueda() string {
	switch s {
	case Primera:
		return "1"
	case Segunda:
		return "2"
	case Tercera:
		return "3"
	}
	return ""
}

// Fecha es un día de publicación. Se serializa como AAAA-MM-DD.
type Fecha struct{ time.Time }

const (
	formatoAPI   = "2006-01-02" // el que expone notarum
	formatoSitio = "20060102"   // el que usa boletinoficial.gob.ar en las URLs
	formatoPie   = "02/01/2006" // el que aparece impreso en el detalle
)

// ParseFecha acepta AAAA-MM-DD (el formato de la API).
func ParseFecha(s string) (Fecha, error) {
	t, err := time.ParseInLocation(formatoAPI, strings.TrimSpace(s), tzArgentina)
	if err != nil {
		return Fecha{}, fmt.Errorf("fecha %q inválida: se esperaba AAAA-MM-DD", s)
	}
	return Fecha{t}, nil
}

// ParseFechaSitio acepta AAAAMMDD (el formato de las URLs del sitio).
func ParseFechaSitio(s string) (Fecha, error) {
	t, err := time.ParseInLocation(formatoSitio, strings.TrimSpace(s), tzArgentina)
	if err != nil {
		return Fecha{}, fmt.Errorf("fecha %q inválida: se esperaba AAAAMMDD", s)
	}
	return Fecha{t}, nil
}

func (f Fecha) API() string   { return f.Format(formatoAPI) }
func (f Fecha) Sitio() string { return f.Format(formatoSitio) }
func (f Fecha) Pie() string   { return f.Format(formatoPie) }

func (f Fecha) MarshalJSON() ([]byte, error) { return []byte(`"` + f.API() + `"`), nil }

func (f *Fecha) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	p, err := ParseFecha(s)
	if err != nil {
		return err
	}
	*f = p
	return nil
}

// tzArgentina fija el huso en el que el Boletín publica. Se resuelve en
// init() para no depender de la base de zonas horarias del contenedor.
var tzArgentina = time.FixedZone("ART", -3*60*60)

// HoyEnArgentina es la fecha corriente en el huso del Boletín.
func HoyEnArgentina() Fecha {
	n := time.Now().In(tzArgentina)
	return Fecha{time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, tzArgentina)}
}

// Aviso es una entrada del sumario de una edición.
type Aviso struct {
	ID          string  `json:"id"`
	Seccion     Seccion `json:"seccion"`
	Fecha       Fecha   `json:"fecha"`
	Rubro       string  `json:"rubro"`
	Organismo   string  `json:"organismo"`
	Norma       string  `json:"norma,omitempty"`
	Referencia  string  `json:"referencia,omitempty"`
	Sintesis    string  `json:"sintesis,omitempty"`
	TieneAnexos bool    `json:"tiene_anexos"`
	Repetido    bool    `json:"repetido"`
	Suplemento  bool    `json:"suplemento"`
	URL         string  `json:"url"`
}

// TamanoDePagina es de a cuántos avisos entrega el sitio el sumario de una
// edición. La portada trae los primeros; el resto llega por
// /seccion/actualizar/<sección>?pag=N, que es lo que el sitio pide solo cuando
// el lector se desplaza hasta el final.
//
// Medido el 19/11/2026: la Segunda de ese día tiene 387 avisos y la del
// 1/9/2026, 410; la Primera del 1/9/2026, 129. Leer sólo la portada era servir
// 100 como si fueran todos.
const TamanoDePagina = 100

// Edicion es el sumario de una sección en una fecha.
type Edicion struct {
	Seccion  Seccion `json:"seccion"`
	Fecha    Fecha   `json:"fecha"`
	Cantidad int     `json:"cantidad"`
	// Declarados es cuántos avisos anuncia el propio sitio para esa edición,
	// sumando su índice de rubros. Vale 0 si la página no trae el índice.
	Declarados int `json:"cantidad_declarada,omitempty"`
	// Parcial avisa que la edición puede no estar entera: se extrajeron menos
	// avisos de los que el sitio declara, o no se pudo confirmar que estén
	// todos. Una edición parcial no se guarda para siempre.
	Parcial bool `json:"parcial"`
	// Paginas es cuántas páginas del sitio hubo que leer para armarla. Las
	// ediciones guardadas antes de que notarum siguiera las páginas no lo
	// traen, y por eso se las reconoce.
	Paginas       int            `json:"paginas,omitempty"`
	PorRubro      map[string]int `json:"por_rubro"`
	ConSuplemento bool           `json:"con_suplemento"`
	Avisos        []Aviso        `json:"avisos"`
}

// DeAntesDeLasPaginas dice si esta edición se guardó cuando notarum leía sólo
// la portada y quedó justo en el tope: es la firma de una edición cortada.
// Puede ser una edición que de verdad tenía 100 avisos; volver a leerla cuesta
// un pedido y lo confirma.
func (e *Edicion) DeAntesDeLasPaginas() bool {
	return e.Paginas == 0 && e.Cantidad == TamanoDePagina
}

// Resumen es una edición sin sus avisos, para listar rangos.
type Resumen struct {
	Seccion       Seccion        `json:"seccion"`
	Fecha         Fecha          `json:"fecha"`
	Cantidad      int            `json:"cantidad"`
	Parcial       bool           `json:"parcial"`
	PorRubro      map[string]int `json:"por_rubro"`
	ConSuplemento bool           `json:"con_suplemento"`
}

// Anexo es un archivo adjunto a un aviso.
type Anexo struct {
	ID     string `json:"id"`
	Numero string `json:"numero"`
	Nombre string `json:"nombre"`
	URL    string `json:"url"`
}

// Detalle es un aviso con su texto completo.
type Detalle struct {
	Aviso
	Texto            string  `json:"texto"`
	HTML             string  `json:"html"`
	Anexos           []Anexo `json:"anexos"`
	FechaPublicacion string  `json:"fecha_publicacion,omitempty"`
}

// Calendario son los días con edición de una sección en un año.
type Calendario struct {
	Anio          int     `json:"anio"`
	Seccion       Seccion `json:"seccion"`
	Fechas        []Fecha `json:"fechas"`
	ConSuplemento []Fecha `json:"con_suplemento"`
}

// Rubro es una entrada del catálogo de rubros de una sección.
type Rubro struct {
	ID     string `json:"id"`
	Nombre string `json:"nombre"`
}

// ResultadoBusqueda es una página de resultados de la búsqueda avanzada.
type ResultadoBusqueda struct {
	Pagina   int     `json:"pagina"`
	Cantidad int     `json:"cantidad"`
	HayMas   bool    `json:"hay_mas"`
	Avisos   []Aviso `json:"avisos"`
}

// timeParsePie lee la fecha impresa al pie del detalle (DD/MM/AAAA).
func timeParsePie(s string) (time.Time, error) {
	return time.ParseInLocation(formatoPie, strings.TrimSpace(s), tzArgentina)
}
