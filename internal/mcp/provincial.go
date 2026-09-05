package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/diegoparras/notarum/internal/saij"
)

// Las herramientas de normativa provincial. El Boletín Oficial de la Nación
// no publica las leyes de las provincias, así que sin esto un modelo que
// pregunta por la ley de educación de Mendoza no tiene dónde mirar.

// herramientasProvinciales se suman a las del Boletín. Están aparte porque
// responden otra pregunta: no hay ediciones ni días, hay un catálogo.
func herramientasProvinciales() []Herramienta {
	provincia := map[string]any{
		"type":        "string",
		"description": "Nombre de la provincia, o su código INDEC. Sin esto busca en las 24 jurisdicciones.",
	}
	return []Herramienta{
		{
			Nombre: "provincial_buscar",
			Titulo: "Buscar normativa provincial",
			Descripcion: "Busca leyes, decretos, códigos y constituciones de las 24 provincias, " +
				"que el Boletín Oficial de la Nación no publica. Es la Base SAIJ del Ministerio " +
				"de Justicia: 81 mil normas desde 1855. Devuelve los datos de cada norma y el " +
				"enlace a su ficha; el texto completo casi nunca está publicado, así que no lo " +
				"esperes de acá.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"texto": map[string]any{
						"type":        "string",
						"description": "Palabras a buscar en el título y las materias. No hace falta poner los acentos. Todas las palabras tienen que estar.",
					},
					"provincia": provincia,
					"tipo": map[string]any{
						"type":        "string",
						"description": `Tipo de norma: "Ley", "Decreto Ley", "Constitución Provincial", "Código Procesal Penal"… La lista está en provincial_tipos. Para encontrar la constitución de una provincia conviene filtrar por tipo en vez de buscarla por texto.`,
					},
					"desde":    map[string]any{"type": "integer", "description": "Año de sanción más viejo."},
					"hasta":    map[string]any{"type": "integer", "description": "Año de sanción más nuevo."},
					"vigentes": map[string]any{"type": "boolean", "description": "Deja fuera lo derogado, lo caduco y las modificatorias.", "default": false},
					"pagina":   map[string]any{"type": "integer", "minimum": 1, "default": 1},
					"limite":   map[string]any{"type": "integer", "minimum": 1, "maximum": saij.LimiteMaximo, "default": saij.LimitePorDefecto},
				},
			},
		},
		{
			Nombre:      "provincial_norma",
			Titulo:      "Una norma provincial",
			Descripcion: "Trae una norma provincial por su identificador de SAIJ, del estilo LPB1000000. Los identificadores salen de provincial_buscar.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Identificador de SAIJ, como LPB1000000."},
				},
				"required": []string{"id"},
			},
		},
		{
			Nombre: "provincial_tipos",
			Titulo: "Provincias y tipos de norma",
			Descripcion: "Devuelve las 24 jurisdicciones con cuántas normas hay de cada una, y los tipos " +
				"de norma que existen. Conviene mirarlo antes de filtrar, para usar los valores " +
				"tal como están escritos.",
			Esquema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func (s *Servidor) hProvincialBuscar(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Texto     string `json:"texto"`
		Provincia string `json:"provincia"`
		Tipo      string `json:"tipo"`
		Desde     int    `json:"desde"`
		Hasta     int    `json:"hasta"`
		Vigentes  bool   `json:"vigentes"`
		Pagina    int    `json:"pagina"`
		Limite    int    `json:"limite"`
	}
	if len(crudo) > 0 {
		if err := json.Unmarshal(crudo, &a); err != nil {
			return errorDeHerramienta("no se entendieron los argumentos: " + err.Error())
		}
	}
	if !s.srv.CatalogoProvincialCargado() {
		return errorDeHerramienta(sinCatalogoProvincial)
	}
	// Una provincia mal escrita devolvería cero sin explicar por qué; un
	// modelo necesita saber que el problema es el nombre y no el tema.
	if a.Provincia != "" {
		if _, hay := saij.BuscarProvincia(a.Provincia); !hay {
			return errorDeHerramienta("no se reconoce la provincia " + a.Provincia +
				". Mirá provincial_tipos para ver cómo se escriben.")
		}
	}
	if a.Pagina < 1 {
		a.Pagina = 1
	}
	if a.Limite <= 0 {
		a.Limite = saij.LimitePorDefecto
	}

	res := s.srv.BuscarProvincial(saij.Consulta{
		Texto: a.Texto, Provincia: a.Provincia, Tipo: a.Tipo,
		Desde: a.Desde, Hasta: a.Hasta, SoloVigentes: a.Vigentes,
		Limite: a.Limite, Desplazamiento: (a.Pagina - 1) * a.Limite,
	})

	// Se devuelve una vista y no la norma cruda: el modelo necesita el enlace
	// armado y la lista de materias, no los campos como vienen del CSV.
	return comoJSON(struct {
		Total  int              `json:"total"`
		Pagina int              `json:"pagina"`
		HayMas bool             `json:"hay_mas"`
		Normas []vistaNormaProv `json:"normas"`
	}{res.Total, a.Pagina, res.Truncado, vistasDe(res.Normas)})
}

func (s *Servidor) hProvincialNorma(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	if strings.TrimSpace(a.ID) == "" {
		return errorDeHerramienta(`falta "id": es el identificador de SAIJ, como LPB1000000`)
	}
	if !s.srv.CatalogoProvincialCargado() {
		return errorDeHerramienta(sinCatalogoProvincial)
	}
	n, hay := s.srv.NormaProvincial(a.ID)
	if !hay {
		return errorDeHerramienta("no hay ninguna norma provincial con el identificador " + a.ID +
			". Buscala con provincial_buscar.")
	}
	return comoJSON(vistaDe(n))
}

func (s *Servidor) hProvincialTipos(context.Context) *ResultadoHerramienta {
	if !s.srv.CatalogoProvincialCargado() {
		return errorDeHerramienta(sinCatalogoProvincial)
	}
	return comoJSON(struct {
		Provincias any `json:"provincias"`
		Tipos      any `json:"tipos"`
	}{s.srv.ProvinciasConNormas(), s.srv.TiposProvinciales()})
}

const sinCatalogoProvincial = "esta instancia todavía no bajó la normativa provincial; " +
	"quien la opera tiene que correr `notarum provincial` una vez"

// vistaNormaProv es la norma como le sirve a un modelo: con el enlace armado
// y las materias separadas.
type vistaNormaProv struct {
	ID         string `json:"id"`
	Provincia  string `json:"provincia"`
	Tipo       string `json:"tipo"`
	Numero     string `json:"numero,omitempty"`
	Titulo     string `json:"titulo"`
	Estado     string `json:"estado,omitempty"`
	Vigente    bool   `json:"vigente"`
	Sancionada string `json:"sancionada,omitempty"`
	Publicada  string `json:"publicada,omitempty"`
	// Materias son los términos con los que SAIJ clasifica la norma. A veces
	// vienen separados y a veces todos juntos en un solo elemento: la fuente
	// no es pareja y no hay forma de partirlos sin inventar.
	Materias []string `json:"materias,omitempty"`
	Ficha    string   `json:"ficha"`
}

func vistaDe(n saij.Norma) vistaNormaProv {
	return vistaNormaProv{
		ID: n.ID, Provincia: n.Provincia, Tipo: n.Tipo, Numero: n.Numero,
		Titulo: n.Titulo(), Estado: n.Estado, Vigente: n.Vigente(),
		Sancionada: n.Fecha, Publicada: n.FechaPublicacio,
		Materias: n.Materias(), Ficha: n.URLFicha(),
	}
}

func vistasDe(normas []saij.Norma) []vistaNormaProv {
	v := make([]vistaNormaProv, 0, len(normas))
	for _, n := range normas {
		v = append(v, vistaDe(n))
	}
	return v
}
