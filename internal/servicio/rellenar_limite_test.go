package servicio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
)

// armarConLimite arma el servicio contra un sitio falso que contesta 429 a los
// pedidos que limitar señale (se numeran desde 1) y bien a los demás. La pausa
// ante un 429 la decide la prueba: cortita si quiere ver que el trabajo espera
// y sigue, o un Retry-After largo si quiere ver qué pasa mientras espera.
func armarConLimite(t *testing.T, limitar func(n int32) bool, retryAfter string, pausaBase, pausaTope time.Duration) (*Servicio, *int32) {
	t.Helper()
	cal := fixture(t, "calendario_primera_2026.json")
	portada := fixture(t, "portada_primera_20260901.html")
	detalle := fixture(t, "detalle_primera_346633.html")
	var pedidos int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if limitar(atomic.AddInt32(&pedidos, 1)) {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/calendario/"):
			w.Write(cal)
		case r.URL.Path == "/seccion/primera/20260901":
			w.Write(portada)
		case strings.HasPrefix(r.URL.Path, "/seccion/"):
			// El fixture es la portada del 1/9 y el parser comprueba la fecha:
			// los demás días contestan como un día sin edición.
			http.Redirect(w, r, "/", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/detalleAviso/"):
			w.Write(detalle)
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: srv.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
		PausaBase: pausaBase, PausaTope: pausaTope,
	})
	c, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Nuevo(cli, c), &pedidos
}

// Un relleno que se encuentra con el límite del sitio espera a que pase la
// ventana y sigue donde estaba: la bajada del sábado corre sin nadie mirando, y
// cortarse en el primer 429 la dejaba a medias hasta que alguien lo notara.
func TestRellenarEsperaLaVentanaYSigue(t *testing.T) {
	// Pasan el calendario y la primera edición; la segunda edición recibe 429
	// una vez, y de ahí en más el sitio vuelve a contestar.
	s, pedidos := armarConLimite(t, func(n int32) bool { return n == 3 }, "", 60*time.Millisecond, 0)
	desde, _ := boletin.ParseFecha("2026-09-01")
	hasta, _ := boletin.ParseFecha("2026-09-04")

	var pausas []Avance
	r, err := s.Rellenar(context.Background(), boletin.Primera, desde, hasta, func(a Avance) {
		if !a.EnPausaHasta.IsZero() {
			pausas = append(pausas, a)
		}
	})

	if err != nil {
		t.Fatalf("pasada la ventana el relleno tenía que terminar bien: %v", err)
	}
	// El día que se topó con el límite se volvió a pedir: no quedó como falla.
	if r.Bajadas != 1 || r.SinEdicion != 3 || r.Fallidas != 0 {
		t.Errorf("bajadas = %d, sin edición = %d, fallidas = %d; se esperaban 1, 3 y 0", r.Bajadas, r.SinEdicion, r.Fallidas)
	}
	if n := atomic.LoadInt32(pedidos); n != 6 {
		t.Errorf("pedidos al sitio = %d, se esperaban 6 (calendario, los cuatro días y el que recibió 429)", n)
	}
	if len(pausas) != 1 {
		t.Fatalf("avisos de pausa = %d, se esperaba 1", len(pausas))
	}
	// Quien mira el panel tiene que entender por qué no avanza y hasta cuándo.
	if txt := pausas[0].Texto(); !strings.Contains(txt, "pausa") || !strings.Contains(txt, "se retoma a las") {
		t.Errorf("el aviso de pausa no se entiende: %q", txt)
	}
	if pausas[0].Fecha.API() != "2026-09-02" {
		t.Errorf("la pausa se anotó en el %s, se esperaba el día que quedó esperando (2026-09-02)", pausas[0].Fecha.API())
	}
}

// La paciencia tiene un tope: si el sitio sigue limitando ventana tras ventana,
// el trabajo se corta en vez de quedar reintentando para siempre.
func TestRellenarSeCortaSiElSitioNoDejaDeLimitar(t *testing.T) {
	s, pedidos := armarConLimite(t, func(n int32) bool { return n > 2 }, "", 10*time.Millisecond, 20*time.Millisecond)
	desde, _ := boletin.ParseFecha("2026-09-01")
	hasta, _ := boletin.ParseFecha("2026-09-04")

	r, err := s.Rellenar(context.Background(), boletin.Primera, desde, hasta, nil)

	var lim *boletin.ErrLimitado
	if !errors.As(err, &lim) {
		t.Fatalf("err = %v, se esperaba que dijera que el sitio está limitando", err)
	}
	if r.Bajadas != 1 {
		t.Errorf("bajadas = %d, se esperaba 1: lo que alcanzó a bajar se cuenta", r.Bajadas)
	}
	if r.Fallidas != 0 {
		t.Errorf("fallidas = %d: los días que no se bajaron por el límite no son fallas del día", r.Fallidas)
	}
	// Calendario, una edición, y un pedido de prueba por cada ventana que se
	// esperó más el que agotó la paciencia. Ni uno más.
	if n, esperados := atomic.LoadInt32(pedidos), int32(2+VentanasDePaciencia+1); n != esperados {
		t.Errorf("pedidos al sitio = %d, se esperaban %d", n, esperados)
	}
}

// Mientras espera, el trabajo se tiene que poder cancelar: desde el panel, o
// porque el servicio se está apagando.
func TestRellenarSePuedeCancelarMientrasEspera(t *testing.T) {
	// Una hora de Retry-After: si la espera no atendiera la cancelación, la
	// prueba no terminaría.
	s, _ := armarConLimite(t, func(n int32) bool { return n > 1 }, "3600", time.Hour, 0)
	desde, _ := boletin.ParseFecha("2026-09-01")
	hasta, _ := boletin.ParseFecha("2026-09-04")
	ctx, cancelar := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelar()

	inicio := time.Now()
	_, err := s.Rellenar(ctx, boletin.Primera, desde, hasta, nil)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, se esperaba el de la cancelación", err)
	}
	if d := time.Since(inicio); d > 5*time.Second {
		t.Errorf("tardó %v en enterarse de la cancelación", d)
	}
}

func TestRellenarConAvisosEsperaLaVentanaYSigue(t *testing.T) {
	// Pasan el calendario, la edición del único día y dos avisos; el tercero
	// recibe 429 una vez. La edición trae cien avisos y tienen que quedar los
	// cien: el que se topó con el límite se vuelve a pedir, no se saltea.
	s, pedidos := armarConLimite(t, func(n int32) bool { return n == 5 }, "", 60*time.Millisecond, 0)
	dia, _ := boletin.ParseFecha("2026-09-01")

	var pausas int
	r, err := s.RellenarConAvisos(context.Background(), boletin.Primera, dia, dia, func(a Avance) {
		if !a.EnPausaHasta.IsZero() {
			pausas++
		}
	})

	if err != nil {
		t.Fatalf("pasada la ventana la bajada de textos tenía que terminar bien: %v", err)
	}
	if r.TextosBajados != 100 {
		t.Errorf("textos = %d, se esperaban 100", r.TextosBajados)
	}
	if n := atomic.LoadInt32(pedidos); n != 103 {
		t.Errorf("pedidos al sitio = %d, se esperaban 103 (calendario, edición, cien avisos y el que recibió 429)", n)
	}
	if pausas != 1 {
		t.Errorf("avisos de pausa = %d, se esperaba 1", pausas)
	}
}

func TestRellenarConAvisosSeCortaSiElSitioNoDejaDeLimitar(t *testing.T) {
	s, pedidos := armarConLimite(t, func(n int32) bool { return n > 4 }, "", 10*time.Millisecond, 20*time.Millisecond)
	dia, _ := boletin.ParseFecha("2026-09-01")

	r, err := s.RellenarConAvisos(context.Background(), boletin.Primera, dia, dia, nil)

	var lim *boletin.ErrLimitado
	if !errors.As(err, &lim) {
		t.Fatalf("err = %v, se esperaba que dijera que el sitio está limitando", err)
	}
	if r.TextosBajados != 2 {
		t.Errorf("textos = %d, se esperaban 2", r.TextosBajados)
	}
	if n, esperados := atomic.LoadInt32(pedidos), int32(4+VentanasDePaciencia+1); n != esperados {
		t.Errorf("pedidos al sitio = %d, se esperaban %d", n, esperados)
	}
}

// La hora de la pausa se dice en hora argentina, que es la de quien mira el
// panel, y no en la del servidor.
func TestElAvisoDePausaDiceLaHoraArgentina(t *testing.T) {
	dia, _ := boletin.ParseFecha("2026-11-17")
	a := Avance{
		Seccion: boletin.Primera, Fecha: dia, Dia: 2, DeDias: 5,
		EnPausaHasta: time.Date(2026, 11, 21, 7, 37, 0, 0, time.UTC),
	}
	if txt := a.Texto(); !strings.Contains(txt, "se retoma a las 04:37") {
		t.Errorf("texto = %q, se esperaba «se retoma a las 04:37»", txt)
	}
}

// El primer pedido de un relleno es el calendario. Si justo ese se encuentra
// con el límite —el trabajo arrancó con una ventana ya abierta por otro—,
// también espera: fallar ahí sería cortarse antes de empezar.
func TestRellenarEsperaTambienSiElLimiteLlegaConElCalendario(t *testing.T) {
	s, pedidos := armarConLimite(t, func(n int32) bool { return n == 1 }, "", 60*time.Millisecond, 0)
	dia, _ := boletin.ParseFecha("2026-09-01")

	r, err := s.Rellenar(context.Background(), boletin.Primera, dia, dia, nil)

	if err != nil {
		t.Fatalf("pasada la ventana el relleno tenía que terminar bien: %v", err)
	}
	if r.Bajadas != 1 {
		t.Errorf("bajadas = %d, se esperaba 1", r.Bajadas)
	}
	if n := atomic.LoadInt32(pedidos); n != 3 {
		t.Errorf("pedidos al sitio = %d, se esperaban 3 (el calendario que recibió 429, el calendario y la edición)", n)
	}
}
