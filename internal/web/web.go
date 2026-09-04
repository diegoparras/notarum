// Package web sirve el lector del Boletín: las mismas lecturas que la API,
// pero para leerlas. Sigue el sistema visual del Ecosistema Escriba.
//
// Todo se arma en el servidor con html/template y viaja embebido en el
// binario: no hay build de JavaScript ni recursos de terceros.
package web

import (
	"embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
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
}

// Nuevo arma el sitio. Devuelve error si una plantilla no compila: mejor no
// arrancar que servir páginas rotas.
func Nuevo(srv *servicio.Servicio, version string) (*Sitio, error) {
	s := &Sitio{
		srv:       srv,
		version:   version,
		mux:       http.NewServeMux(),
		plantilla: map[string]*template.Template{},
	}
	for _, nombre := range []string{"edicion", "aviso", "buscar", "calendario", "error"} {
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
	s.mux.HandleFunc("GET /buscar", s.buscar)
	s.mux.HandleFunc("GET /ir", s.ir)
	s.mux.HandleFunc("GET /ir-calendario", s.irCalendario)
}

var funciones = template.FuncMap{}

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
	Version     string
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
	}
}

func (s *Sitio) mostrar(w http.ResponseWriter, r *http.Request, nombre string, datos any, codigo int) {
	t, hay := s.plantilla[nombre]
	if !hay {
		s.fallo(w, r, http.StatusInternalServerError, "Error interno", "no existe la plantilla "+nombre)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(codigo)
	if err := t.ExecuteTemplate(w, "base", datos); err != nil {
		// La respuesta ya empezó a salir: sólo queda dejarlo anotado.
		slog.Error("no se pudo dibujar la página", "plantilla", nombre, "err", err)
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
		comun:      s.base(sec, fecha.API()),
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
		comun:      s.base(sec, fecha.API()),
		FechaLarga: fechaLarga(fecha),
		Detalle:    d,
		// El HTML ya viene saneado del parser: sin scripts, sin estilos y sin
		// atributos. Por eso se puede marcar como seguro acá.
		Cuerpo: template.HTML(d.HTML),
	}
	datos.Angosto = true
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
		comun: s.base(sec, hoy.API()),
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
		comun: s.base(sec, boletin.HoyEnArgentina().API()),
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
