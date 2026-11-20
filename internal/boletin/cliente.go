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
	"sort"
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

// Opciones configura el cliente.
type Opciones struct {
	Base       string        // origen del sitio; por defecto BaseSitio
	UserAgent  string        // se envía en cada pedido, con contacto
	Intervalo  time.Duration // espera mínima entre pedidos al sitio
	Timeout    time.Duration // por pedido
	Reintentos int           // ante 5xx o error de red
	EsperaBase time.Duration // primera espera del backoff
	HTTP       *http.Client
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
// con protección F5: un pedido cada medio segundo, sin paralelismo agresivo.
func NuevoCliente(o Opciones) *Cliente {
	if o.Base == "" {
		o.Base = BaseSitio
	}
	if o.UserAgent == "" {
		o.UserAgent = "notarum (+https://github.com/diegoparras/notarum)"
	}
	if o.Intervalo <= 0 {
		o.Intervalo = 500 * time.Millisecond
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
		http:       cli,
	}
}

// Metricas describe lo que el cliente le pidió al sitio.
type Metricas struct {
	Lecturas      int64      `json:"lecturas"`
	Errores       int64      `json:"errores"`
	UltimaLectura *time.Time `json:"ultima_lectura,omitempty"`
	SitioResponde bool       `json:"ultimo_pedido_ok"`
}

func (c *Cliente) Metricas() Metricas {
	c.cont.Lock()
	defer c.cont.Unlock()
	m := Metricas{Lecturas: c.cont.lecturas, Errores: c.cont.errores, SitioResponde: c.cont.ultimoOK}
	if !c.cont.ultimaLectura.IsZero() {
		t := c.cont.ultimaLectura
		m.UltimaLectura = &t
	}
	return m
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
	cookies  []*http.Cookie
}

// pedir hace un pedido con ritmo y reintentos. Los 4xx no se reintentan.
func (c *Cliente) pedir(ctx context.Context, op, metodo, ruta string, form url.Values) (*respuesta, error) {
	return c.pedirCon(ctx, op, metodo, ruta, form, nil)
}

// pedirCon es pedir con cabeceras propias del pedido. Las cookies van por acá
// y no en un tarro compartido: la sesión del sitio recuerda qué edición se
// está mirando, y dos lecturas simultáneas de fechas distintas se pisarían.
func (c *Cliente) pedirCon(ctx context.Context, op, metodo, ruta string, form url.Values, cabeceras http.Header) (*respuesta, error) {
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
		if err := c.esperarTurno(ctx); err != nil {
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
		for clave, valores := range cabeceras {
			for _, v := range valores {
				req.Header.Set(clave, v)
			}
		}

		res, err := c.http.Do(req)
		if err != nil {
			ultimo = err
			c.anotar(false)
			continue
		}
		cuerpo, errLec := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		loc := res.Header.Get("Location")
		codigo := res.StatusCode
		cookies := res.Cookies()
		res.Body.Close()

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
		if codigo >= 400 {
			return nil, &ErrDelSitio{Operacion: op, URL: destino, Codigo: codigo}
		}
		return &respuesta{cuerpo: cuerpo, codigo: codigo, location: loc, cookies: cookies}, nil
	}
	return nil, &ErrDelSitio{Operacion: op, URL: destino, Causa: ultimo}
}

// ------------------------------------------------------------------ lecturas

// maxPaginasDeEdicion corta el recorrido de una edición que no termina nunca.
// Son 6.000 avisos: la Segunda más cargada que se midió tiene 410.
const maxPaginasDeEdicion = 60

// TraerEdicion lee el sumario entero de una sección en una fecha. Devuelve
// ErrSinEdicion si ese día no hubo.
//
// La portada trae los primeros 100 avisos y cuenta, en su índice de rubros,
// cuántos tiene la edición. Si faltan, se piden las páginas siguientes como
// las pide el sitio al desplazarse: /seccion/actualizar/<sección>?pag=N. Ese
// pedido no lleva fecha: el sitio sabe de qué edición se habla por la cookie
// de sesión que dejó la portada, así que cada lectura usa las suyas.
//
// Si una página falla, falla la lectura: una edición a medias no se entrega
// como si fuera entera. Si el recorrido termina bien pero con menos avisos de
// los declarados, la edición sale marcada como Parcial.
//
// Con rubro se lee sólo la portada filtrada, sin seguir páginas; la edición
// queda Parcial si ese rubro declara más avisos de los que entraron.
func (c *Cliente) TraerEdicion(ctx context.Context, sec Seccion, fecha Fecha, rubro string) (*Edicion, error) {
	const op = "leer edición"
	ruta := "/seccion/" + string(sec) + "/" + fecha.Sitio()
	if rubro != "" {
		ruta += "?rubro=" + url.QueryEscape(rubro)
	}
	res, err := c.pedir(ctx, op, http.MethodGet, ruta, nil)
	if err != nil {
		return nil, err
	}
	if res.codigo >= 300 && res.codigo < 400 {
		return nil, ErrSinEdicion
	}
	p, err := parsearPortada(res.cuerpo, sec, rubro)
	if err != nil {
		return nil, &ErrDelSitio{Operacion: op, URL: c.base + ruta, Causa: err}
	}
	ed := &Edicion{Seccion: sec, Fecha: fecha, Declarados: p.declarados, Paginas: 1}
	if err := ed.sumar(p.avisos); err != nil {
		return nil, &ErrDelSitio{Operacion: op, URL: c.base + ruta, Causa: err}
	}

	// ¿Hace falta seguir? Si la portada trae el índice de rubros, alcanza con
	// comparar. Si no lo trae, la única forma de saberlo es preguntar.
	faltan := ed.Cantidad < ed.Declarados || (ed.Declarados == 0 && ed.Cantidad > 0)
	if rubro != "" || !faltan {
		ed.cerrar(false)
		return ed, nil
	}

	sesion := map[string]string{}
	anotarCookies(sesion, res.cookies)
	ultimoRubro := p.ultimoRubro
	quedaban := true
	for pag := 2; pag <= maxPaginasDeEdicion; pag++ {
		q := url.Values{}
		q.Set("pag", strconv.Itoa(pag))
		if ultimoRubro != "" {
			q.Set("ult_rubro", ultimoRubro)
		}
		rutaPag := "/seccion/actualizar/" + string(sec) + "?" + q.Encode()
		cab := http.Header{}
		cab.Set("X-Requested-With", "XMLHttpRequest")
		cab.Set("Referer", c.base+ruta)
		if ck := cabeceraCookie(sesion); ck != "" {
			cab.Set("Cookie", ck)
		}
		r, err := c.pedirCon(ctx, op, http.MethodGet, rutaPag, nil, cab)
		if err != nil {
			return nil, err
		}
		if r.codigo >= 300 {
			return nil, &ErrDelSitio{Operacion: op, URL: c.base + rutaPag, Codigo: r.codigo}
		}
		anotarCookies(sesion, r.cookies)

		// El rubro con el que arranca la página es el último que se vio, no
		// el que informa el sitio: son el mismo, pero el propio es el que ya
		// está en los avisos anteriores.
		rubroInicial := ultimoRubro
		if n := len(ed.Avisos); n > 0 {
			rubroInicial = ed.Avisos[n-1].Rubro
		}
		pg, err := parsearPaginaDeSeccion(r.cuerpo, sec, rubroInicial)
		if err != nil {
			return nil, &ErrDelSitio{Operacion: op, URL: c.base + rutaPag, Causa: err}
		}
		antes := ed.Cantidad
		if err := ed.sumar(pg.avisos); err != nil {
			return nil, &ErrDelSitio{Operacion: op, URL: c.base + rutaPag, Causa: err}
		}
		if len(pg.avisos) > 0 {
			ed.Paginas++
		}
		if pg.ultimoRubro != "" {
			ultimoRubro = pg.ultimoRubro
		}
		switch {
		case !pg.hayMas:
			// El sitio dice que se terminó.
			quedaban = false
		case ed.Declarados > 0 && ed.Cantidad >= ed.Declarados:
			// Ya está todo lo declarado: la página que sigue viene vacía.
			quedaban = false
		case ed.Cantidad == antes:
			// Una página sin nada nuevo: el sitio está dando vueltas. Se corta
			// y queda dicho que no se pudo confirmar el final.
			ed.cerrar(true)
			return ed, nil
		}
		if !quedaban {
			break
		}
	}
	ed.cerrar(quedaban)
	return ed, nil
}

// anotarCookies guarda las cookies que deja una respuesta, pisando las viejas.
func anotarCookies(sesion map[string]string, cookies []*http.Cookie) {
	for _, ck := range cookies {
		if ck.Name != "" {
			sesion[ck.Name] = ck.Value
		}
	}
}

// cabeceraCookie arma el valor de Cookie, en orden estable.
func cabeceraCookie(sesion map[string]string) string {
	nombres := make([]string, 0, len(sesion))
	for n := range sesion {
		nombres = append(nombres, n)
	}
	sort.Strings(nombres)
	partes := make([]string, 0, len(nombres))
	for _, n := range nombres {
		partes = append(partes, n+"="+sesion[n])
	}
	return strings.Join(partes, "; ")
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
