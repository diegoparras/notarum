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

// El tope por defecto es para el sitio real, que contesta 429 después de unos
// 140 pedidos en cinco minutos. Contra otro origen (un servidor de prueba) no
// se supone ningún tope.
func TestElTopePorDefectoEsParaElSitioReal(t *testing.T) {
	real := NuevoCliente(Opciones{})
	if real.cadaFicha != VentanaDelTope/TopePorDefecto || real.rafaga != RafagaPorDefecto {
		t.Errorf("contra el sitio real: una ficha cada %v, ráfaga %v", real.cadaFicha, real.rafaga)
	}
	// Lo peor que puede pasar en una ventana: la ráfaga más lo que se repone.
	if peor := TopePorDefecto + RafagaPorDefecto; peor >= 140 {
		t.Errorf("en cinco minutos pueden salir %d pedidos: el sitio corta cerca de los 140", peor)
	}

	otro := NuevoCliente(Opciones{Base: "http://127.0.0.1:1"})
	if otro.cadaFicha != 0 {
		t.Errorf("contra otro origen no debería haber tope: una ficha cada %v", otro.cadaFicha)
	}
	apagado := NuevoCliente(Opciones{Tope: SinTope})
	if apagado.cadaFicha != 0 {
		t.Errorf("con SinTope no debería haber tope: una ficha cada %v", apagado.cadaFicha)
	}
}

// Pasada la ráfaga, los pedidos salen al ritmo del tope y no al del intervalo.
func TestElTopeFrenaDespuesDeLaRafaga(t *testing.T) {
	cuerpo := fixture(t, "rubros_primera.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	defer srv.Close()
	// 20 pedidos por segundo sostenidos (uno cada 50 ms), con ráfaga de 3.
	c := NuevoCliente(Opciones{
		Base: srv.URL, Intervalo: time.Millisecond,
		Tope: 20, Ventana: time.Second, Rafaga: 3,
	})

	inicio := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
			t.Fatal(err)
		}
	}
	rafaga := time.Since(inicio)
	for i := 0; i < 3; i++ {
		if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
			t.Fatal(err)
		}
	}
	total := time.Since(inicio)

	// Los tres que siguen a la ráfaga esperan una ficha cada uno: 150 ms,
	// menos lo poco que se repuso mientras salía la ráfaga.
	if total-rafaga < 100*time.Millisecond {
		t.Errorf("después de la ráfaga, tres pedidos tardaron %v: el tope no frenó", total-rafaga)
	}
}

// sitioQueLimita contesta 429 las primeras veces y después contesta bien.
func sitioQueLimita(t *testing.T, veces int32, retryAfter string) (*httptest.Server, *int32) {
	t.Helper()
	cuerpo := fixture(t, "rubros_primera.json")
	var pedidos int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&pedidos, 1) <= veces {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write(cuerpo)
	}))
	t.Cleanup(srv.Close)
	return srv, &pedidos
}

// Un trabajo largo (el relleno) puede esperar: ante un 429 hace la pausa y
// sigue, en vez de dar el día por perdido.
func TestUn429SeEsperaYSeReintentaConPaciencia(t *testing.T) {
	srv, pedidos := sitioQueLimita(t, 1, "")
	c := NuevoCliente(Opciones{
		Base: srv.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
		EsperaTras429: 80 * time.Millisecond,
	})

	inicio := time.Now()
	rs, err := c.TraerRubros(ConPaciencia(context.Background()), Primera)
	if err != nil {
		t.Fatalf("con paciencia se esperaba el reintento: %v", err)
	}
	if len(rs) == 0 {
		t.Error("vino vacío")
	}
	if *pedidos != 2 {
		t.Errorf("pedidos = %d, se esperaban 2", *pedidos)
	}
	if d := time.Since(inicio); d < 80*time.Millisecond {
		t.Errorf("tardó %v: no respetó la pausa de 80 ms", d)
	}
	if m := c.Metricas(); m.Limitados != 1 {
		t.Errorf("limitados = %d, se esperaba 1", m.Limitados)
	}
}

// Quien está esperando una respuesta HTTP no puede esperar un minuto: el 429
// vuelve enseguida, diciendo cuándo reintentar. Y mientras dura la pausa no
// se le pega más al sitio: cada pedido de más alarga el castigo.
func TestUn429SinPacienciaVuelveEnseguidaYPausaATodos(t *testing.T) {
	srv, pedidos := sitioQueLimita(t, 1, "")
	c := NuevoCliente(Opciones{
		Base: srv.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
		EsperaTras429: 400 * time.Millisecond,
	})
	c.aguante = 20 * time.Millisecond

	inicio := time.Now()
	_, err := c.TraerRubros(context.Background(), Primera)
	var es *ErrDelSitio
	if !errors.As(err, &es) || !es.Limitado() {
		t.Fatalf("err = %v, se esperaba un ErrDelSitio por límite de pedidos", err)
	}
	if es.ReintentarEn != 400*time.Millisecond {
		t.Errorf("reintentar en %v, se esperaban 400 ms", es.ReintentarEn)
	}

	// El segundo ni sale: la pausa es del cliente entero.
	_, err = c.TraerRubros(context.Background(), Primera)
	if !errors.As(err, &es) || !es.Limitado() || es.ReintentarEn <= 0 {
		t.Fatalf("durante la pausa: err = %v", err)
	}
	if *pedidos != 1 {
		t.Errorf("pedidos al sitio = %d: durante la pausa no tiene que salir ninguno", *pedidos)
	}
	if d := time.Since(inicio); d > 300*time.Millisecond {
		t.Errorf("tardó %v: sin paciencia tenía que volver enseguida", d)
	}
	if m := c.Metricas(); m.EnPausaHasta == nil {
		t.Error("las métricas no dicen que el cliente está en pausa")
	}

	// Pasada la pausa, vuelve a andar.
	time.Sleep(450 * time.Millisecond)
	if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
		t.Fatalf("pasada la pausa: %v", err)
	}
}

// Si el sitio dice cuánto esperar, manda el sitio.
func TestRetryAfterDelSitioManda(t *testing.T) {
	srv, _ := sitioQueLimita(t, 1, "7")
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond})
	_, err := c.TraerRubros(context.Background(), Primera)
	var es *ErrDelSitio
	if !errors.As(err, &es) || es.ReintentarEn != 7*time.Second {
		t.Fatalf("err = %v: se esperaba reintentar en 7 s, como pidió el sitio", err)
	}
}

func TestLeerRetryAfter(t *testing.T) {
	ahora := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	casos := []struct {
		valor string
		d     time.Duration
		ok    bool
	}{
		{"", 0, false},
		{"120", 2 * time.Minute, true},
		{" 5 ", 5 * time.Second, true},
		{"0", 0, false},
		{"-3", 0, false},
		{"pronto", 0, false},
		{"Fri, 20 Nov 2026 12:01:30 GMT", 90 * time.Second, true},
		{"Fri, 20 Nov 2026 11:00:00 GMT", 0, false}, // ya pasó
	}
	for _, c := range casos {
		d, ok := leerRetryAfter(c.valor, ahora)
		if d != c.d || ok != c.ok {
			t.Errorf("leerRetryAfter(%q) = %v, %v; se esperaba %v, %v", c.valor, d, ok, c.d, c.ok)
		}
	}
}

// Sin Retry-After la espera crece con cada 429 seguido, hasta un máximo.
func TestLaEsperaCreceConCada429(t *testing.T) {
	c := NuevoCliente(Opciones{Base: "http://127.0.0.1:1"})
	if d := c.esperaDe429("", 0); d != time.Minute {
		t.Errorf("primera espera = %v", d)
	}
	if d := c.esperaDe429("", 2); d != 4*time.Minute {
		t.Errorf("tercera espera = %v", d)
	}
	if d := c.esperaDe429("", 30); d != esperaMaximaTras429 {
		t.Errorf("espera sin techo: %v", d)
	}
	if d := c.esperaDe429("86400", 0); d != esperaMaximaTras429 {
		t.Errorf("un Retry-After de un día tiene que quedar en el máximo: %v", d)
	}
}
