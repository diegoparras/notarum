package api

import (
	"net/http"
	"strconv"

	"github.com/diegoparras/notarum/internal/infoleg"
)

// La búsqueda de normativa nacional, que sale de InfoLEG.
//
// El Boletín publica la norma como salió ese día; InfoLEG la mantiene con sus
// modificaciones. Buscar acá es buscar en las 428 mil normas nacionales por
// título y materias, que es lo que /v1/buscar no hace: ése recorre los avisos
// de una edición, día por día.

func (s *Servidor) buscarNacional(w http.ResponseWriter, r *http.Request) {
	if !s.srv.BuscadorInfoLEGActivo() {
		escribirError(w, r, http.StatusNotFound, OrigenNotarum,
			"esta instancia no tiene el buscador de normativa nacional",
			"se enciende con NOTARUM_BUSCADOR_INFOLEG=1; son unos 350 MB de memoria")
		return
	}
	if !s.srv.CatalogoNacionalCargado() {
		escribirError(w, r, http.StatusServiceUnavailable, OrigenNotarum,
			"el catálogo de InfoLEG todavía no se bajó",
			"quien opera la instancia tiene que sincronizarlo desde /admin o con `notarum infoleg`")
		return
	}
	q := r.URL.Query()

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
		limite = infoleg.LimitePorDefecto
	}

	res := s.srv.BuscarNacional(infoleg.Consulta{
		Texto:          q.Get("texto"),
		Tipo:           q.Get("tipo"),
		Desde:          desde,
		Hasta:          hasta,
		SoloConTexto:   esSi(q.Get("con_texto")),
		Limite:         limite,
		Desplazamiento: (pagina - 1) * limite,
	})

	escribirJSON(w, r, http.StatusOK, struct {
		Total    int             `json:"total"`
		Pagina   int             `json:"pagina"`
		Normas   []vistaNacional `json:"normas"`
		Truncado bool            `json:"hay_mas"`
	}{res.Total, pagina, vistasNacionales(res.Normas), res.Truncado}, cacheProvincial)
}

func (s *Servidor) verTiposNacionales(w http.ResponseWriter, r *http.Request) {
	if !s.srv.BuscadorInfoLEGActivo() {
		escribirError(w, r, http.StatusNotFound, OrigenNotarum,
			"esta instancia no tiene el buscador de normativa nacional",
			"se enciende con NOTARUM_BUSCADOR_INFOLEG=1")
		return
	}
	escribirJSON(w, r, http.StatusOK, s.srv.TiposNacionales(), cacheProvincial)
}

// vistaNacional es la norma como se devuelve: con los enlaces armados, que es
// lo que hace falta para llegar al texto.
type vistaNacional struct {
	ID     int    `json:"id"`
	Tipo   string `json:"tipo"`
	Numero string `json:"numero,omitempty"`
	Titulo string `json:"titulo,omitempty"`
	Fecha  string `json:"fecha_sancion,omitempty"`
	// TieneTexto dice si InfoLEG publicó el texto de esta norma.
	TieneTexto bool `json:"tiene_texto"`
	// Ficha es la página de la norma en InfoLEG; Texto, el texto original.
	Ficha string `json:"ficha"`
	Texto string `json:"texto,omitempty"`
	// EnNotarum es la ruta de esta misma API que trae la norma entera.
	EnNotarum string `json:"en_notarum"`
}

func vistasNacionales(normas []infoleg.EnIndice) []vistaNacional {
	v := make([]vistaNacional, 0, len(normas))
	for _, n := range normas {
		id := int(n.ID)
		x := vistaNacional{
			ID: id, Tipo: n.Tipo, Numero: n.Numero, Titulo: n.Titulo,
			Fecha: n.Fecha, TieneTexto: n.TieneTexto,
			Ficha:     infoleg.URLFicha(id),
			EnNotarum: "/v1/nacional/" + strconv.Itoa(id),
		}
		if n.TieneTexto {
			x.Texto = infoleg.URLTexto(id)
		}
		v = append(v, x)
	}
	return v
}

// verNormaNacional trae una norma entera, con lo que el catálogo guardó de
// ella: el índice tiene sólo lo justo para encontrarla.
func (s *Servidor) verNormaNacional(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido,
			"el identificador tiene que ser un número",
			"los de InfoLEG son números, como 24240")
		return
	}
	n := s.srv.NormaGuardada(id)
	if n == nil {
		escribirError(w, r, http.StatusNotFound, OrigenPedido,
			"no hay ninguna norma con ese identificador en el catálogo guardado",
			"puede que el catálogo de InfoLEG no esté sincronizado en esta instancia")
		return
	}
	escribirJSON(w, r, http.StatusOK, struct {
		*infoleg.Norma
		Ficha string `json:"ficha"`
		Texto string `json:"texto,omitempty"`
	}{n, n.URLFicha(), n.URLTexto()}, cacheProvincial)
}

// Las relaciones entre normas.
//
// El catálogo trae "modificada por 7" y nada más, que es un dato que no lleva
// a ningún lado: hay que ir a buscar cuáles a otro lado igual. Estas dos rutas
// dan la lista, con los datos de cada norma al lado para no tener que pedirlas
// de a una.

// verModificadaPor: qué normas modificaron a ésta.
func (s *Servidor) verModificadaPor(w http.ResponseWriter, r *http.Request) {
	s.verRelaciones(w, r, "modificada_por", s.srv.ModificadaPor)
}

// verModificaA: a qué normas modificó ésta.
func (s *Servidor) verModificaA(w http.ResponseWriter, r *http.Request) {
	s.verRelaciones(w, r, "modifica_a", s.srv.ModificaA)
}

func (s *Servidor) verRelaciones(w http.ResponseWriter, r *http.Request, campo string, buscar func(int) []infoleg.Relacion) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido,
			"el identificador tiene que ser un número",
			"los de InfoLEG son números, como 24240")
		return
	}
	// Una lista vacía y una norma que no existe llevan a cosas distintas: la
	// primera es que no la modificó nadie, la segunda es que hay que revisar
	// el identificador o sincronizar el catálogo.
	if s.srv.NormaGuardada(id) == nil {
		escribirError(w, r, http.StatusNotFound, OrigenPedido,
			"no hay ninguna norma con ese identificador en el catálogo guardado",
			"puede que el catálogo de InfoLEG no esté sincronizado en esta instancia")
		return
	}

	rs := buscar(id)
	salida := map[string]any{
		"id":     id,
		"total":  len(rs),
		campo:    vistasRelacion(rs),
		"origen": "InfoLEG, bases complementarias de normas modificadas y modificatorias",
	}
	escribirJSON(w, r, http.StatusOK, salida, cacheProvincial)
}

// vistaRelacion es una norma relacionada con sus enlaces armados.
type vistaRelacion struct {
	infoleg.Relacion
	Ficha     string `json:"ficha"`
	EnNotarum string `json:"en_notarum"`
}

func vistasRelacion(rs []infoleg.Relacion) []vistaRelacion {
	v := make([]vistaRelacion, 0, len(rs))
	for _, r := range rs {
		v = append(v, vistaRelacion{
			Relacion:  r,
			Ficha:     infoleg.URLFicha(r.ID),
			EnNotarum: "/v1/nacional/" + strconv.Itoa(r.ID),
		})
	}
	return v
}
