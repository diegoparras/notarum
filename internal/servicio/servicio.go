// Package servicio une la lectura del sitio con la caché en disco. Es la capa
// que decide qué se lee de nuevo y qué ya está guardado.
package servicio

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cache"
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
	cache *cache.Disco
}

func Nuevo(cli *boletin.Cliente, c *cache.Disco) *Servicio {
	return &Servicio{cli: cli, cache: c}
}

func (s *Servicio) Cliente() *boletin.Cliente { return s.cli }
func (s *Servicio) Cache() *cache.Disco       { return s.cache }

// ttlPara decide cuánto dura lo que se acaba de leer: una edición pasada no
// cambia nunca; la de hoy (o una futura) sí.
func ttlPara(fecha boletin.Fecha) time.Duration {
	if fecha.API() < boletin.HoyEnArgentina().API() {
		return cache.SinVencimiento
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
	ttl := cache.SinVencimiento
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
		_ = s.cache.Guardar(clave, crudo, cache.SinVencimiento)
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
