package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/saij"
	"github.com/diegoparras/notarum/internal/servicio"
)

// Qué apareció desde una fecha.
//
// Un programa que consulta todos los días no quiere el catálogo entero: quiere
// lo que cambió. Sin esto hay que bajar 428 mil normas para descubrir que
// ninguna es nueva, o escribir la comparación por fuera, que es lo que termina
// haciendo cada quien por su cuenta y de una forma distinta.

// tope es cuántas novedades se devuelven de una vez.
const topeDeNovedades = 500

func (s *Servidor) verNovedadesNacionales(w http.ResponseWriter, r *http.Request) {
	desde, ok := desdeDeLaConsulta(w, r)
	if !ok {
		return
	}
	n := s.srv.NovedadesDesde("nacional", desde)

	normas := make([]any, 0, min(len(n.IDs), topeDeNovedades))
	for _, id := range n.IDs {
		if len(normas) >= topeDeNovedades {
			break
		}
		numero, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		norma := s.srv.NormaGuardada(numero)
		if norma == nil {
			// Estaba cuando se anotó y ya no está: el catálogo la sacó. Se
			// informa igual, porque para quien sigue el catálogo eso también
			// es una novedad.
			normas = append(normas, map[string]any{"id": numero, "ya_no_esta": true})
			continue
		}
		normas = append(normas, struct {
			*infoleg.Norma
			Ficha     string `json:"ficha"`
			EnNotarum string `json:"en_notarum"`
		}{norma, norma.URLFicha(), "/v1/nacional/" + id})
	}
	escribirNovedades(w, r, n, normas)
}

func (s *Servidor) verNovedadesProvinciales(w http.ResponseWriter, r *http.Request) {
	desde, ok := desdeDeLaConsulta(w, r)
	if !ok {
		return
	}
	n := s.srv.NovedadesDesde("provincial", desde)

	normas := make([]any, 0, min(len(n.IDs), topeDeNovedades))
	for _, id := range n.IDs {
		if len(normas) >= topeDeNovedades {
			break
		}
		norma, hay := s.srv.NormaProvincial(id)
		if !hay {
			normas = append(normas, map[string]any{"id": id, "ya_no_esta": true})
			continue
		}
		normas = append(normas, struct {
			saij.Norma
			EnNotarum string `json:"en_notarum"`
		}{norma, "/v1/provincial/" + id})
	}
	escribirNovedades(w, r, n, normas)
}

// desdeDeLaConsulta lee la fecha desde la que se pregunta.
func desdeDeLaConsulta(w http.ResponseWriter, r *http.Request) (string, bool) {
	crudo := strings.TrimSpace(r.URL.Query().Get("desde"))
	if crudo == "" {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido,
			`falta "desde"`,
			"es la fecha desde la que se quiere lo nuevo, en AAAA-MM-DD; "+
				"lo habitual es la de la última consulta")
		return "", false
	}
	if _, err := time.Parse("2006-01-02", crudo); err != nil {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido,
			"la fecha no se entiende", "va en AAAA-MM-DD, como 2026-09-01")
		return "", false
	}
	return crudo, true
}

// escribirNovedades arma la respuesta, con lo que hace falta para volver a
// preguntar mañana sin perder nada.
func escribirNovedades(w http.ResponseWriter, r *http.Request, n servicio.Novedades, normas []any) {
	salida := struct {
		Desde         string `json:"desde"`
		Total         int    `json:"total"`
		Devueltas     int    `json:"devueltas"`
		HayMas        bool   `json:"hay_mas"`
		Completo      bool   `json:"completo"`
		RegistroDesde string `json:"registro_desde,omitempty"`
		Nota          string `json:"nota,omitempty"`
		Normas        []any  `json:"normas"`
	}{
		Desde: n.Desde, Total: n.Total, Devueltas: len(normas),
		HayMas:        len(normas) < n.Total,
		Completo:      n.Completo,
		RegistroDesde: n.RegistroDesde,
		Normas:        normas,
	}
	if !n.Completo {
		// Un programa que pregunta por antes de donde llega el registro tiene
		// que saber que la respuesta está incompleta, en vez de suponer que no
		// pasó nada. Esa suposición es un agujero que no se nota nunca.
		salida.Nota = "el registro de novedades no llega hasta esa fecha: " +
			"para no perderte nada, bajá el catálogo entero una vez y seguí desde ahí"
	}
	escribirJSON(w, r, http.StatusOK, salida, cacheProvincial)
}
