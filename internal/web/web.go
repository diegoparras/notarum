// Package web sirve el lector del Boletín: las mismas lecturas que la API,
// pero para leerlas. Sigue el sistema visual del Ecosistema Escriba.
//
// Todo se arma en el servidor con html/template y viaja embebido en el
// binario: no hay build de JavaScript ni recursos de terceros.
package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/asistente"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/contrato"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/lockatus"
	"github.com/diegoparras/notarum/internal/mcp"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/tareas"
)

//go:embed plantillas/*.html
var archivosPlantillas embed.FS

//go:embed estatico
var archivosEstaticos embed.FS

// Sitio atiende las páginas del lector.
type Sitio struct {
	srv       *servicio.Servicio
	version   string
	mux       *http.ServeMux
	plantilla map[string]*template.Template
	// conMCP dice si esta instancia expone el endpoint MCP, para no documentar
	// algo que está apagado.
	conMCP bool
	// registro habilita las cuentas. Sin él no hay login ni tokens, y notarum
	// se comporta como siempre.
	registro *cuentas.Registro
	politica cuentas.Politica
	// hub, si está, delega el login en Lockatus. Convive con el login propio:
	// se suma una forma de entrar, no se reemplaza la que había.
	hub *lockatus.Cliente
	// tareas corre los trabajos largos que se lanzan desde el panel.
	tareas *tareas.Ejecutor
	// programador los corre solos, todos los días.
	programador *tareas.Programador
	// marca dice desde cuándo guarda este almacén, o si arrancó vacío.
	marca almacen.Marca
	// asistente arma consultas a partir de un pedido en castellano.
	asistente *asistente.Cliente
}

// ConCuentas habilita el login y la gestión de tokens.
func (s *Sitio) ConCuentas(reg *cuentas.Registro, p cuentas.Politica) *Sitio {
	s.registro = reg
	s.politica = p
	if reg != nil {
		reg.CargarPolitica(p)
	}
	return s
}

// vigente es la política que rige ahora, que se puede cambiar desde el panel.
func (s *Sitio) vigente() cuentas.Politica {
	if s.registro != nil {
		return s.registro.Politica()
	}
	return s.politica
}

// ConTareas le da al lector con qué correr los trabajos largos del panel.
func (s *Sitio) ConTareas(e *tareas.Ejecutor) *Sitio {
	s.tareas = e
	return s
}

// ConMarca le dice al panel si lo guardado sobrevivió al despliegue anterior.
func (s *Sitio) ConMarca(m almacen.Marca) *Sitio {
	s.marca = m
	return s
}

// ConProgramador le da al panel con qué mostrar la actualización automática.
func (s *Sitio) ConProgramador(p *tareas.Programador) *Sitio {
	s.programador = p
	return s
}

// ConMCP le avisa al lector que el endpoint MCP está encendido.
func (s *Sitio) ConMCP(activo bool) *Sitio {
	s.conMCP = activo
	return s
}

// Nuevo arma el sitio. Devuelve error si una plantilla no compila: mejor no
// arrancar que servir páginas rotas.
func Nuevo(srv *servicio.Servicio, version string) (*Sitio, error) {
	s := &Sitio{
		srv:       srv,
		version:   version,
		mux:       http.NewServeMux(),
		plantilla: map[string]*template.Template{},
		// Sin cuentas no hay nada que pedir, pero la documentación igual tiene
		// que decir en qué modo está: sin este piso mostraría cuotas en cero.
		politica: cuentas.PoliticaPorDefecto(false),
	}
	for _, nombre := range []string{"edicion", "aviso", "buscar", "calendario", "norma", "docs", "entrar", "cuenta", "error", "provincial", "normaprov", "admin"} {
		t, err := template.New("base").Funcs(funciones).ParseFS(archivosPlantillas,
			"plantillas/base.html", "plantillas/"+nombre+".html")
		if err != nil {
			return nil, err
		}
		s.plantilla[nombre] = t
	}
	s.rutas()
	return s, nil
}

func (s *Sitio) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Sitio) rutas() {
	s.mux.Handle("GET /estatico/", http.FileServer(http.FS(archivosEstaticos)))
	s.mux.HandleFunc("GET /{$}", s.inicio)
	s.mux.HandleFunc("GET /ed/{seccion}/{fecha}", s.edicion)
	s.mux.HandleFunc("GET /av/{seccion}/{id}/{fecha}", s.aviso)
	s.mux.HandleFunc("GET /calendario/{seccion}/{anio}", s.calendario)
	s.mux.HandleFunc("GET /norma/{id}", s.norma)
	s.mux.HandleFunc("GET /buscar", s.buscar)
	s.mux.HandleFunc("GET /docs", s.docs)
	s.mux.HandleFunc("GET /entrar", s.verEntrar)
	s.mux.HandleFunc("POST /entrar", s.hacerEntrar)
	s.mux.HandleFunc("GET /salir", s.salir)
	s.mux.HandleFunc("GET /admin", s.verAdmin)
	s.mux.HandleFunc("POST /docs/generar", s.generar)
	s.mux.HandleFunc("POST /cuenta/clave-ia", s.guardarClaveIA)
	s.mux.HandleFunc("POST /cuenta/clave-ia/borrar", s.borrarClaveIA)
	s.mux.HandleFunc("POST /admin/politica", s.guardarPolitica)
	s.mux.HandleFunc("POST /admin/politica/olvidar", s.olvidarPolitica)
	s.mux.HandleFunc("POST /admin/tareas/{tipo}", s.lanzarTarea)
	s.mux.HandleFunc("POST /admin/tareas/{tipo}/cortar", s.cortarTarea)
	s.mux.HandleFunc("GET /provincial", s.verProvincial)
	s.mux.HandleFunc("GET /provincial/{id}", s.verNormaProvincial)
	s.mux.HandleFunc("GET /entrar/lockatus", s.irAlHub)
	s.mux.HandleFunc("GET /entrar/lockatus/volver", s.volverDelHub)
	s.mux.HandleFunc("GET /cuenta", s.verCuenta)
	s.mux.HandleFunc("POST /cuenta/tokens", s.crearToken)
	s.mux.HandleFunc("POST /cuenta/tokens/{id}/revocar", s.revocarToken)
	s.mux.HandleFunc("GET /ir", s.ir)
	s.mux.HandleFunc("GET /ir-calendario", s.irCalendario)
}

var funciones = template.FuncMap{
	"haceCuanto": haceCuanto,
	"enCuanto":   enCuanto,
	"fechaCorta": fechaCorta,
	"dict":       dict,
}

// fechaCorta escribe una fecha como 2026-09-01, o nada si no hay.
func fechaCorta(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// dict arma un mapa sobre la marcha, para poder pasarle más de un valor a una
// plantilla incluida. Es la forma habitual de hacerlo con html/template.
func dict(pares ...any) (map[string]any, error) {
	if len(pares)%2 != 0 {
		return nil, errors.New("dict necesita pares de clave y valor")
	}
	m := make(map[string]any, len(pares)/2)
	for i := 0; i < len(pares); i += 2 {
		clave, ok := pares[i].(string)
		if !ok {
			return nil, errors.New("las claves de dict son textos")
		}
		m[clave] = pares[i+1]
	}
	return m, nil
}

var meses = [...]string{"enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

var dias = [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// fechaLarga escribe "martes 1 de septiembre de 2026".
func fechaLarga(f boletin.Fecha) string {
	return dias[int(f.Weekday())] + " " + strconv.Itoa(f.Day()) + " de " +
		meses[int(f.Month())-1] + " de " + strconv.Itoa(f.Year())
}

// conMayuscula pone en mayúscula sólo la primera letra: en castellano los
// días y los meses van en minúscula, y capitalizar cada palabra queda mal.
func conMayuscula(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

// comun es lo que toda página necesita para dibujar la cabecera y el pie.
type comun struct {
	Version string
	// Cuentas dice si esta instancia tiene login, y Yo quién está mirando.
	Cuentas bool
	Yo      string
	// SoyAdmin enciende el enlace al panel. Que el enlace no aparezca es una
	// cortesía, no la protección: quien entre a mano igual rebota.
	SoyAdmin    bool
	Seccion     boletin.Seccion
	Secciones   []boletin.Seccion
	FechaActual string
	Angosto     bool
}

func (s *Sitio) base(sec boletin.Seccion, fecha string) comun {
	if fecha == "" {
		fecha = boletin.HoyEnArgentina().API()
	}
	return comun{
		Version:     s.version,
		Seccion:     sec,
		Secciones:   boletin.SeccionesValidas,
		FechaActual: fecha,
		Cuentas:     s.registro != nil,
	}
}

// baseCon arma lo común sabiendo quién mira, para que la cabecera muestre su
// nombre en vez de "entrar".
func (s *Sitio) baseCon(r *http.Request, sec boletin.Seccion, fecha string) comun {
	c := s.base(sec, fecha)
	if u := s.yo(r); u != nil {
		c.Yo = u.Nombre
		c.SoyAdmin = u.Rol == cuentas.RolAdmin
	}
	return c
}

func (s *Sitio) mostrar(w http.ResponseWriter, r *http.Request, nombre string, datos any, codigo int) {
	t, hay := s.plantilla[nombre]
	if !hay {
		s.fallo(w, r, http.StatusInternalServerError, "Error interno", "no existe la plantilla "+nombre)
		return
	}
	// Se dibuja en memoria y recién después se manda.
	//
	// Escribiendo derecho sobre la conexión, una plantilla que falla a mitad
	// de camino deja salir media página con un código de éxito ya mandado: el
	// navegador muestra algo roto, un proxy adelante puede cortar la
	// respuesta a medio terminar, y lo único que queda es una línea de log.
	// Una página entera cuesta unos kilobytes; equivocarse en silencio cuesta
	// bastante más.
	var pagina bytes.Buffer
	if err := t.ExecuteTemplate(&pagina, "base", datos); err != nil {
		slog.Error("no se pudo dibujar la página", "plantilla", nombre, "err", err)
		s.fallo(w, r, http.StatusInternalServerError, "No se pudo dibujar la página",
			"Quedó anotado en el registro del servidor con el detalle.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(pagina.Len()))
	w.WriteHeader(codigo)
	if _, err := pagina.WriteTo(w); err != nil {
		// Acá ya no se puede hacer nada: casi siempre es alguien que cerró la
		// pestaña antes de que terminara de cargar.
		slog.Debug("no se pudo mandar la página", "plantilla", nombre, "err", err)
	}
}

func (s *Sitio) fallo(w http.ResponseWriter, r *http.Request, codigo int, titulo, detalle string) {
	datos := struct {
		comun
		Titulo string
		Error  string
	}{s.base("", ""), titulo, detalle}
	datos.Angosto = true
	if t, hay := s.plantilla["error"]; hay {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(codigo)
		_ = t.ExecuteTemplate(w, "base", datos)
		return
	}
	http.Error(w, titulo+": "+detalle, codigo)
}

// mensajeDeError traduce un error de lectura a algo que se pueda leer en una
// página, sin tecnicismos ni rastros internos.
func mensajeDeError(err error) string {
	var es *boletin.ErrDelSitio
	if errors.As(err, &es) {
		return "El sitio del Boletín Oficial no respondió como se esperaba. Probá de nuevo en un momento."
	}
	return "No se pudo completar la consulta."
}

// ------------------------------------------------------------------- inicio

// inicio manda a la edición más reciente de la primera sección. Si hoy no
// hubo, busca hacia atrás en el calendario en vez de mostrar una página vacía.
func (s *Sitio) inicio(w http.ResponseWriter, r *http.Request) {
	fecha := s.ultimaConEdicion(r, boletin.Primera)
	http.Redirect(w, r, "/ed/primera/"+fecha, http.StatusFound)
}

func (s *Sitio) ultimaConEdicion(r *http.Request, sec boletin.Seccion) string {
	hoy := boletin.HoyEnArgentina()
	cal, err := s.srv.Calendario(r.Context(), sec, hoy.Year())
	if err != nil || len(cal.Fechas) == 0 {
		return hoy.API()
	}
	ultima := ""
	for _, f := range cal.Fechas {
		if f.API() <= hoy.API() {
			ultima = f.API()
		}
	}
	if ultima == "" {
		return hoy.API()
	}
	return ultima
}

// ir y irCalendario existen para que los formularios naveguen con URLs
// limpias en vez de arrastrar parámetros.
func (s *Sitio) ir(w http.ResponseWriter, r *http.Request) {
	sec := r.URL.Query().Get("seccion")
	if _, err := boletin.ParseSeccion(sec); err != nil {
		sec = "primera"
	}
	fecha := r.URL.Query().Get("fecha")
	if _, err := boletin.ParseFecha(fecha); err != nil {
		fecha = boletin.HoyEnArgentina().API()
	}
	http.Redirect(w, r, "/ed/"+sec+"/"+fecha, http.StatusFound)
}

func (s *Sitio) irCalendario(w http.ResponseWriter, r *http.Request) {
	sec := r.URL.Query().Get("seccion")
	if _, err := boletin.ParseSeccion(sec); err != nil {
		sec = "primera"
	}
	anio, err := strconv.Atoi(r.URL.Query().Get("anio"))
	if err != nil || anio < 1990 {
		anio = boletin.HoyEnArgentina().Year()
	}
	http.Redirect(w, r, "/calendario/"+sec+"/"+strconv.Itoa(anio), http.StatusFound)
}

// ------------------------------------------------------------------ edición

type rubroContado struct {
	Nombre   string
	Cantidad int
}

type datosEdicion struct {
	comun
	FechaLarga string
	Anio       int
	Edicion    *boletin.Edicion
	Rubros     []rubroContado
	Rubro      string
	Total      int
	Anterior   string
	Siguiente  string
	SinEdicion bool
	Error      string
}

func (s *Sitio) edicion(w http.ResponseWriter, r *http.Request) {
	sec, err := boletin.ParseSeccion(r.PathValue("seccion"))
	if err != nil {
		s.fallo(w, r, http.StatusNotFound, "Esa sección no existe", "Las secciones son primera, segunda y tercera.")
		return
	}
	fecha, err := boletin.ParseFecha(r.PathValue("fecha"))
	if err != nil {
		s.fallo(w, r, http.StatusNotFound, "Esa fecha no se entiende", "El formato es AAAA-MM-DD.")
		return
	}
	rubro := strings.TrimSpace(r.URL.Query().Get("rubro"))

	d := datosEdicion{
		comun:      s.baseCon(r, sec, fecha.API()),
		FechaLarga: conMayuscula(fechaLarga(fecha)),
		Anio:       fecha.Year(),
		Rubro:      rubro,
	}
	d.Anterior, d.Siguiente = s.vecinos(r, sec, fecha)

	// La edición completa da los rubros con sus cuentas; la filtrada, la lista.
	completa, err := s.srv.Edicion(r.Context(), sec, fecha, "")
	switch {
	case errors.Is(err, servicio.ErrSinEdicion):
		d.SinEdicion = true
		s.mostrar(w, r, "edicion", d, http.StatusOK)
		return
	case err != nil:
		d.Error = mensajeDeError(err)
		s.mostrar(w, r, "edicion", d, http.StatusBadGateway)
		return
	}

	d.Total = completa.Cantidad
	for nombre, n := range completa.PorRubro {
		d.Rubros = append(d.Rubros, rubroContado{nombre, n})
	}
	sort.Slice(d.Rubros, func(i, j int) bool {
		if d.Rubros[i].Cantidad != d.Rubros[j].Cantidad {
			return d.Rubros[i].Cantidad > d.Rubros[j].Cantidad
		}
		return d.Rubros[i].Nombre < d.Rubros[j].Nombre
	})

	d.Edicion = completa
	if rubro != "" {
		filtrada, err := s.srv.Edicion(r.Context(), sec, fecha, rubro)
		if err != nil {
			d.Error = mensajeDeError(err)
			s.mostrar(w, r, "edicion", d, http.StatusBadGateway)
			return
		}
		d.Edicion = filtrada
	}
	s.mostrar(w, r, "edicion", d, http.StatusOK)
}

// vecinos busca el día con edición anterior y el siguiente, para poder
// moverse sin caer en feriados.
func (s *Sitio) vecinos(r *http.Request, sec boletin.Seccion, fecha boletin.Fecha) (anterior, siguiente string) {
	cal, err := s.srv.Calendario(r.Context(), sec, fecha.Year())
	if err != nil {
		return "", ""
	}
	hoy := fecha.API()
	for _, f := range cal.Fechas {
		switch {
		case f.API() < hoy:
			anterior = f.API()
		case f.API() > hoy && siguiente == "":
			siguiente = f.API()
		}
	}
	return anterior, siguiente
}

// --------------------------------------------------------------------- aviso

type datosAviso struct {
	comun
	FechaLarga string
	Detalle    *boletin.Detalle
	Cuerpo     template.HTML
	// Norma es lo que InfoLEG sabe de esta misma norma, cuando se la pudo
	// cruzar. El Boletín publica el texto como salió ese día; InfoLEG lo
	// mantiene actualizado, y ahí está el valor de mostrar las dos cosas.
	Norma *infoleg.Norma
	// InfoLEGAtrasado se prende cuando el catálogo todavía no llegó a la fecha
	// del aviso: así se distingue "no existe" de "todavía no está".
	InfoLEGAtrasado bool
	UltimaFechaBO   string
}

func (s *Sitio) aviso(w http.ResponseWriter, r *http.Request) {
	sec, err := boletin.ParseSeccion(r.PathValue("seccion"))
	if err != nil {
		s.fallo(w, r, http.StatusNotFound, "Esa sección no existe", "Las secciones son primera, segunda y tercera.")
		return
	}
	fecha, err := boletin.ParseFecha(r.PathValue("fecha"))
	if err != nil {
		s.fallo(w, r, http.StatusNotFound, "Esa fecha no se entiende", "El formato es AAAA-MM-DD.")
		return
	}
	d, err := s.srv.Aviso(r.Context(), sec, r.PathValue("id"), fecha)
	if err != nil {
		s.fallo(w, r, http.StatusBadGateway, "No se pudo leer el aviso", mensajeDeError(err))
		return
	}

	datos := datosAviso{
		comun:      s.baseCon(r, sec, fecha.API()),
		FechaLarga: fechaLarga(fecha),
		Detalle:    d,
		// El HTML ya viene saneado del parser: sin scripts, sin estilos y sin
		// atributos. Por eso se puede marcar como seguro acá.
		Cuerpo: template.HTML(d.HTML),
	}
	datos.Angosto = true

	if s.srv.InfoLEGDisponible() {
		datos.Norma = s.srv.NormaDelAviso(d.Aviso)
		if datos.Norma == nil {
			// Si el catálogo no llega a esta fecha, la norma puede existir y
			// no estar todavía. Decirlo es más honesto que callar.
			e := s.srv.EstadoInfoLEG()
			datos.UltimaFechaBO = e.UltimaFechaBO
			if _, esNorma := infoleg.ParsearNorma(d.Norma); esNorma &&
				e.Sincronizado && e.UltimaFechaBO != "" && d.Fecha.API() > e.UltimaFechaBO {
				datos.InfoLEGAtrasado = true
			}
		}
	}
	s.mostrar(w, r, "aviso", datos, http.StatusOK)
}

// ------------------------------------------------------------------- buscar

type datosBuscar struct {
	comun
	Texto           string
	Desde           string
	Hasta           string
	Resultado       *servicio.Busqueda
	PaginaPrevia    string
	PaginaSiguiente string
	Error           string
}

func (s *Sitio) buscar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sec, err := boletin.ParseSeccion(q.Get("seccion"))
	if err != nil {
		sec = boletin.Primera
	}
	hoy := boletin.HoyEnArgentina()
	desde := q.Get("desde")
	hasta := q.Get("hasta")
	if desde == "" {
		desde = hoy.AddDate(0, -1, 0).Format("2006-01-02")
	}
	if hasta == "" {
		hasta = hoy.API()
	}

	d := datosBuscar{
		comun: s.baseCon(r, sec, hoy.API()),
		Texto: q.Get("texto"),
		Desde: desde,
		Hasta: hasta,
	}

	// Sin texto se muestra el formulario y nada más: no tiene sentido traer
	// un mes entero porque alguien entró a la página.
	if strings.TrimSpace(d.Texto) == "" {
		s.mostrar(w, r, "buscar", d, http.StatusOK)
		return
	}

	fDesde, err1 := boletin.ParseFecha(desde)
	fHasta, err2 := boletin.ParseFecha(hasta)
	if err1 != nil || err2 != nil {
		d.Error = "Las fechas tienen que estar en formato AAAA-MM-DD."
		s.mostrar(w, r, "buscar", d, http.StatusBadRequest)
		return
	}
	if fHasta.Before(fDesde.Time) {
		d.Error = "El rango está al revés: la fecha final es anterior a la inicial."
		s.mostrar(w, r, "buscar", d, http.StatusBadRequest)
		return
	}

	pagina, _ := strconv.Atoi(q.Get("pagina"))
	if pagina < 1 {
		pagina = 1
	}

	var res *servicio.Busqueda
	usarIndice := false
	if s.srv.TieneIndice() {
		_, _, usarIndice = s.srv.PuedeBuscarLocal(r.Context(), sec, fDesde, fHasta)
	}
	if usarIndice {
		res, err = s.srv.BuscarEnIndice(r.Context(), almacen.ConsultaLocal{
			Texto: d.Texto, Seccion: sec, Desde: fDesde, Hasta: fHasta, Limite: 40,
		}, pagina)
	} else {
		res, err = s.srv.BuscarEnSitio(r.Context(), boletin.ConsultaBusqueda{
			Texto: d.Texto, Seccion: sec, Desde: fDesde, Hasta: fHasta, Pagina: pagina,
		})
	}
	if err != nil {
		d.Error = mensajeDeError(err)
		s.mostrar(w, r, "buscar", d, http.StatusBadGateway)
		return
	}
	d.Resultado = res

	base := url.Values{}
	base.Set("seccion", string(sec))
	base.Set("texto", d.Texto)
	base.Set("desde", desde)
	base.Set("hasta", hasta)
	if pagina > 1 {
		base.Set("pagina", strconv.Itoa(pagina-1))
		d.PaginaPrevia = "/buscar?" + base.Encode()
	}
	if res.HayMas {
		base.Set("pagina", strconv.Itoa(pagina+1))
		d.PaginaSiguiente = "/buscar?" + base.Encode()
	}
	s.mostrar(w, r, "buscar", d, http.StatusOK)
}

// --------------------------------------------------------------- calendario

type diaCalendario struct {
	Numero     int
	Fecha      string
	Hay        bool
	Suplemento bool
}

type mesCalendario struct {
	Nombre string
	Dias   []diaCalendario
}

type datosCalendario struct {
	comun
	Anio             int
	AnioPrevio       int
	AnioSiguiente    int
	Meses            []mesCalendario
	TotalDias        int
	TotalSuplementos int
	Error            string
}

func (s *Sitio) calendario(w http.ResponseWriter, r *http.Request) {
	sec, err := boletin.ParseSeccion(r.PathValue("seccion"))
	if err != nil {
		s.fallo(w, r, http.StatusNotFound, "Esa sección no existe", "Las secciones son primera, segunda y tercera.")
		return
	}
	anio, err := strconv.Atoi(r.PathValue("anio"))
	esteAnio := boletin.HoyEnArgentina().Year()
	if err != nil || anio < 1990 || anio > esteAnio+1 {
		s.fallo(w, r, http.StatusNotFound, "Ese año no se puede consultar",
			"Se puede pedir desde 1990 hasta "+strconv.Itoa(esteAnio+1)+".")
		return
	}

	d := datosCalendario{
		comun: s.baseCon(r, sec, boletin.HoyEnArgentina().API()),
		Anio:  anio,
	}
	if anio > 1990 {
		d.AnioPrevio = anio - 1
	}
	if anio < esteAnio {
		d.AnioSiguiente = anio + 1
	}

	cal, err := s.srv.Calendario(r.Context(), sec, anio)
	if err != nil {
		d.Error = mensajeDeError(err)
		s.mostrar(w, r, "calendario", d, http.StatusBadGateway)
		return
	}

	conEdicion := map[string]bool{}
	conSuplemento := map[string]bool{}
	for _, f := range cal.Fechas {
		conEdicion[f.API()] = true
	}
	for _, f := range cal.ConSuplemento {
		conSuplemento[f.API()] = true
	}
	d.TotalDias = len(cal.Fechas)
	d.TotalSuplementos = len(cal.ConSuplemento)

	for m := 1; m <= 12; m++ {
		mes := mesCalendario{Nombre: meses[m-1]}
		primero := time.Date(anio, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
		ultimo := primero.AddDate(0, 1, -1).Day()
		// Rellenar hasta el día de semana del primero, para que las columnas
		// caigan bajo su día.
		for i := 0; i < int(primero.Weekday()); i++ {
			mes.Dias = append(mes.Dias, diaCalendario{})
		}
		for dia := 1; dia <= ultimo; dia++ {
			f := time.Date(anio, time.Month(m), dia, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
			mes.Dias = append(mes.Dias, diaCalendario{
				Numero: dia, Fecha: f,
				Hay: conEdicion[f], Suplemento: conSuplemento[f],
			})
		}
		d.Meses = append(d.Meses, mes)
	}
	s.mostrar(w, r, "calendario", d, http.StatusOK)
}

// ---------------------------------------------------------------- InfoLEG

type datosNorma struct {
	comun
	Norma  *infoleg.Norma
	Cuerpo template.HTML
	Volver string
	Error  string
}

// norma muestra el texto que InfoLEG mantiene actualizado, que es lo que el
// Boletín no da: el Boletín publica la norma como salió ese día.
func (s *Sitio) norma(w http.ResponseWriter, r *http.Request) {
	if !s.srv.InfoLEGDisponible() {
		s.fallo(w, r, http.StatusNotFound, "InfoLEG no está disponible",
			"Esta instancia no tiene el enriquecimiento con InfoLEG activado.")
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		s.fallo(w, r, http.StatusNotFound, "Ese identificador no se entiende",
			"El de InfoLEG es un número.")
		return
	}

	d := datosNorma{comun: s.base("", "")}
	d.Angosto = true
	// El volver se toma del propio sitio y nunca de lo que venga afuera, para
	// que nadie use esta página como trampolín a otro lado.
	if v := r.URL.Query().Get("volver"); strings.HasPrefix(v, "/av/") && !strings.Contains(v, "//") {
		d.Volver = v
	}

	texto, err := s.srv.TextoDeNorma(r.Context(), id)
	if errors.Is(err, infoleg.ErrSinTexto) {
		s.fallo(w, r, http.StatusNotFound, "InfoLEG no publicó el texto de esta norma",
			"Está registrada en el catálogo, pero su texto no está disponible. "+
				"Le pasa a más de la mitad de las normas.")
		return
	}
	if err != nil {
		d.Error = mensajeDeError(err)
		s.mostrar(w, r, "norma", d, http.StatusBadGateway)
		return
	}

	d.Norma = &infoleg.Norma{ID: id}
	if n := s.srv.NormaGuardada(id); n != nil {
		d.Norma = n
	}
	// El HTML ya viene saneado del cliente de InfoLEG.
	d.Cuerpo = template.HTML(texto.HTML)
	s.mostrar(w, r, "norma", d, http.StatusOK)
}

// ---------------------------------------------------------- documentación

// argumentoDoc es un argumento de una herramienta MCP, listo para dibujar.
type argumentoDoc struct {
	Nombre      string
	Tipo        string
	Obligatorio bool
	PorDefecto  string
	Descripcion string
	Opciones    []string
}

type herramientaDoc struct {
	Nombre      string
	Titulo      string
	Descripcion string
	Argumentos  []argumentoDoc
}

type datosDocs struct {
	comun
	Doc          *contrato.Documento
	Herramientas []herramientaDoc
	ConMCP       bool
	TokenMCP     bool
	Base         string
	// Politica se muestra tal como está configurada: qué deja hacer esta
	// instancia no es algo que la documentación pueda dar por sabido, porque
	// lo decide quien la levanta.
	Politica cuentas.Politica

	// El asistente que arma la consulta.
	Asistente      datosAsistente
	HayAsistente   bool
	TieneClaveIA   bool
	ErrorAsistente string
}

// docs dibuja la documentación de la API y del MCP.
//
// Sale de las mismas fuentes que usan la API y el servidor MCP —el contrato
// OpenAPI embebido y la lista de herramientas—, así que no puede quedar
// desactualizada: si se agrega una ruta o una herramienta, aparece sola.
func (s *Sitio) docs(w http.ResponseWriter, r *http.Request) {
	s.dibujarDocs(w, r, "", "", http.StatusOK)
}

// dibujarDocs arma la página. El asistente vive acá porque es donde alguien
// está leyendo qué rutas hay: es el momento en que necesita la consulta.
func (s *Sitio) dibujarDocs(w http.ResponseWriter, r *http.Request, pedido, errMsg string, codigo int) {
	s.dibujarDocsCon(w, r, datosAsistente{Pedido: pedido}, errMsg, codigo)
}

func (s *Sitio) dibujarDocsCon(w http.ResponseWriter, r *http.Request, a datosAsistente, errMsg string, codigo int) {
	doc, err := contrato.Leer()
	if err != nil {
		s.fallo(w, r, http.StatusInternalServerError, "No se pudo leer el contrato",
			"El archivo openapi.json que viene con esta versión no se pudo interpretar.")
		return
	}
	d := datosDocs{
		comun:  s.baseCon(r, "", ""),
		Doc:    doc,
		ConMCP: s.conMCP,
		Base:   baseVisible(r),

		Politica:       s.vigente(),
		Asistente:      a,
		HayAsistente:   s.PuedeAsistir(),
		ErrorAsistente: errMsg,
	}
	if u := s.yo(r); u != nil && s.registro != nil {
		d.TieneClaveIA = s.registro.TieneClaveIA(u.Nombre)
	}
	for _, h := range mcp.Herramientas() {
		d.Herramientas = append(d.Herramientas, aHerramientaDoc(h))
	}
	s.mostrar(w, r, "docs", d, codigo)
}

// baseVisible arma la dirección tal como la ve quien está mirando, para que
// los ejemplos se puedan copiar y funcionen.
func baseVisible(r *http.Request) string {
	esquema := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		esquema = "https"
	}
	host := r.Host
	if reenviado := r.Header.Get("X-Forwarded-Host"); reenviado != "" {
		host = reenviado
	}
	if host == "" {
		return ""
	}
	return esquema + "://" + host
}

// aHerramientaDoc traduce el esquema JSON de una herramienta a algo que se
// pueda poner en una tabla.
func aHerramientaDoc(h mcp.Herramienta) herramientaDoc {
	out := herramientaDoc{Nombre: h.Nombre, Titulo: h.Titulo, Descripcion: h.Descripcion}

	esquema, ok := h.Esquema.(map[string]any)
	if !ok {
		return out
	}
	props, ok := esquema["properties"].(map[string]any)
	if !ok {
		return out
	}
	obligatorios := map[string]bool{}
	if req, ok := esquema["required"].([]string); ok {
		for _, r := range req {
			obligatorios[r] = true
		}
	}

	nombres := make([]string, 0, len(props))
	for n := range props {
		nombres = append(nombres, n)
	}
	// Primero los obligatorios, después el resto, cada grupo alfabético: es el
	// orden en que conviene leerlos.
	sort.Slice(nombres, func(i, j int) bool {
		if obligatorios[nombres[i]] != obligatorios[nombres[j]] {
			return obligatorios[nombres[i]]
		}
		return nombres[i] < nombres[j]
	})

	for _, n := range nombres {
		prop, _ := props[n].(map[string]any)
		a := argumentoDoc{Nombre: n, Obligatorio: obligatorios[n]}
		if t, ok := prop["type"].(string); ok {
			a.Tipo = t
		}
		if d, ok := prop["description"].(string); ok {
			a.Descripcion = d
		}
		if v, hay := prop["default"]; hay {
			a.PorDefecto = fmt.Sprint(v)
		}
		if op, ok := prop["enum"].([]string); ok {
			a.Opciones = op
		}
		out.Argumentos = append(out.Argumentos, a)
	}
	return out
}

// IPDe dice de dónde vino un pedido, mirando primero lo que ponen los proxys.
//
// EasyPanel mete Traefik adelante, así que RemoteAddr es el del proxy y no el
// de quien pide. Sin esto, todos los pedidos vendrían de la misma dirección y
// el límite por IP sería un límite para todos juntos.
//
// Vive acá y no en api porque los dos la necesitan y api ya importa web: dos
// copias se desincronizan el día que aparece otra cabecera de proxy.
func IPDe(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			v = v[:i]
		}
		if ip := strings.TrimSpace(v); ip != "" {
			return ip
		}
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
