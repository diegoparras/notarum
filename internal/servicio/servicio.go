// Package servicio une la lectura del sitio con la caché en disco. Es la capa
// que decide qué se lee de nuevo y qué ya está guardado.
package servicio

import (
	"sync"

	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/saij"
)

// TTLHoy es cuánto vale la edición del día en curso antes de volver a mirar.
const TTLHoy = 5 * time.Minute

// TTLRubros y TTLCalendarioEnCurso valen para cosas que sí cambian.
const (
	TTLRubros            = 24 * time.Hour
	TTLCalendarioEnCurso = 6 * time.Hour
	TTLBusqueda          = 5 * time.Minute
	MaxDiasRango         = 366
)

// ErrSinEdicion se reexporta para que la capa HTTP no dependa de boletin.
var ErrSinEdicion = boletin.ErrSinEdicion

// Servicio sirve datos del Boletín, de la caché cuando puede y del sitio
// cuando hace falta.
type Servicio struct {
	cli   *boletin.Cliente
	cache almacen.Almacen
	// indice es el mismo almacén, cuando el motor sabe buscar por su cuenta.
	// Con disco es nil; con SQLite o Postgres, no.
	indice almacen.Indexador
	// infoleg enriquece los avisos con la norma actualizada. Es opcional: sin
	// él notarum sirve el Boletín igual, sólo que sin ese agregado.
	infoleg *infoleg.Cliente
	// saij trae la normativa de las provincias, que el Boletín nacional no
	// publica. También es opcional.
	saij *saij.Cliente
	// El índice provincial se arma la primera vez que alguien lo consulta:
	// son 77 MB que no tiene por qué pagar quien no lo use.
	saijMu     sync.RWMutex
	saijIndice *saij.Indice
	// saijCargado es de cuándo era el catálogo que está en memoria, y
	// saijMirado cuándo se preguntó por última vez si había uno más nuevo.
	// Sin esto, `notarum provincial` corrido aparte —que es como se hace en
	// un contenedor— no se notaba hasta reiniciar el servicio.
	saijCargado time.Time
	saijMirado  time.Time
}

// ConSAIJ habilita la consulta de normativa provincial.
func (s *Servicio) ConSAIJ(c *saij.Cliente) *Servicio {
	s.saij = c
	return s
}

// ConInfoLEG habilita el enriquecimiento de avisos con la normativa de
// InfoLEG. Sin esto, notarum funciona igual: es un accesorio, no el corazón.
func (s *Servicio) ConInfoLEG(c *infoleg.Cliente) *Servicio {
	s.infoleg = c
	return s
}

func Nuevo(cli *boletin.Cliente, c almacen.Almacen) *Servicio {
	s := &Servicio{cli: cli, cache: c}
	if ix, ok := c.(almacen.Indexador); ok {
		s.indice = ix
	}
	return s
}

func (s *Servicio) Cliente() *boletin.Cliente { return s.cli }
func (s *Servicio) Almacen() almacen.Almacen  { return s.cache }

// TieneIndice dice si este servicio puede buscar sin pedirle nada al Boletín.
func (s *Servicio) TieneIndice() bool { return s.indice != nil }

// ttlPara decide cuánto dura lo que se acaba de leer: una edición pasada no
// cambia nunca; la de hoy (o una futura) sí.
func ttlPara(fecha boletin.Fecha) time.Duration {
	if fecha.API() < boletin.HoyEnArgentina().API() {
		return almacen.SinVencimiento
	}
	return TTLHoy
}

const marcaSinEdicion = `{"sin_edicion":true}`

func claveEdicion(sec boletin.Seccion, f boletin.Fecha) string {
	return "ediciones/" + string(sec) + "/" + f.API()
}

// Edicion devuelve el sumario de una sección en una fecha. Si se pasa rubro,
// filtra en memoria: la edición completa ya está en caché, no hace falta
// volver a pedirla al sitio por cada rubro.
func (s *Servicio) Edicion(ctx context.Context, sec boletin.Seccion, fecha boletin.Fecha, rubro string) (*boletin.Edicion, error) {
	clave := claveEdicion(sec, fecha)
	if crudo, ok := s.cache.Leer(clave); ok {
		if string(crudo) == marcaSinEdicion {
			return nil, ErrSinEdicion
		}
		var ed boletin.Edicion
		if err := json.Unmarshal(crudo, &ed); err == nil {
			return filtrarPorRubro(&ed, rubro), nil
		}
	}

	ed, err := s.cli.TraerEdicion(ctx, sec, fecha, "")
	if err != nil {
		if errors.Is(err, ErrSinEdicion) {
			_ = s.cache.Guardar(clave, []byte(marcaSinEdicion), ttlPara(fecha))
		}
		return nil, err
	}
	if crudo, err := json.Marshal(ed); err == nil {
		_ = s.cache.Guardar(clave, crudo, ttlPara(fecha))
	}
	s.indexar(ed)
	return filtrarPorRubro(ed, rubro), nil
}

// filtrarPorRubro se queda con los avisos cuyo rubro coincide, por nombre o
// por prefijo (los rubros de la tercera son jerárquicos: "SUMINISTROS - ...").
func filtrarPorRubro(ed *boletin.Edicion, rubro string) *boletin.Edicion {
	rubro = strings.TrimSpace(rubro)
	if rubro == "" {
		return ed
	}
	buscado := strings.ToUpper(rubro)
	out := &boletin.Edicion{
		Seccion:  ed.Seccion,
		Fecha:    ed.Fecha,
		PorRubro: map[string]int{},
		Avisos:   []boletin.Aviso{},
	}
	for _, a := range ed.Avisos {
		r := strings.ToUpper(a.Rubro)
		if r == buscado || strings.HasPrefix(r, buscado) {
			out.Avisos = append(out.Avisos, a)
			out.PorRubro[a.Rubro]++
			if a.Suplemento {
				out.ConSuplemento = true
			}
		}
	}
	out.Cantidad = len(out.Avisos)
	return out
}

// Aviso devuelve un aviso con su texto completo.
func (s *Servicio) Aviso(ctx context.Context, sec boletin.Seccion, id string, fecha boletin.Fecha) (*boletin.Detalle, error) {
	clave := "avisos/" + string(sec) + "/" + fecha.API() + "/" + sanear(id)
	if crudo, ok := s.cache.Leer(clave); ok {
		var d boletin.Detalle
		if err := json.Unmarshal(crudo, &d); err == nil {
			return &d, nil
		}
	}
	d, err := s.cli.TraerAviso(ctx, sec, id, fecha)
	if err != nil {
		return nil, err
	}
	if crudo, err := json.Marshal(d); err == nil {
		_ = s.cache.Guardar(clave, crudo, ttlPara(fecha))
	}
	s.indexarDetalle(d)
	return d, nil
}

// Calendario devuelve los días con edición de un año.
func (s *Servicio) Calendario(ctx context.Context, sec boletin.Seccion, anio int) (*boletin.Calendario, error) {
	clave := fmt.Sprintf("calendarios/%s/%d", sec, anio)
	if crudo, ok := s.cache.Leer(clave); ok {
		var cal boletin.Calendario
		if err := json.Unmarshal(crudo, &cal); err == nil {
			return &cal, nil
		}
	}
	cal, err := s.cli.TraerCalendario(ctx, sec, anio)
	if err != nil {
		return nil, err
	}
	ttl := almacen.SinVencimiento
	if anio >= boletin.HoyEnArgentina().Year() {
		ttl = TTLCalendarioEnCurso
	}
	if crudo, err := json.Marshal(cal); err == nil {
		_ = s.cache.Guardar(clave, crudo, ttl)
	}
	return cal, nil
}

// Rubros devuelve el catálogo de rubros de una sección.
func (s *Servicio) Rubros(ctx context.Context, sec boletin.Seccion) ([]boletin.Rubro, error) {
	clave := "rubros/" + string(sec)
	if crudo, ok := s.cache.Leer(clave); ok {
		var rs []boletin.Rubro
		if err := json.Unmarshal(crudo, &rs); err == nil {
			return rs, nil
		}
	}
	rs, err := s.cli.TraerRubros(ctx, sec)
	if err != nil {
		return nil, err
	}
	if crudo, err := json.Marshal(rs); err == nil {
		_ = s.cache.Guardar(clave, crudo, TTLRubros)
	}
	return rs, nil
}

// Rango es el resultado de pedir varios días de una sección.
type Rango struct {
	Seccion   boletin.Seccion   `json:"seccion"`
	Desde     boletin.Fecha     `json:"desde"`
	Hasta     boletin.Fecha     `json:"hasta"`
	Ediciones []boletin.Resumen `json:"ediciones"`
	// Faltantes son los días con edición que todavía no están en la caché.
	// Se llenan con `notarum rellenar`; la API no baja un año entero adentro
	// de un pedido HTTP.
	Faltantes []boletin.Fecha `json:"faltantes"`
}

// Resumenes arma los resúmenes de un rango con lo que ya está en caché.
func (s *Servicio) Resumenes(ctx context.Context, sec boletin.Seccion, desde, hasta boletin.Fecha) (*Rango, error) {
	if hasta.Before(desde.Time) {
		return nil, fmt.Errorf("el rango está al revés: desde %s hasta %s", desde.API(), hasta.API())
	}
	if dias := int(hasta.Sub(desde.Time).Hours()/24) + 1; dias > MaxDiasRango {
		return nil, fmt.Errorf("el rango pedido es de %d días y el tope son %d", dias, MaxDiasRango)
	}

	fechas, err := s.fechasConEdicion(ctx, sec, desde, hasta)
	if err != nil {
		return nil, err
	}
	r := &Rango{
		Seccion:   sec,
		Desde:     desde,
		Hasta:     hasta,
		Ediciones: []boletin.Resumen{},
		Faltantes: []boletin.Fecha{},
	}
	for _, f := range fechas {
		crudo, ok := s.cache.Leer(claveEdicion(sec, f))
		if !ok {
			r.Faltantes = append(r.Faltantes, f)
			continue
		}
		if string(crudo) == marcaSinEdicion {
			continue
		}
		var ed boletin.Edicion
		if err := json.Unmarshal(crudo, &ed); err != nil {
			r.Faltantes = append(r.Faltantes, f)
			continue
		}
		r.Ediciones = append(r.Ediciones, boletin.Resumen{
			Seccion:       ed.Seccion,
			Fecha:         ed.Fecha,
			Cantidad:      ed.Cantidad,
			PorRubro:      ed.PorRubro,
			ConSuplemento: ed.ConSuplemento,
		})
	}
	return r, nil
}

// fechasConEdicion cruza el calendario de cada año tocado por el rango.
func (s *Servicio) fechasConEdicion(ctx context.Context, sec boletin.Seccion, desde, hasta boletin.Fecha) ([]boletin.Fecha, error) {
	var fechas []boletin.Fecha
	for anio := desde.Year(); anio <= hasta.Year(); anio++ {
		cal, err := s.Calendario(ctx, sec, anio)
		if err != nil {
			return nil, err
		}
		for _, f := range cal.Fechas {
			if f.API() >= desde.API() && f.API() <= hasta.API() {
				fechas = append(fechas, f)
			}
		}
	}
	return fechas, nil
}

// Buscar consulta la búsqueda avanzada del sitio, con una caché corta para no
// repetir la misma consulta.
func (s *Servicio) Buscar(ctx context.Context, q boletin.ConsultaBusqueda) (*boletin.ResultadoBusqueda, error) {
	clave := "busquedas/" + huella(fmt.Sprintf("%s|%s|%v|%s|%s|%d|%v",
		q.Texto, q.Seccion, q.Rubros, q.Desde.API(), q.Hasta.API(), q.Pagina, q.TodasLasPalabras))
	if crudo, ok := s.cache.Leer(clave); ok {
		var r boletin.ResultadoBusqueda
		if err := json.Unmarshal(crudo, &r); err == nil {
			return &r, nil
		}
	}
	r, err := s.cli.Buscar(ctx, q)
	if err != nil {
		return nil, err
	}
	if crudo, err := json.Marshal(r); err == nil {
		_ = s.cache.Guardar(clave, crudo, TTLBusqueda)
	}
	return r, nil
}

// Anexo devuelve el PDF de un anexo. Se guarda en base64 porque la caché es
// de JSON; un anexo publicado no cambia.
func (s *Servicio) Anexo(ctx context.Context, sec boletin.Seccion, nro, id string, fecha boletin.Fecha) ([]byte, error) {
	clave := "anexos/" + string(sec) + "/" + fecha.API() + "/" + sanear(id) + "-" + sanear(nro)
	if crudo, ok := s.cache.Leer(clave); ok {
		var b64 string
		if err := json.Unmarshal(crudo, &b64); err == nil {
			if pdf, err := base64.StdEncoding.DecodeString(b64); err == nil {
				return pdf, nil
			}
		}
	}
	pdf, err := s.cli.TraerAnexo(ctx, sec, nro, id, fecha)
	if err != nil {
		return nil, err
	}
	if crudo, err := json.Marshal(base64.StdEncoding.EncodeToString(pdf)); err == nil {
		_ = s.cache.Guardar(clave, crudo, almacen.SinVencimiento)
	}
	return pdf, nil
}

// TieneEdicionEnCache dice si una fecha ya se bajó (incluido el "no hubo
// edición"). Lo usa el relleno para saltear lo hecho.
func (s *Servicio) TieneEdicionEnCache(sec boletin.Seccion, f boletin.Fecha) bool {
	return s.cache.Existe(claveEdicion(sec, f))
}

// FechasDelAnio expone el calendario para el relleno.
func (s *Servicio) FechasDelAnio(ctx context.Context, sec boletin.Seccion, desde, hasta boletin.Fecha) ([]boletin.Fecha, error) {
	return s.fechasConEdicion(ctx, sec, desde, hasta)
}

func sanear(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	if sb.Len() == 0 {
		return "_"
	}
	return sb.String()
}

func huella(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

// ------------------------------------------------------------ búsqueda local

// Fuente dice de dónde salieron los resultados de una búsqueda.
type Fuente string

const (
	// FuenteIndice: se respondió con el índice local, sin tocar el Boletín.
	FuenteIndice Fuente = "indice"
	// FuenteSitio: se le preguntó a la búsqueda avanzada del sitio.
	FuenteSitio Fuente = "sitio"
)

// Busqueda es el resultado de buscar, venga de donde venga.
type Busqueda struct {
	Fuente Fuente          `json:"fuente"`
	Total  int             `json:"total"`
	Pagina int             `json:"pagina"`
	HayMas bool            `json:"hay_mas"`
	Avisos []boletin.Aviso `json:"avisos"`
	// DiasIndexados y DiasConEdicion dicen cuánta historia del rango tiene el
	// índice. Si no coinciden, la búsqueda local vio menos de lo que hay.
	DiasIndexados  int `json:"dias_indexados,omitempty"`
	DiasConEdicion int `json:"dias_con_edicion,omitempty"`
}

// PuedeBuscarLocal dice si el índice cubre el rango entero de una sección.
// Con cobertura parcial se puede buscar igual, pero el resultado avisa.
func (s *Servicio) PuedeBuscarLocal(ctx context.Context, sec boletin.Seccion, desde, hasta boletin.Fecha) (indexados, conEdicion int, ok bool) {
	if s.indice == nil {
		return 0, 0, false
	}
	indexados, err := s.indice.Cobertura(sec, desde, hasta)
	if err != nil {
		return 0, 0, false
	}
	// La cobertura es informativa: se calcula con el calendario que ya esté
	// guardado. Buscar en el índice no puede terminar pidiéndole nada al
	// Boletín, que es justamente lo que el índice viene a evitar.
	return indexados, s.diasConEdicionEnCache(sec, desde, hasta), indexados > 0
}

// diasConEdicionEnCache cuenta los días con edición de un rango usando sólo
// los calendarios ya guardados. Devuelve 0 si no hay ninguno.
func (s *Servicio) diasConEdicionEnCache(sec boletin.Seccion, desde, hasta boletin.Fecha) int {
	var n int
	for anio := desde.Year(); anio <= hasta.Year(); anio++ {
		crudo, ok := s.cache.Leer(fmt.Sprintf("calendarios/%s/%d", sec, anio))
		if !ok {
			continue
		}
		var cal boletin.Calendario
		if err := json.Unmarshal(crudo, &cal); err != nil {
			continue
		}
		for _, f := range cal.Fechas {
			if f.API() >= desde.API() && f.API() <= hasta.API() {
				n++
			}
		}
	}
	return n
}

// BuscarEnIndice busca sobre lo que está indexado, sin pedirle nada al sitio.
func (s *Servicio) BuscarEnIndice(ctx context.Context, q almacen.ConsultaLocal, pagina int) (*Busqueda, error) {
	if s.indice == nil {
		return nil, errors.New("esta instancia no tiene índice local: se levantó con el almacén de disco")
	}
	if pagina < 1 {
		pagina = 1
	}
	if q.Limite <= 0 {
		q.Limite = 50
	}
	q.Desplazamiento = (pagina - 1) * q.Limite

	res, err := s.indice.BuscarLocal(q)
	if err != nil {
		return nil, err
	}
	b := &Busqueda{
		Fuente: FuenteIndice,
		Total:  res.Total,
		Pagina: pagina,
		HayMas: res.Desplazamiento+len(res.Avisos) < res.Total,
		Avisos: res.Avisos,
	}
	b.DiasIndexados, b.DiasConEdicion, _ = s.PuedeBuscarLocal(ctx, q.Seccion, q.Desde, q.Hasta)
	return b, nil
}

// BuscarEnSitio consulta la búsqueda avanzada del Boletín.
func (s *Servicio) BuscarEnSitio(ctx context.Context, q boletin.ConsultaBusqueda) (*Busqueda, error) {
	r, err := s.Buscar(ctx, q)
	if err != nil {
		return nil, err
	}
	return &Busqueda{
		Fuente: FuenteSitio,
		Total:  r.Cantidad,
		Pagina: r.Pagina,
		HayMas: r.HayMas,
		Avisos: r.Avisos,
	}, nil
}

// indexarDetalle suma el texto completo de un aviso al índice de búsqueda.
func (s *Servicio) indexarDetalle(d *boletin.Detalle) {
	if s.indice == nil || d == nil {
		return
	}
	if err := s.indice.IndexarDetalle(d); err != nil {
		slog.Warn("no se pudo indexar el texto del aviso",
			"seccion", d.Seccion, "id", d.ID, "err", err)
	}
}

// indexar vuelca una edición al índice, si hay uno. Un fallo del índice no
// puede tumbar la lectura: la edición ya está guardada y se sirve igual.
func (s *Servicio) indexar(ed *boletin.Edicion) {
	if s.indice == nil || ed == nil {
		return
	}
	if err := s.indice.IndexarEdicion(ed); err != nil {
		slog.Warn("no se pudo indexar la edición",
			"seccion", ed.Seccion, "fecha", ed.Fecha.API(), "err", err)
	}
}
