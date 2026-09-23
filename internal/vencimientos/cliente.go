package vencimientos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// CodigoTodos es "TODOS" en el desplegable: una consulta trae la agenda
	// entera en vez de una por impuesto.
	CodigoTodos = "999"
	// terminacionTodas pide todas las terminaciones. El filtro por
	// terminación se hace después: la terminación viene en cada fila.
	terminacionTodas = "Todos"
	// tamanoMaximo acota lo que se lee. El año 2026 entero pesó 4,3 MB.
	tamanoMaximo = 64 << 20
)

// ErrDeARCA envuelve lo que sale mal del lado de ARCA, para distinguirlo de
// un error de notarum al informarlo.
type ErrDeARCA struct {
	Que   string
	Causa error
}

func (e *ErrDeARCA) Error() string {
	if e.Causa == nil {
		return "ARCA: " + e.Que
	}
	return "ARCA: " + e.Que + ": " + e.Causa.Error()
}

func (e *ErrDeARCA) Unwrap() error { return e.Causa }

// Opciones configura el cliente.
type Opciones struct {
	URL       string // para los tests
	UserAgent string
	HTTP      *http.Client
}

// Cliente habla con el formulario de ARCA. No guarda sesión: el POST sin
// cookie contesta lo mismo que con ella.
type Cliente struct {
	url       string
	userAgent string
	http      *http.Client
}

func NuevoCliente(o Opciones) *Cliente {
	c := &Cliente{url: strings.TrimSpace(o.URL), userAgent: o.UserAgent, http: o.HTTP}
	if c.url == "" {
		c.url = URLPorDefecto
	}
	if c.userAgent == "" {
		c.userAgent = "notarum (+https://github.com/diegoparras/notarum)"
	}
	if c.http == nil {
		// Un año entero tardó 32 segundos.
		c.http = &http.Client{Timeout: 3 * time.Minute}
	}
	return c
}

// Consultar pide la agenda de una ventana de fechas, en una sola consulta.
func (c *Cliente) Consultar(ctx context.Context, desde, hasta time.Time) (Agenda, error) {
	if hasta.Before(desde) {
		return Agenda{}, fmt.Errorf("la ventana está al revés: desde %s hasta %s",
			desde.Format(formatoARCA), hasta.Format(formatoARCA))
	}
	form := url.Values{
		"fechaVDesde":            {desde.Format(formatoARCA)},
		"fechaVHasta":            {hasta.Format(formatoARCA)},
		"terminacionCuit":        {terminacionTodas},
		"impuestosSeleccionados": {CodigoTodos},
		"Submit":                 {"Buscar"},
	}
	cuerpo, err := c.pedir(ctx, http.MethodPost, strings.NewReader(form.Encode()))
	if err != nil {
		return Agenda{}, err
	}
	return Parsear(cuerpo, desde, hasta)
}

// Catalogo pide el formulario y devuelve los impuestos de su desplegable.
func (c *Cliente) Catalogo(ctx context.Context) ([]Impuesto, error) {
	cuerpo, err := c.pedir(ctx, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	return ParsearCatalogo(cuerpo)
}

func (c *Cliente) pedir(ctx context.Context, metodo string, cuerpo io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, metodo, c.url, cuerpo)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if cuerpo != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &ErrDeARCA{Que: "no contestó", Causa: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &ErrDeARCA{Que: fmt.Sprintf("contestó %d", resp.StatusCode)}
	}
	datos, err := io.ReadAll(io.LimitReader(resp.Body, tamanoMaximo+1))
	if err != nil {
		return nil, &ErrDeARCA{Que: "cortó la respuesta a la mitad", Causa: err}
	}
	if len(datos) > tamanoMaximo {
		return nil, &ErrDeARCA{Que: "la respuesta es más grande de lo razonable"}
	}
	// Un Content-Length que no coincide es una respuesta cortada que igual
	// parsea, y trae menos vencimientos de los que hay.
	if resp.ContentLength > 0 && int64(len(datos)) != resp.ContentLength {
		return nil, &ErrDeARCA{Que: fmt.Sprintf("declaró %d bytes y mandó %d", resp.ContentLength, len(datos))}
	}
	return datos, nil
}

// VentanaAtras es cuánto hacia atrás se pide. Una baja sólo se puede dar
// adentro de la ventana consultada, así que sin mirar atrás un vencimiento de
// la semana pasada que ARCA corrige quedaría con la fecha vieja para siempre.
// Mes y medio cubre el ciclo mensual y el de las quincenas.
const VentanaAtras = 45 * 24 * time.Hour

// Ventana es lo que se prefiere pedir: hasta fin del año que viene.
func Ventana(ahora time.Time) (desde, hasta time.Time) {
	ahora = ahora.UTC()
	return ahora.Add(-VentanaAtras).Truncate(24 * time.Hour),
		time.Date(ahora.Year()+1, 12, 31, 0, 0, 0, 0, time.UTC)
}

// VentanaDeRespaldo es la que se pide cuando ARCA todavía no cargó el año
// siguiente: hasta fin del año en curso.
func VentanaDeRespaldo(ahora time.Time) (desde, hasta time.Time) {
	ahora = ahora.UTC()
	return ahora.Add(-VentanaAtras).Truncate(24 * time.Hour),
		time.Date(ahora.Year(), 12, 31, 0, 0, 0, 0, time.UTC)
}
