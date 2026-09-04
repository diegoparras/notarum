package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

func fixture(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "boletin", "testdata", nombre))
	if err != nil {
		t.Fatalf("no se pudo leer el fixture %s: %v", nombre, err)
	}
	return b
}

// sitioFalso responde como el Boletín para las rutas que usamos.
func sitioFalso(t *testing.T) (http.Handler, *int32) {
	t.Helper()
	var pedidos int32
	portada := fixture(t, "portada_primera_20260901.html")
	detalle := fixture(t, "detalle_primera_346633.html")
	cal := fixture(t, "calendario_primera_2026.json")
	rubros := fixture(t, "rubros_primera.json")
	busq := fixture(t, "busqueda_primera.json")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pedidos, 1)
		p := r.URL.Path
		switch {
		case p == "/seccion/primera/20260901":
			w.Write(portada)
		case p == "/seccion/primera/20260817":
			http.Redirect(w, r, "/", http.StatusFound)
		case strings.HasPrefix(p, "/detalleAviso/"):
			w.Write(detalle)
		case strings.HasPrefix(p, "/calendario/"):
			w.Write(cal)
		case strings.HasSuffix(p, "/rubros"):
			w.Write(rubros)
		case p == "/busquedaAvanzada/realizarBusqueda":
			w.Write(busq)
		case p == "/pdf/download_anexo":
			w.Write([]byte(`{"pdfBase64":"JVBERi0xLjQ="}`))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}), &pedidos
}

func servidorDePrueba(t *testing.T) *httptest.Server {
	t.Helper()
	h, _ := sitioFalso(t)
	origen := httptest.NewServer(h)
	t.Cleanup(origen.Close)

	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
	})
	c, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := Nuevo(Config{Servicio: servicio.Nuevo(cli, c), PorMinuto: 0, Version: "test"})
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return srv
}

func pedir(t *testing.T, srv *httptest.Server, ruta string) (*http.Response, []byte) {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + ruta)
	if err != nil {
		t.Fatalf("GET %s: %v", ruta, err)
	}
	defer res.Body.Close()
	cuerpo := make([]byte, 0)
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		cuerpo = append(cuerpo, buf[:n]...)
		if err != nil {
			break
		}
	}
	return res, cuerpo
}

func TestEdicionOK(t *testing.T) {
	srv := servidorDePrueba(t)
	res, cuerpo := pedir(t, srv, "/v1/ediciones/primera/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d, cuerpo = %s", res.StatusCode, cuerpo)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	var ed boletin.Edicion
	if err := json.Unmarshal(cuerpo, &ed); err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 100 || len(ed.Avisos) != 100 {
		t.Errorf("cantidad = %d, avisos = %d", ed.Cantidad, len(ed.Avisos))
	}
	if ed.Fecha.API() != "2026-09-01" {
		t.Errorf("fecha = %s", ed.Fecha.API())
	}
	// Una edición pasada se puede cachear para siempre.
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "31536000") {
		t.Errorf("cache-control = %q", cc)
	}
	if res.Header.Get("ETag") == "" {
		t.Error("falta el ETag")
	}
}

func TestETagDevuelve304(t *testing.T) {
	srv := servidorDePrueba(t)
	res, _ := pedir(t, srv, "/v1/ediciones/primera/2026-09-01")
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("falta el ETag")
	}
	req, _ := http.NewRequest("GET", srv.URL+"/v1/ediciones/primera/2026-09-01", nil)
	req.Header.Set("If-None-Match", etag)
	res2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusNotModified {
		t.Errorf("codigo = %d, se esperaba 304", res2.StatusCode)
	}
}

func TestEdicionFiltradaPorRubro(t *testing.T) {
	srv := servidorDePrueba(t)
	_, cuerpo := pedir(t, srv, "/v1/ediciones/primera/2026-09-01?rubro=DECRETOS")
	var ed boletin.Edicion
	if err := json.Unmarshal(cuerpo, &ed); err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad == 0 || ed.Cantidad == 100 {
		t.Errorf("cantidad filtrada = %d", ed.Cantidad)
	}
	for _, a := range ed.Avisos {
		if a.Rubro != "DECRETOS" {
			t.Errorf("se coló %q", a.Rubro)
		}
	}
}

// Un día sin edición es un 404 explícito, no un error del servicio.
func TestSinEdicionEs404Explicito(t *testing.T) {
	srv := servidorDePrueba(t)
	res, cuerpo := pedir(t, srv, "/v1/ediciones/primera/2026-08-17")
	if res.StatusCode != 404 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	var e RespuestaError
	if err := json.Unmarshal(cuerpo, &e); err != nil {
		t.Fatal(err)
	}
	if !e.SinEdicion {
		t.Error("falta sin_edicion: true")
	}
	if e.Origen != OrigenSitio {
		t.Errorf("origen = %q", e.Origen)
	}
}

func TestParametrosInvalidos(t *testing.T) {
	srv := servidorDePrueba(t)
	casos := []struct {
		ruta   string
		codigo int
	}{
		{"/v1/ediciones/cuarta/2026-09-01", 400},
		{"/v1/ediciones/primera/01-09-2026", 400},
		{"/v1/ediciones/primera/2026-13-45", 400},
		{"/v1/calendario/1800/primera", 400},
		{"/v1/calendario/abc/primera", 400},
		{"/v1/ediciones/primera", 400},
		{"/v1/ediciones/primera?desde=2026-09-04&hasta=2026-09-01", 400},
		{"/v1/buscar?seccion=primera", 400},
		{"/v1/no/existe", 404},
	}
	for _, c := range casos {
		t.Run(c.ruta, func(t *testing.T) {
			res, cuerpo := pedir(t, srv, c.ruta)
			if res.StatusCode != c.codigo {
				t.Errorf("codigo = %d, se esperaba %d (cuerpo: %s)", res.StatusCode, c.codigo, cuerpo)
			}
			var e RespuestaError
			if err := json.Unmarshal(cuerpo, &e); err != nil {
				t.Fatalf("el error no vino como JSON: %s", cuerpo)
			}
			if e.Origen == "" {
				t.Error("el error no dice de quién es la culpa")
			}
		})
	}
}

func TestAvisoOK(t *testing.T) {
	srv := servidorDePrueba(t)
	res, cuerpo := pedir(t, srv, "/v1/avisos/primera/346633/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d: %s", res.StatusCode, cuerpo)
	}
	var d boletin.Detalle
	if err := json.Unmarshal(cuerpo, &d); err != nil {
		t.Fatal(err)
	}
	if d.Texto == "" || d.HTML == "" {
		t.Error("el aviso vino sin texto o sin html")
	}
	if len(d.Anexos) == 0 {
		t.Fatal("el aviso vino sin anexos")
	}
	if !strings.HasPrefix(d.Anexos[0].URL, "/v1/anexos/") {
		t.Errorf("la url del anexo no apunta a esta API: %q", d.Anexos[0].URL)
	}
}

func TestAnexoDevuelvePDF(t *testing.T) {
	srv := servidorDePrueba(t)
	res, cuerpo := pedir(t, srv, "/v1/anexos/primera/12/7756488/20260901.pdf")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d: %s", res.StatusCode, cuerpo)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.HasPrefix(string(cuerpo), "%PDF") {
		t.Errorf("no parece un PDF: %q", cuerpo)
	}
}

func TestCalendarioRubrosSeccionesSalud(t *testing.T) {
	srv := servidorDePrueba(t)

	res, cuerpo := pedir(t, srv, "/v1/calendario/2026/primera")
	if res.StatusCode != 200 {
		t.Fatalf("calendario: %d", res.StatusCode)
	}
	var cal boletin.Calendario
	if err := json.Unmarshal(cuerpo, &cal); err != nil || len(cal.Fechas) == 0 {
		t.Errorf("calendario mal: %v (%d fechas)", err, len(cal.Fechas))
	}

	res, cuerpo = pedir(t, srv, "/v1/rubros/primera")
	if res.StatusCode != 200 {
		t.Fatalf("rubros: %d", res.StatusCode)
	}
	var rs []boletin.Rubro
	if err := json.Unmarshal(cuerpo, &rs); err != nil || len(rs) == 0 {
		t.Errorf("rubros mal: %v", err)
	}

	res, _ = pedir(t, srv, "/v1/secciones")
	if res.StatusCode != 200 {
		t.Errorf("secciones: %d", res.StatusCode)
	}

	res, cuerpo = pedir(t, srv, "/v1/salud")
	if res.StatusCode != 200 {
		t.Fatalf("salud: %d", res.StatusCode)
	}
	var salud Salud
	if err := json.Unmarshal(cuerpo, &salud); err != nil {
		t.Fatal(err)
	}
	if !salud.OK {
		t.Error("salud.ok = false")
	}
}

func TestBuscar(t *testing.T) {
	srv := servidorDePrueba(t)
	res, cuerpo := pedir(t, srv, "/v1/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-03")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d: %s", res.StatusCode, cuerpo)
	}
	var r servicio.Busqueda
	if err := json.Unmarshal(cuerpo, &r); err != nil {
		t.Fatal(err)
	}
	if r.Total == 0 {
		t.Error("la búsqueda no devolvió avisos")
	}
	// Sin motor sqlite no hay índice: la búsqueda tiene que ir al sitio.
	if r.Fuente != servicio.FuenteSitio {
		t.Errorf("fuente = %q, se esperaba sitio", r.Fuente)
	}
}

func TestRangoListaFaltantes(t *testing.T) {
	srv := servidorDePrueba(t)
	res, cuerpo := pedir(t, srv, "/v1/ediciones/primera?desde=2026-09-01&hasta=2026-09-10")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d: %s", res.StatusCode, cuerpo)
	}
	var rango servicio.Rango
	if err := json.Unmarshal(cuerpo, &rango); err != nil {
		t.Fatal(err)
	}
	if len(rango.Faltantes) == 0 {
		t.Error("sin caché debería haber faltantes")
	}
}

// El índice de la API vive en /v1/: la raíz es del lector web.
func TestIndiceYOpenAPI(t *testing.T) {
	srv := servidorDePrueba(t)
	for _, ruta := range []string{"/v1/", "/v1/openapi.json"} {
		res, cuerpo := pedir(t, srv, ruta)
		if res.StatusCode != 200 {
			t.Errorf("%s: codigo = %d", ruta, res.StatusCode)
		}
		var v any
		if err := json.Unmarshal(cuerpo, &v); err != nil {
			t.Errorf("%s: no devolvió JSON válido: %v", ruta, err)
		}
	}
}

func TestCORSyOptions(t *testing.T) {
	srv := servidorDePrueba(t)
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/v1/secciones", nil)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("codigo = %d", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("falta el CORS abierto")
	}
}

func TestLimitePorIP(t *testing.T) {
	h, _ := sitioFalso(t)
	origen := httptest.NewServer(h)
	defer origen.Close()
	cli := boletin.NuevoCliente(boletin.Opciones{Base: origen.URL, Intervalo: time.Millisecond})
	c, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := Nuevo(Config{Servicio: servicio.Nuevo(cli, c), PorMinuto: 3})
	srv := httptest.NewServer(api)
	defer srv.Close()

	var limitados int
	for i := 0; i < 6; i++ {
		res, _ := pedir(t, srv, "/v1/secciones")
		if res.StatusCode == http.StatusTooManyRequests {
			limitados++
		}
	}
	if limitados == 0 {
		t.Error("con 3 pedidos por minuto, 6 pedidos deberían chocar con el límite")
	}
	// El chequeo de salud nunca se limita.
	res, _ := pedir(t, srv, "/v1/salud")
	if res.StatusCode != 200 {
		t.Errorf("salud fue limitada: %d", res.StatusCode)
	}
}
