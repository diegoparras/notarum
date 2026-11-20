package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

func apiContra(t *testing.T, origen http.Handler, o boletin.Opciones) *httptest.Server {
	t.Helper()
	sitio := httptest.NewServer(origen)
	t.Cleanup(sitio.Close)
	o.Base = sitio.URL
	o.Intervalo = time.Millisecond
	o.EsperaBase = time.Millisecond
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Nuevo(Config{
		Servicio: servicio.Nuevo(boletin.NuevoCliente(o), alm), PorMinuto: 0, Version: "test",
	}))
	t.Cleanup(srv.Close)
	return srv
}

type errorDeLimite struct {
	Error         string     `json:"error"`
	Detalle       string     `json:"detalle"`
	Origen        string     `json:"origen"`
	LimitadoHasta *time.Time `json:"limitado_hasta"`
}

func leerLimite(t *testing.T, res *http.Response, cuerpo []byte) errorDeLimite {
	t.Helper()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("codigo = %d, se esperaba 503: %s", res.StatusCode, cuerpo)
	}
	var e errorDeLimite
	if err := json.Unmarshal(cuerpo, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// Hay clientes que reconocen el límite del Boletín buscando «respondió 429» en
// el detalle, de cuando notarum pasaba el 429 adentro de un 502. Al cambiar el
// 502 por un 503 propio esa señal se perdía, y esos clientes seguían pidiendo.
// Tiene que estar tanto en el pedido que recibió el 429 como en los que no
// salieron por la pausa.
func TestEl503DiceQueElSitioRespondio429(t *testing.T) {
	var pedidos int32
	srv := apiContra(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}), boletin.Opciones{})

	for _, ruta := range []string{
		"/v1/avisos/primera/1/2026-09-01", // el que recibe el 429
		"/v1/avisos/primera/2/2026-09-01", // el que no sale, por la pausa
	} {
		antes := time.Now()
		res, cuerpo := pedir(t, srv, ruta)
		e := leerLimite(t, res, cuerpo)
		if !strings.Contains(e.Detalle, "respondió 429") {
			t.Errorf("%s: el detalle no dice «respondió 429»: %q", ruta, e.Detalle)
		}
		// La hora a la que tiene sentido volver, además de los segundos de
		// Retry-After: es lo mismo, sin tener que saber cuándo llegó la
		// respuesta.
		if e.LimitadoHasta == nil {
			t.Fatalf("%s: falta limitado_hasta: %s", ruta, cuerpo)
		}
		if falta := e.LimitadoHasta.Sub(antes); falta <= 0 || falta > 43*time.Second {
			t.Errorf("%s: limitado_hasta queda a %v, se esperaban hasta 42 segundos", ruta, falta)
		}
	}
	if n := atomic.LoadInt32(&pedidos); n != 1 {
		t.Errorf("pedidos al sitio = %d, se esperaba 1", n)
	}
}

// Si el que frena es el tope propio de notarum, el sitio no respondió ningún
// 429 y el detalle no lo puede decir. Quien pide tiene que esperar un poco, no
// dar al sitio por bloqueado.
func TestEl503DelTopePropioNoHablaDeUn429(t *testing.T) {
	sitio, _ := sitioFalso(t)
	srv := apiContra(t, sitio, boletin.Opciones{Tope: 1, Ventana: time.Hour, Rafaga: 1})

	if res, cuerpo := pedir(t, srv, "/v1/rubros/primera"); res.StatusCode != 200 {
		t.Fatalf("codigo = %d: %s", res.StatusCode, cuerpo)
	}
	res, cuerpo := pedir(t, srv, "/v1/calendario/2026/primera")
	e := leerLimite(t, res, cuerpo)
	if strings.Contains(e.Detalle, "respondió 429") {
		t.Errorf("el sitio no respondió 429 y el detalle dice que sí: %q", e.Detalle)
	}
	if res.Header.Get("Retry-After") == "" || e.LimitadoHasta == nil || !e.LimitadoHasta.After(time.Now()) {
		t.Errorf("igual tiene que decir cuándo volver: Retry-After = %q, limitado_hasta = %v",
			res.Header.Get("Retry-After"), e.LimitadoHasta)
	}
}
