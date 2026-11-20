package servicio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
)

// Un 429 en medio de un relleno no puede costar el día, y menos todos los que
// siguen: el relleno espera lo que haga falta y continúa.
func TestElRellenoEsperaAnteUn429(t *testing.T) {
	cal := fixture(t, "calendario_primera_2026.json")
	sitio := primera20260901(t)
	var ediciones, limitados int32
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/calendario/") {
			w.Write(cal)
			return
		}
		// La segunda página de la edición choca una vez con el límite.
		if strings.HasPrefix(r.URL.Path, "/seccion/actualizar/") && atomic.AddInt32(&limitados, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/seccion/primera/") {
			atomic.AddInt32(&ediciones, 1)
		}
		sitio(w, r)
	}))
	t.Cleanup(origen.Close)

	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
		EsperaTras429: 50 * time.Millisecond,
	})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Nuevo(cli, alm)

	fecha, _ := boletin.ParseFecha("2026-09-01")
	r, err := s.Rellenar(context.Background(), boletin.Primera, fecha, fecha, nil)
	if err != nil {
		t.Fatalf("el relleno falló por un 429 que se podía esperar: %v", err)
	}
	if r.Bajadas != 1 || r.Fallidas != 0 || r.Avisos != 129 {
		t.Errorf("relleno = %+v; se esperaba una bajada de 129 avisos y ninguna fallida", r)
	}
	if ediciones != 1 {
		t.Errorf("la portada se pidió %d veces: el reintento es de la página, no de la edición", ediciones)
	}
	if m := cli.Metricas(); m.Limitados != 1 {
		t.Errorf("limitados = %d, se esperaba 1", m.Limitados)
	}

	// El mismo 429, para alguien que espera una respuesta, vuelve como error.
	atomic.StoreInt32(&limitados, 0)
	otra, _ := almacen.NuevoDisco(t.TempDir())
	_, err = Nuevo(cli, otra).Edicion(context.Background(), boletin.Primera, fecha, "")
	if err == nil {
		t.Fatal("sin paciencia se esperaba el error, no una espera")
	}
}
