package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func sitioDePrueba(t *testing.T) *httptest.Server {
	t.Helper()
	portada := fixture(t, "portada_primera_20260901.html")
	detalle := fixture(t, "detalle_primera_346633.html")
	cal := fixture(t, "calendario_primera_2026.json")
	busq := fixture(t, "busqueda_primera.json")

	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		case p == "/busquedaAvanzada/realizarBusqueda":
			w.Write(busq)
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(origen.Close)

	cli := boletin.NuevoCliente(boletin.Opciones{
		Base: origen.URL, Intervalo: time.Millisecond, EsperaBase: time.Millisecond,
	})
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sitio, err := Nuevo(servicio.Nuevo(cli, alm), "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv
}

func pedir(t *testing.T, srv *httptest.Server, ruta string) (*http.Response, string) {
	t.Helper()
	// Sin seguir redirecciones: hay que poder mirarlas.
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := cli.Get(srv.URL + ruta)
	if err != nil {
		t.Fatalf("GET %s: %v", ruta, err)
	}
	defer res.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res, sb.String()
}

// Las plantillas tienen que compilar; si no, el sitio no arranca.
func TestPlantillasCompilan(t *testing.T) {
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Nuevo(servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm), "test"); err != nil {
		t.Fatalf("las plantillas no compilan: %v", err)
	}
}

func TestEdicionSeDibuja(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/ed/primera/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	for _, esperado := range []string{
		"Martes 1 de septiembre de 2026", // fecha en castellano, sólo la inicial en mayúscula
		"PODER EJECUTIVO",                // el primer aviso
		"Decreto 845/2026",
		"DECRETOS",            // el rubro
		"/av/primera/346633/", // enlace al aviso
		"notarum",             // la marca
		"estilo.css",          // la hoja de estilo
	} {
		if !strings.Contains(html, esperado) {
			t.Errorf("la página no contiene %q", esperado)
		}
	}
	// El aviso con anexos se marca.
	if !strings.Contains(html, "anexos") {
		t.Error("no se marcó ningún aviso con anexos")
	}
}

func TestEdicionFiltradaPorRubro(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/ed/primera/2026-09-01?rubro=DECRETOS")
	if !strings.Contains(html, "rubro-pastilla activa") {
		t.Error("no se marcó el rubro activo")
	}
	if strings.Contains(html, "RESOLUCIONES SINTETIZADAS</h2>") {
		t.Error("con el filtro de DECRETOS aparecieron avisos de otro rubro")
	}
}

// Un feriado se explica, no se muestra como error.
func TestSinEdicionSeExplica(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/ed/primera/2026-08-17")
	if res.StatusCode != 200 {
		t.Errorf("codigo = %d: un feriado no es un error", res.StatusCode)
	}
	if !strings.Contains(html, "No hubo edición este día") {
		t.Error("no se explicó que no hubo edición")
	}
	if !strings.Contains(html, "/calendario/primera/2026") {
		t.Error("no se ofreció el calendario como salida")
	}
}

func TestAvisoSeDibuja(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/av/primera/346633/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, esperado := range []string{
		"PODER EJECUTIVO",
		"Decreto 845/2026",
		"Anexo - 1", // los anexos, con enlace propio
		"/v1/anexos/primera/1/7756488/2026-09-01.pdf",
		"boletinoficial.gob.ar", // el enlace al original
	} {
		if !strings.Contains(html, esperado) {
			t.Errorf("la página no contiene %q", esperado)
		}
	}
	// El cuerpo del aviso llega saneado: sin scripts ni estilos del origen.
	if strings.Contains(html, "<script") || strings.Contains(html, "<style") {
		t.Error("el cuerpo del aviso trajo script o style")
	}
}

func TestCalendarioSeDibuja(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/calendario/primera/2026")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, esperado := range []string{"enero", "diciembre", "días con edición", "dia hay"} {
		if !strings.Contains(html, esperado) {
			t.Errorf("el calendario no contiene %q", esperado)
		}
	}
	// El feriado del 17/8 no puede ser un día con edición.
	if strings.Contains(html, `href="/ed/primera/2026-08-17"`) {
		t.Error("el 17/8/2026 es feriado y aparece como día con edición")
	}
}

func TestBuscarSinTextoMuestraElFormulario(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/buscar")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(html, `name="texto"`) {
		t.Error("no está el campo de búsqueda")
	}
	if strings.Contains(html, "resultado") {
		t.Error("sin texto no debería buscar nada")
	}
}

func TestBuscarConTexto(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-03")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(html, "de la búsqueda del Boletín Oficial") {
		t.Error("no se dijo de dónde salieron los resultados")
	}
	if !strings.Contains(html, "/av/primera/") {
		t.Error("no hay enlaces a los avisos encontrados")
	}
}

func TestBuscarRangoAlReves(t *testing.T) {
	srv := sitioDePrueba(t)
	res, html := pedir(t, srv, "/buscar?seccion=primera&texto=x&desde=2026-09-05&hasta=2026-09-01")
	if res.StatusCode != 400 {
		t.Errorf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(html, "al revés") {
		t.Errorf("no se explicó el problema: %s", html)
	}
}

// La raíz lleva a la última edición publicada, no a un día vacío.
func TestInicioLlevaALaUltimaEdicion(t *testing.T) {
	srv := sitioDePrueba(t)
	res, _ := pedir(t, srv, "/")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("codigo = %d, se esperaba una redirección", res.StatusCode)
	}
	destino := res.Header.Get("Location")
	if !strings.HasPrefix(destino, "/ed/primera/") {
		t.Errorf("destino = %q", destino)
	}
}

func TestFormulariosNavegan(t *testing.T) {
	srv := sitioDePrueba(t)
	res, _ := pedir(t, srv, "/ir?seccion=tercera&fecha=2026-09-01")
	if res.Header.Get("Location") != "/ed/tercera/2026-09-01" {
		t.Errorf("destino = %q", res.Header.Get("Location"))
	}
	res, _ = pedir(t, srv, "/ir-calendario?seccion=segunda&anio=2025")
	if res.Header.Get("Location") != "/calendario/segunda/2025" {
		t.Errorf("destino = %q", res.Header.Get("Location"))
	}
	// Con basura, en vez de romper, va a algo razonable.
	res, _ = pedir(t, srv, "/ir?seccion=inventada&fecha=no-es-fecha")
	if !strings.HasPrefix(res.Header.Get("Location"), "/ed/primera/") {
		t.Errorf("destino = %q", res.Header.Get("Location"))
	}
}

func TestRutasInvalidas(t *testing.T) {
	srv := sitioDePrueba(t)
	casos := []struct {
		ruta   string
		codigo int
	}{
		{"/ed/cuarta/2026-09-01", 404},
		{"/ed/primera/01-09-2026", 404},
		{"/calendario/primera/1800", 404},
		{"/av/cuarta/1/2026-09-01", 404},
	}
	for _, c := range casos {
		t.Run(c.ruta, func(t *testing.T) {
			res, html := pedir(t, srv, c.ruta)
			if res.StatusCode != c.codigo {
				t.Errorf("codigo = %d, se esperaba %d", res.StatusCode, c.codigo)
			}
			// Aun el error se dibuja como página, no como texto pelado.
			if !strings.Contains(html, "volver al inicio") {
				t.Error("la página de error no ofrece salida")
			}
		})
	}
}

func TestEstaticoSeSirve(t *testing.T) {
	srv := sitioDePrueba(t)
	res, css := pedir(t, srv, "/estatico/estilo.css")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(css, "--crema") || !strings.Contains(css, "--acento") {
		t.Error("la hoja de estilo no trae las variables del sistema Escriba")
	}
}

func TestFechaLarga(t *testing.T) {
	casos := map[string]string{
		"2026-09-01": "martes 1 de septiembre de 2026",
		"2026-08-17": "lunes 17 de agosto de 2026",
		"2025-03-10": "lunes 10 de marzo de 2025",
	}
	for entrada, esperado := range casos {
		f, err := boletin.ParseFecha(entrada)
		if err != nil {
			t.Fatal(err)
		}
		if got := fechaLarga(f); got != esperado {
			t.Errorf("fechaLarga(%s) = %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

// Los días de relleno antes del primero del mes van vacíos, no en cero.
func TestCalendarioNoMuestraCeros(t *testing.T) {
	srv := sitioDePrueba(t)
	_, html := pedir(t, srv, "/calendario/primera/2026")
	if strings.Contains(html, `<span class="dia">0</span>`) {
		t.Error("los días de relleno se dibujan como 0")
	}
}
