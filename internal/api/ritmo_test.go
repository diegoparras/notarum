package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// Cuando el Boletín limita los pedidos, quien consulta la API se entera
// enseguida y sabe cuándo volver: 503 con Retry-After, no un 502 genérico ni
// una conexión colgada un minuto.
func TestUn429DelSitioSeAvisaConRetryAfter(t *testing.T) {
	var pedidos int
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pedidos++
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(origen.Close)

	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
	})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Nuevo(Config{Servicio: servicio.Nuevo(cli, alm), PorMinuto: 0, Version: "test"}))
	t.Cleanup(srv.Close)

	inicio := time.Now()
	res, cuerpo := pedir(t, srv, "/v1/ediciones/segunda/2026-09-01")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("codigo = %d, se esperaba 503: %s", res.StatusCode, cuerpo)
	}
	if ra := res.Header.Get("Retry-After"); ra != "42" {
		t.Errorf("Retry-After = %q, se esperaba 42, lo que pidió el sitio", ra)
	}
	var e RespuestaError
	if err := json.Unmarshal(cuerpo, &e); err != nil {
		t.Fatal(err)
	}
	if e.Origen != OrigenSitio || e.Error == "" {
		t.Errorf("error = %+v", e)
	}
	if d := time.Since(inicio); d > 5*time.Second {
		t.Errorf("tardó %v en avisar", d)
	}

	// Mientras dura la pausa, el siguiente ni le pega al sitio.
	res, _ = pedir(t, srv, "/v1/ediciones/primera/2026-09-01")
	if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("Retry-After") == "" {
		t.Errorf("durante la pausa: codigo = %d, Retry-After = %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
	if pedidos != 1 {
		t.Errorf("pedidos al sitio = %d: durante la pausa no tiene que salir ninguno", pedidos)
	}

	// Y /v1/salud lo muestra.
	_, cuerpo = pedir(t, srv, "/v1/salud")
	var salud Salud
	if err := json.Unmarshal(cuerpo, &salud); err != nil {
		t.Fatal(err)
	}
	if salud.Sitio.Limitados != 1 || salud.Sitio.EnPausaHasta == nil {
		t.Errorf("salud.sitio = %+v: tiene que contar el 429 y decir hasta cuándo es la pausa", salud.Sitio)
	}
}
