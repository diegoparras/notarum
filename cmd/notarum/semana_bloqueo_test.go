package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// La bajada de la semana son tres secciones, y una que falla no se lleva a las
// otras. Salvo que haya fallado porque el Boletín no deja de limitar: las otras
// dos se encontrarían con lo mismo, y esperar e insistir tres veces es
// triplicar los pedidos contra un sitio que ya está bloqueando.
func TestLaSemanaNoInsisteSeccionPorSeccionContraUnBloqueo(t *testing.T) {
	var pedidos int32
	sitio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(sitio.Close)
	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: sitio.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
		EsperaTras429: 5 * time.Millisecond,
	})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	_, err = trabajoSemanaDelBoletin(servicio.Nuevo(cli, alm))(context.Background(), func(string) {})

	var es *boletin.ErrDelSitio
	if !errors.As(err, &es) || !es.Limitado() {
		t.Fatalf("err = %v, se esperaba que dijera que el Boletín está limitando", err)
	}
	// Un pedido y sus tres reintentos, los de la primera sección. Las otras
	// dos no se intentan.
	if n := atomic.LoadInt32(&pedidos); n != 4 {
		t.Errorf("pedidos al sitio = %d, se esperaban 4", n)
	}
}
