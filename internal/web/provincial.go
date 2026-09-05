package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/diegoparras/notarum/internal/saij"
	"github.com/diegoparras/notarum/internal/servicio"
)

// El lector de normativa provincial. Es una cara distinta de la del Boletín
// porque el material es distinto: no hay ediciones ni días, hay un catálogo
// de 81 mil normas que se filtra.

// porPaginaProvincial es cuántas entran en una página del lector. Más que en
// la API, porque acá se está mirando una lista y no consumiendo datos.
const porPaginaProvincial = 25

type datosProvincial struct {
	comun
	Cargado   bool
	Guardadas int

	Texto     string
	Provincia string
	Tipo      string
	Desde     int
	Hasta     int
	Vigentes  bool

	Provincias []servicio.ProvinciaConNormas
	Tipos      []saij.ConteoTipo
	Resultado  *saij.Resultado
	Error      string

	Pagina              int
	Paginas             bool
	Anterior, Siguiente string
}

type datosNormaProv struct {
	comun
	Norma    saij.Norma
	Materias []string
	Volver   string
}

func (s *Sitio) verProvincial(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := datosProvincial{
		comun:      s.baseCon(r, "", ""),
		Cargado:    s.srv.CatalogoProvincialCargado(),
		Provincias: s.srv.ProvinciasConNormas(),
		Texto:      strings.TrimSpace(q.Get("texto")),
		Provincia:  strings.TrimSpace(q.Get("provincia")),
		Tipo:       strings.TrimSpace(q.Get("tipo")),
		Vigentes:   q.Get("vigentes") != "",
		Pagina:     1,
	}
	if !d.Cargado {
		s.mostrar(w, r, "provincial", d, http.StatusOK)
		return
	}
	d.Tipos = s.srv.TiposProvinciales()
	for _, p := range d.Provincias {
		d.Guardadas += p.Normas
	}

	// La provincia se guarda por su código, para que el desplegable la
	// muestre elegida sin importar cómo la haya escrito quien busca.
	if d.Provincia != "" {
		p, hay := saij.BuscarProvincia(d.Provincia)
		if !hay {
			d.Error = "No se reconoce la provincia " + d.Provincia + "."
			s.mostrar(w, r, "provincial", d, http.StatusOK)
			return
		}
		d.Provincia = p.ID
	}
	d.Desde = anioDe(q.Get("desde"))
	d.Hasta = anioDe(q.Get("hasta"))
	if d.Desde > 0 && d.Hasta > 0 && d.Hasta < d.Desde {
		d.Desde, d.Hasta = d.Hasta, d.Desde // dos años al revés son un descuido, no un error
	}
	if p, err := strconv.Atoi(q.Get("pagina")); err == nil && p > 1 {
		d.Pagina = p
	}

	d.Resultado = s.srv.BuscarProvincial(saij.Consulta{
		Texto: d.Texto, Provincia: d.Provincia, Tipo: d.Tipo,
		Desde: d.Desde, Hasta: d.Hasta, SoloVigentes: d.Vigentes,
		Limite: porPaginaProvincial, Desplazamiento: (d.Pagina - 1) * porPaginaProvincial,
	})
	d.Anterior, d.Siguiente = d.enlacesDePagina()
	d.Paginas = d.Anterior != "" || d.Siguiente != ""

	s.mostrar(w, r, "provincial", d, http.StatusOK)
}

// enlacesDePagina arma las direcciones de la página anterior y la siguiente
// conservando los filtros: perderlos al pasar de página sería empezar de cero.
func (d datosProvincial) enlacesDePagina() (anterior, siguiente string) {
	con := func(pagina int) string {
		v := url.Values{}
		if d.Texto != "" {
			v.Set("texto", d.Texto)
		}
		if d.Provincia != "" {
			v.Set("provincia", d.Provincia)
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
		if d.Vigentes {
			v.Set("vigentes", "1")
		}
		if pagina > 1 {
			v.Set("pagina", strconv.Itoa(pagina))
		}
		if len(v) == 0 {
			return "/provincial"
		}
		return "/provincial?" + v.Encode()
	}
	if d.Pagina > 1 {
		anterior = con(d.Pagina - 1)
	}
	if d.Resultado != nil && d.Resultado.Truncado {
		siguiente = con(d.Pagina + 1)
	}
	return anterior, siguiente
}

func (s *Sitio) verNormaProvincial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, hay := s.srv.NormaProvincial(id)
	if !hay {
		if !s.srv.CatalogoProvincialCargado() {
			s.fallo(w, r, http.StatusServiceUnavailable,
				"Esta instancia todavía no bajó la normativa provincial",
				"Quien la opera tiene que correr `notarum provincial` una vez.")
			return
		}
		s.fallo(w, r, http.StatusNotFound, "No hay una norma con ese identificador",
			"Los identificadores son del estilo LPB1000000. Buscala desde /provincial.")
		return
	}
	d := datosNormaProv{
		comun:    s.baseCon(r, "", ""),
		Norma:    n,
		Materias: n.Materias(),
		// Se vuelve a la lista de la provincia y no a la búsqueda vacía: es
		// lo más cerca de donde estaba quien llegó acá.
		Volver: "/provincial?provincia=" + n.ProvinciaID,
	}
	d.Angosto = true
	s.mostrar(w, r, "normaprov", d, http.StatusOK)
}

// anioDe lee un año de la consulta; lo que no se entiende es "sin límite".
func anioDe(v string) int {
	a, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || a < 1800 || a > 2200 {
		return 0
	}
	return a
}
