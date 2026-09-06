package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/diegoparras/notarum/internal/infoleg"
)

// El lector de normativa nacional.
//
// Es la cara web de lo que InfoLEG mantiene: la norma con sus modificaciones
// al día, que es otra cosa que el Boletín. El Boletín publica lo que salió ese
// día; acá se busca por tipo y número sin saber la fecha, que es como se busca
// una ley cuando uno sabe cuál quiere.

// porPaginaNacional es cuántas entran en una página del lector. Más que en la
// API, porque acá se está mirando una lista y no consumiendo datos.
const porPaginaNacional = 25

type datosNacional struct {
	comun
	// Encendido dice si esta instancia armó el índice de búsqueda, que es
	// opcional por lo que ocupa.
	Encendido bool
	// Cargado dice si además hay catálogo bajado con qué armarlo.
	Cargado bool
	Total   int

	Texto    string
	Tipo     string
	Desde    int
	Hasta    int
	ConTexto bool

	Tipos     []infoleg.ConteoTipo
	Resultado *infoleg.Resultado
	Error     string

	Pagina              int
	Paginas             bool
	Anterior, Siguiente string
}

func (s *Sitio) verNacional(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := datosNacional{
		comun:     s.baseCon(r, "", ""),
		Encendido: s.srv.BuscadorInfoLEGActivo(),
		Texto:     strings.TrimSpace(q.Get("texto")),
		Tipo:      strings.TrimSpace(q.Get("tipo")),
		ConTexto:  q.Get("con_texto") != "",
		Pagina:    1,
	}
	if !d.Encendido {
		s.mostrar(w, r, "nacional", d, http.StatusOK)
		return
	}
	d.Cargado = s.srv.CatalogoNacionalCargado()
	if !d.Cargado {
		s.mostrar(w, r, "nacional", d, http.StatusOK)
		return
	}
	d.Tipos = s.srv.TiposNacionales()

	d.Desde = anioDe(q.Get("desde"))
	d.Hasta = anioDe(q.Get("hasta"))
	if d.Desde > 0 && d.Hasta > 0 && d.Hasta < d.Desde {
		d.Desde, d.Hasta = d.Hasta, d.Desde // dos años al revés son un descuido, no un error
	}
	if p, err := strconv.Atoi(q.Get("pagina")); err == nil && p > 1 {
		d.Pagina = p
	}

	d.Resultado = s.srv.BuscarNacional(infoleg.Consulta{
		Texto: d.Texto, Tipo: d.Tipo,
		Desde: d.Desde, Hasta: d.Hasta, SoloConTexto: d.ConTexto,
		Limite: porPaginaNacional, Desplazamiento: (d.Pagina - 1) * porPaginaNacional,
	})
	if d.Resultado != nil {
		d.Total = d.Resultado.Total
	}
	d.Anterior, d.Siguiente = d.enlacesDePagina()
	d.Paginas = d.Anterior != "" || d.Siguiente != ""

	s.mostrar(w, r, "nacional", d, http.StatusOK)
}

// enlacesDePagina arma las direcciones de la anterior y la siguiente
// conservando los filtros: perderlos al pasar de página sería empezar de cero.
func (d datosNacional) enlacesDePagina() (anterior, siguiente string) {
	con := func(pagina int) string {
		v := url.Values{}
		if d.Texto != "" {
			v.Set("texto", d.Texto)
		}
		if d.Tipo != "" {
			v.Set("tipo", d.Tipo)
		}
		if d.Desde > 0 {
			v.Set("desde", strconv.Itoa(d.Desde))
		}
		if d.Hasta > 0 {
			v.Set("hasta", strconv.Itoa(d.Hasta))
		}
		if d.ConTexto {
			v.Set("con_texto", "1")
		}
		if pagina > 1 {
			v.Set("pagina", strconv.Itoa(pagina))
		}
		if len(v) == 0 {
			return "/nacional"
		}
		return "/nacional?" + v.Encode()
	}
	if d.Pagina > 1 {
		anterior = con(d.Pagina - 1)
	}
	if d.Resultado != nil && d.Resultado.Truncado {
		siguiente = con(d.Pagina + 1)
	}
	return anterior, siguiente
}
