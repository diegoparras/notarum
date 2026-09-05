// Package api expone el Boletín Oficial como JSON de sólo lectura.
package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/asistente"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/contrato"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/lockatus"
	"github.com/diegoparras/notarum/internal/mcp"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/tareas"
	"github.com/diegoparras/notarum/internal/web"
)

// Config configura el servidor HTTP.
type Config struct {
	Servicio  *servicio.Servicio
	PorMinuto int    // pedidos por minuto por IP; 0 desactiva el límite
	Version   string // se informa en /v1/salud
	// TokenMCP, si no está vacío, exige Bearer en /mcp. Vacío lo deja abierto,
	// como el resto de la API.
	TokenMCP string
	// SinMCP apaga el endpoint /mcp.
	SinMCP bool
	// SinWeb apaga el lector web y deja sólo la API.
	SinWeb bool
	// Registro habilita las cuentas. Sin él no hay login ni tokens.
	Registro *cuentas.Registro
	// Politica dice qué puede hacer quien no se identificó y cuánta cuota
	// tiene cada rol. La define quien opera la instancia.
	Politica cuentas.Politica
	// Hub, si está, delega el login en Lockatus. Sólo lo usa el lector web:
	// la API y el MCP se identifican con tokens de acá.
	Hub *lockatus.Cliente
	// Tareas corre los trabajos largos que se lanzan desde el panel.
	Tareas *tareas.Ejecutor
	// Asistente arma consultas a partir de un pedido en castellano. Sólo lo
	// usa el lector web.
	Asistente *asistente.Cliente
}

// Servidor atiende las rutas de /v1.
type Servidor struct {
	srv      *servicio.Servicio
	version  string
	mux      *http.ServeMux
	handler  http.Handler
	inicio   time.Time
	mcp      http.Handler
	conMCP   bool
	web      http.Handler
	conWeb   bool
	politica cuentas.Politica
}

// Nuevo arma el servidor con sus rutas y middlewares.
func Nuevo(cfg Config) *Servidor {
	politica := cfg.Politica
	if !politica.Modo.Valido() {
		politica = cuentas.PoliticaPorDefecto(cfg.Registro != nil && cfg.Registro.HayUsuarios())
		// PorMinuto es la forma corta de fijar la cuota anónima sin armar una
		// política entera; se sigue usando en los tests y en el arranque.
		if cfg.PorMinuto > 0 {
			politica.Anonimo = cfg.PorMinuto
		}
	}
	s := &Servidor{
		srv:      cfg.Servicio,
		version:  cfg.Version,
		mux:      http.NewServeMux(),
		inicio:   time.Now(),
		politica: politica,
	}
	if !cfg.SinMCP {
		s.mcp = mcp.Nuevo(cfg.Servicio, cfg.Version).Handler(cfg.TokenMCP)
		s.conMCP = true
	}
	if !cfg.SinWeb {
		sitio, err := web.Nuevo(cfg.Servicio, cfg.Version)
		if err != nil {
			// Una plantilla rota es un error de programa, no de configuración:
			// vale más enterarse al arrancar que servir páginas rotas.
			panic("no se pudo armar el lector web: " + err.Error())
		}
		sitio = sitio.ConMCP(!cfg.SinMCP).ConCuentas(cfg.Registro, politica)
		if cfg.Hub != nil {
			sitio = sitio.ConLockatus(cfg.Hub)
		}
		if cfg.Tareas != nil {
			sitio = sitio.ConTareas(cfg.Tareas)
		}
		if cfg.Asistente != nil {
			sitio = sitio.ConAsistente(cfg.Asistente)
		}
		s.web = sitio
		s.conWeb = true
	}
	s.rutas()
	s.handler = conPanico(conLog(conCORS(nuevaGuardia(cfg.Registro, s.politica).envolver(s.mux))))
	return s
}

func (s *Servidor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// rutasDeLaAPI son los caminos que atiende /v1, en la notación de OpenAPI.
//
// Están en una lista y no sueltos en rutas() para que el contrato no se pueda
// desactualizar sin que nadie se entere: hay un test que compara esta lista
// contra openapi.json. Agregar una ruta acá y olvidarse de documentarla
// rompe la suite, que es lo que tiene que pasar.
func (s *Servidor) rutasDeLaAPI() []ruta {
	return []ruta{
		{"/v1/calendario/{anio}/{seccion}", s.verCalendario},
		{"/v1/ediciones/{seccion}/{fecha}", s.verEdicion},
		{"/v1/ediciones/{seccion}", s.verRango},
		{"/v1/avisos/{seccion}/{id}/{fecha}", s.verAviso},
		{"/v1/anexos/{seccion}/{nro}/{id}/{fecha}", s.verAnexo},
		{"/v1/rubros/{seccion}", s.verRubros},
		{"/v1/buscar", s.buscar},
		{"/v1/secciones", s.verSecciones},
		{"/v1/provincial", s.buscarProvincial},
		{"/v1/provincial/provincias", s.verProvincias},
		{"/v1/provincial/tipos", s.verTiposProvinciales},
		{"/v1/provincial/{id}", s.verNormaProvincial},
		{"/v1/nacional", s.buscarNacional},
		{"/v1/nacional/tipos", s.verTiposNacionales},
		{"/v1/nacional/{id}", s.verNormaNacional},
		{"/v1/salud", s.verSalud},
		{"/v1/openapi.json", s.verOpenAPI},
	}
}

// ruta es un camino de la API con quien lo atiende.
type ruta struct {
	Camino  string
	Atiende http.HandlerFunc
}

func (s *Servidor) rutas() {
	m := s.mux
	for _, r := range s.rutasDeLaAPI() {
		m.HandleFunc("GET "+r.Camino, r.Atiende)
	}
	if s.mcp != nil {
		m.Handle("/mcp", s.mcp)
		m.Handle("/mcp/", s.mcp)
	}
	m.HandleFunc("GET /v1/{$}", s.verIndice)
	// Una ruta desconocida bajo /v1 sigue siendo un error de la API, con su
	// JSON: quien la pidió es un programa, no alguien mirando páginas.
	m.HandleFunc("/v1/", s.noEncontrado)
	if s.conWeb {
		// El lector se queda con la raíz; la API vive bajo /v1.
		m.Handle("/", s.web)
	} else {
		m.HandleFunc("GET /{$}", s.verIndice)
		m.HandleFunc("/", s.noEncontrado)
	}
}

// --------------------------------------------------------------- parámetros

func (s *Servidor) seccionDe(w http.ResponseWriter, r *http.Request) (boletin.Seccion, bool) {
	sec, err := boletin.ParseSeccion(r.PathValue("seccion"))
	if err != nil {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "sección inválida", err.Error())
		return "", false
	}
	return sec, true
}

func (s *Servidor) fechaDe(w http.ResponseWriter, r *http.Request, nombre, valor string) (boletin.Fecha, bool) {
	f, err := boletin.ParseFecha(valor)
	if err != nil {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, nombre+" inválida", err.Error())
		return boletin.Fecha{}, false
	}
	return f, true
}

// cacheDeFecha: lo pasado no cambia; lo de hoy, sí.
func cacheDeFecha(f boletin.Fecha) string {
	if f.API() < boletin.HoyEnArgentina().API() {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=300"
}

// ---------------------------------------------------------------- handlers

func (s *Servidor) verCalendario(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.seccionDe(w, r)
	if !ok {
		return
	}
	anio, err := strconv.Atoi(r.PathValue("anio"))
	if err != nil || anio < 1990 || anio > boletin.HoyEnArgentina().Year()+1 {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "año inválido",
			"se esperaba un año entre 1990 y "+strconv.Itoa(boletin.HoyEnArgentina().Year()+1))
		return
	}
	cal, err := s.srv.Calendario(r.Context(), sec, anio)
	if err != nil {
		escribirErrorDeLectura(w, r, err)
		return
	}
	cache := "public, max-age=31536000, immutable"
	if anio >= boletin.HoyEnArgentina().Year() {
		cache = "public, max-age=21600"
	}
	escribirJSON(w, r, http.StatusOK, cal, cache)
}

func (s *Servidor) verEdicion(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.seccionDe(w, r)
	if !ok {
		return
	}
	fecha, ok := s.fechaDe(w, r, "fecha", r.PathValue("fecha"))
	if !ok {
		return
	}
	ed, err := s.srv.Edicion(r.Context(), sec, fecha, r.URL.Query().Get("rubro"))
	if err != nil {
		escribirErrorDeLectura(w, r, err)
		return
	}
	escribirJSON(w, r, http.StatusOK, ed, cacheDeFecha(fecha))
}

func (s *Servidor) verRango(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.seccionDe(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("desde") == "" || q.Get("hasta") == "" {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "faltan desde y hasta",
			"ejemplo: /v1/ediciones/primera?desde=2026-09-01&hasta=2026-09-30")
		return
	}
	desde, ok := s.fechaDe(w, r, "desde", q.Get("desde"))
	if !ok {
		return
	}
	hasta, ok := s.fechaDe(w, r, "hasta", q.Get("hasta"))
	if !ok {
		return
	}
	rango, err := s.srv.Resumenes(r.Context(), sec, desde, hasta)
	if err != nil {
		if strings.Contains(err.Error(), "rango") {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido, "rango inválido", err.Error())
			return
		}
		escribirErrorDeLectura(w, r, err)
		return
	}
	escribirJSON(w, r, http.StatusOK, rango, "public, max-age=300")
}

func (s *Servidor) verAviso(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.seccionDe(w, r)
	if !ok {
		return
	}
	fecha, ok := s.fechaDe(w, r, "fecha", r.PathValue("fecha"))
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "falta el id del aviso", "")
		return
	}
	d, err := s.srv.Aviso(r.Context(), sec, id, fecha)
	if err != nil {
		escribirErrorDeLectura(w, r, err)
		return
	}
	escribirJSON(w, r, http.StatusOK, d, cacheDeFecha(fecha))
}

func (s *Servidor) verAnexo(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.seccionDe(w, r)
	if !ok {
		return
	}
	// La fecha se acepta en AAAA-MM-DD y también en AAAAMMDD, que es como la
	// escribe el sitio y como podría venir copiada de un enlace viejo.
	fechaTxt := strings.TrimSuffix(r.PathValue("fecha"), ".pdf")
	fecha, err := boletin.ParseFecha(fechaTxt)
	if err != nil {
		if f, err2 := boletin.ParseFechaSitio(fechaTxt); err2 == nil {
			fecha = f
		} else {
			escribirError(w, r, http.StatusBadRequest, OrigenPedido, "fecha inválida", err.Error())
			return
		}
	}
	pdf, err := s.srv.Anexo(r.Context(), sec, r.PathValue("nro"), r.PathValue("id"), fecha)
	if err != nil {
		escribirErrorDeLectura(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Content-Disposition", `inline; filename="anexo-`+r.PathValue("id")+`.pdf"`)
	_, _ = w.Write(pdf)
}

func (s *Servidor) verRubros(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.seccionDe(w, r)
	if !ok {
		return
	}
	rs, err := s.srv.Rubros(r.Context(), sec)
	if err != nil {
		escribirErrorDeLectura(w, r, err)
		return
	}
	escribirJSON(w, r, http.StatusOK, rs, "public, max-age=86400")
}

func (s *Servidor) buscar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sec, err := boletin.ParseSeccion(q.Get("seccion"))
	if err != nil {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "sección inválida", err.Error())
		return
	}
	if q.Get("desde") == "" || q.Get("hasta") == "" {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "faltan desde y hasta",
			"ejemplo: /v1/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-03")
		return
	}
	desde, ok := s.fechaDe(w, r, "desde", q.Get("desde"))
	if !ok {
		return
	}
	hasta, ok := s.fechaDe(w, r, "hasta", q.Get("hasta"))
	if !ok {
		return
	}
	if hasta.Before(desde.Time) {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "rango inválido", "hasta es anterior a desde")
		return
	}
	pagina, _ := strconv.Atoi(q.Get("pagina"))
	rubro := q.Get("rubro")

	// fuente: "indice" busca sin tocar el Boletín (necesita el motor sqlite),
	// "sitio" siempre le pregunta al Boletín, y "auto" (por defecto) usa el
	// índice cuando tiene historia del rango.
	fuente := strings.ToLower(strings.TrimSpace(q.Get("fuente")))
	if fuente == "" {
		fuente = "auto"
	}
	switch fuente {
	case "indice", "sitio", "auto":
	default:
		escribirError(w, r, http.StatusBadRequest, OrigenPedido, "fuente inválida",
			`se esperaba indice, sitio o auto`)
		return
	}
	if fuente == "indice" && !s.srv.TieneIndice() {
		escribirError(w, r, http.StatusBadRequest, OrigenPedido,
			"esta instancia no tiene índice local",
			"se levantó con el almacén de disco; para buscar sin pegarle al Boletín, arrancá con NOTARUM_ALMACEN=sqlite y llená la historia con `notarum rellenar`")
		return
	}

	usarIndice := fuente == "indice"
	if fuente == "auto" && s.srv.TieneIndice() {
		_, _, hayCobertura := s.srv.PuedeBuscarLocal(r.Context(), sec, desde, hasta)
		usarIndice = hayCobertura
	}

	var (
		res  *servicio.Busqueda
		errB error
	)
	if usarIndice {
		limite, _ := strconv.Atoi(q.Get("limite"))
		res, errB = s.srv.BuscarEnIndice(r.Context(), almacen.ConsultaLocal{
			Texto:   q.Get("texto"),
			Seccion: sec,
			Rubro:   rubro,
			Desde:   desde,
			Hasta:   hasta,
			Limite:  limite,
		}, pagina)
	} else {
		var rubros []string
		if rubro != "" {
			rubros = []string{rubro}
		}
		res, errB = s.srv.BuscarEnSitio(r.Context(), boletin.ConsultaBusqueda{
			Texto:            q.Get("texto"),
			Seccion:          sec,
			Rubros:           rubros,
			Desde:            desde,
			Hasta:            hasta,
			Pagina:           pagina,
			TodasLasPalabras: q.Get("todas") == "true" || q.Get("todas") == "1",
		})
	}
	if errB != nil {
		escribirErrorDeLectura(w, r, errB)
		return
	}
	escribirJSON(w, r, http.StatusOK, res, "public, max-age=300")
}

func (s *Servidor) verSecciones(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID          string `json:"id"`
		Nombre      string `json:"nombre"`
		Descripcion string `json:"descripcion"`
	}
	escribirJSON(w, r, http.StatusOK, []item{
		{"primera", "Primera", "Legislación y avisos oficiales: decretos, resoluciones, disposiciones."},
		{"segunda", "Segunda", "Sociedades, convocatorias, sucesiones y edictos judiciales."},
		{"tercera", "Tercera", "Contrataciones: licitaciones, adjudicaciones y concursos."},
	}, "public, max-age=86400")
}

// Salud es lo que devuelve /v1/salud.
type Salud struct {
	OK            bool             `json:"ok"`
	Version       string           `json:"version,omitempty"`
	EnPieDesde    time.Time        `json:"en_pie_desde"`
	SitioResponde bool             `json:"sitio_responde"`
	UltimaLectura *time.Time       `json:"ultima_lectura,omitempty"`
	Sitio         boletin.Metricas `json:"sitio"`
	Cache         any              `json:"cache"`
}

func (s *Servidor) verSalud(w http.ResponseWriter, r *http.Request) {
	m := s.srv.Cliente().Metricas()
	salud := Salud{
		OK:            true,
		Version:       s.version,
		EnPieDesde:    s.inicio.UTC(),
		SitioResponde: m.SitioResponde || m.Lecturas == 0,
		UltimaLectura: m.UltimaLectura,
		Sitio:         m,
		Cache:         s.srv.Almacen().Metricas(),
	}
	escribirJSON(w, r, http.StatusOK, salud, "no-store")
}

func (s *Servidor) verOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(contrato.JSON())
}

func (s *Servidor) verIndice(w http.ResponseWriter, r *http.Request) {
	hoy := boletin.HoyEnArgentina().API()
	escribirJSON(w, r, http.StatusOK, map[string]any{
		"nombre":      "notarum",
		"descripcion": "API abierta de sólo lectura del Boletín Oficial de la República Argentina.",
		"version":     s.version,
		"fuente":      boletin.BaseSitio,
		"contrato":    "/v1/openapi.json",
		"rutas": []string{
			"/v1/secciones",
			"/v1/calendario/{anio}/{seccion}",
			"/v1/ediciones/{seccion}/{fecha}",
			"/v1/ediciones/{seccion}?desde=&hasta=",
			"/v1/avisos/{seccion}/{id}/{fecha}",
			"/v1/anexos/{seccion}/{nro}/{id}/{fecha}.pdf",
			"/v1/rubros/{seccion}",
			"/v1/buscar?seccion=&texto=&desde=&hasta=",
			"/v1/salud",
		},
		"mcp":     mapaMCP(s.conMCP),
		"ejemplo": "/v1/ediciones/primera/" + hoy,
	}, "public, max-age=3600")
}

// mapaMCP describe el endpoint MCP en el índice, para que quien llega a la
// raíz sepa que además de la API hay herramientas para un modelo.
func mapaMCP(activo bool) any {
	if !activo {
		return map[string]any{"activo": false}
	}
	return map[string]any{
		"activo":    true,
		"ruta":      "/mcp",
		"protocolo": mcp.VersionProtocolo,
		"como":      "JSON-RPC 2.0 por POST; probá con initialize y tools/list",
	}
}

// mapaWeb describe el lector en el índice de la API.
func mapaWeb(activo bool) any {
	if !activo {
		return map[string]any{"activo": false}
	}
	return map[string]any{"activo": true, "ruta": "/"}
}

func (s *Servidor) noEncontrado(w http.ResponseWriter, r *http.Request) {
	escribirError(w, r, http.StatusNotFound, OrigenPedido, "ruta no encontrada",
		"el índice de rutas está en /v1/")
}
