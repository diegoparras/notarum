package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/servicio"
)

// Las herramientas que faltaban: buscar en las tres fuentes de una vez, y
// preguntar qué apareció desde una fecha.
//
// Un modelo que ayuda con esto pregunta las dos cosas todo el tiempo —«¿qué
// hay sobre X?» y «¿qué salió esta semana?»— y sin herramienta tiene que
// elegir a ciegas en cuál de las tres fuentes mirar.

func herramientasDeBusqueda() []Herramienta {
	return []Herramienta{
		{
			Nombre: "buscar_todo",
			Titulo: "Buscar en las tres fuentes",
			Descripcion: "Busca lo mismo en el Boletín Oficial, en la normativa nacional de InfoLEG y en " +
				"la provincial de SAIJ, y devuelve todo junto con el origen de cada resultado. Es por " +
				"donde conviene empezar cuando no se sabe en cuál de las tres está lo que se busca. " +
				"Las fuentes que esta instancia no tenga encendidas aparecen en «sin_mirar» con el " +
				"motivo: una fuente apagada no es lo mismo que una sin resultados.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"texto":      map[string]any{"type": "string", "description": "Lo que se busca."},
					"tipo":       map[string]any{"type": "string", "description": "Tipo de norma en las dos de normativa; rubro en el Boletín."},
					"provincia":  map[string]any{"type": "string", "description": "Sólo aplica a la provincial: nombre, código INDEC o prefijo."},
					"seccion":    map[string]any{"type": "string", "enum": []string{"primera", "segunda", "tercera"}, "description": "Sólo aplica al Boletín."},
					"vigentes":   map[string]any{"type": "boolean", "description": "En la provincial, deja fuera lo derogado."},
					"por_fuente": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 10, "description": "Cuántos traer de cada una."},
				},
				"required": []string{"texto"},
			},
		},
		{
			Nombre: "novedades",
			Titulo: "Qué apareció desde una fecha",
			Descripcion: "Devuelve lo que apareció en un catálogo desde una fecha. «Nuevo» quiere decir " +
				"que notarum no lo había visto, y no que la norma sea reciente: el portal agrega normas " +
				"viejas todo el tiempo, y una de 1998 que aparece hoy es una novedad para quien sigue el " +
				"catálogo. La respuesta trae «completo»: si es false, el registro no llega hasta esa " +
				"fecha y lo que se devuelve está incompleto.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fuente": map[string]any{
						"type": "string", "enum": []string{"nacional", "provincial"},
						"description": "En qué catálogo mirar.",
					},
					"desde":  map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}$`, "description": "Desde cuándo, en AAAA-MM-DD."},
					"limite": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50, "description": "Cuántas traer."},
				},
				"required": []string{"fuente", "desde"},
			},
		},
	}
}

func (s *Servidor) hBuscarTodo(ctx context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Texto     string `json:"texto"`
		Tipo      string `json:"tipo"`
		Provincia string `json:"provincia"`
		Seccion   string `json:"seccion"`
		Vigentes  bool   `json:"vigentes"`
		PorFuente int    `json:"por_fuente"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	if strings.TrimSpace(a.Texto) == "" {
		return errorDeHerramienta(`falta "texto": es lo que se busca`)
	}
	if a.PorFuente <= 0 {
		a.PorFuente = 10
	}
	if a.PorFuente > 50 {
		a.PorFuente = 50
	}
	return comoJSON(s.srv.BuscarEnTodo(ctx, servicio.Criterios{
		Texto: strings.TrimSpace(a.Texto), Tipo: a.Tipo,
		Provincia: a.Provincia, Seccion: a.Seccion, SoloVigentes: a.Vigentes,
	}, a.PorFuente))
}

func (s *Servidor) hNovedades(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Fuente string `json:"fuente"`
		Desde  string `json:"desde"`
		Limite int    `json:"limite"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	if a.Fuente != "nacional" && a.Fuente != "provincial" {
		return errorDeHerramienta(`"fuente" tiene que ser nacional o provincial`)
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(a.Desde)); err != nil {
		return errorDeHerramienta(`"desde" va en AAAA-MM-DD, como 2026-09-01`)
	}
	if a.Limite <= 0 {
		a.Limite = 50
	}
	if a.Limite > 200 {
		a.Limite = 200
	}

	n := s.srv.NovedadesDesde(a.Fuente, strings.TrimSpace(a.Desde))
	ids := n.IDs
	if len(ids) > a.Limite {
		ids = ids[:a.Limite]
	}
	return comoJSON(struct {
		Fuente        string   `json:"fuente"`
		Desde         string   `json:"desde"`
		Total         int      `json:"total"`
		Devueltas     int      `json:"devueltas"`
		Completo      bool     `json:"completo"`
		RegistroDesde string   `json:"registro_desde,omitempty"`
		Nota          string   `json:"nota,omitempty"`
		IDs           []string `json:"ids"`
	}{
		Fuente: a.Fuente, Desde: n.Desde, Total: n.Total, Devueltas: len(ids),
		Completo: n.Completo, RegistroDesde: n.RegistroDesde, IDs: ids,
		Nota: notaDeNovedades(n),
	})
}

func notaDeNovedades(n servicio.Novedades) string {
	if n.Completo {
		return ""
	}
	return "el registro no llega hasta esa fecha: lo que falta hay que traerlo del catálogo entero"
}
