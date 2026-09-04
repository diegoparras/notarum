// Package mcp expone el Boletín Oficial como herramientas MCP, para que un
// modelo pueda consultarlo igual que lo haría una persona con la API.
//
// Habla JSON-RPC 2.0 por dos vías: por entrada y salida estándar (para un
// cliente local como Claude Desktop) y por HTTP (para la instancia desplegada).
package mcp

import "encoding/json"

// VersionProtocolo es la revisión de MCP que este servidor habla.
const VersionProtocolo = "2024-11-05"

// Pedido es un mensaje JSON-RPC entrante. Sin id es una notificación y no
// lleva respuesta.
type Pedido struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Metodo  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (p *Pedido) esNotificacion() bool { return len(p.ID) == 0 }

// Respuesta es un mensaje JSON-RPC saliente.
type Respuesta struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *ErrorRPC       `json:"error,omitempty"`
}

// ErrorRPC es un error del protocolo: el pedido no se pudo procesar.
type ErrorRPC struct {
	Codigo  int    `json:"code"`
	Mensaje string `json:"message"`
	Datos   any    `json:"data,omitempty"`
}

// Códigos estándar de JSON-RPC.
const (
	CodigoParseo         = -32700
	CodigoPedidoInvalido = -32600
	CodigoMetodoNoExiste = -32601
	CodigoParamsInvalido = -32602
	CodigoErrorInterno   = -32603
)

func respuestaOK(id json.RawMessage, resultado any) *Respuesta {
	return &Respuesta{JSONRPC: "2.0", ID: id, Result: resultado}
}

func respuestaError(id json.RawMessage, codigo int, mensaje string) *Respuesta {
	return &Respuesta{JSONRPC: "2.0", ID: id, Error: &ErrorRPC{Codigo: codigo, Mensaje: mensaje}}
}

// Herramienta es lo que el modelo ve al listar: nombre, para qué sirve y qué
// argumentos toma.
type Herramienta struct {
	Nombre      string `json:"name"`
	Titulo      string `json:"title,omitempty"`
	Descripcion string `json:"description"`
	Esquema     any    `json:"inputSchema"`
}

// Contenido es un pedazo de la respuesta de una herramienta.
type Contenido struct {
	Tipo  string `json:"type"`
	Texto string `json:"text,omitempty"`
}

// ResultadoHerramienta es lo que devuelve una llamada. EsError marca un
// problema del pedido — datos que no existen, parámetros mal — que el modelo
// puede leer y corregir, a diferencia de un error de protocolo.
type ResultadoHerramienta struct {
	Contenido []Contenido `json:"content"`
	EsError   bool        `json:"isError,omitempty"`
}

func texto(s string) *ResultadoHerramienta {
	return &ResultadoHerramienta{Contenido: []Contenido{{Tipo: "text", Texto: s}}}
}

func errorDeHerramienta(s string) *ResultadoHerramienta {
	return &ResultadoHerramienta{Contenido: []Contenido{{Tipo: "text", Texto: s}}, EsError: true}
}

// comoJSON devuelve el valor serializado y legible: el modelo lo lee mejor
// indentado, y así también lo lee una persona que mire el log.
func comoJSON(v any) *ResultadoHerramienta {
	datos, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorDeHerramienta("no se pudo serializar la respuesta: " + err.Error())
	}
	return texto(string(datos))
}
