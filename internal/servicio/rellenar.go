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
func (s *Servicio) Rellenar(ctx context.Context, sec boletin.Seccion, desde, hasta boletin.Fecha, avisar func(Avance)) (Relleno, error) {
	var r Relleno
	var pac paciencia

	// El calendario es el primer pedido, y también puede encontrarse con el
	// límite: el trabajo pudo arrancar con una ventana ya abierta por otro.
	var fechas []boletin.Fecha
	err, corte := pac.insistir(ctx, func() (err error) {
		fechas, err = s.FechasDelAnio(ctx, sec, desde, hasta)
		return err
	}, func(pausa time.Time) {
		slog.Warn("el relleno espera antes de empezar: el Boletín está limitando los pedidos",
			"seccion", sec, "hasta", pausa.UTC())
		if avisar != nil {
			avisar(Avance{Seccion: sec, Fecha: desde, EnPausaHasta: pausa})
		}
	})
	if corte != nil {
		return r, motivoDelCorte("el relleno", sec, desde, corte)
	}
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
		// Si el sitio pide que se le pida menos, se espera a que pase la
		// ventana y se vuelve a pedir este mismo día.
		var ed *boletin.Edicion
		err, corte := pac.insistir(ctx, func() (err error) {
			ed, err = s.Edicion(ctx, sec, f, "")
			return err
		}, func(pausa time.Time) {
			slog.Warn("el relleno espera: el Boletín está limitando los pedidos",
				"seccion", sec, "fecha", f.API(), "hasta", pausa.UTC())
			if avisar != nil {
				avisar(Avance{Seccion: sec, Fecha: f, Dia: i + 1, DeDias: r.Dias, Relleno: r, EnPausaHasta: pausa})
			}
		})
		if corte != nil {
			// Seguir recorriendo sería anotar una falla por cada día sin haber
			// pedido ninguno: se corta acá, y como el relleno se retoma, no se
			// pierde nada.
			r.Tardo = time.Since(inicio)
			return r, motivoDelCorte("el relleno", sec, f, corte)
		}
		switch {
		case errors.Is(err, ErrSinEdicion):
			// Un día sin edición no es una falla: son los feriados.
			r.SinEdicion++
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
	r, err := s.Rellenar(ctx, sec, desde, hasta, avisar)
	if err != nil {
		return r, err
	}
	fechas, err := s.FechasDelAnio(ctx, sec, desde, hasta)
	if err != nil {
		return r, err
	}
	var pac paciencia
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
			// Igual que con las ediciones: ante el límite se espera y se vuelve
			// a pedir este mismo aviso, no se lo saltea.
			errAviso, corte := pac.insistir(ctx, func() error {
				_, err := s.Aviso(ctx, sec, a.ID, a.Fecha)
				return err
			}, func(pausa time.Time) {
				slog.Warn("la bajada de textos espera: el Boletín está limitando los pedidos",
					"seccion", sec, "fecha", f.API(), "hasta", pausa.UTC())
				if avisar != nil {
					avisar(Avance{
						Seccion: sec, Fecha: f, Dia: i + 1, DeDias: len(fechas),
						Relleno: r, BajandoTextos: true, EnPausaHasta: pausa,
					})
				}
			})
			if corte != nil {
				// Una edición son cientos de avisos: sin este corte, cada uno
				// de los que faltan dejaría su línea de error.
				return r, motivoDelCorte("la bajada de textos", sec, f, corte)
			}
			if errAviso != nil {
				slog.Warn("no se pudo bajar el aviso", "id", a.ID, "err", errAviso)
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

// sitioLimitando dice si el error es el del Boletín pidiendo que se le pida
// menos.
func sitioLimitando(err error) bool {
	var lim *boletin.ErrLimitado
	return errors.As(err, &lim)
}

// paciencia lleva la cuenta de cuántas ventanas de límite seguidas esperó un
// trabajo largo.
//
// Un pedido de la API no espera: contesta 503 y que decida el cliente. Un
// relleno sí, porque corre sin nadie mirando —la bajada del sábado, a las
// cuatro de la mañana— y cortarse en el primer 429 lo deja a medias hasta que
// alguien lo note. Pero no para siempre: si el sitio sigue limitando ventana
// tras ventana, lo que hay es un bloqueo, y contra eso insistir sólo empeora.
type paciencia struct {
	seguidas int
}

// esperar mira el resultado de un pedido al sitio. Si es el límite, avisa,
// espera a que pase la ventana y devuelve reintentar. Si no lo es, no hace
// nada: el error, si hay, es asunto de quien llama. Devuelve un error propio
// en dos casos: se agotó la paciencia (el del límite) o se canceló el trabajo
// mientras esperaba (el del contexto).
func (p *paciencia) esperar(ctx context.Context, err error, avisar func(hasta time.Time)) (reintentar bool, _ error) {
	var lim *boletin.ErrLimitado
	if !errors.As(err, &lim) {
		p.seguidas = 0
		return false, nil
	}
	p.seguidas++
	if p.seguidas > VentanasDePaciencia {
		return false, err
	}
	avisar(lim.Hasta)
	t := time.NewTimer(time.Until(lim.Hasta))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-t.C:
		return true, nil
	}
}

// insistir hace un pedido y, mientras choque con el límite, espera y lo
// repite. Devuelve por separado cómo le fue al pedido (cualquier resultado
// menos el del límite) y, si hubo que dejar de insistir, por qué: se agotó la
// paciencia o se canceló el trabajo.
func (p *paciencia) insistir(ctx context.Context, pedir func() error, avisar func(hasta time.Time)) (errPedido, corte error) {
	for {
		err := pedir()
		reintentar, errEspera := p.esperar(ctx, err, avisar)
		if errEspera != nil {
			return nil, errEspera
		}
		if !reintentar {
			return err, nil
		}
	}
}

// motivoDelCorte arma el error de un trabajo que dejó de insistir. Una
// cancelación pasa tal cual; el límite se explica, porque es lo que va a leer
// quien mire el panel a la mañana siguiente.
func motivoDelCorte(que string, sec boletin.Seccion, f boletin.Fecha, corte error) error {
	if !sitioLimitando(corte) {
		return corte
	}
	slog.Warn(que+" se corta: el Boletín sigue limitando los pedidos",
		"seccion", sec, "fecha", f.API(), "err", corte)
	return fmt.Errorf("%s de la sección %s se cortó en el %s, después de esperar %d veces a que el Boletín dejara de limitar; se puede retomar después: %w",
		que, sec, f.API(), VentanasDePaciencia, corte)
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
	// EnPausaHasta, cuando no es cero, dice que el recorrido está esperando
	// porque el Boletín pidió que se le pida menos, y hasta cuándo.
	EnPausaHasta time.Time
}

// VentanasDePaciencia es cuántas ventanas seguidas de límite espera un trabajo
// largo antes de cortarse. Con la espera creciente del cliente —1, 2, 4, 8, 16
// y 30 minutos— seis ventanas son alrededor de una hora.
const VentanasDePaciencia = 6

// Texto arma la línea que se muestra en el panel.
func (a Avance) Texto() string {
	if !a.EnPausaHasta.IsZero() {
		// La hora, en la de la Argentina: es la de quien mira el panel, no la
		// del servidor.
		return fmt.Sprintf("el Boletín pidió una pausa; se retoma a las %s · %s · día %d de %d",
			boletin.EnArgentina(a.EnPausaHasta).Format("15:04"), a.Fecha.API(), a.Dia, a.DeDias)
	}
	que := "mirando"
	if a.BajandoTextos {
		que = "bajando los textos de"
	}
	return fmt.Sprintf("%s %s · día %d de %d · %d bajadas, %d ya estaban",
		que, a.Fecha.API(), a.Dia, a.DeDias, a.Relleno.Bajadas, a.Relleno.YaEstaban)
}
