package boletin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sitioQueLimita contesta 429 mientras limitar diga que sí, y los rubros de
// siempre cuando no. Cuenta cada pedido que le llega: es lo que permite decir
// "durante la ventana no salió ninguno".
func sitioQueLimita(t *testing.T, limitar func(n int32) bool, retryAfter string) (*httptest.Server, *int32) {
	t.Helper()
	cuerpo := fixture(t, "rubros_primera.json")
	var pedidos int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&pedidos, 1)
		if limitar(n) {
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

func siempre(int32) bool { return true }

func limitadoDe(t *testing.T, err error) *ErrLimitado {
	t.Helper()
	var lim *ErrLimitado
	if !errors.As(err, &lim) {
		t.Fatalf("err = %v, se esperaba ErrLimitado", err)
	}
	return lim
}

// Lo central: después de un 429, el cliente no le manda nada más al sitio
// mientras dura la ventana.
func TestAnte429NoSaleNingunPedidoDuranteLaVentana(t *testing.T) {
	srv, pedidos := sitioQueLimita(t, siempre, "")
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond, PausaBase: time.Hour})

	_, err := c.TraerRubros(context.Background(), Primera)
	primero := limitadoDe(t, err)
	if primero.SinPedir {
		t.Error("el primer pedido sí salió: SinPedir debería ser false")
	}
	// Oculum reconoce el caso por este texto: no se puede perder.
	if !strings.Contains(err.Error(), "respondió 429") {
		t.Errorf("el error no dice «respondió 429»: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, err := c.TraerRubros(context.Background(), Primera)
		lim := limitadoDe(t, err)
		if !lim.SinPedir {
			t.Errorf("pedido %d: se esperaba SinPedir", i)
		}
		if !lim.Hasta.Equal(primero.Hasta) {
			t.Errorf("pedido %d: la ventana se movió de %v a %v", i, primero.Hasta, lim.Hasta)
		}
		if !strings.Contains(err.Error(), "respondió 429") {
			t.Errorf("el error del pedido frenado no dice «respondió 429»: %v", err)
		}
	}
	if n := atomic.LoadInt32(pedidos); n != 1 {
		t.Errorf("al sitio le llegaron %d pedidos; durante la ventana no tenía que salir ninguno más que el primero", n)
	}
}

// Un error de límite sigue siendo un error del sitio para quien sólo pregunta
// eso (el lector web, por ejemplo).
func TestErrLimitadoEsUnErrorDelSitio(t *testing.T) {
	srv, _ := sitioQueLimita(t, siempre, "")
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond, PausaBase: time.Hour})
	_, err := c.TraerRubros(context.Background(), Primera)
	var es *ErrDelSitio
	if !errors.As(err, &es) || es.Codigo != http.StatusTooManyRequests {
		t.Errorf("err = %v, se esperaba que envolviera un ErrDelSitio con código 429", err)
	}
}

func TestAlTerminarLaVentanaSeReanuda(t *testing.T) {
	srv, pedidos := sitioQueLimita(t, func(n int32) bool { return n == 1 }, "")
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond, PausaBase: 80 * time.Millisecond})

	_, err := c.TraerRubros(context.Background(), Primera)
	lim := limitadoDe(t, err)
	if _, err := c.TraerRubros(context.Background(), Primera); err == nil {
		t.Fatal("adentro de la ventana el pedido tenía que frenarse")
	}
	if n := atomic.LoadInt32(pedidos); n != 1 {
		t.Fatalf("pedidos al sitio = %d, se esperaba 1", n)
	}

	time.Sleep(time.Until(lim.Hasta) + 20*time.Millisecond)

	if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
		t.Fatalf("terminada la ventana se esperaba que volviera a pedir: %v", err)
	}
	if n := atomic.LoadInt32(pedidos); n != 2 {
		t.Errorf("pedidos al sitio = %d, se esperaban 2", n)
	}
	if m := c.Metricas(); m.Limitando || m.LimitadoHasta != nil {
		t.Errorf("ya no está limitando y las métricas dicen que sí: %+v", m)
	}
}

func TestRespetaRetryAfter(t *testing.T) {
	enDosMinutos := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	casos := []struct {
		nombre     string
		retryAfter string
		min, max   time.Duration
	}{
		{"en segundos", "120", 120 * time.Second, 120 * time.Second},
		// La fecha HTTP no tiene fracciones de segundo: se acepta un margen.
		{"como fecha", enDosMinutos, 110 * time.Second, 121 * time.Second},
		{"desmesurado: tiene tope", "999999999", TopeRetryAfter, TopeRetryAfter},
		{"ilegible: se usa la espera propia", "pronto", 7 * time.Second, 7 * time.Second},
		{"cero: se usa la espera propia", "0", 7 * time.Second, 7 * time.Second},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			srv, _ := sitioQueLimita(t, siempre, caso.retryAfter)
			c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond, PausaBase: 7 * time.Second})
			_, err := c.TraerRubros(context.Background(), Primera)
			lim := limitadoDe(t, err)
			if d := lim.Hasta.Sub(lim.Desde); d < caso.min || d > caso.max {
				t.Errorf("la ventana duró %v, se esperaba entre %v y %v", d, caso.min, caso.max)
			}
		})
	}
}

// Sin Retry-After la espera se duplica con cada 429 seguido, hasta un tope, y
// vuelve al principio cuando el sitio contesta bien.
func TestLaEsperaCreceTieneTopeYSeReinicia(t *testing.T) {
	const base, tope = 20 * time.Millisecond, 50 * time.Millisecond
	// 429, 429, 429, 429, bien, 429.
	srv, _ := sitioQueLimita(t, func(n int32) bool { return n != 5 }, "")
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond, PausaBase: base, PausaTope: tope})

	pedirYEsperar := func() time.Duration {
		t.Helper()
		_, err := c.TraerRubros(context.Background(), Primera)
		lim := limitadoDe(t, err)
		if lim.SinPedir {
			t.Fatal("este pedido tenía que salir: la ventana anterior ya había terminado")
		}
		time.Sleep(time.Until(lim.Hasta) + 10*time.Millisecond)
		return lim.Hasta.Sub(lim.Desde)
	}

	for i, esperada := range []time.Duration{base, 2 * base, tope, tope} {
		if d := pedirYEsperar(); d != esperada {
			t.Errorf("429 número %d: la ventana duró %v, se esperaba %v", i+1, d, esperada)
		}
	}
	if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
		t.Fatalf("el quinto pedido tenía que andar: %v", err)
	}
	if d := pedirYEsperar(); d != base {
		t.Errorf("después de una respuesta buena la ventana duró %v, se esperaba volver a %v", d, base)
	}
}

// Los pedidos que estaban esperando su turno cuando llegó el 429 tampoco salen.
func TestLosPedidosEnColaNoSalenSiElSitioEmpezoALimitar(t *testing.T) {
	srv, pedidos := sitioQueLimita(t, siempre, "")
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: 150 * time.Millisecond, PausaBase: time.Hour})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.TraerRubros(context.Background(), Primera)
			var lim *ErrLimitado
			if !errors.As(err, &lim) {
				t.Errorf("err = %v, se esperaba ErrLimitado", err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(pedidos); n != 1 {
		t.Errorf("al sitio le llegaron %d pedidos, se esperaba sólo el primero", n)
	}
}

func TestMetricasMuestranElLimite(t *testing.T) {
	srv, _ := sitioQueLimita(t, siempre, "300")
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: time.Millisecond})

	if m := c.Metricas(); m.Limitando || m.LimitadoHasta != nil || m.Ultimo429 != nil {
		t.Errorf("sin ningún 429 las métricas no tienen que hablar de límite: %+v", m)
	}

	_, err := c.TraerRubros(context.Background(), Primera)
	lim := limitadoDe(t, err)
	for i := 0; i < 3; i++ {
		_, _ = c.TraerRubros(context.Background(), Primera)
	}

	m := c.Metricas()
	if !m.Limitando {
		t.Error("limitando = false")
	}
	if m.LimitadoHasta == nil || !m.LimitadoHasta.Equal(lim.Hasta) {
		t.Errorf("limitado_hasta = %v, se esperaba %v", m.LimitadoHasta, lim.Hasta)
	}
	if m.Ultimo429 == nil {
		t.Error("ultimo_429 vacío")
	}
	if m.VecesLimitado != 1 {
		t.Errorf("veces_limitado = %d, se esperaba 1", m.VecesLimitado)
	}
	if m.PedidosFrenados != 3 {
		t.Errorf("pedidos_frenados = %d, se esperaban 3", m.PedidosFrenados)
	}
	// Los frenados no son lecturas: no llegaron al sitio.
	if m.Lecturas != 1 {
		t.Errorf("lecturas = %d, se esperaba 1", m.Lecturas)
	}
}

// El ritmo por defecto tiene que quedar por debajo de lo que se midió: unos
// 140 pedidos en cinco minutos antes del primer 429 (19/11/2026). No es un
// límite que el sitio documente, pero es la única referencia que hay.
func TestElIntervaloPorDefectoQuedaPorDebajoDeLoMedido(t *testing.T) {
	const medidosEnCincoMinutos = 140
	c := NuevoCliente(Opciones{})
	if c.intervalo != IntervaloPorDefecto {
		t.Errorf("intervalo sin configurar = %v, se esperaba IntervaloPorDefecto (%v)", c.intervalo, IntervaloPorDefecto)
	}
	if caben := int(5 * time.Minute / c.intervalo); caben >= medidosEnCincoMinutos {
		t.Errorf("con %v entre pedidos caben %d en cinco minutos; el 429 apareció a los %d",
			c.intervalo, caben, medidosEnCincoMinutos)
	}
}
