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

func clienteDePrueba(t *testing.T, h http.Handler) *Cliente {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NuevoCliente(Opciones{
		Base:       srv.URL,
		Intervalo:  time.Millisecond,
		EsperaBase: time.Millisecond,
	})
}

// Un 302 a "/" es "no hubo edición", no un error.
func TestTraerEdicionSinEdicion(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	fecha, _ := ParseFecha("2026-08-17")
	_, err := c.TraerEdicion(context.Background(), Primera, fecha, "")
	if !errors.Is(err, ErrSinEdicion) {
		t.Fatalf("err = %v, se esperaba ErrSinEdicion", err)
	}
}

func TestTraerEdicionOK(t *testing.T) {
	cuerpo := fixture(t, "portada_primera_20260901.html")
	var pedidos int32
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		if r.URL.Path != "/seccion/primera/20260901" {
			t.Errorf("ruta pedida = %q", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("no se envió User-Agent")
		}
		w.Write(cuerpo)
	}))
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := c.TraerEdicion(context.Background(), Primera, fecha, "")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 100 {
		t.Errorf("cantidad = %d", ed.Cantidad)
	}
	if pedidos != 1 {
		t.Errorf("pedidos = %d, se esperaba 1", pedidos)
	}
}

func TestTraerEdicionPasaElRubro(t *testing.T) {
	cuerpo := fixture(t, "portada_tercera_rubro1566_20260901.html")
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("rubro"); got != "1566" {
			t.Errorf("rubro = %q, se esperaba 1566", got)
		}
		w.Write(cuerpo)
	}))
	fecha, _ := ParseFecha("2026-09-01")
	ed, err := c.TraerEdicion(context.Background(), Tercera, fecha, "1566")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 9 {
		t.Errorf("cantidad = %d, se esperaba 9", ed.Cantidad)
	}
}

// Ante un 5xx se reintenta; ante un 4xx no.
func TestReintentaSoloEn5xx(t *testing.T) {
	cuerpo := fixture(t, "calendario_primera_2026.json")
	var pedidos int32
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&pedidos, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write(cuerpo)
	}))
	if _, err := c.TraerCalendario(context.Background(), Primera, 2026); err != nil {
		t.Fatalf("se esperaba éxito tras reintentar: %v", err)
	}
	if pedidos != 3 {
		t.Errorf("pedidos = %d, se esperaban 3", pedidos)
	}

	var pedidos404 int32
	c2 := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos404, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c2.TraerCalendario(context.Background(), Primera, 2026)
	if err == nil {
		t.Fatal("se esperaba error ante un 404")
	}
	var es *ErrDelSitio
	if !errors.As(err, &es) || es.Codigo != 404 {
		t.Errorf("err = %v, se esperaba ErrDelSitio con código 404", err)
	}
	if pedidos404 != 1 {
		t.Errorf("pedidos = %d: un 404 no se reintenta", pedidos404)
	}
}

// El ritmo es global al cliente, no por goroutine.
func TestRespetaElIntervalo(t *testing.T) {
	cuerpo := fixture(t, "rubros_primera.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	defer srv.Close()
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: 60 * time.Millisecond})

	inicio := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
			t.Fatal(err)
		}
	}
	// Tres pedidos con 60 ms de separación no pueden tardar menos de 120 ms.
	if d := time.Since(inicio); d < 120*time.Millisecond {
		t.Errorf("tres pedidos tardaron %v: no se respetó el intervalo", d)
	}
}

func TestUnErrorDelSitioSeIdentificaComoTal(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>otra cosa</body></html>"))
	}))
	fecha, _ := ParseFecha("2026-09-01")
	_, err := c.TraerAviso(context.Background(), Primera, "1", fecha)
	var es *ErrDelSitio
	if !errors.As(err, &es) {
		t.Fatalf("err = %v, se esperaba ErrDelSitio", err)
	}
}

func TestMetricasCuentanLecturas(t *testing.T) {
	cuerpo := fixture(t, "rubros_primera.json")
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(cuerpo)
	}))
	if _, err := c.TraerRubros(context.Background(), Primera); err != nil {
		t.Fatal(err)
	}
	m := c.Metricas()
	if m.Lecturas != 1 || m.Errores != 0 || !m.SitioResponde {
		t.Errorf("metricas = %+v", m)
	}
	if m.UltimaLectura == nil {
		t.Error("ultima_lectura vacía")
	}
}

func TestBuscarArmaElCuerpoQueEsperaElSitio(t *testing.T) {
	cuerpo := fixture(t, "busqueda_primera.json")
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("método = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		params := r.PostForm.Get("params")
		for _, esperado := range []string{`"fechaDesde":"01/09/2026"`, `"seccion":["1"]`, `"texto":"decreto"`} {
			if !contiene(params, esperado) {
				t.Errorf("params no contiene %s\nparams = %s", esperado, params)
			}
		}
		w.Write(cuerpo)
	}))
	desde, _ := ParseFecha("2026-09-01")
	hasta, _ := ParseFecha("2026-09-03")
	res, err := c.Buscar(context.Background(), ConsultaBusqueda{
		Texto: "decreto", Seccion: Primera, Desde: desde, Hasta: hasta,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cantidad == 0 {
		t.Error("la búsqueda no devolvió avisos")
	}
}

func TestTraerAnexoDecodificaBase64(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// "%PDF-1.4" en base64
		w.Write([]byte(`{"pdfBase64":"JVBERi0xLjQ="}`))
	}))
	fecha, _ := ParseFecha("2026-09-01")
	pdf, err := c.TraerAnexo(context.Background(), Primera, "1", "7756488", fecha)
	if err != nil {
		t.Fatal(err)
	}
	if string(pdf) != "%PDF-1.4" {
		t.Errorf("pdf = %q", pdf)
	}
}

func contiene(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
