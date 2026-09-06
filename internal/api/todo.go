package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/diegoparras/notarum/internal/servicio"
)

// Buscar en las tres fuentes de una vez.
//
// Hoy hay que saber de antemano si lo que se busca está en el Boletín, en
// InfoLEG o en la base provincial, y hacer tres consultas distintas. Eso es
// pedirle a quien pregunta que conozca cómo está organizado el Estado antes de
// poder buscar, que es exactamente al revés de para qué sirve esto.

// porFuentePorDefecto es cuántos resultados trae cada fuente.
const (
	porFuentePorDefecto = 10
	porFuenteMaximo     = 50
)

func (s *Servidor) buscarEnTodo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	texto := strings.TrimSpace(q.Get("texto"))
	if texto == "" {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido,
			`falta "texto"`, "es lo que se busca; sin eso no hay nada que buscar")
		return
	}

	porFuente := porFuentePorDefecto
	if v := q.Get("por_fuente"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > porFuenteMaximo {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido,
				"por_fuente tiene que ser un número entre 1 y "+strconv.Itoa(porFuenteMaximo), "")
			return
		}
		porFuente = n
	}

	res := s.srv.BuscarEnTodo(r.Context(), servicio.Criterios{
		Texto:        texto,
		Tipo:         strings.TrimSpace(q.Get("tipo")),
		Provincia:    strings.TrimSpace(q.Get("provincia")),
		Seccion:      strings.TrimSpace(q.Get("seccion")),
		SoloVigentes: q.Get("vigentes") != "",
	}, porFuente)

	escribirJSON(w, r, http.StatusOK, res, cacheProvincial)
}
