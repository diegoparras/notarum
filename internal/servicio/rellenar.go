package servicio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

// Rellenar recorre el calendario de una sección y baja lo que falte.
//
// Es lo que hace que la API conteste rápido después, y lo que llena el índice
// de búsqueda local. Vive acá y no en el comando porque también se lanza
// desde el panel: quien monta la instancia en un panel de deploy no tiene por
// qué abrir una consola para ponerla en marcha.
//
// Se puede cortar y retomar: los días que ya están guardados se saltean.
//
// Es un trabajo largo y nadie está esperando la respuesta de cada pedido: si
// el Boletín contesta 429, el cliente hace la pausa y sigue, en vez de dar
// por fallido ese día y todos los que vienen detrás.
func (s *Servicio) Rellenar(ctx context.Context, sec boletin.Seccion, desde, hasta boletin.Fecha, avisar func(Avance)) (Relleno, error) {
	ctx = boletin.ConPaciencia(ctx)
	var r Relleno
	fechas, err := s.FechasDelAnio(ctx, sec, desde, hasta)
	if err != nil {
		return r, err
	}
	r.Dias = len(fechas)
	slog.Info("rellenando", "seccion", sec, "desde", desde.API(), "hasta", hasta.API(), "dias", r.Dias)

	inicio := time.Now()
	for i, f := range fechas {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if s.TieneEdicionEnCache(sec, f) {
			r.YaEstaban++
			s.avisarDe(avisar, sec, f, i, r)
			continue
		}
		ed, err := s.Edicion(ctx, sec, f, "")
		switch {
		case errors.Is(err, ErrSinEdicion):
			// Un día sin edición no es una falla: son los feriados.
			r.SinEdicion++
		case sigueLimitando(err):
			r.Tardo = time.Since(inicio)
			return r, corteDe("el relleno", sec, f, err)
		case err != nil:
			r.Fallidas++
			slog.Warn("no se pudo bajar", "seccion", sec, "fecha", f.API(), "err", err)
		default:
			r.Bajadas++
			r.Avisos += ed.Cantidad
		}
		s.avisarDe(avisar, sec, f, i, r)
	}
	r.Tardo = time.Since(inicio)
	slog.Info("relleno listo", "seccion", sec, "bajadas", r.Bajadas,
		"ya_estaban", r.YaEstaban, "fallidas", r.Fallidas,
		"tardo", r.Tardo.Round(time.Second).String())

	// Los días que fallaron se cuentan, pero no tiran abajo lo que sí se
	// bajó: volver a correr el relleno retoma donde quedó.
	if r.Fallidas > 0 {
		return r, fmt.Errorf("quedaron %d días sin bajar en la sección %s: volvé a correr el relleno", r.Fallidas, sec)
	}
	return r, nil
}

// RellenarConAvisos hace lo mismo y además baja el texto de cada aviso, que
// es lo que permite buscar por el cuerpo y no sólo por el sumario.
func (s *Servicio) RellenarConAvisos(ctx context.Context, sec boletin.Seccion, desde, hasta boletin.Fecha, avisar func(Avance)) (Relleno, error) {
	ctx = boletin.ConPaciencia(ctx)
	r, err := s.Rellenar(ctx, sec, desde, hasta, avisar)
	if err != nil {
		return r, err
	}
	fechas, err := s.FechasDelAnio(ctx, sec, desde, hasta)
	if err != nil {
		return r, err
	}
	for i, f := range fechas {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		ed, err := s.Edicion(ctx, sec, f, "")
		if err != nil {
			continue
		}
		for _, a := range ed.Avisos {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			if _, err := s.Aviso(ctx, sec, a.ID, a.Fecha); err != nil {
				if sigueLimitando(err) {
					return r, corteDe("la bajada de textos", sec, f, err)
				}
				slog.Warn("no se pudo bajar el aviso", "id", a.ID, "err", err)
				continue
			}
			r.TextosBajados++
		}
		if avisar != nil {
			avisar(Avance{
				Seccion: sec, Fecha: f, Dia: i + 1, DeDias: len(fechas),
				Relleno: r, BajandoTextos: true,
			})
		}
	}
	return r, nil
}

// sigueLimitando dice si un pedido de un trabajo con paciencia volvió igual con
// el límite del Boletín. Con paciencia el cliente ya esperó la pausa y
// reintentó, varias veces, antes de devolver ese error: llegado acá no es un
// tope de ritmo que se arregla esperando un poco más, es un bloqueo.
func sigueLimitando(err error) bool {
	var es *boletin.ErrDelSitio
	return errors.As(err, &es) && es.Limitado()
}

// corteDe arma el error de un trabajo largo que se corta porque el Boletín no
// deja de limitar.
//
// Se corta en vez de seguir con el día o el aviso que viene porque seguir es
// volver a esperar y a insistir, una vez por cada uno de los que faltan —y
// una edición son cientos de avisos—, contra un sitio que acaba de decir que
// no varias veces seguidas. Lo bajado queda guardado y el trabajo se retoma
// donde quedó.
func corteDe(que string, sec boletin.Seccion, f boletin.Fecha, err error) error {
	slog.Warn(que+" se corta: el Boletín sigue limitando después de esperar",
		"seccion", sec, "fecha", f.API(), "err", err)
	return fmt.Errorf("%s de la sección %s se cortó en el %s: el Boletín siguió limitando los pedidos después de esperar y reintentar; lo bajado queda guardado y se puede retomar más tarde: %w",
		que, sec, f.API(), err)
}

func (s *Servicio) avisarDe(avisar func(Avance), sec boletin.Seccion, f boletin.Fecha, i int, r Relleno) {
	if avisar == nil {
		return
	}
	avisar(Avance{Seccion: sec, Fecha: f, Dia: i + 1, DeDias: r.Dias, Relleno: r})
}

// Relleno es cómo le fue al recorrido.
type Relleno struct {
	Dias       int `json:"dias"`
	Bajadas    int `json:"bajadas"`
	YaEstaban  int `json:"ya_estaban"`
	SinEdicion int `json:"sin_edicion"`
	Fallidas   int `json:"fallidas"`
	Avisos     int `json:"avisos"`
	// TextosBajados sólo se llena cuando además se piden los avisos enteros.
	TextosBajados int           `json:"textos,omitempty"`
	Tardo         time.Duration `json:"-"`
}

// Avance es una novedad del recorrido, para poder mostrarlo mientras pasa.
type Avance struct {
	Seccion       boletin.Seccion
	Fecha         boletin.Fecha
	Dia, DeDias   int
	Relleno       Relleno
	BajandoTextos bool
}

// Texto arma la línea que se muestra en el panel.
func (a Avance) Texto() string {
	que := "mirando"
	if a.BajandoTextos {
		que = "bajando los textos de"
	}
	return fmt.Sprintf("%s %s · día %d de %d · %d bajadas, %d ya estaban",
		que, a.Fecha.API(), a.Dia, a.DeDias, a.Relleno.Bajadas, a.Relleno.YaEstaban)
}
