package boletin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func limitadoDe(t *testing.T, err error) *ErrDelSitio {
	t.Helper()
	var es *ErrDelSitio
	if !errors.As(err, &es) || !es.Limitado() {
		t.Fatalf("err = %v, se esperaba un ErrDelSitio limitado", err)
	}
	return es
}

// Un Limitado tiene dos motivos y hay que poder distinguirlos: el sitio
// contestó 429, o notarum se frenó solo antes de que el sitio se quejara.
// Decir "el Boletín respondió 429" en el segundo caso sería mentira.
func TestUnLimitadoDiceSiFueElSitio(t *testing.T) {
	var pedidos int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond})

	// El pedido que recibió el 429.
	_, err := c.TraerRubros(context.Background(), Primera)
	if es := limitadoDe(t, err); !es.PorElSitio {
		t.Error("el sitio contestó 429 y el error no lo dice")
	}

	// El que no salió porque sigue la pausa de ese 429.
	_, err = c.TraerRubros(context.Background(), Primera)
	if es := limitadoDe(t, err); !es.PorElSitio {
		t.Error("el pedido se frenó por la pausa de un 429 del sitio y el error no lo dice")
	}
	if n := atomic.LoadInt32(&pedidos); n != 1 {
		t.Errorf("pedidos al sitio = %d, se esperaba 1", n)
	}
}

func TestElTopePropioNoEsUn429DelSitio(t *testing.T) {
	cuerpo := fixture(t, "rubros_primera.json")
	var pedidos int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		w.Write(cuerpo)
	}))
	t.Cleanup(srv.Close)
	// Un pedido por hora: el segundo queda mucho más lejos de lo que aguanta
	// alguien que espera una respuesta.
	c := NuevoCliente(Opciones{
		Base: srv.URL, Intervalo: time.Millisecond,
		Tope: 1, Ventana: time.Hour, Rafaga: 1,
	})

	if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
		t.Fatal(err)
	}
	_, err := c.TraerRubros(context.Background(), Primera)
	if es := limitadoDe(t, err); es.PorElSitio {
		t.Error("el sitio no contestó ningún 429: el freno fue el tope propio")
	}
	if n := atomic.LoadInt32(&pedidos); n != 1 {
		t.Errorf("pedidos al sitio = %d, se esperaba 1", n)
	}
}
