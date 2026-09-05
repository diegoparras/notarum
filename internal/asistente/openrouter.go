// Package asistente arma consultas a notarum a partir de un pedido escrito en
// castellano.
//
// La API tiene catorce rutas con sus parámetros y el MCP nueve herramientas.
// Todo eso está documentado en /docs, pero leer una tabla y traducirla al
// cliente HTTP de n8n es un trabajo aparte. Acá se le pasa el contrato a un
// modelo junto con lo que la persona quiere, y sale la consulta armada.
//
// El modelo no ejecuta nada: escribe algo para copiar y pegar. Si se
// equivoca, se ve antes de correrlo.
package asistente

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BaseOpenRouter es el punto de entrada del proveedor.
const BaseOpenRouter = "https://openrouter.ai/api/v1"

// maxTokensDeSalida es el techo de la respuesta. Holgado a propósito: un
// modelo de razonamiento gasta tokens pensando antes de escribir, y con un
// techo justo se queda sin ninguno para la respuesta y devuelve vacío.
const maxTokensDeSalida = 8000

// ModeloPorDefecto es con el que se genera si no se elige otro. Se prefiere
// uno rápido y barato: la tarea es traducir un pedido a una consulta con el
// contrato delante, no razonar.
//
// Un nombre de modelo escrito acá envejece: el proveedor los agrega y los
// retira, y el día que éste deje de existir todas las generaciones fallan sin
// que nada en el código haya cambiado. Ya pasó —el anterior era
// "anthropic/claude-3.5-haiku", que no está en el catálogo— así que hay un
// test con la etiqueta `red` que lo comprueba contra el catálogo de verdad.
const ModeloPorDefecto = "anthropic/claude-haiku-4.5"

// Opciones configura el cliente.
type Opciones struct {
	Base  string
	HTTP  *http.Client
	Sitio string // la dirección de esta instancia, que OpenRouter pide
}

// Cliente habla con OpenRouter.
type Cliente struct {
	base  string
	sitio string
	http  *http.Client
}

func NuevoCliente(o Opciones) *Cliente {
	c := &Cliente{base: strings.TrimRight(strings.TrimSpace(o.Base), "/"), sitio: o.Sitio, http: o.HTTP}
	if c.base == "" {
		c.base = BaseOpenRouter
	}
	if c.http == nil {
		// Menos que el timeout de un proxy común, que suele estar en 30
		// segundos: si el proveedor tarda más, el error tiene que darlo
		// notarum —que puede explicarlo— y no el proxy, que muestra una
		// página que no dice nada y hace pensar que el servicio se cayó.
		c.http = &http.Client{Timeout: 25 * time.Second}
	}
	return c
}

// Errores que hay que distinguir para poder explicarlos.
var (
	// ErrClaveRechazada es que el proveedor no acepta la clave.
	ErrClaveRechazada = errors.New("el proveedor rechazó la clave")
	// ErrSinSaldo es que la cuenta del proveedor no tiene crédito.
	ErrSinSaldo = errors.New("la cuenta del proveedor no tiene saldo")
	// ErrProveedorOcupado es un límite de uso del proveedor.
	ErrProveedorOcupado = errors.New("el proveedor está limitando los pedidos")
	// ErrProveedorLento es que tardó más de lo que se puede esperar en una
	// página. Se distingue del que no contesta: éste probablemente ande, y
	// volver a intentar tiene sentido.
	ErrProveedorLento = errors.New("el proveedor tardó demasiado")
	// ErrModeloDesconocido es que el modelo pedido no existe en el proveedor.
	// Lleva a otra cosa que un error de la clave o del saldo: hay que elegir
	// otro modelo, y se elige desde la cuenta.
	ErrModeloDesconocido = errors.New("el proveedor no conoce ese modelo")
)

type mensaje struct {
	Rol       string `json:"role"`
	Contenido string `json:"content"`
}

// pedido es el cuerpo que se le manda al proveedor.
//
// Los parámetros van como punteros para poder omitirlos: no todos los modelos
// aceptan los mismos, y mandarle a uno algo que no acepta hace fallar la
// generación entera. La familia GPT-5 rechaza temperature, por ejemplo. Qué
// acepta cada uno lo dice el catálogo, así que no hay que adivinarlo ni
// mantener acá una lista que envejece sola.
type pedido struct {
	Modelo      string    `json:"model"`
	Mensajes    []mensaje `json:"messages"`
	Temperatura *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
}

type respuesta struct {
	Opciones []struct {
		Mensaje mensaje `json:"message"`
	} `json:"choices"`
	Uso struct {
		Entrada int `json:"prompt_tokens"`
		Salida  int `json:"completion_tokens"`
	} `json:"usage"`
	Error struct {
		Mensaje string `json:"message"`
		Codigo  any    `json:"code"`
	} `json:"error"`
}

// Generado es lo que devuelve el modelo.
type Generado struct {
	// Texto es la consulta armada, lista para copiar.
	Texto string
	// Modelo es con cuál se generó, para poder decirlo.
	Modelo string
	// Tokens es lo que costó, para que quien paga lo vea.
	TokensEntrada, TokensSalida int
	// Tardo queda anotado en el log: si un día empieza a fallar, lo primero
	// que hay que saber es si el proveedor se puso lento.
	Tardo time.Duration
}

// Generar le pide al modelo la consulta.
func (c *Cliente) Generar(ctx context.Context, clave, modelo, sistema, pedidoDeLaPersona string) (*Generado, error) {
	if strings.TrimSpace(clave) == "" {
		return nil, ErrClaveRechazada
	}
	if modelo == "" {
		modelo = ModeloPorDefecto
	}
	// Qué acepta este modelo. Se le pregunta al catálogo en vez de suponerlo:
	// cualquier modelo que el proveedor ofrezca tiene que poder usarse, y
	// mandarle un parámetro que no acepta lo rompe antes de empezar.
	m := c.buscarModelo(ctx, clave, modelo)

	p := pedido{
		Modelo: modelo,
		Mensajes: []mensaje{
			{Rol: "system", Contenido: sistema},
			{Rol: "user", Contenido: pedidoDeLaPersona},
		},
	}
	if m.Acepta("temperature") {
		// Temperatura baja: se quiere la consulta correcta, no una variada.
		// El que no la acepta genera con la suya, que es lo que corresponde.
		baja := 0.1
		p.Temperatura = &baja
	}
	if m.Acepta("max_tokens") {
		// Con techo, acotado a lo que este modelo admite: cada token se le
		// cobra a quien puso la clave, y pedir más de lo que puede escribir
		// es otro error evitable.
		techo := m.TechoDeSalida(maxTokensDeSalida)
		p.MaxTokens = &techo
	}
	cuerpo, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/chat/completions", bytes.NewReader(cuerpo))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+clave)
	req.Header.Set("Content-Type", "application/json")
	// OpenRouter pide identificar la aplicación; es lo que muestra en el
	// panel de quien paga para saber qué gastó qué.
	if c.sitio != "" {
		req.Header.Set("HTTP-Referer", c.sitio)
	}
	req.Header.Set("X-Title", "notarum")

	empezo := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		// Distinguir el tardó del no contestó: llevan a cosas distintas.
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			return nil, fmt.Errorf("%w: tardó más de %s", ErrProveedorLento, c.http.Timeout)
		}
		return nil, fmt.Errorf("no se pudo hablar con el proveedor: %w", err)
	}
	defer res.Body.Close()
	crudo, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var r respuesta
	if err := json.Unmarshal(crudo, &r); err != nil {
		return nil, fmt.Errorf("el proveedor contestó algo que no es JSON (%d)", res.StatusCode)
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrClaveRechazada
	case http.StatusPaymentRequired:
		return nil, ErrSinSaldo
	case http.StatusTooManyRequests:
		return nil, ErrProveedorOcupado
	case http.StatusBadRequest, http.StatusNotFound:
		// El caso frecuente: un modelo que ya no está. Se distingue por el
		// mensaje porque el proveedor usa el mismo código para varias cosas.
		if pareceModeloDesconocido(r.Error.Mensaje) {
			return nil, fmt.Errorf("%w: %s", ErrModeloDesconocido, modelo)
		}
		if r.Error.Mensaje != "" {
			return nil, fmt.Errorf("el proveedor devolvió un error: %s", r.Error.Mensaje)
		}
		return nil, fmt.Errorf("el proveedor contestó %d", res.StatusCode)
	default:
		if r.Error.Mensaje != "" {
			return nil, fmt.Errorf("el proveedor devolvió un error: %s", r.Error.Mensaje)
		}
		return nil, fmt.Errorf("el proveedor contestó %d", res.StatusCode)
	}
	if len(r.Opciones) == 0 || strings.TrimSpace(r.Opciones[0].Mensaje.Contenido) == "" {
		// Le pasa a los modelos que razonan cuando gastan todo el presupuesto
		// pensando: contestan bien, pero sin nada escrito.
		return nil, fmt.Errorf("%s contestó sin escribir nada; probá con otro modelo", modelo)
	}

	return &Generado{
		Texto:         strings.TrimSpace(r.Opciones[0].Mensaje.Contenido),
		Modelo:        modelo,
		TokensEntrada: r.Uso.Entrada,
		TokensSalida:  r.Uso.Salida,
		Tardo:         time.Since(empezo),
	}, nil
}

// Probar verifica que una clave sirva, sin gastar casi nada. Se usa al
// cargarla: mejor enterarse ahí que cuando alguien quiere generar algo.
func (c *Cliente) Probar(ctx context.Context, clave string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/key", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+clave)
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo hablar con el proveedor: %w", err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))

	switch res.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrClaveRechazada
	case http.StatusTooManyRequests:
		return ErrProveedorOcupado
	}
	return fmt.Errorf("el proveedor contestó %d al revisar la clave", res.StatusCode)
}

// pareceModeloDesconocido mira el mensaje del proveedor: no hay un código
// aparte para esto, así que se reconoce por lo que dice.
func pareceModeloDesconocido(mensaje string) bool {
	m := strings.ToLower(mensaje)
	for _, seña := range []string{"not a valid model", "no endpoints found", "unknown model", "model not found"} {
		if strings.Contains(m, seña) {
			return true
		}
	}
	return false
}
