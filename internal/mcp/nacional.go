package mcp

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/diegoparras/notarum/internal/infoleg"
)

// Las herramientas de normativa nacional.
//
// Sin esto, un modelo que busca "la ley de defensa del consumidor" tenía que
// adivinar en qué edición del Boletín salió y pedirla por fecha. Acá busca en
// las 428 mil normas de InfoLEG por título y materias, sin saber la fecha.

func herramientasNacionales() []Herramienta {
	return []Herramienta{
		{
			Nombre: "nacional_buscar",
			Titulo: "Buscar normativa nacional",
			Descripcion: "Busca leyes, decretos, resoluciones y disposiciones nacionales en la base " +
				"de InfoLEG: 428 mil normas. Es la forma de encontrar una norma sin saber en qué " +
				"día salió; para eso, la herramienta buscar recorre las ediciones del Boletín día " +
				"por día. Devuelve los datos de cada norma y los enlaces a su ficha y a su texto.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"texto": map[string]any{
						"type":        "string",
						"description": "Palabras a buscar en el título y las materias. No hace falta poner los acentos; todas tienen que estar. Para una norma puntual sirve poner el tipo y el número: \"ley 24240\".",
					},
					"tipo": map[string]any{
						"type":        "string",
						"description": "Ley, Decreto, Resolución, Disposición, Decisión Administrativa… La lista está en nacional_tipos.",
					},
					"desde":     map[string]any{"type": "integer", "description": "Año de sanción más viejo."},
					"hasta":     map[string]any{"type": "integer", "description": "Año de sanción más nuevo."},
					"con_texto": map[string]any{"type": "boolean", "description": "Deja sólo las que tienen el texto publicado en InfoLEG.", "default": false},
					"pagina":    map[string]any{"type": "integer", "minimum": 1, "default": 1},
					"limite":    map[string]any{"type": "integer", "minimum": 1, "maximum": infoleg.LimiteMaximo, "default": infoleg.LimitePorDefecto},
				},
			},
		},
		{
			Nombre:      "nacional_norma",
			Titulo:      "Una norma nacional",
			Descripcion: "Trae una norma nacional entera por su identificador de InfoLEG, con sus fechas, su organismo, cuántas normas la modificaron y los enlaces a su ficha y su texto.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "Identificador de InfoLEG, un número. Sale de nacional_buscar."},
				},
				"required": []string{"id"},
			},
		},
		{
			Nombre:      "nacional_tipos",
			Titulo:      "Tipos de norma nacional",
			Descripcion: "Devuelve los tipos de norma que trae el catálogo de InfoLEG, del más frecuente al menos. Conviene mirarlo antes de filtrar, para usar los valores tal como están escritos.",
			Esquema:     map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

const sinBuscadorNacional = "esta instancia no tiene el buscador de normativa nacional; " +
	"se enciende con NOTARUM_BUSCADOR_INFOLEG=1"

const sinCatalogoNacional = "el catálogo de InfoLEG todavía no se bajó en esta instancia; " +
	"quien la opera tiene que sincronizarlo"

func (s *Servidor) hNacionalBuscar(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Texto    string `json:"texto"`
		Tipo     string `json:"tipo"`
		Desde    int    `json:"desde"`
		Hasta    int    `json:"hasta"`
		ConTexto bool   `json:"con_texto"`
		Pagina   int    `json:"pagina"`
		Limite   int    `json:"limite"`
	}
	if len(crudo) > 0 {
		if err := json.Unmarshal(crudo, &a); err != nil {
			return errorDeHerramienta("no se entendieron los argumentos: " + err.Error())
		}
	}
	if !s.srv.BuscadorInfoLEGActivo() {
		return errorDeHerramienta(sinBuscadorNacional)
	}
	if !s.srv.CatalogoNacionalCargado() {
		return errorDeHerramienta(sinCatalogoNacional)
	}
	if a.Pagina < 1 {
		a.Pagina = 1
	}
	if a.Limite <= 0 {
		a.Limite = infoleg.LimitePorDefecto
	}

	res := s.srv.BuscarNacional(infoleg.Consulta{
		Texto: a.Texto, Tipo: a.Tipo, Desde: a.Desde, Hasta: a.Hasta,
		SoloConTexto: a.ConTexto,
		Limite:       a.Limite, Desplazamiento: (a.Pagina - 1) * a.Limite,
	})
	return comoJSON(struct {
		Total  int             `json:"total"`
		Pagina int             `json:"pagina"`
		HayMas bool            `json:"hay_mas"`
		Normas []vistaNormaNac `json:"normas"`
	}{res.Total, a.Pagina, res.Truncado, vistasNac(res.Normas)})
}

func (s *Servidor) hNacionalNorma(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	if a.ID <= 0 {
		return errorDeHerramienta(`falta "id": es el identificador de InfoLEG, un número`)
	}
	n := s.srv.NormaGuardada(a.ID)
	if n == nil {
		return errorDeHerramienta("no hay ninguna norma con el identificador " +
			strconv.Itoa(a.ID) + " en el catálogo guardado. Buscala con nacional_buscar.")
	}
	return comoJSON(struct {
		*infoleg.Norma
		Ficha string `json:"ficha"`
		Texto string `json:"texto,omitempty"`
	}{n, n.URLFicha(), n.URLTexto()})
}

func (s *Servidor) hNacionalTipos(context.Context) *ResultadoHerramienta {
	if !s.srv.BuscadorInfoLEGActivo() {
		return errorDeHerramienta(sinBuscadorNacional)
	}
	if !s.srv.CatalogoNacionalCargado() {
		return errorDeHerramienta(sinCatalogoNacional)
	}
	return comoJSON(s.srv.TiposNacionales())
}

// vistaNormaNac es la norma como le sirve a un modelo: con los enlaces
// armados y sin los campos internos del índice.
type vistaNormaNac struct {
	ID         int    `json:"id"`
	Tipo       string `json:"tipo"`
	Numero     string `json:"numero,omitempty"`
	Titulo     string `json:"titulo,omitempty"`
	Sancionada string `json:"sancionada,omitempty"`
	TieneTexto bool   `json:"tiene_texto"`
	Ficha      string `json:"ficha"`
	Texto      string `json:"texto,omitempty"`
}

func vistasNac(normas []infoleg.EnIndice) []vistaNormaNac {
	v := make([]vistaNormaNac, 0, len(normas))
	for _, n := range normas {
		id := int(n.ID)
		x := vistaNormaNac{
			ID: id, Tipo: n.Tipo, Numero: n.Numero, Titulo: n.Titulo,
			Sancionada: n.Fecha, TieneTexto: n.TieneTexto,
			Ficha: infoleg.URLFicha(id),
		}
		if n.TieneTexto {
			x.Texto = infoleg.URLTexto(id)
		}
		v = append(v, x)
	}
	return v
}
