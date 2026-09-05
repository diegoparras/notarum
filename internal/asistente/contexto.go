package asistente

import (
	"fmt"
	"strings"

	"github.com/diegoparras/notarum/internal/contrato"
	"github.com/diegoparras/notarum/internal/mcp"
)

// El contexto que se le da al modelo.
//
// Sale del contrato OpenAPI y de la lista de herramientas del MCP, que son
// las mismas fuentes de las que se dibuja /docs. Así no hay una segunda
// descripción de la API que se pueda desactualizar: si mañana se agrega una
// ruta, el modelo se entera solo.

// Instrucciones arma lo que el modelo tiene que saber para responder.
func Instrucciones(base string) (string, error) {
	doc, err := contrato.Leer()
	if err != nil {
		return "", err
	}
	var b strings.Builder

	b.WriteString(`Sos parte de notarum, un servicio que expone el Boletín Oficial de la
República Argentina y la normativa provincial. Tu trabajo es escribir la
consulta que la persona pide, en el lenguaje o la herramienta que pida.

REGLAS
- Devolvé el código y nada más: sin explicación previa, sin "acá tenés".
  Podés poner comentarios adentro del código si aclaran algo.
- Usá exactamente las rutas y los parámetros de abajo. No inventes rutas,
  parámetros ni campos: si algo no está, decilo en una línea y ofrecé lo más
  parecido que sí exista.
- La dirección de esta instancia es ` + base + `. Usala en los ejemplos.
- Las fechas van en AAAA-MM-DD.
- Si la persona no dice el lenguaje, usá curl.
- Para n8n devolvé el JSON del nodo, listo para pegar en el editor.
- Si hace falta un token, mostralo como TU_TOKEN y aclará en un comentario que
  se crea en ` + base + `/cuenta.
- Escribí en castellano rioplatense, sin "tú" ni "vosotros".

LAS RUTAS DE LA API
`)

	for _, g := range doc.Grupos {
		fmt.Fprintf(&b, "\n## %s\n", g.Nombre)
		if g.Descripcion != "" {
			fmt.Fprintf(&b, "%s\n", g.Descripcion)
		}
		for _, r := range g.Rutas {
			fmt.Fprintf(&b, "\n%s %s — %s\n", r.Metodo, r.Camino, r.Resumen)
			if r.Descripcion != "" {
				fmt.Fprintf(&b, "  %s\n", unaLinea(r.Descripcion))
			}
			for _, p := range r.Parametros {
				obligatorio := ""
				if p.Obligatorio {
					obligatorio = ", obligatorio"
				}
				fmt.Fprintf(&b, "  - %s (%s%s): %s\n", p.Nombre, p.En, obligatorio, unaLinea(p.Descripcion))
			}
		}
	}

	b.WriteString("\n\nLAS HERRAMIENTAS DEL MCP\n")
	b.WriteString("El MCP está en " + base + "/mcp y habla JSON-RPC 2.0.\n")
	for _, h := range mcp.Herramientas() {
		fmt.Fprintf(&b, "\n%s — %s\n", h.Nombre, unaLinea(h.Descripcion))
		for _, arg := range argumentosDe(h) {
			fmt.Fprintf(&b, "  - %s\n", arg)
		}
	}

	b.WriteString(`

CÓMO SE VE UNA RESPUESTA CON ERROR
{"error": "...", "detalle": "...", "origen": "pedido|sitio|notarum"}

El origen dice de quién es el problema: "pedido" es que está mal armado,
"sitio" es que el Boletín Oficial no contestó, y "notarum" es una falla
propia.
`)
	return b.String(), nil
}

// argumentosDe saca los argumentos de una herramienta de su esquema, para no
// mandarle al modelo el JSON Schema entero.
func argumentosDe(h mcp.Herramienta) []string {
	esquema, _ := h.Esquema.(map[string]any)
	props, _ := esquema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	obligatorios := map[string]bool{}
	if req, ok := esquema["required"].([]string); ok {
		for _, r := range req {
			obligatorios[r] = true
		}
	}
	var salida []string
	for nombre, crudo := range props {
		p, _ := crudo.(map[string]any)
		tipo, _ := p["type"].(string)
		desc, _ := p["description"].(string)
		linea := nombre
		if tipo != "" {
			linea += " (" + tipo
			if obligatorios[nombre] {
				linea += ", obligatorio"
			}
			linea += ")"
		}
		if desc != "" {
			linea += ": " + unaLinea(desc)
		}
		salida = append(salida, linea)
	}
	return salida
}

// unaLinea aplana un texto: el contexto se arma con renglones y un salto de
// más lo desordena.
func unaLinea(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
