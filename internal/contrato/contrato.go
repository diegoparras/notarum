// Package contrato guarda el contrato OpenAPI de la API y lo deja leer como
// estructura, para que la documentación que ve una persona y la que consume un
// programa salgan del mismo lugar.
//
// Vive aparte de la API porque lo usan los dos lados: la API lo sirve tal cual
// en /v1/openapi.json, y el lector lo dibuja como página. Si estuviera adentro
// de uno de los dos, el otro no podría leerlo sin un ciclo de imports.
package contrato

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed openapi.json
var crudo []byte

// JSON devuelve el contrato tal como se publica.
func JSON() []byte { return crudo }

// Documento es el contrato en la forma en que conviene dibujarlo: agrupado por
// tema y con las rutas en un orden estable.
type Documento struct {
	Titulo      string
	Version     string
	Descripcion string
	Grupos      []Grupo
	Esquemas    []Esquema
}

// Grupo junta las rutas de un mismo tema, como los declara el contrato.
type Grupo struct {
	Nombre      string
	Descripcion string
	Rutas       []Ruta
}

// Ruta es una operación de la API.
type Ruta struct {
	Metodo      string
	Camino      string
	Resumen     string
	Descripcion string
	Parametros  []Parametro
	Respuestas  []Respuesta
	Ejemplo     string // el camino con sus valores de ejemplo puestos
}

type Parametro struct {
	Nombre      string
	En          string // path o query
	Obligatorio bool
	Tipo        string
	Opciones    []string
	PorDefecto  string
	Descripcion string
	Ejemplo     string
}

type Respuesta struct {
	Codigo      string
	Descripcion string
}

// Esquema es una de las formas que devuelve la API.
type Esquema struct {
	Nombre      string
	Descripcion string
	Campos      []Campo
}

type Campo struct {
	Nombre      string
	Tipo        string
	Obligatorio bool
	Descripcion string
	Opciones    []string
}

// Leer arma el documento a partir del contrato embebido.
func Leer() (*Documento, error) {
	var oa estructuraOpenAPI
	if err := json.Unmarshal(crudo, &oa); err != nil {
		return nil, err
	}

	d := &Documento{
		Titulo:      oa.Info.Title,
		Version:     oa.Info.Version,
		Descripcion: oa.Info.Description,
	}

	// Los grupos salen de los tags del contrato, en el orden en que están
	// declarados: es el orden en que conviene leerlos.
	//
	// Se arman como punteros sueltos y recién al final se vuelcan al slice: si
	// se guardaran punteros a elementos del slice, el siguiente append podría
	// realocarlo y dejarlos apuntando al array viejo.
	orden := make([]*Grupo, 0, len(oa.Tags))
	porNombre := map[string]*Grupo{}
	for _, t := range oa.Tags {
		g := &Grupo{Nombre: t.Name, Descripcion: t.Description}
		orden = append(orden, g)
		porNombre[t.Name] = g
	}
	otros := &Grupo{Nombre: "otras"}

	caminos := make([]string, 0, len(oa.Paths))
	for c := range oa.Paths {
		caminos = append(caminos, c)
	}
	sort.Strings(caminos)

	for _, camino := range caminos {
		for metodo, op := range oa.Paths[camino] {
			r := Ruta{
				Metodo:      strings.ToUpper(metodo),
				Camino:      camino,
				Resumen:     op.Summary,
				Descripcion: op.Description,
			}
			for _, p := range op.Parameters {
				r.Parametros = append(r.Parametros, aParametro(p, oa))
			}
			for codigo, resp := range op.Responses {
				r.Respuestas = append(r.Respuestas, Respuesta{Codigo: codigo, Descripcion: resp.Description})
			}
			sort.Slice(r.Respuestas, func(i, j int) bool { return r.Respuestas[i].Codigo < r.Respuestas[j].Codigo })
			r.Ejemplo = armarEjemplo(camino, r.Parametros)

			destino := otros
			if len(op.Tags) > 0 {
				if g, hay := porNombre[op.Tags[0]]; hay {
					destino = g
				}
			}
			destino.Rutas = append(destino.Rutas, r)
		}
	}
	if len(otros.Rutas) > 0 {
		orden = append(orden, otros)
	}
	for _, g := range orden {
		sort.Slice(g.Rutas, func(a, b int) bool { return g.Rutas[a].Camino < g.Rutas[b].Camino })
		d.Grupos = append(d.Grupos, *g)
	}

	// Los esquemas, en el orden en que se entienden: primero lo que se lee
	// seguido, después el resto en orden alfabético.
	nombres := make([]string, 0, len(oa.Components.Schemas))
	for n := range oa.Components.Schemas {
		nombres = append(nombres, n)
	}
	sort.Strings(nombres)
	for _, n := range nombres {
		d.Esquemas = append(d.Esquemas, aEsquema(n, oa.Components.Schemas[n], oa.Components.Schemas))
	}
	return d, nil
}

// armarEjemplo reemplaza los {parametros} del camino por sus ejemplos, para
// que se pueda copiar y pegar.
func armarEjemplo(camino string, params []Parametro) string {
	ejemplo := camino
	var query []string
	for _, p := range params {
		valor := p.Ejemplo
		if valor == "" && len(p.Opciones) > 0 {
			valor = p.Opciones[0]
		}
		if valor == "" {
			continue
		}
		switch p.En {
		case "path":
			ejemplo = strings.ReplaceAll(ejemplo, "{"+p.Nombre+"}", valor)
		case "query":
			if p.Obligatorio {
				query = append(query, p.Nombre+"="+valor)
			}
		}
	}
	if len(query) > 0 {
		ejemplo += "?" + strings.Join(query, "&")
	}
	if strings.Contains(ejemplo, "{") {
		return "" // quedó algún hueco: mejor no mostrar un ejemplo a medias
	}
	return ejemplo
}

func aParametro(p parametroOpenAPI, oa estructuraOpenAPI) Parametro {
	if p.Ref != "" {
		if resuelto, hay := oa.Components.Parameters[nombreDeRef(p.Ref)]; hay {
			resuelto.Ref = ""
			p = resuelto
		}
	}
	out := Parametro{
		Nombre:      p.Name,
		En:          p.In,
		Obligatorio: p.Required,
		Descripcion: p.Description,
		Tipo:        p.Schema.Type,
	}
	for _, e := range p.Schema.Enum {
		out.Opciones = append(out.Opciones, textoDe(e))
	}
	if p.Schema.Default != nil {
		out.PorDefecto = textoDe(p.Schema.Default)
	}
	if p.Example != nil {
		out.Ejemplo = textoDe(p.Example)
	}
	if p.Schema.Format != "" && out.Tipo != "" {
		out.Tipo += " (" + p.Schema.Format + ")"
	}
	return out
}

func aEsquema(nombre string, e esquemaOpenAPI, todos map[string]esquemaOpenAPI) Esquema {
	out := Esquema{Nombre: nombre, Descripcion: e.Description}
	// Un esquema con allOf hereda los campos de los que compone, y esas partes
	// suelen ser referencias: hay que resolverlas o el detalle de un aviso
	// aparece con tres campos en vez de con todos.
	partes := []esquemaOpenAPI{e}
	for _, parte := range e.AllOf {
		if parte.Ref != "" {
			if resuelto, hay := todos[nombreDeRef(parte.Ref)]; hay {
				parte = resuelto
			}
		}
		partes = append(partes, parte)
	}
	obligatorios := map[string]bool{}
	for _, p := range partes {
		for _, r := range p.Required {
			obligatorios[r] = true
		}
	}
	vistos := map[string]bool{}
	for _, p := range partes {
		campos := make([]string, 0, len(p.Properties))
		for n := range p.Properties {
			campos = append(campos, n)
		}
		sort.Strings(campos)
		for _, n := range campos {
			if vistos[n] {
				continue
			}
			vistos[n] = true
			prop := p.Properties[n]
			c := Campo{
				Nombre:      n,
				Tipo:        tipoDe(prop),
				Obligatorio: obligatorios[n],
				Descripcion: prop.Description,
			}
			for _, v := range prop.Enum {
				c.Opciones = append(c.Opciones, textoDe(v))
			}
			out.Campos = append(out.Campos, c)
		}
	}
	return out
}

func tipoDe(e esquemaOpenAPI) string {
	switch {
	case e.Ref != "":
		return nombreDeRef(e.Ref)
	case e.Type == "array" && e.Items != nil:
		return "lista de " + tipoDe(*e.Items)
	case e.Type == "":
		return "objeto"
	case e.Format != "":
		return e.Type + " (" + e.Format + ")"
	}
	return e.Type
}

func nombreDeRef(ref string) string {
	i := strings.LastIndex(ref, "/")
	if i < 0 {
		return ref
	}
	return ref[i+1:]
}

func textoDe(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == float64(int64(x)) {
			return strings.TrimSuffix(strings.TrimRight(formatoFloat(x), "0"), ".")
		}
		return formatoFloat(x)
	case nil:
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func formatoFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// ------------------------------------------ la forma cruda del archivo

type estructuraOpenAPI struct {
	Info struct {
		Title       string `json:"title"`
		Version     string `json:"version"`
		Description string `json:"description"`
	} `json:"info"`
	Tags []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"tags"`
	Paths      map[string]map[string]operacionOpenAPI `json:"paths"`
	Components struct {
		Parameters map[string]parametroOpenAPI `json:"parameters"`
		Schemas    map[string]esquemaOpenAPI   `json:"schemas"`
	} `json:"components"`
}

type operacionOpenAPI struct {
	Tags        []string           `json:"tags"`
	Summary     string             `json:"summary"`
	Description string             `json:"description"`
	Parameters  []parametroOpenAPI `json:"parameters"`
	Responses   map[string]struct {
		Description string `json:"description"`
	} `json:"responses"`
}

type parametroOpenAPI struct {
	Ref         string         `json:"$ref"`
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Required    bool           `json:"required"`
	Description string         `json:"description"`
	Example     any            `json:"example"`
	Schema      esquemaOpenAPI `json:"schema"`
}

type esquemaOpenAPI struct {
	Ref         string                    `json:"$ref"`
	Type        string                    `json:"type"`
	Format      string                    `json:"format"`
	Description string                    `json:"description"`
	Enum        []any                     `json:"enum"`
	Default     any                       `json:"default"`
	Required    []string                  `json:"required"`
	Properties  map[string]esquemaOpenAPI `json:"properties"`
	Items       *esquemaOpenAPI           `json:"items"`
	AllOf       []esquemaOpenAPI          `json:"allOf"`
}
