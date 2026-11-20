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

// reintentosDePrueba es lo que insiste el cliente de estas pruebas ante un 429
// cuando el trabajo tiene paciencia: un pedido y tres reintentos.
const pedidosHastaRendirse = 4

// contraUnSitioQueBloquea arma el servicio contra un sitio que contesta bien
// lo que `anda` diga y 429 todo lo demás, sin aflojar nunca. Cuenta los
// pedidos que recibieron 429.
func contraUnSitioQueBloquea(t *testing.T, anda func(ruta string) http.HandlerFunc) (*Servicio, *int32) {
	t.Helper()
	var rechazados int32
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := anda(r.URL.Path); h != nil {
			h(w, r)
			return
		}
		atomic.AddInt32(&rechazados, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(origen.Close)
	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
		EsperaTras429: 5 * time.Millisecond,
	})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Nuevo(cli, alm), &rechazados
}

func esLimite(err error) bool {
	var es *boletin.ErrDelSitio
	return errors.As(err, &es) && es.Limitado()
}

// El relleno espera ante un 429 y reintenta. Pero si después de esperar el
// sitio sigue limitando, eso ya no es un tope de ritmo: es un bloqueo. Seguir
// con el día siguiente es volver a esperar y a insistir, día tras día, contra
// un sitio que no deja de decir que no. Se corta ahí.
func TestElRellenoSeCortaSiElSitioNoDejaDeLimitar(t *testing.T) {
	cal := fixture(t, "calendario_primera_2026.json")
	s, rechazados := contraUnSitioQueBloquea(t, func(ruta string) http.HandlerFunc {
		if strings.HasPrefix(ruta, "/calendario/") {
			return func(w http.ResponseWriter, r *http.Request) { w.Write(cal) }
		}
		return nil
	})
	desde, _ := boletin.ParseFecha("2026-09-01")
	hasta, _ := boletin.ParseFecha("2026-09-04")

	r, err := s.Rellenar(context.Background(), boletin.Primera, desde, hasta, nil)

	if !esLimite(err) {
		t.Fatalf("err = %v, se esperaba que dijera que el Boletín está limitando", err)
	}
	if r.Fallidas != 0 {
		t.Errorf("fallidas = %d: los días que no se llegaron a pedir no fallaron", r.Fallidas)
	}
	// Los cuatro intentos del primer día, y nada más: los otros tres días no
	// se piden.
	if n := atomic.LoadInt32(rechazados); n != pedidosHastaRendirse {
		t.Errorf("pedidos rechazados = %d, se esperaban %d", n, pedidosHastaRendirse)
	}
}

// Lo mismo bajando los textos, que es donde más pesa: una edición son cientos
// de avisos, y sin el corte cada uno espera y reintenta por su cuenta.
func TestLaBajadaDeTextosSeCortaSiElSitioNoDejaDeLimitar(t *testing.T) {
	cal := fixture(t, "calendario_primera_2026.json")
	edicion := primera20260901(t)
	s, rechazados := contraUnSitioQueBloquea(t, func(ruta string) http.HandlerFunc {
		switch {
		case strings.HasPrefix(ruta, "/calendario/"):
			return func(w http.ResponseWriter, r *http.Request) { w.Write(cal) }
		case strings.HasPrefix(ruta, "/seccion/"):
			return edicion
		}
		return nil
	})
	dia, _ := boletin.ParseFecha("2026-09-01")

	r, err := s.RellenarConAvisos(context.Background(), boletin.Primera, dia, dia, nil)

	if !esLimite(err) {
		t.Fatalf("err = %v, se esperaba que dijera que el Boletín está limitando", err)
	}
	if r.Bajadas != 1 || r.TextosBajados != 0 {
		t.Errorf("relleno = %+v; se esperaba la edición bajada y ningún texto", r)
	}
	if n := atomic.LoadInt32(rechazados); n != pedidosHastaRendirse {
		t.Errorf("pedidos rechazados = %d, se esperaban %d: los otros 128 avisos no se piden", n, pedidosHastaRendirse)
	}
}
