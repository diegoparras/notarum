package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// Servidor atiende pedidos MCP contra el Boletín.
type Servidor struct {
	srv     *servicio.Servicio
	version string

	mu           sync.Mutex
	inicializado bool
}

func Nuevo(srv *servicio.Servicio, version string) *Servidor {
	return &Servidor{srv: srv, version: version}
}

// Atender procesa un mensaje y devuelve la respuesta, o nil si era una
// notificación.
func (s *Servidor) Atender(ctx context.Context, crudo []byte) *Respuesta {
	var p Pedido
	if err := json.Unmarshal(crudo, &p); err != nil {
		return respuestaError(nil, CodigoParseo, "el mensaje no es JSON válido")
	}
	if p.JSONRPC != "2.0" {
		return respuestaError(p.ID, CodigoPedidoInvalido, `falta "jsonrpc":"2.0"`)
	}

	switch p.Metodo {
	case "initialize":
		s.mu.Lock()
		s.inicializado = true
		s.mu.Unlock()
		return respuestaOK(p.ID, map[string]any{
			"protocolVersion": VersionProtocolo,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    "notarum",
				"title":   "Boletín Oficial de la República Argentina",
				"version": s.version,
			},
			"instructions": instrucciones,
		})

	case "notifications/initialized", "initialized":
		return nil

	case "ping":
		return respuestaOK(p.ID, map[string]any{})

	case "tools/list":
		return respuestaOK(p.ID, map[string]any{"tools": Herramientas()})

	case "tools/call":
		if p.esNotificacion() {
			return nil
		}
		return respuestaOK(p.ID, s.llamar(ctx, p.Params))

	default:
		if p.esNotificacion() {
			return nil
		}
		return respuestaError(p.ID, CodigoMetodoNoExiste, "método desconocido: "+p.Metodo)
	}
}

const instrucciones = `Consulta el Boletín Oficial de la República Argentina.

Tres secciones: primera (decretos, resoluciones, disposiciones), segunda
(sociedades, edictos, sucesiones) y tercera (licitaciones y contrataciones).
Las fechas van en AAAA-MM-DD.

No todos los días hay edición: los feriados no tienen. Antes de pedir un rango
de fechas conviene mirar el calendario. Un aviso marcado como repetido ya se
había publicado en una edición anterior.`

// ServirStdio atiende el protocolo por entrada y salida estándar, un mensaje
// JSON por línea, que es como lo habla un cliente local.
func (s *Servidor) ServirStdio(ctx context.Context, entrada io.Reader, salida io.Writer) error {
	lector := bufio.NewScanner(entrada)
	lector.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	escritor := json.NewEncoder(salida)

	for lector.Scan() {
		linea := strings.TrimSpace(lector.Text())
		if linea == "" {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res := s.Atender(ctx, []byte(linea))
		if res == nil {
			continue
		}
		if err := escritor.Encode(res); err != nil {
			return fmt.Errorf("no se pudo responder: %w", err)
		}
	}
	if err := lector.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- llamadas

type pedidoLlamada struct {
	Nombre     string          `json:"name"`
	Argumentos json.RawMessage `json:"arguments"`
}

func (s *Servidor) llamar(ctx context.Context, params json.RawMessage) *ResultadoHerramienta {
	var p pedidoLlamada
	if err := json.Unmarshal(params, &p); err != nil {
		return errorDeHerramienta("no se entendieron los argumentos: " + err.Error())
	}
	switch p.Nombre {
	case "calendario":
		return s.hCalendario(ctx, p.Argumentos)
	case "edicion":
		return s.hEdicion(ctx, p.Argumentos)
	case "aviso":
		return s.hAviso(ctx, p.Argumentos)
	case "buscar":
		return s.hBuscar(ctx, p.Argumentos)
	case "rubros":
		return s.hRubros(ctx, p.Argumentos)
	case "estado":
		return s.hEstado(ctx)
	case "provincial_buscar":
		return s.hProvincialBuscar(ctx, p.Argumentos)
	case "provincial_norma":
		return s.hProvincialNorma(ctx, p.Argumentos)
	case "provincial_tipos":
		return s.hProvincialTipos(ctx)
	case "nacional_buscar":
		return s.hNacionalBuscar(ctx, p.Argumentos)
	case "nacional_norma":
		return s.hNacionalNorma(ctx, p.Argumentos)
	case "nacional_tipos":
		return s.hNacionalTipos(ctx)
	default:
		return errorDeHerramienta("no existe la herramienta " + p.Nombre)
	}
}

// leerSeccion y leerFecha traducen los argumentos con mensajes que el modelo
// pueda usar para corregirse.
func leerSeccion(v string) (boletin.Seccion, error) {
	if strings.TrimSpace(v) == "" {
		return "", errors.New(`falta "seccion": tiene que ser primera, segunda o tercera`)
	}
	return boletin.ParseSeccion(v)
}

func leerFecha(nombre, v string) (boletin.Fecha, error) {
	if strings.TrimSpace(v) == "" {
		return boletin.Fecha{}, fmt.Errorf(`falta %q en formato AAAA-MM-DD`, nombre)
	}
	f, err := boletin.ParseFecha(v)
	if err != nil {
		return boletin.Fecha{}, fmt.Errorf("%s: %w", nombre, err)
	}
	return f, nil
}

func (s *Servidor) hCalendario(ctx context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Seccion string `json:"seccion"`
		Anio    int    `json:"anio"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	sec, err := leerSeccion(a.Seccion)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	if a.Anio == 0 {
		a.Anio = boletin.HoyEnArgentina().Year()
	}
	cal, err := s.srv.Calendario(ctx, sec, a.Anio)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	return comoJSON(cal)
}

func (s *Servidor) hEdicion(ctx context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Seccion string `json:"seccion"`
		Fecha   string `json:"fecha"`
		Rubro   string `json:"rubro"`
		Limite  int    `json:"limite"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	sec, err := leerSeccion(a.Seccion)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	if strings.TrimSpace(a.Fecha) == "" {
		a.Fecha = boletin.HoyEnArgentina().API()
	}
	fecha, err := leerFecha("fecha", a.Fecha)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}

	ed, err := s.srv.Edicion(ctx, sec, fecha, a.Rubro)
	if errors.Is(err, servicio.ErrSinEdicion) {
		return texto(fmt.Sprintf(
			"El %s no hubo edición de la sección %s: es feriado o fin de semana. "+
				"Mirá el calendario para saber qué días sí hubo.", fecha.API(), sec))
	}
	if err != nil {
		return errorDeHerramienta(err.Error())
	}

	// Una edición entera puede traer cientos de avisos: se recorta y se avisa,
	// para no llenar la ventana de contexto sin que el modelo lo sepa.
	limite := a.Limite
	if limite <= 0 {
		limite = 40
	}
	salida := map[string]any{
		"seccion":        ed.Seccion,
		"fecha":          ed.Fecha,
		"cantidad":       ed.Cantidad,
		"por_rubro":      ed.PorRubro,
		"con_suplemento": ed.ConSuplemento,
	}
	if len(ed.Avisos) > limite {
		salida["avisos"] = ed.Avisos[:limite]
		salida["recortado"] = fmt.Sprintf(
			"se muestran %d de %d avisos; pedí un rubro o subí el límite para ver el resto",
			limite, len(ed.Avisos))
	} else {
		salida["avisos"] = ed.Avisos
	}
	return comoJSON(salida)
}

func (s *Servidor) hAviso(ctx context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Seccion string `json:"seccion"`
		ID      string `json:"id"`
		Fecha   string `json:"fecha"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	sec, err := leerSeccion(a.Seccion)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	if strings.TrimSpace(a.ID) == "" {
		return errorDeHerramienta(`falta "id": el identificador del aviso, como aparece en la edición`)
	}
	fecha, err := leerFecha("fecha", a.Fecha)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	d, err := s.srv.Aviso(ctx, sec, a.ID, fecha)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	// El HTML no le sirve al modelo: se manda el texto plano y la lista de
	// anexos con su URL.
	return comoJSON(map[string]any{
		"id": d.ID, "seccion": d.Seccion, "fecha": d.Fecha, "rubro": d.Rubro,
		"organismo": d.Organismo, "norma": d.Norma, "referencia": d.Referencia,
		"sintesis": d.Sintesis, "texto": d.Texto, "anexos": d.Anexos, "url": d.URL,
	})
}

func (s *Servidor) hBuscar(ctx context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Seccion string `json:"seccion"`
		Texto   string `json:"texto"`
		Desde   string `json:"desde"`
		Hasta   string `json:"hasta"`
		Rubro   string `json:"rubro"`
		Pagina  int    `json:"pagina"`
		Limite  int    `json:"limite"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	sec, err := leerSeccion(a.Seccion)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	desde, err := leerFecha("desde", a.Desde)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	hasta, err := leerFecha("hasta", a.Hasta)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	if hasta.Before(desde.Time) {
		return errorDeHerramienta("el rango está al revés: hasta es anterior a desde")
	}

	if s.srv.TieneIndice() {
		if _, _, hay := s.srv.PuedeBuscarLocal(ctx, sec, desde, hasta); hay {
			res, err := s.srv.BuscarEnIndice(ctx, almacen.ConsultaLocal{
				Texto: a.Texto, Seccion: sec, Rubro: a.Rubro,
				Desde: desde, Hasta: hasta, Limite: a.Limite,
			}, a.Pagina)
			if err != nil {
				return errorDeHerramienta(err.Error())
			}
			return comoJSON(res)
		}
	}
	var rubros []string
	if a.Rubro != "" {
		rubros = []string{a.Rubro}
	}
	res, err := s.srv.BuscarEnSitio(ctx, boletin.ConsultaBusqueda{
		Texto: a.Texto, Seccion: sec, Rubros: rubros,
		Desde: desde, Hasta: hasta, Pagina: a.Pagina,
	})
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	return comoJSON(res)
}

func (s *Servidor) hRubros(ctx context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Seccion string `json:"seccion"`
	}
	if err := json.Unmarshal(crudo, &a); err != nil {
		return errorDeHerramienta(err.Error())
	}
	sec, err := leerSeccion(a.Seccion)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	rs, err := s.srv.Rubros(ctx, sec)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	return comoJSON(rs)
}

func (s *Servidor) hEstado(context.Context) *ResultadoHerramienta {
	m := s.srv.Cliente().Metricas()
	return comoJSON(map[string]any{
		"version":        s.version,
		"hoy":            boletin.HoyEnArgentina().API(),
		"fuente":         boletin.BaseSitio,
		"indice_local":   s.srv.TieneIndice(),
		"almacen":        s.srv.Almacen().Metricas(),
		"lecturas_sitio": m.Lecturas,
		"errores_sitio":  m.Errores,
	})
}
