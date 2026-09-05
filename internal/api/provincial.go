package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/diegoparras/notarum/internal/saij"
)

// La normativa provincial, que sale de la Base SAIJ del Ministerio de
// Justicia. Es lo que el Boletín Oficial de la Nación no publica: las leyes
// de cada provincia salen en el boletín de su provincia.
//
// Va bajo /v1/provincial y no mezclada con las ediciones porque es otra cosa:
// no hay ediciones ni fechas de publicación diarias, hay un catálogo.

// cacheProvincial es larga a propósito: el catálogo se publica una vez por
// mes y lo que se responde no cambia entre sincronizaciones.
const cacheProvincial = "public, max-age=3600"

func (s *Servidor) verProvincias(w http.ResponseWriter, r *http.Request) {
	escribirJSON(w, r, http.StatusOK, s.srv.ProvinciasConNormas(), cacheProvincial)
}

func (s *Servidor) verTiposProvinciales(w http.ResponseWriter, r *http.Request) {
	escribirJSON(w, r, http.StatusOK, s.srv.TiposProvinciales(), cacheProvincial)
}

func (s *Servidor) verNormaProvincial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, hay := s.srv.NormaProvincial(id)
	if !hay {
		if !s.srv.CatalogoProvincialCargado() {
			s.avisarSinCatalogo(w, r)
			return
		}
		escribirError(w, r, http.StatusNotFound, OrigenPedido,
			"no hay una norma provincial con ese identificador",
			"los identificadores son del estilo LPB1000000; se encuentran buscando en /v1/provincial")
		return
	}
	escribirJSON(w, r, http.StatusOK, struct {
		saij.Norma
		Ficha string `json:"ficha"`
	}{n, n.URLFicha()}, cacheProvincial)
}

func (s *Servidor) buscarProvincial(w http.ResponseWriter, r *http.Request) {
	if !s.srv.CatalogoProvincialCargado() {
		s.avisarSinCatalogo(w, r)
		return
	}
	q := r.URL.Query()

	// Una provincia mal escrita devolvería cero resultados sin explicar por
	// qué; es mejor decirlo.
	if p := q.Get("provincia"); p != "" {
		if _, hay := saij.BuscarProvincia(p); !hay {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido,
				"no se reconoce la provincia "+p,
				"se acepta el nombre, el código INDEC o el prefijo; la lista está en /v1/provincial/provincias")
			return
		}
	}
	desde, ok := s.anioDe(w, r, "desde", q.Get("desde"))
	if !ok {
		return
	}
	hasta, ok := s.anioDe(w, r, "hasta", q.Get("hasta"))
	if !ok {
		return
	}
	if desde > 0 && hasta > 0 && hasta < desde {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "rango inválido",
			"hasta es anterior a desde")
		return
	}
	limite, _ := strconv.Atoi(q.Get("limite"))
	pagina, _ := strconv.Atoi(q.Get("pagina"))
	if pagina < 1 {
		pagina = 1
	}
	if limite <= 0 {
		limite = saij.LimitePorDefecto
	}

	res := s.srv.BuscarProvincial(saij.Consulta{
		Texto:          q.Get("texto"),
		Provincia:      q.Get("provincia"),
		Tipo:           q.Get("tipo"),
		Desde:          desde,
		Hasta:          hasta,
		SoloVigentes:   esSi(q.Get("vigentes")),
		Limite:         limite,
		Desplazamiento: (pagina - 1) * limite,
	})
	escribirJSON(w, r, http.StatusOK, struct {
		Total    int          `json:"total"`
		Pagina   int          `json:"pagina"`
		Normas   []saij.Norma `json:"normas"`
		Truncado bool         `json:"hay_mas"`
	}{res.Total, pagina, res.Normas, res.Truncado}, cacheProvincial)
}

// anioDe lee un año de la consulta. Vacío es "sin límite" y no un error: los
// dos extremos son opcionales.
func (s *Servidor) anioDe(w http.ResponseWriter, r *http.Request, campo, valor string) (int, bool) {
	valor = strings.TrimSpace(valor)
	if valor == "" {
		return 0, true
	}
	a, err := strconv.Atoi(valor)
	if err != nil || a < 1800 || a > 2200 {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido,
			campo+" inválido: "+valor, "se espera un año de cuatro dígitos, como 1994")
		return 0, false
	}
	return a, true
}

func esSi(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "si", "sí", "true", "s":
		return true
	}
	return false
}

// avisarSinCatalogo explica que la base provincial todavía no se bajó. Es
// distinto de "no hay resultados": no se buscó en ningún lado.
func (s *Servidor) avisarSinCatalogo(w http.ResponseWriter, r *http.Request) {
	escribirError(w, r, http.StatusServiceUnavailable, OrigenNotarum,
		"esta instancia todavía no bajó la normativa provincial",
		"quien la opera tiene que correr `notarum provincial` una vez; son 81 mil normas y tarda unos segundos")
}
