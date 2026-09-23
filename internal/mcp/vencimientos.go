package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/vencimientos"
)

// Las herramientas de la agenda de vencimientos de ARCA. Contestan "¿qué me
// vence esta semana?" y "¿ARCA prorrogó algo?", que son las dos preguntas que
// alguien le hace a un asistente sobre impuestos.
//
// Cada respuesta dice de cuándo es la agenda. Un modelo que recibe una lista
// sin fecha la presenta como el estado de hoy, porque no tiene cómo saber
// otra cosa.

// limiteVencimientosMCP es más bajo que el de la API: cada vencimiento son
// unos cientos de tokens, y cien ya son muchos para una conversación.
const limiteVencimientosMCP = 100

func herramientasDeVencimientos() []Herramienta {
	cuit := map[string]any{
		"type":        "string",
		"description": "CUIT entero (con o sin guiones) o sólo su último dígito. Trae lo que le vence a esa terminación más lo que vence para todas. Sin esto, todas las terminaciones.",
	}
	impuesto := map[string]any{
		"type":        "string",
		"description": `Nombre del impuesto, como lo devuelve vencimientos_impuestos: "IMPUESTO AL VALOR AGREGADO", "IMPUESTO A LAS GANANCIAS"… No distingue mayúsculas ni acentos.`,
	}
	return []Herramienta{
		{
			Nombre: "vencimientos_proximos",
			Titulo: "Qué vence pronto",
			Descripcion: "Los vencimientos impositivos de ARCA (ex-AFIP) de hoy y los próximos días. " +
				"Es la herramienta para «¿qué me vence esta semana?». Con un CUIT, sólo lo que le toca.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dias":     map[string]any{"type": "integer", "minimum": 1, "maximum": 366, "default": 7, "description": "Cuántos días mirar, contando hoy."},
					"cuit":     cuit,
					"impuesto": impuesto,
				},
			},
		},
		{
			Nombre: "vencimientos_buscar",
			Titulo: "Buscar en la agenda de vencimientos",
			Descripcion: "Busca en la agenda de vencimientos de ARCA por período, impuesto, CUIT o texto. " +
				"Las obligaciones de plazo relativo («dentro de los 10 días hábiles de…») no tienen fecha " +
				"de almanaque: no salen en una búsqueda por fechas y se piden con sin_fecha_fija.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"desde":          map[string]any{"type": "string", "description": "Fecha de vencimiento más temprana, AAAA-MM-DD."},
					"hasta":          map[string]any{"type": "string", "description": "Fecha de vencimiento más tardía, AAAA-MM-DD."},
					"impuesto":       impuesto,
					"cuit":           cuit,
					"texto":          map[string]any{"type": "string", "description": "Palabras a buscar en impuesto, régimen, obligación, período y sujeto: «retenciones», «monotributo», «bienes personales». No hace falta poner los acentos."},
					"sin_fecha_fija": map[string]any{"type": "boolean", "default": false, "description": "Trae sólo las obligaciones de plazo relativo."},
					"pagina":         map[string]any{"type": "integer", "minimum": 1, "default": 1},
					"limite":         map[string]any{"type": "integer", "minimum": 1, "maximum": limiteVencimientosMCP, "default": 30},
				},
			},
		},
		{
			Nombre: "vencimientos_cambios",
			Titulo: "Qué movió ARCA en la agenda",
			Descripcion: "Las prórrogas, los adelantos, las altas y las bajas que ARCA hizo en la agenda, " +
				"de lo más nuevo a lo más viejo. ARCA no publica esto: sale de comparar cada bajada diaria " +
				"con la anterior, y cada cambio dice entre qué dos bajadas se detectó.",
			Esquema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"desde":  map[string]any{"type": "string", "description": "Sólo lo detectado desde esta fecha, AAAA-MM-DD. Por defecto, los últimos 90 días."},
					"limite": map[string]any{"type": "integer", "minimum": 1, "maximum": limiteVencimientosMCP, "default": 50},
				},
			},
		},
		{
			Nombre:      "vencimientos_impuestos",
			Titulo:      "Impuestos de la agenda",
			Descripcion: "Los impuestos del formulario de ARCA con cuántos vencimientos tiene cargados cada uno. Conviene mirarlo antes de filtrar por impuesto, para usar el nombre tal como está escrito.",
			Esquema:     map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

// agendaParaModelo es de cuándo es la agenda, dicho para que se cite.
type agendaParaModelo struct {
	BajadaEn string `json:"bajada_en"`
	Periodo  string `json:"periodo_consultado"`
	Nota     string `json:"nota"`
}

func (s *Servidor) agendaParaModelo() agendaParaModelo {
	e := s.srv.EstadoVencimientos()
	return agendaParaModelo{
		BajadaEn: e.SincronizadoEn.Format(time.RFC3339),
		Periodo:  e.Desde + " a " + e.Hasta,
		Nota: "Es la agenda que ARCA publicaba al momento de la bajada: citá esa fecha en vez de presentarla " +
			"como la de hoy. Afuera del período consultado no se preguntó nada.",
	}
}

// vistaVencimiento es el vencimiento como le sirve a un modelo: sin los
// campos internos de seguimiento.
type vistaVencimiento struct {
	Fecha           string               `json:"fecha,omitempty"`
	SinFechaFija    bool                 `json:"sin_fecha_fija,omitempty"`
	Impuesto        string               `json:"impuesto"`
	Regimen         string               `json:"regimen,omitempty"`
	Obligacion      string               `json:"obligacion"`
	Periodo         string               `json:"periodo,omitempty"`
	TerminacionCUIT string               `json:"terminacion_cuit,omitempty"`
	Sujeto          string               `json:"sujeto,omitempty"`
	Formularios     string               `json:"formularios,omitempty"`
	Normas          []vencimientos.Norma `json:"normas,omitempty"`
	// PublicadaDesde es desde cuándo ARCA publica esta fecha.
	PublicadaDesde string `json:"fecha_publicada_desde"`
}

func vistasDeVencimientos(rs []vencimientos.Registro) []vistaVencimiento {
	out := make([]vistaVencimiento, 0, len(rs))
	for _, r := range rs {
		out = append(out, vistaVencimiento{
			Fecha: r.Fecha, SinFechaFija: r.SinFechaFija, Impuesto: r.Impuesto, Regimen: r.Regimen,
			Obligacion: r.Obligacion, Periodo: r.Periodo, TerminacionCUIT: r.TerminacionCUIT,
			Sujeto: r.Sujeto, Formularios: r.Formularios, Normas: r.Normas,
			PublicadaDesde: r.FechaDesde.Format(vencimientos.FormatoISO),
		})
	}
	return out
}

func (s *Servidor) sinAgenda() *ResultadoHerramienta {
	if !s.srv.VencimientosDisponible() {
		return errorDeHerramienta("esta instancia no sigue la agenda de vencimientos de ARCA")
	}
	if !s.srv.AgendaCargada() {
		return errorDeHerramienta("esta instancia todavía no bajó la agenda de vencimientos; " +
			"se baja sola todos los días, y quien la opera puede bajarla ya con `notarum vencimientos`. " +
			"Que no haya respuesta no significa que no venza nada.")
	}
	return nil
}

func (s *Servidor) contestarVencimientos(c vencimientos.Consulta, pagina int) *ResultadoHerramienta {
	res := s.srv.BuscarVencimientos(c)
	salida := struct {
		Agenda       agendaParaModelo   `json:"agenda"`
		Total        int                `json:"total"`
		Pagina       int                `json:"pagina"`
		HayMas       bool               `json:"hay_mas"`
		Aviso        string             `json:"aviso,omitempty"`
		Vencimientos []vistaVencimiento `json:"vencimientos"`
	}{s.agendaParaModelo(), res.Total, pagina, res.Truncado, "", vistasDeVencimientos(res.Vencimientos)}
	// Una lista cortada tiene que decirlo: si no, el modelo la toma por
	// completa.
	if res.Truncado {
		salida.Aviso = fmt.Sprintf("Se muestran %d de %d. Pedí la página siguiente o acotá por impuesto, CUIT o fechas.",
			len(res.Vencimientos), res.Total)
	}
	if res.Total == 0 {
		salida.Aviso = "No vence nada con esos criterios adentro del período consultado."
	}
	return comoJSON(salida)
}

func (s *Servidor) hVencimientosProximos(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Dias     int    `json:"dias"`
		CUIT     string `json:"cuit"`
		Impuesto string `json:"impuesto"`
	}
	if len(crudo) > 0 {
		if err := json.Unmarshal(crudo, &a); err != nil {
			return errorDeHerramienta("no se entendieron los argumentos: " + err.Error())
		}
	}
	if r := s.sinAgenda(); r != nil {
		return r
	}
	if a.Dias == 0 {
		a.Dias = 7
	}
	if a.Dias < 1 || a.Dias > 366 {
		return errorDeHerramienta(`"dias" tiene que estar entre 1 y 366`)
	}
	term, err := vencimientos.TerminacionDeCUIT(a.CUIT)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	// Hoy es el de la Argentina: entre las 21 y la medianoche UTC ya es
	// mañana, y ahí se escondería lo que todavía se puede pagar hoy.
	hoy := boletin.HoyEnArgentina().Time
	return s.contestarVencimientos(vencimientos.Consulta{
		Desde:       hoy.Format(vencimientos.FormatoISO),
		Hasta:       hoy.AddDate(0, 0, a.Dias-1).Format(vencimientos.FormatoISO),
		Terminacion: term, Impuesto: a.Impuesto, Limite: limiteVencimientosMCP,
	}, 1)
}

func (s *Servidor) hVencimientosBuscar(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Desde        string `json:"desde"`
		Hasta        string `json:"hasta"`
		Impuesto     string `json:"impuesto"`
		CUIT         string `json:"cuit"`
		Texto        string `json:"texto"`
		SinFechaFija bool   `json:"sin_fecha_fija"`
		Pagina       int    `json:"pagina"`
		Limite       int    `json:"limite"`
	}
	if len(crudo) > 0 {
		if err := json.Unmarshal(crudo, &a); err != nil {
			return errorDeHerramienta("no se entendieron los argumentos: " + err.Error())
		}
	}
	if r := s.sinAgenda(); r != nil {
		return r
	}
	var err error
	if a.Desde, err = vencimientos.ParsearFecha(a.Desde); err != nil {
		return errorDeHerramienta(err.Error())
	}
	if a.Hasta, err = vencimientos.ParsearFecha(a.Hasta); err != nil {
		return errorDeHerramienta(err.Error())
	}
	term, err := vencimientos.TerminacionDeCUIT(a.CUIT)
	if err != nil {
		return errorDeHerramienta(err.Error())
	}
	if a.Pagina < 1 {
		a.Pagina = 1
	}
	if a.Limite <= 0 {
		a.Limite = 30
	}
	if a.Limite > limiteVencimientosMCP {
		a.Limite = limiteVencimientosMCP
	}
	return s.contestarVencimientos(vencimientos.Consulta{
		Desde: a.Desde, Hasta: a.Hasta, Impuesto: a.Impuesto, Terminacion: term,
		Texto: a.Texto, SinFechaFija: a.SinFechaFija,
		Limite: a.Limite, Desplazamiento: (a.Pagina - 1) * a.Limite,
	}, a.Pagina)
}

func (s *Servidor) hVencimientosCambios(_ context.Context, crudo json.RawMessage) *ResultadoHerramienta {
	var a struct {
		Desde  string `json:"desde"`
		Limite int    `json:"limite"`
	}
	if len(crudo) > 0 {
		if err := json.Unmarshal(crudo, &a); err != nil {
			return errorDeHerramienta("no se entendieron los argumentos: " + err.Error())
		}
	}
	if r := s.sinAgenda(); r != nil {
		return r
	}
	desde := time.Now().AddDate(0, 0, -90)
	if a.Desde != "" {
		d, err := time.Parse(vencimientos.FormatoISO, a.Desde)
		if err != nil {
			return errorDeHerramienta("«" + a.Desde + "» no es una fecha: se escribe AAAA-MM-DD")
		}
		desde = d
	}
	if a.Limite <= 0 {
		a.Limite = 50
	}
	if a.Limite > limiteVencimientosMCP {
		a.Limite = limiteVencimientosMCP
	}
	cambios := s.srv.CambiosDeVencimientos(desde, a.Limite)
	salida := struct {
		Agenda  agendaParaModelo      `json:"agenda"`
		Desde   string                `json:"desde"`
		Aviso   string                `json:"aviso,omitempty"`
		Cambios []vencimientos.Cambio `json:"cambios"`
	}{s.agendaParaModelo(), desde.Format(vencimientos.FormatoISO), "", cambios}
	if len(cambios) == 0 {
		// Una lista vacía se lee como "ARCA no movió nada", y puede ser que
		// todavía no haya dos bajadas para comparar.
		salida.Aviso = "No se detectó ningún cambio desde esa fecha. Hace falta al menos una segunda bajada " +
			"de la agenda para que haya algo contra qué comparar."
	}
	return comoJSON(salida)
}

func (s *Servidor) hVencimientosImpuestos(context.Context) *ResultadoHerramienta {
	if r := s.sinAgenda(); r != nil {
		return r
	}
	return comoJSON(struct {
		Agenda    agendaParaModelo              `json:"agenda"`
		Impuestos []vencimientos.ConteoImpuesto `json:"impuestos"`
	}{s.agendaParaModelo(), s.srv.ImpuestosDeVencimientos()})
}
