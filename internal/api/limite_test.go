package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// servidorConSitioQueLimita arma la API contra un sitio falso que anda como el
// de siempre hasta que se prende limitar: desde ahí contesta 429 con
// Retry-After de dos minutos. Devuelve también cuántos pedidos le llegaron.
func servidorConSitioQueLimita(t *testing.T) (srv *httptest.Server, limitar *atomic.Bool, alSitio *int32) {
	t.Helper()
	normal, _ := sitioFalso(t)
	limitar = &atomic.Bool{}
	var pedidos int32
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		if limitar.Load() {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		normal.ServeHTTP(w, r)
	}))
	t.Cleanup(origen.Close)

	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
	})
	c, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := Nuevo(Config{Servicio: servicio.Nuevo(cli, c), PorMinuto: 0, Version: "test"})
	srv = httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return srv, limitar, &pedidos
}

type errorLimitado struct {
	Error         string     `json:"error"`
	Detalle       string     `json:"detalle"`
	Origen        string     `json:"origen"`
	LimitadoHasta *time.Time `json:"limitado_hasta"`
}

func esperarRespuestaDeLimite(t *testing.T, res *http.Response, cuerpo []byte) errorLimitado {
	t.Helper()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("codigo = %d, se esperaba 503 (cuerpo: %s)", res.StatusCode, cuerpo)
	}
	seg, err := strconv.Atoi(res.Header.Get("Retry-After"))
	if err != nil || seg < 1 || seg > 120 {
		t.Errorf("Retry-After = %q, se esperaba un número de segundos entre 1 y 120", res.Header.Get("Retry-After"))
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control = %q: un error de límite no se guarda", cc)
	}
	var e errorLimitado
	if err := json.Unmarshal(cuerpo, &e); err != nil {
		t.Fatal(err)
	}
	if e.Origen != "sitio" {
		t.Errorf("origen = %q, se esperaba sitio", e.Origen)
	}
	if e.Error != "el Boletín Oficial está limitando los pedidos" {
		t.Errorf("error = %q", e.Error)
	}
	// La señal que busca Oculum para dejar de pedir.
	if !strings.Contains(e.Detalle, "respondió 429") {
		t.Errorf("detalle = %q: tiene que decir «respondió 429»", e.Detalle)
	}
	if e.LimitadoHasta == nil || !e.LimitadoHasta.After(time.Now()) {
		t.Errorf("limitado_hasta = %v, se esperaba una hora futura", e.LimitadoHasta)
	}
	return e
}

func TestCuandoElSitioLimitaLaAPIFrenaYAvisa(t *testing.T) {
	srv, limitar, alSitio := servidorConSitioQueLimita(t)

	// Un aviso entra a la caché mientras el sitio todavía contesta.
	if res, cuerpo := pedir(t, srv, "/v1/avisos/primera/346633/2026-09-01"); res.StatusCode != 200 {
		t.Fatalf("codigo = %d, cuerpo = %s", res.StatusCode, cuerpo)
	}

	limitar.Store(true)

	// El pedido que se encuentra con el 429.
	res, cuerpo := pedir(t, srv, "/v1/avisos/primera/1/2026-09-01")
	primero := esperarRespuestaDeLimite(t, res, cuerpo)
	antes := atomic.LoadInt32(alSitio)

	// Los que vienen después se contestan sin tocar el sitio, por cualquier ruta.
	for _, ruta := range []string{
		"/v1/avisos/primera/2/2026-09-01",
		"/v1/ediciones/primera/2026-09-01",
		"/v1/calendario/2026/primera",
		"/v1/rubros/primera",
	} {
		res, cuerpo := pedir(t, srv, ruta)
		e := esperarRespuestaDeLimite(t, res, cuerpo)
		if !e.LimitadoHasta.Equal(*primero.LimitadoHasta) {
			t.Errorf("%s: limitado_hasta = %v, se esperaba el mismo que el primero (%v)", ruta, e.LimitadoHasta, primero.LimitadoHasta)
		}
	}
	if despues := atomic.LoadInt32(alSitio); despues != antes {
		t.Errorf("durante la ventana salieron %d pedidos al sitio; no tenía que salir ninguno", despues-antes)
	}

	// Lo que está en caché se sigue sirviendo.
	if res, cuerpo := pedir(t, srv, "/v1/avisos/primera/346633/2026-09-01"); res.StatusCode != 200 {
		t.Errorf("lo cacheado dejó de servirse: codigo = %d, cuerpo = %s", res.StatusCode, cuerpo)
	}
	if despues := atomic.LoadInt32(alSitio); despues != antes {
		t.Errorf("servir lo cacheado tocó el sitio")
	}

	// Y la salud lo dice.
	res, cuerpo = pedir(t, srv, "/v1/salud")
	if res.StatusCode != 200 {
		t.Fatalf("salud: codigo = %d", res.StatusCode)
	}
	var salud struct {
		OK                 bool       `json:"ok"`
		SitioResponde      bool       `json:"sitio_responde"`
		SitioLimitando     bool       `json:"sitio_limitando"`
		SitioLimitadoHasta *time.Time `json:"sitio_limitado_hasta"`
		Sitio              struct {
			VecesLimitado   int64 `json:"veces_limitado"`
			PedidosFrenados int64 `json:"pedidos_frenados"`
		} `json:"sitio"`
	}
	if err := json.Unmarshal(cuerpo, &salud); err != nil {
		t.Fatal(err)
	}
	if !salud.OK {
		t.Error("ok = false: notarum sigue en pie, el que limita es el sitio")
	}
	if !salud.SitioLimitando {
		t.Errorf("sitio_limitando = false (cuerpo: %s)", cuerpo)
	}
	if salud.SitioLimitadoHasta == nil || !salud.SitioLimitadoHasta.Equal(*primero.LimitadoHasta) {
		t.Errorf("sitio_limitado_hasta = %v, se esperaba %v", salud.SitioLimitadoHasta, primero.LimitadoHasta)
	}
	if salud.SitioResponde {
		t.Error("sitio_responde = true con el sitio limitando")
	}
	if salud.Sitio.VecesLimitado != 1 || salud.Sitio.PedidosFrenados != 4 {
		t.Errorf("sitio = %+v, se esperaba 1 vez limitado y 4 pedidos frenados", salud.Sitio)
	}
}

func TestSaludSinLimiteNoHablaDeHastaCuando(t *testing.T) {
	srv := servidorDePrueba(t)
	_, cuerpo := pedir(t, srv, "/v1/salud")
	var salud map[string]any
	if err := json.Unmarshal(cuerpo, &salud); err != nil {
		t.Fatal(err)
	}
	if v, hay := salud["sitio_limitando"]; !hay || v != false {
		t.Errorf("sitio_limitando = %v, se esperaba false y presente", v)
	}
	if _, hay := salud["sitio_limitado_hasta"]; hay {
		t.Errorf("sitio_limitado_hasta no tiene que aparecer si no hay límite: %s", cuerpo)
	}
}

// Las dos respuestas nuevas tienen que estar en el contrato.
func TestContratoConElSitioLimitando(t *testing.T) {
	srv, limitar, _ := servidorConSitioQueLimita(t)
	limitar.Store(true)
	validar := validadorDelContrato(t, srv)
	validar(t, "/v1/avisos/primera/1/2026-09-01", http.StatusServiceUnavailable)
	validar(t, "/v1/ediciones/primera/2026-09-01", http.StatusServiceUnavailable)
	validar(t, "/v1/calendario/2026/primera", http.StatusServiceUnavailable)
	validar(t, "/v1/rubros/primera", http.StatusServiceUnavailable)
	validar(t, "/v1/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-03", http.StatusServiceUnavailable)
	validar(t, "/v1/salud", http.StatusOK)
}
