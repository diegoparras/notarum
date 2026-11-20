package boletin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// El sitio del Boletín limita los pedidos por dirección: medido en noviembre
// de 2026, contesta 429 después de unos 140 en cinco minutos. Un intervalo
// fijo no alcanza para respetarlo: con medio segundo entre pedidos salen 600
// en cinco minutos, y con el intervalo que haría falta (más de dos segundos)
// leer una Segunda entera, que son cinco pedidos, tardaría diez segundos
// aunque nadie hubiera pedido nada en toda la mañana.
//
// Por eso son dos cosas:
//
//   - Un tope de pedidos por ventana, con ráfaga. Los primeros salen seguidos
//     (lo que necesita quien consulta de a una edición) y, agotada la ráfaga,
//     salen al ritmo que el sitio tolera (lo que necesita un relleno largo).
//   - Si el 429 llega igual, una pausa de todo el cliente: mientras el sitio
//     castiga, cada pedido de más lo alarga.
const (
	// TopePorDefecto es cuántos pedidos sostenidos se le hacen al sitio real
	// por ventana: uno cada tres segundos.
	TopePorDefecto = 100
	// VentanaDelTope es la ventana sobre la que mide el sitio.
	VentanaDelTope = 5 * time.Minute
	// RafagaPorDefecto es cuántos pedidos pueden salir seguidos sin esperar.
	// En la peor ventana salen la ráfaga más el tope: 120, bajo los 140.
	RafagaPorDefecto = 20
	// SinTope, en Opciones.Tope, apaga el tope.
	SinTope = -1

	// esperaTras429PorDefecto es la pausa ante un 429 que no dice cuánto
	// esperar. Se duplica con cada 429 seguido, así que tres reintentos cubren
	// siete minutos: más que la ventana del sitio.
	esperaTras429PorDefecto = time.Minute
	// esperaMaximaTras429 es el techo, lo diga quien lo diga.
	esperaMaximaTras429 = 15 * time.Minute
	// aguantePorDefecto es lo máximo que espera su turno un pedido que no
	// tiene paciencia. Más que eso, es mejor avisar que hacer esperar.
	aguantePorDefecto = 20 * time.Second
)

type clavePaciencia struct{}

// ConPaciencia marca un contexto como el de un trabajo que puede esperar: un
// relleno, una bajada programada. Ante un 429 el cliente hace la pausa y
// reintenta, en vez de devolver el error.
//
// Sin esa marca el cliente supone que hay alguien esperando la respuesta, y
// le devuelve enseguida un ErrDelSitio que dice cuándo reintentar.
func ConPaciencia(ctx context.Context) context.Context {
	return context.WithValue(ctx, clavePaciencia{}, true)
}

func tienePaciencia(ctx context.Context) bool {
	v, _ := ctx.Value(clavePaciencia{}).(bool)
	return v
}

// errSinTurno es lo que devuelve esperarTurno cuando el turno queda demasiado
// lejos para un pedido sin paciencia. No se consumió nada.
type errSinTurno struct{ espera time.Duration }

func (e *errSinTurno) Error() string {
	return "el turno para pedirle al sitio queda a " + e.espera.Round(time.Second).String()
}

// esperarTurno reserva el próximo momento en que se le puede pedir algo al
// sitio y espera hasta ahí. Respeta tres cosas: el intervalo mínimo entre
// pedidos, la pausa en curso si el sitio contestó 429, y el tope por ventana.
//
// El tope es un balde de fichas: se llena hasta la ráfaga, cada pedido gasta
// una y se repone una cada cadaFicha.
func (c *Cliente) esperarTurno(ctx context.Context) error {
	c.mu.Lock()
	ahora := time.Now()
	turno := ahora
	if c.proximo.After(turno) {
		turno = c.proximo
	}
	if c.pausaHasta.After(turno) {
		turno = c.pausaHasta
	}
	fichas, fichasAl := c.fichas, c.fichasAl
	if c.cadaFicha > 0 {
		if turno.After(fichasAl) {
			fichas += float64(turno.Sub(fichasAl)) / float64(c.cadaFicha)
			if fichas > c.rafaga {
				fichas = c.rafaga
			}
			fichasAl = turno
		}
		if fichas < 1 {
			turno = turno.Add(time.Duration((1 - fichas) * float64(c.cadaFicha)))
			fichas, fichasAl = 1, turno
		}
		fichas--
	}
	espera := turno.Sub(ahora)
	if espera > c.aguante && !tienePaciencia(ctx) {
		c.mu.Unlock()
		return &errSinTurno{espera: espera}
	}
	c.proximo = turno.Add(c.intervalo)
	c.fichas, c.fichasAl = fichas, fichasAl
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

// pausar frena a todo el cliente: el límite del sitio es por dirección, así
// que no importa qué pedido lo disparó. Lo que quedaba de ráfaga se pierde,
// porque el sitio acaba de decir que ya se pidió de más.
func (c *Cliente) pausar(espera time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if hasta := time.Now().Add(espera); hasta.After(c.pausaHasta) {
		c.pausaHasta = hasta
	}
	c.fichas = 0
	if c.pausaHasta.After(c.fichasAl) {
		c.fichasAl = c.pausaHasta
	}
}

// esperaDe429 decide cuánto frenar ante un 429. Si el sitio lo dice en
// Retry-After, manda el sitio; si no, la espera propia, que se duplica con
// cada 429 seguido. Siempre con techo.
func (c *Cliente) esperaDe429(retryAfter string, seguidos int) time.Duration {
	d, ok := leerRetryAfter(retryAfter, time.Now())
	if !ok {
		d = c.esperaTras429
		for i := 0; i < seguidos && d < esperaMaximaTras429; i++ {
			d *= 2
		}
	}
	if d > esperaMaximaTras429 {
		d = esperaMaximaTras429
	}
	return d
}

// leerRetryAfter entiende las dos formas de la cabecera: segundos o una fecha
// HTTP. Un valor vacío, ilegible, cero o ya pasado no sirve de nada.
func leerRetryAfter(valor string, ahora time.Time) (time.Duration, bool) {
	valor = strings.TrimSpace(valor)
	if valor == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(valor); err == nil {
		if s <= 0 {
			return 0, false
		}
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(valor); err == nil {
		if d := t.Sub(ahora); d > 0 {
			return d, true
		}
	}
	return 0, false
}
