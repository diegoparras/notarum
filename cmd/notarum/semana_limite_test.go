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
// otras. Salvo que haya fallado porque el Boletín no deja de limitar: ahí las
// otras dos se encontrarían con lo mismo, y esperar la paciencia entera tres
// veces es triplicar la insistencia contra un sitio que ya está bloqueando.
func TestLaSemanaNoInsisteSeccionPorSeccionContraUnBloqueo(t *testing.T) {
	var pedidos int32
	sitio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(sitio.Close)
	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: sitio.URL, Intervalo: time.Millisecond,
		PausaBase: 5 * time.Millisecond, PausaTope: 10 * time.Millisecond,
	})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(cli, alm)

	_, err = trabajoSemanaDelBoletin(srv)(context.Background(), func(string) {})

	var lim *boletin.ErrLimitado
	if !errors.As(err, &lim) {
		t.Fatalf("err = %v, se esperaba que dijera que el sitio está limitando", err)
	}
	// Una sola paciencia: el pedido de cada ventana y el que la agota.
	if n, esperados := atomic.LoadInt32(&pedidos), int32(servicio.VentanasDePaciencia+1); n != esperados {
		t.Errorf("pedidos al sitio = %d, se esperaban %d", n, esperados)
	}
}
