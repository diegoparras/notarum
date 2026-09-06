// Package armador escribe la consulta a una ruta en el formato de cada
// herramienta, sin pedirle nada a nadie.
//
// Un nodo de n8n o una línea de curl no son cosas que haya que redactar: son
// una estructura fija con los datos de la ruta adentro, y notarum ya conoce
// todas sus rutas y todos sus parámetros por el contrato. Armarlos con una
// plantilla sale siempre válido, no cuesta nada, no necesita una clave de
// ningún proveedor y no depende de que un modelo acierte una forma.
//
// El asistente sigue teniendo sentido para lo que no se puede plantillar —un
// pedido en castellano que combina varias rutas, o una herramienta que acá no
// está— pero para esto, no.
package armador

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/diegoparras/notarum/internal/contrato"
)

// TokenDeEjemplo es lo que va donde iría el token de verdad.
const TokenDeEjemplo = "TU_TOKEN"

// Valores son los que se ponen en los parámetros: los que faltan salen del
// ejemplo del contrato, para que lo armado se pueda correr tal cual.
type Valores map[string]string

// URL arma la dirección completa de una ruta, con los parámetros del camino
// puestos y los de consulta en la query.
func URL(base string, r contrato.Ruta, v Valores) string {
	camino := r.Camino
	var query []string
	for _, p := range r.Parametros {
		valor := valorDe(p, v)
		if valor == "" {
			continue
		}
		if p.En == "path" {
			camino = strings.ReplaceAll(camino, "{"+p.Nombre+"}", valor)
			continue
		}
		query = append(query, p.Nombre+"="+valor)
	}
	url := strings.TrimRight(base, "/") + camino
	if len(query) > 0 {
		url += "?" + strings.Join(query, "&")
	}
	return url
}

// valorDe elige qué poner en un parámetro: lo que se pidió, o el ejemplo del
// contrato, o lo que trae por defecto. Los que no son obligatorios y no tienen
// nada quedan afuera: una query llena de parámetros vacíos no ayuda a nadie.
func valorDe(p contrato.Parametro, v Valores) string {
	if valor, hay := v[p.Nombre]; hay {
		return strings.TrimSpace(valor)
	}
	if p.Ejemplo != "" {
		return p.Ejemplo
	}
	// Un parámetro con opciones cerradas se llena con la primera: cualquiera
	// sirve de ejemplo, y una consulta con un hueco adentro no sirve para
	// nada. Vale igual para los del camino y para los de consulta.
	if len(p.Opciones) > 0 && (p.Obligatorio || p.En == "path") {
		return p.Opciones[0]
	}
	if p.PorDefecto != "" && (p.Obligatorio || p.En == "path") {
		return p.PorDefecto
	}
	if p.Obligatorio || p.En == "path" {
		// Sin nada con qué llenarlo, queda a la vista que hay que completarlo.
		return "TU_" + strings.ToUpper(p.Nombre)
	}
	return ""
}

// Curl escribe la línea de comando.
func Curl(base string, r contrato.Ruta, v Valores, conToken bool) string {
	var b strings.Builder
	b.WriteString("curl -s")
	if r.Metodo != "" && !strings.EqualFold(r.Metodo, "GET") {
		b.WriteString(" -X " + strings.ToUpper(r.Metodo))
	}
	if conToken {
		b.WriteString(` -H "Authorization: Bearer ` + TokenDeEjemplo + `"`)
	}
	// Entre comillas: las URLs llevan & y sin comillas el shell lo toma como
	// "mandá esto al fondo", que corta la dirección por la mitad.
	b.WriteString(` "` + URL(base, r, v) + `"`)
	return b.String()
}

// ------------------------------------------------------------------- n8n

// nodoN8N es un nodo HTTP Request tal como lo espera n8n al pegarlo.
type nodoN8N struct {
	Parametros parametrosN8N `json:"parameters"`
	Tipo       string        `json:"type"`
	Version    float64       `json:"typeVersion"`
	Posicion   [2]int        `json:"position"`
	ID         string        `json:"id"`
	Nombre     string        `json:"name"`
}

type parametrosN8N struct {
	URL          string    `json:"url"`
	Metodo       string    `json:"method,omitempty"`
	MandaQuery   bool      `json:"sendQuery,omitempty"`
	Query        *listaN8N `json:"queryParameters,omitempty"`
	MandaHeaders bool      `json:"sendHeaders,omitempty"`
	Headers      *listaN8N `json:"headerParameters,omitempty"`
	Opciones     struct{}  `json:"options"`
}

type listaN8N struct {
	Parametros []parN8N `json:"parameters"`
}

type parN8N struct {
	Nombre string `json:"name"`
	Valor  string `json:"value"`
}

// pegableN8N es lo que se pega en el lienzo de n8n: un nodo suelto no alcanza,
// tiene que venir envuelto en un flujo aunque sea de uno solo.
type pegableN8N struct {
	Nodos   []nodoN8N `json:"nodes"`
	Uniones struct{}  `json:"connections"`
	Fijados struct{}  `json:"pinData"`
}

// N8N escribe el nodo HTTP Request, listo para pegar en el lienzo.
func N8N(base string, r contrato.Ruta, v Valores, conToken bool) (string, error) {
	p := parametrosN8N{URL: soloElCamino(base, r, v)}
	if m := strings.ToUpper(r.Metodo); m != "" && m != "GET" {
		p.Metodo = m
	}

	// Los parámetros de consulta van en su propia lista y no pegados a la URL:
	// así se editan desde el nodo, que es para lo que sirve tenerlo en n8n.
	var query []parN8N
	for _, par := range r.Parametros {
		if par.En == "path" {
			continue
		}
		if valor := valorDe(par, v); valor != "" {
			query = append(query, parN8N{Nombre: par.Nombre, Valor: valor})
		}
	}
	if len(query) > 0 {
		p.MandaQuery = true
		p.Query = &listaN8N{Parametros: query}
	}
	if conToken {
		p.MandaHeaders = true
		p.Headers = &listaN8N{Parametros: []parN8N{
			{Nombre: "Authorization", Valor: "Bearer " + TokenDeEjemplo},
		}}
	}

	nodo := nodoN8N{
		Parametros: p,
		Tipo:       "n8n-nodes-base.httpRequest",
		Version:    4.2,
		Posicion:   [2]int{0, 0},
		ID:         identificador(r.Metodo + " " + r.Camino),
		Nombre:     nombreDelNodo(r),
	}
	crudo, err := json.MarshalIndent(pegableN8N{Nodos: []nodoN8N{nodo}}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(crudo), nil
}

// soloElCamino es la URL sin los parámetros de consulta: en n8n van aparte.
func soloElCamino(base string, r contrato.Ruta, v Valores) string {
	camino := r.Camino
	for _, p := range r.Parametros {
		if p.En != "path" {
			continue
		}
		if valor := valorDe(p, v); valor != "" {
			camino = strings.ReplaceAll(camino, "{"+p.Nombre+"}", valor)
		}
	}
	return strings.TrimRight(base, "/") + camino
}

// nombreDelNodo es lo que se lee en el lienzo.
func nombreDelNodo(r contrato.Ruta) string {
	if r.Resumen != "" {
		return "notarum · " + r.Resumen
	}
	return "notarum · " + r.Camino
}

// identificador arma un UUID a partir de la ruta.
//
// n8n sólo necesita que sea único dentro del flujo, pero sale del camino y no
// del azar para que armar dos veces la misma consulta dé exactamente el mismo
// texto: si cambia, uno se pregunta qué cambió.
func identificador(semilla string) string {
	h := sha256.Sum256([]byte("notarum/n8n/v1:" + semilla))
	var b [16]byte
	copy(b[:], h[:16])
	b[6] = (b[6] & 0x0f) | 0x40 // versión 4
	b[8] = (b[8] & 0x3f) | 0x80 // variante
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
