package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/vencimientos"
)

// La agenda de vencimientos de ARCA: qué obligación impositiva vence cada día,
// para qué terminación de CUIT y bajo qué norma, y qué fechas movió ARCA.
//
// Cada respuesta dice de cuándo es la agenda y qué período se le pidió a ARCA.
// Sin eso, una lista vacía para noviembre no distingue "no vence nada" de "no
// se preguntó por noviembre".

// cacheVencimientos es corta: la agenda se baja una vez por día, pero "hoy"
// cambia a medianoche.
const cacheVencimientos = "public, max-age=600"

// AgendaInfo es de cuándo es la agenda que contesta.
type AgendaInfo struct {
	BajadaEn time.Time `json:"bajada_en"`
	// Desde y Hasta son el período que se le pidió a ARCA. Afuera no se
	// preguntó nada.
	Desde string `json:"desde"`
	Hasta string `json:"hasta"`
}

func agendaInfo(e servicio.EstadoVencimientos) AgendaInfo {
	return AgendaInfo{BajadaEn: e.SincronizadoEn, Desde: e.Desde, Hasta: e.Hasta}
}

// ResultadoVencimientos es una página de la agenda.
type ResultadoVencimientos struct {
	Agenda       AgendaInfo              `json:"agenda"`
	Total        int                     `json:"total"`
	Pagina       int                     `json:"pagina"`
	HayMas       bool                    `json:"hay_mas"`
	Vencimientos []vencimientos.Registro `json:"vencimientos"`
}

func (s *Servidor) buscarVencimientos(w http.ResponseWriter, r *http.Request) {
	if !s.hayAgenda(w, r) {
		return
	}
	q := r.URL.Query()
	c, ok := s.consultaDeVencimientos(w, r)
	if !ok {
		return
	}
	for _, campo := range []struct {
		nombre string
		dest   *string
	}{{"desde", &c.Desde}, {"hasta", &c.Hasta}} {
		f, err := vencimientos.ParsearFecha(q.Get(campo.nombre))
		if err != nil {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido, campo.nombre+" inválido", err.Error())
			return
		}
		*campo.dest = f
	}
	if c.Desde != "" && c.Hasta != "" && c.Hasta < c.Desde {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "rango inválido", "hasta es anterior a desde")
		return
	}
	c.Texto = q.Get("texto")
	c.SinFechaFija = esSi(q.Get("sin_fecha"))
	c.ConRetirados = esSi(q.Get("retirados"))
	s.contestarVencimientos(w, r, c)
}

// verVencimientosDelDia es lo que vence un día; sin fecha, hoy en Argentina.
func (s *Servidor) verVencimientosDelDia(w http.ResponseWriter, r *http.Request) {
	if !s.hayAgenda(w, r) {
		return
	}
	c, ok := s.consultaDeVencimientos(w, r)
	if !ok {
		return
	}
	dia := boletin.HoyEnArgentina().API()
	if f := r.URL.Query().Get("fecha"); f != "" {
		var err error
		if dia, err = vencimientos.ParsearFecha(f); err != nil {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido, "fecha inválida", err.Error())
			return
		}
	}
	c.Desde, c.Hasta = dia, dia
	s.contestarVencimientos(w, r, c)
}

// verVencimientosProximos es lo que vence de hoy a los próximos días. Arranca
// hoy: lo que vence hoy es lo más urgente que hay.
func (s *Servidor) verVencimientosProximos(w http.ResponseWriter, r *http.Request) {
	if !s.hayAgenda(w, r) {
		return
	}
	c, ok := s.consultaDeVencimientos(w, r)
	if !ok {
		return
	}
	dias := 7
	if v := r.URL.Query().Get("dias"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 366 {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido, "dias inválido: "+v,
				"se espera un número de días entre 1 y 366")
			return
		}
		dias = n
	}
	hoy := boletin.HoyEnArgentina().Time
	c.Desde = hoy.Format(vencimientos.FormatoISO)
	c.Hasta = hoy.AddDate(0, 0, dias-1).Format(vencimientos.FormatoISO)
	s.contestarVencimientos(w, r, c)
}

// consultaDeVencimientos lee lo que comparten las tres búsquedas: impuesto,
// CUIT y paginado.
func (s *Servidor) consultaDeVencimientos(w http.ResponseWriter, r *http.Request) (vencimientos.Consulta, bool) {
	q := r.URL.Query()
	c := vencimientos.Consulta{Impuesto: q.Get("impuesto")}
	// Un CUIT mal escrito no se ignora: la respuesta sería la agenda de todos
	// y quien la pidió creería que es la suya.
	term, err := vencimientos.TerminacionDeCUIT(q.Get("cuit"))
	if err != nil {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "cuit inválido", err.Error())
		return c, false
	}
	c.Terminacion = term
	limite, _ := strconv.Atoi(q.Get("limite"))
	pagina, _ := strconv.Atoi(q.Get("pagina"))
	if pagina < 1 {
		pagina = 1
	}
	if limite <= 0 {
		limite = vencimientos.LimitePorDefecto
	}
	if limite > vencimientos.LimiteMaximo {
		limite = vencimientos.LimiteMaximo
	}
	c.Limite, c.Desplazamiento = limite, (pagina-1)*limite
	return c, true
}

func (s *Servidor) contestarVencimientos(w http.ResponseWriter, r *http.Request, c vencimientos.Consulta) {
	res := s.srv.BuscarVencimientos(c)
	escribirJSON(w, r, http.StatusOK, ResultadoVencimientos{
		Agenda:       agendaInfo(s.srv.EstadoVencimientos()),
		Total:        res.Total,
		Pagina:       c.Desplazamiento/c.Limite + 1,
		HayMas:       res.Truncado,
		Vencimientos: res.Vencimientos,
	}, cacheVencimientos)
}

func (s *Servidor) verCambiosDeVencimientos(w http.ResponseWriter, r *http.Request) {
	if !s.hayAgenda(w, r) {
		return
	}
	q := r.URL.Query()
	var desde time.Time
	if v := strings.TrimSpace(q.Get("desde")); v != "" {
		d, err := time.Parse(vencimientos.FormatoISO, v)
		if err != nil {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido, "desde inválido: "+v, "se espera AAAA-MM-DD")
			return
		}
		desde = d
	}
	limite, _ := strconv.Atoi(q.Get("limite"))
	cambios := s.srv.CambiosDeVencimientos(desde, limite)
	escribirJSON(w, r, http.StatusOK, struct {
		Agenda   AgendaInfo            `json:"agenda"`
		Cantidad int                   `json:"cantidad"`
		Cambios  []vencimientos.Cambio `json:"cambios"`
	}{agendaInfo(s.srv.EstadoVencimientos()), len(cambios), cambios}, cacheVencimientos)
}

func (s *Servidor) verImpuestosDeVencimientos(w http.ResponseWriter, r *http.Request) {
	if !s.hayAgenda(w, r) {
		return
	}
	escribirJSON(w, r, http.StatusOK, s.srv.ImpuestosDeVencimientos(), cacheVencimientos)
}

// hayAgenda contesta por su cuenta cuando no hay con qué responder. "No se
// bajó la agenda" no es lo mismo que "no vence nada", y una lista vacía los
// mostraría iguales.
func (s *Servidor) hayAgenda(w http.ResponseWriter, r *http.Request) bool {
	if !s.srv.VencimientosDisponible() {
		escribirError(w, r, http.StatusNotFound, OrigenNotarum,
			"esta instancia no sigue la agenda de vencimientos",
			"quien la opera la apagó con NOTARUM_SIN_VENCIMIENTOS")
		return false
	}
	if !s.srv.AgendaCargada() {
		escribirError(w, r, http.StatusServiceUnavailable, OrigenNotarum,
			"esta instancia todavía no bajó la agenda de vencimientos",
			"se baja sola todos los días; quien la opera puede bajarla ya desde /admin o con `notarum vencimientos`")
		return false
	}
	return true
}
