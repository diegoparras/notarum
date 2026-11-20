package boletin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrSinEdicion indica que ese día no hubo edición: el sitio contesta con un
// 302 a la portada. No es una falla.
var ErrSinEdicion = errors.New("no hubo edición ese día")

// ErrDelSitio envuelve todo lo que salió mal del lado de boletinoficial.gob.ar:
// no contestó, contestó mal, o cambió de forma. Sirve para que la API pueda
// decir de quién es la culpa.
type ErrDelSitio struct {
	Operacion string
	URL       string
	Codigo    int
	Causa     error
}

func (e *ErrDelSitio) Error() string {
	if e.Codigo != 0 {
		return fmt.Sprintf("%s: el Boletín Oficial respondió %d en %s", e.Operacion, e.Codigo, e.URL)
	}
	return fmt.Sprintf("%s: %v", e.Operacion, e.Causa)
}

func (e *ErrDelSitio) Unwrap() error { return e.Causa }

// IntervaloPorDefecto es la espera entre pedidos al sitio cuando nadie
// configura otra (NOTARUM_INTERVALO).
//
// Hasta la 1.7.2 fue medio segundo. El 19/11/2026, con ese valor, el sitio
// empezó a contestar 429 después de unos 140 pedidos en cinco minutos desde
// una misma dirección. No es un límite que el Boletín documente, es lo que se
// vio ese día; pero medio segundo deja salir 600 en cinco minutos, más de
// cuatro veces eso. Con dos segundos y medio salen 120.
const IntervaloPorDefecto = 2500 * time.Millisecond

// TopeRetryAfter es lo máximo que se le cree a un Retry-After del sitio. Lo
// que el sitio pide se respeta, pero un valor desmesurado (o mal formado, que
// viene a ser lo mismo) no puede dejar al cliente mudo por días: pasada la
// hora sale un pedido de prueba, y si el sitio sigue limitando lo vuelve a
// decir.
const TopeRetryAfter = time.Hour

// ErrLimitado dice que el sitio pidió que se le pida menos (contestó 429) y
// que el cliente dejó de pedirle hasta Hasta.
//
// Hay dos casos y los dos llevan este error: el pedido que se encontró con el
// 429, y los que vinieron después y no salieron (SinPedir). El texto de los dos
// dice "respondió 429": hay clientes de la API que reconocen el caso por esas
// palabras, y cambiarlas los deja insistiendo.
type ErrLimitado struct {
	Operacion string
	URL       string
	Desde     time.Time // cuándo contestó 429 el sitio
	Hasta     time.Time // hasta cuándo no se le vuelve a pedir
	SinPedir  bool      // este pedido no llegó a salir
}

func (e *ErrLimitado) Error() string {
	hasta := e.Hasta.UTC().Format(time.RFC3339)
	if e.SinPedir {
		return fmt.Sprintf("%s: no se le pidió al Boletín Oficial, que respondió 429 (demasiados pedidos) a las %s; no se le vuelve a pedir hasta las %s",
			e.Operacion, e.Desde.UTC().Format(time.RFC3339), hasta)
	}
	return fmt.Sprintf("%s: el Boletín Oficial respondió 429 en %s; no se le vuelve a pedir hasta las %s",
		e.Operacion, e.URL, hasta)
}

// Unwrap deja que quien sólo pregunta "¿fue culpa del sitio?" lo siga viendo
// como un ErrDelSitio con código 429.
func (e *ErrLimitado) Unwrap() error {
	return &ErrDelSitio{Operacion: e.Operacion, URL: e.URL, Codigo: http.StatusTooManyRequests}
}

// Opciones configura el cliente.
type Opciones struct {
	Base       string        // origen del sitio; por defecto BaseSitio
	UserAgent  string        // se envía en cada pedido, con contacto
	Intervalo  time.Duration // espera mínima entre pedidos al sitio
	Timeout    time.Duration // por pedido
	Reintentos int           // ante 5xx o error de red
	EsperaBase time.Duration // primera espera del backoff
	// PausaBase es cuánto se deja de pedir ante un 429 que no trae
	// Retry-After. Se duplica con cada 429 seguido, hasta PausaTope, y vuelve
	// al principio cuando el sitio contesta bien.
	PausaBase time.Duration
	PausaTope time.Duration
	HTTP      *http.Client
}

// Cliente lee el sitio del Boletín Oficial respetando un ritmo fijo. Es seguro
// para uso concurrente: el ritmo es global al cliente, no por goroutine.
type Cliente struct {
	base       string
	ua         string
	intervalo  time.Duration
	reintentos int
	esperaBase time.Duration
	http       *http.Client

	mu      sync.Mutex
	proximo time.Time

	pausaBase time.Duration
	pausaTope time.Duration

	// freno es la ventana en la que no se le pide nada al sitio porque
	// contestó 429. Ver frenar y limitado.
	freno struct {
		sync.Mutex
		desde    time.Time // cuándo llegó el último 429
		hasta    time.Time // hasta cuándo no se pide
		seguidos int       // 429 seguidos, sin una respuesta buena en el medio
		veces    int64     // 429 recibidos desde que arrancó
		sinPedir int64     // pedidos que no salieron por estar en la ventana
	}

	// contadores para /v1/salud
	cont struct {
		sync.Mutex
		lecturas      int64
		errores       int64
		ultimaLectura time.Time
		ultimoOK      bool
	}
}

// NuevoCliente arma un cliente con valores razonables para un sitio público
// con protección F5: un pedido cada IntervaloPorDefecto, sin paralelismo
// agresivo.
func NuevoCliente(o Opciones) *Cliente {
	if o.Base == "" {
		o.Base = BaseSitio
	}
	if o.UserAgent == "" {
		o.UserAgent = "notarum (+https://github.com/diegoparras/notarum)"
	}
	if o.Intervalo <= 0 {
		o.Intervalo = IntervaloPorDefecto
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Reintentos <= 0 {
		o.Reintentos = 3
	}
	if o.EsperaBase <= 0 {
		o.EsperaBase = time.Second
	}
	if o.PausaBase <= 0 {
		o.PausaBase = time.Minute
	}
	if o.PausaTope <= 0 {
		o.PausaTope = 30 * time.Minute
	}
	if o.PausaTope < o.PausaBase {
		o.PausaTope = o.PausaBase
	}
	cli := o.HTTP
	if cli == nil {
		cli = &http.Client{Timeout: o.Timeout}
	}
	// Un 302 a "/" significa "no hubo edición": hay que verlo, no seguirlo.
	cli.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Cliente{
		base:       strings.TrimRight(o.Base, "/"),
		ua:         o.UserAgent,
		intervalo:  o.Intervalo,
		reintentos: o.Reintentos,
		esperaBase: o.EsperaBase,
		pausaBase:  o.PausaBase,
		pausaTope:  o.PausaTope,
		http:       cli,
	}
}

// Metricas describe lo que el cliente le pidió al sitio.
type Metricas struct {
	Lecturas      int64      `json:"lecturas"`
	Errores       int64      `json:"errores"`
	UltimaLectura *time.Time `json:"ultima_lectura,omitempty"`
	SitioResponde bool       `json:"ultimo_pedido_ok"`

	// Limitando: el sitio contestó 429 y todavía no se le volvió a pedir.
	// LimitadoHasta dice cuándo se reanuda; sólo viene mientras dura.
	Limitando     bool       `json:"limitando"`
	LimitadoHasta *time.Time `json:"limitado_hasta,omitempty"`
	Ultimo429     *time.Time `json:"ultimo_429,omitempty"`
	// VecesLimitado cuenta los 429 del sitio; PedidosFrenados, los pedidos
	// que no salieron por haber llegado adentro de una ventana. Los frenados
	// no son lecturas: el sitio nunca los vio.
	VecesLimitado   int64 `json:"veces_limitado"`
	PedidosFrenados int64 `json:"pedidos_frenados"`
}

func (c *Cliente) Metricas() Metricas {
	c.cont.Lock()
	defer c.cont.Unlock()
	m := Metricas{Lecturas: c.cont.lecturas, Errores: c.cont.errores, SitioResponde: c.cont.ultimoOK}
	if !c.cont.ultimaLectura.IsZero() {
		t := c.cont.ultimaLectura
		m.UltimaLectura = &t
	}

	c.freno.Lock()
	defer c.freno.Unlock()
	m.VecesLimitado = c.freno.veces
	m.PedidosFrenados = c.freno.sinPedir
	if !c.freno.desde.IsZero() {
		t := c.freno.desde.UTC()
		m.Ultimo429 = &t
	}
	if time.Now().Before(c.freno.hasta) {
		t := c.freno.hasta.UTC()
		m.Limitando = true
		m.LimitadoHasta = &t
	}
	return m
}

// limitado devuelve el error de un pedido que no va a salir porque el sitio
// está limitando, o nil si se puede pedir.
func (c *Cliente) limitado(op, destino string) error {
	c.freno.Lock()
	defer c.freno.Unlock()
	if !time.Now().Before(c.freno.hasta) {
		return nil
	}
	c.freno.sinPedir++
	return &ErrLimitado{Operacion: op, URL: destino, Desde: c.freno.desde, Hasta: c.freno.hasta, SinPedir: true}
}

// frenar abre la ventana después de un 429 y devuelve el error del pedido que
// lo recibió.
//
// Si la ventana ya estaba abierta —dos pedidos salieron casi juntos y los dos
// volvieron con 429— no se toca: es el mismo aviso del sitio, y contarlo dos
// veces haría crecer la espera sin que el sitio haya vuelto a quejarse.
func (c *Cliente) frenar(op, destino, retryAfter string) error {
	c.freno.Lock()
	defer c.freno.Unlock()
	ahora := time.Now()
	if !ahora.Before(c.freno.hasta) {
		pausa, loPidioElSitio := leerRetryAfter(retryAfter, ahora)
		if !loPidioElSitio {
			pausa = c.pausaBase
			for i := 0; i < c.freno.seguidos && pausa < c.pausaTope; i++ {
				pausa *= 2
			}
			if pausa > c.pausaTope {
				pausa = c.pausaTope
			}
		}
		c.freno.seguidos++
		c.freno.veces++
		c.freno.desde = ahora
		c.freno.hasta = ahora.Add(pausa)
	}
	return &ErrLimitado{Operacion: op, URL: destino, Desde: c.freno.desde, Hasta: c.freno.hasta}
}

// soltar anota que el sitio volvió a contestar: el próximo 429 empieza otra
// vez por la pausa más corta.
func (c *Cliente) soltar() {
	c.freno.Lock()
	c.freno.seguidos = 0
	c.freno.Unlock()
}

// leerRetryAfter entiende las dos formas de la cabecera: segundos o una fecha
// HTTP. Un valor vacío, ilegible, de cero o ya vencido no cuenta como pedido
// del sitio: ahí decide la espera propia.
func leerRetryAfter(valor string, ahora time.Time) (time.Duration, bool) {
	valor = strings.TrimSpace(valor)
	if valor == "" {
		return 0, false
	}
	var pausa time.Duration
	if seg, err := strconv.ParseInt(valor, 10, 64); err == nil {
		if seg > int64(TopeRetryAfter/time.Second) {
			return TopeRetryAfter, true
		}
		pausa = time.Duration(seg) * time.Second
	} else if fecha, err := http.ParseTime(valor); err == nil {
		pausa = fecha.Sub(ahora)
	}
	if pausa <= 0 {
		return 0, false
	}
	if pausa > TopeRetryAfter {
		pausa = TopeRetryAfter
	}
	return pausa, true
}

func (c *Cliente) anotar(ok bool) {
	c.cont.Lock()
	defer c.cont.Unlock()
	c.cont.lecturas++
	if !ok {
		c.cont.errores++
	}
	c.cont.ultimaLectura = time.Now().UTC()
	c.cont.ultimoOK = ok
}

// esperarTurno respeta el intervalo entre pedidos al sitio.
func (c *Cliente) esperarTurno(ctx context.Context) error {
	c.mu.Lock()
	ahora := time.Now()
	espera := time.Duration(0)
	if ahora.Before(c.proximo) {
		espera = c.proximo.Sub(ahora)
	}
	c.proximo = ahora.Add(espera + c.intervalo)
	c.mu.Unlock()

	if espera <= 0 {
		return nil
	}
	t := time.NewTimer(espera)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type respuesta struct {
	cuerpo   []byte
	codigo   int
	location string
}

// pedir hace un pedido con ritmo y reintentos. Los 4xx no se reintentan.
//
// Un 429 abre una ventana en la que no sale nada: ni los pedidos nuevos ni los
// que ya estaban esperando turno. Un sitio que acaba de pedir que se le pida
// menos no tiene que recibir, encima, la cola entera.
func (c *Cliente) pedir(ctx context.Context, op, metodo, ruta string, form url.Values) (*respuesta, error) {
	destino := c.base + ruta
	var ultimo error
	for intento := 0; intento <= c.reintentos; intento++ {
		if intento > 0 {
			espera := c.esperaBase * (1 << (intento - 1))
			t := time.NewTimer(espera)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, ctx.Err()
			case <-t.C:
			}
		}
		if err := c.limitado(op, destino); err != nil {
			return nil, err
		}
		if err := c.esperarTurno(ctx); err != nil {
			return nil, err
		}
		// Otra vez: mientras este pedido esperaba su turno, otro pudo haber
		// recibido el 429.
		if err := c.limitado(op, destino); err != nil {
			return nil, err
		}

		var lector io.Reader
		if form != nil {
			lector = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, metodo, destino, lector)
		if err != nil {
			return nil, &ErrDelSitio{Operacion: op, URL: destino, Causa: err}
		}
		req.Header.Set("User-Agent", c.ua)
		req.Header.Set("Accept-Language", "es-AR,es;q=0.9")
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			req.Header.Set("Referer", c.base+"/")
		}

		res, err := c.http.Do(req)
		if err != nil {
			ultimo = err
			c.anotar(false)
			continue
		}
		cuerpo, errLec := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		loc := res.Header.Get("Location")
		retryAfter := res.Header.Get("Retry-After")
		codigo := res.StatusCode
		res.Body.Close()

		if codigo == http.StatusTooManyRequests {
			c.anotar(false)
			return nil, c.frenar(op, destino, retryAfter)
		}

		if errLec != nil {
			ultimo = errLec
			c.anotar(false)
			continue
		}
		if codigo >= 500 {
			ultimo = fmt.Errorf("respondió %d", codigo)
			c.anotar(false)
			continue
		}
		c.anotar(codigo < 400)
		c.soltar()
		if codigo >= 400 {
			return nil, &ErrDelSitio{Operacion: op, URL: destino, Codigo: codigo}
		}
		return &respuesta{cuerpo: cuerpo, codigo: codigo, location: loc}, nil
	}
	return nil, &ErrDelSitio{Operacion: op, URL: destino, Causa: ultimo}
}

// ------------------------------------------------------------------ lecturas

// TraerEdicion lee la portada de una sección en una fecha. Devuelve
// ErrSinEdicion si ese día no hubo.
func (c *Cliente) TraerEdicion(ctx context.Context, sec Seccion, fecha Fecha, rubro string) (*Edicion, error) {
	ruta := "/seccion/" + string(sec) + "/" + fecha.Sitio()
	if rubro != "" {
		ruta += "?rubro=" + url.QueryEscape(rubro)
	}
	res, err := c.pedir(ctx, "leer edición", http.MethodGet, ruta, nil)
	if err != nil {
		return nil, err
	}
	if res.codigo >= 300 && res.codigo < 400 {
		return nil, ErrSinEdicion
	}
	ed, err := ParsearPortada(res.cuerpo, sec, fecha)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "leer edición", URL: c.base + ruta, Causa: err}
	}
	return ed, nil
}

// TraerAviso lee el detalle de un aviso.
func (c *Cliente) TraerAviso(ctx context.Context, sec Seccion, id string, fecha Fecha) (*Detalle, error) {
	ruta := "/detalleAviso/" + string(sec) + "/" + url.PathEscape(id) + "/" + fecha.Sitio()
	res, err := c.pedir(ctx, "leer aviso", http.MethodGet, ruta, nil)
	if err != nil {
		return nil, err
	}
	if res.codigo >= 300 && res.codigo < 400 {
		return nil, ErrSinEdicion
	}
	d, err := ParsearDetalle(res.cuerpo, sec, id, fecha)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "leer aviso", URL: c.base + ruta, Causa: err}
	}
	return d, nil
}

// TraerCalendario lee los días de publicación de un año.
func (c *Cliente) TraerCalendario(ctx context.Context, sec Seccion, anio int) (*Calendario, error) {
	ruta := "/calendario/dias_publicacion/" + strconv.Itoa(anio) + "/" + string(sec)
	res, err := c.pedir(ctx, "leer calendario", http.MethodGet, ruta, nil)
	if err != nil {
		return nil, err
	}
	cal, err := ParsearCalendario(res.cuerpo, sec, anio)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "leer calendario", URL: c.base + ruta, Causa: err}
	}
	return cal, nil
}

// TraerRubros lee el catálogo de rubros de una sección.
func (c *Cliente) TraerRubros(ctx context.Context, sec Seccion) ([]Rubro, error) {
	ruta := "/busquedaAvanzada/" + string(sec) + "/rubros"
	res, err := c.pedir(ctx, "leer rubros", http.MethodGet, ruta, nil)
	if err != nil {
		return nil, err
	}
	rs, err := ParsearRubros(res.cuerpo)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "leer rubros", URL: c.base + ruta, Causa: err}
	}
	return rs, nil
}

// ConsultaBusqueda son los criterios de la búsqueda avanzada.
type ConsultaBusqueda struct {
	Texto            string
	Seccion          Seccion
	Rubros           []string
	Desde            Fecha
	Hasta            Fecha
	Pagina           int
	TodasLasPalabras bool
}

// Buscar consulta la búsqueda avanzada del sitio. El sitio espera las fechas
// en dd/mm/aaaa y la sección como número.
func (c *Cliente) Buscar(ctx context.Context, q ConsultaBusqueda) (*ResultadoBusqueda, error) {
	if q.Pagina < 1 {
		q.Pagina = 1
	}
	rubros := q.Rubros
	if rubros == nil {
		rubros = []string{}
	}
	params := map[string]any{
		"texto":                                q.Texto,
		"seccion":                              []string{q.Seccion.IDBusqueda()},
		"rubros":                               rubros,
		"fechaDesde":                           q.Desde.Format("02/01/2006"),
		"fechaHasta":                           q.Hasta.Format("02/01/2006"),
		"tipoBusqueda":                         "Avanzada",
		"numeroPagina":                         q.Pagina,
		"ultimoRubro":                          "",
		"busquedaRubro":                        false,
		"hayMasResultadosBusqueda":             true,
		"ejecutandoLlamadaAsincronicaBusqueda": false,
		"ultimaSeccion":                        "",
		"todasLasPalabras":                     q.TodasLasPalabras,
		"filtroPorRubrosSeccion":               false,
		"filtroPorRubroBusqueda":               len(rubros) > 0,
		"filtroPorSeccionBusqueda":             false,
		"busquedaOriginal":                     q.Pagina == 1,
		"ordenamientoSegunda":                  true,
		"seccionesOriginales":                  []string{},
	}
	crudo, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("params", string(crudo))
	form.Set("array_volver", "[]")

	const ruta = "/busquedaAvanzada/realizarBusqueda"
	res, err := c.pedir(ctx, "buscar", http.MethodPost, ruta, form)
	if err != nil {
		return nil, err
	}
	r, err := ParsearBusqueda(res.cuerpo, q.Seccion, q.Pagina)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "buscar", URL: c.base + ruta, Causa: err}
	}
	return r, nil
}

// TraerAnexo descarga el PDF de un anexo. El sitio lo devuelve en base64
// adentro de un JSON.
func (c *Cliente) TraerAnexo(ctx context.Context, sec Seccion, nro, idAnexo string, fecha Fecha) ([]byte, error) {
	form := url.Values{}
	form.Set("seccion", string(sec))
	form.Set("nroAnexo", nro)
	form.Set("idAnexo", idAnexo)
	form.Set("fechaPublicacion", fecha.Sitio())

	const ruta = "/pdf/download_anexo"
	res, err := c.pedir(ctx, "descargar anexo", http.MethodPost, ruta, form)
	if err != nil {
		return nil, err
	}
	var payload struct {
		PDF string `json:"pdfBase64"`
	}
	if err := json.Unmarshal(res.cuerpo, &payload); err != nil || payload.PDF == "" {
		return nil, &ErrDelSitio{
			Operacion: "descargar anexo",
			URL:       c.base + ruta,
			Causa:     errors.New("el sitio no devolvió el PDF del anexo"),
		}
	}
	pdf, err := base64.StdEncoding.DecodeString(payload.PDF)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: "descargar anexo", URL: c.base + ruta, Causa: err}
	}
	return pdf, nil
}
