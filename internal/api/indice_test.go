package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// servidorConIndice levanta la API con el motor sqlite, que sí indexa.
func servidorConIndice(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	h, _ := sitioFalso(t)
	var pedidos int32
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(origen.Close)

	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
	})
	db, err := almacen.NuevoSQLite(filepath.Join(t.TempDir(), "notarum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Cerrar() })

	api := Nuevo(Config{Servicio: servicio.Nuevo(cli, db), PorMinuto: 0, Version: "test"})
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return srv, &pedidos
}

// Leer una edición la deja indexada, y buscarla después no le pide nada al
// Boletín: ese es todo el punto del índice local.
func TestBusquedaLocalNoTocaElBoletin(t *testing.T) {
	srv, pedidos := servidorConIndice(t)

	if res, _ := pedir(t, srv, "/v1/ediciones/primera/2026-09-01"); res.StatusCode != 200 {
		t.Fatalf("no se pudo leer la edición: %d", res.StatusCode)
	}
	trasLeer := atomic.LoadInt32(pedidos)

	res, cuerpo := pedir(t, srv,
		"/v1/buscar?seccion=primera&texto=economia&desde=2026-09-01&hasta=2026-09-01&fuente=indice")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d: %s", res.StatusCode, cuerpo)
	}
	var b servicio.Busqueda
	if err := json.Unmarshal(cuerpo, &b); err != nil {
		t.Fatal(err)
	}
	if b.Fuente != servicio.FuenteIndice {
		t.Errorf("fuente = %q, se esperaba indice", b.Fuente)
	}
	if atomic.LoadInt32(pedidos) != trasLeer {
		t.Errorf("la búsqueda local le pidió %d cosas al Boletín", atomic.LoadInt32(pedidos)-trasLeer)
	}
	if b.Total == 0 {
		t.Error("no encontro los avisos del Ministerio de Economia, que estan en la edicion indexada")
	}
	for _, a := range b.Avisos {
		if a.Seccion != boletin.Primera || a.Fecha.API() != "2026-09-01" {
			t.Errorf("aviso fuera de lo pedido: %+v", a)
		}
	}
}

// Sin acentos también tiene que encontrar: nadie escribe "economía" con tilde
// en una caja de búsqueda.
func TestBusquedaLocalSinAcentos(t *testing.T) {
	srv, _ := servidorConIndice(t)
	pedir(t, srv, "/v1/ediciones/primera/2026-09-01")

	var conAcento, sinAcento servicio.Busqueda
	_, c1 := pedir(t, srv, "/v1/buscar?seccion=primera&texto=econom%C3%ADa&desde=2026-09-01&hasta=2026-09-01&fuente=indice")
	_, c2 := pedir(t, srv, "/v1/buscar?seccion=primera&texto=economia&desde=2026-09-01&hasta=2026-09-01&fuente=indice")
	json.Unmarshal(c1, &conAcento)
	json.Unmarshal(c2, &sinAcento)
	if conAcento.Total != sinAcento.Total || conAcento.Total == 0 {
		t.Errorf("con acento = %d, sin acento = %d", conAcento.Total, sinAcento.Total)
	}
}

// Con fuente=auto y el índice vacío, hay que caer al sitio y decirlo.
func TestFuenteAutoCaeAlSitioSinCobertura(t *testing.T) {
	srv, _ := servidorConIndice(t)
	_, cuerpo := pedir(t, srv, "/v1/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-03")
	var b servicio.Busqueda
	if err := json.Unmarshal(cuerpo, &b); err != nil {
		t.Fatal(err)
	}
	if b.Fuente != servicio.FuenteSitio {
		t.Errorf("fuente = %q: sin nada indexado hay que ir al sitio", b.Fuente)
	}
}

// Y con historia indexada, auto tiene que preferir el índice.
func TestFuenteAutoPrefiereElIndice(t *testing.T) {
	srv, _ := servidorConIndice(t)
	pedir(t, srv, "/v1/ediciones/primera/2026-09-01")

	_, cuerpo := pedir(t, srv, "/v1/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-01")
	var b servicio.Busqueda
	if err := json.Unmarshal(cuerpo, &b); err != nil {
		t.Fatal(err)
	}
	if b.Fuente != servicio.FuenteIndice {
		t.Errorf("fuente = %q, se esperaba indice", b.Fuente)
	}
	if b.DiasIndexados != 1 {
		t.Errorf("dias_indexados = %d, se esperaba 1", b.DiasIndexados)
	}
}

// Pedir el índice en una instancia de disco tiene que explicar cómo activarlo.
func TestFuenteIndiceSinIndiceExplicaComoActivarlo(t *testing.T) {
	srv := servidorDePrueba(t)
	res, cuerpo := pedir(t, srv,
		"/v1/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-03&fuente=indice")
	if res.StatusCode != 400 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	var e RespuestaError
	if err := json.Unmarshal(cuerpo, &e); err != nil {
		t.Fatal(err)
	}
	if e.Origen != OrigenPedido {
		t.Errorf("origen = %q", e.Origen)
	}
	if !contiene(e.Detalle, "sqlite") {
		t.Errorf("el error no dice cómo activarlo: %q", e.Detalle)
	}
}

func TestFuenteInvalida(t *testing.T) {
	srv := servidorDePrueba(t)
	res, _ := pedir(t, srv,
		"/v1/buscar?seccion=primera&desde=2026-09-01&hasta=2026-09-03&fuente=telepatia")
	if res.StatusCode != 400 {
		t.Errorf("codigo = %d, se esperaba 400", res.StatusCode)
	}
}

// La salud tiene que decir qué motor está activo.
func TestSaludInformaElMotor(t *testing.T) {
	srv, _ := servidorConIndice(t)
	pedir(t, srv, "/v1/ediciones/primera/2026-09-01")

	_, cuerpo := pedir(t, srv, "/v1/salud")
	var salud struct {
		Cache struct {
			Motor  string `json:"motor"`
			Avisos int64  `json:"avisos"`
		} `json:"cache"`
	}
	if err := json.Unmarshal(cuerpo, &salud); err != nil {
		t.Fatal(err)
	}
	if salud.Cache.Motor != "sqlite" {
		t.Errorf("motor = %q", salud.Cache.Motor)
	}
	if salud.Cache.Avisos != 100 {
		t.Errorf("avisos indexados = %d, se esperaban 100", salud.Cache.Avisos)
	}
}

func contiene(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
