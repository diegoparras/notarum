package infoleg

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clienteDePrueba(t *testing.T, h http.Handler) *Cliente {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NuevoCliente(Opciones{
		Base: srv.URL, BaseDatos: srv.URL, Intervalo: time.Millisecond,
	})
}

// El HTML de InfoLEG viene en ISO-8859-1: si no se convierte, los acentos
// llegan rotos al lector.
func TestTraerTextoConvierteLaCodificacion(t *testing.T) {
	// "Cámara de Diputados de la Nación" tal como lo manda el sitio.
	crudo := []byte("<html><body><p>El Senado y C\xe1mara de Diputados de la Naci\xf3n</p></body></html>")
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anexos/400000-404999/401266/norma.htm" {
			t.Errorf("ruta pedida = %q", r.URL.Path)
		}
		w.Write(crudo)
	}))

	texto, err := c.TraerTexto(context.Background(), 401266)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(texto.Texto, "Cámara de Diputados de la Nación") {
		t.Errorf("los acentos llegaron rotos: %q", texto.Texto)
	}
	if texto.ID != 401266 {
		t.Errorf("id = %d", texto.ID)
	}
}

// Más de la mitad del catálogo no tiene texto publicado: el sitio redirige y
// eso no es una falla.
func TestTraerTextoSinTextoPublicado(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/infolegInternet/mostrarArchivoInexistente.do", http.StatusFound)
	}))
	if _, err := c.TraerTexto(context.Background(), 429014); !errors.Is(err, ErrSinTexto) {
		t.Fatalf("err = %v, se esperaba ErrSinTexto", err)
	}
}

// Una página que vuelve vacía tampoco es texto.
func TestTraerTextoVacio(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>   </body></html>"))
	}))
	if _, err := c.TraerTexto(context.Background(), 1); !errors.Is(err, ErrSinTexto) {
		t.Errorf("err = %v, se esperaba ErrSinTexto", err)
	}
}

func TestTraerTextoSaneaYConservaTablas(t *testing.T) {
	crudo := []byte(`<html><body>
		<style>td{border:1px}</style>
		<script>malicioso()</script>
		<p>VISTO el Expediente</p>
		<table><tr><td>Cargo</td><td>Nombre</td></tr></table>
	</body></html>`)
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(crudo)
	}))
	texto, err := c.TraerTexto(context.Background(), 401266)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibido := range []string{"<script", "<style", "malicioso", "border:1px"} {
		if strings.Contains(texto.HTML, prohibido) {
			t.Errorf("quedó %q en el html", prohibido)
		}
	}
	if !strings.Contains(texto.HTML, "<table") {
		t.Error("se perdió la tabla, que en un texto legal es contenido")
	}
	if !strings.Contains(texto.Texto, "VISTO el Expediente") {
		t.Errorf("texto plano = %q", texto.Texto)
	}
}

func TestTraerTextoIDInvalido(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, id := range []int{0, -1} {
		if _, err := c.TraerTexto(context.Background(), id); err == nil {
			t.Errorf("se aceptó el id %d", id)
		}
	}
}

func TestTraerTextoErrorDelSitio(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, err := c.TraerTexto(context.Background(), 401266)
	var es *ErrDelSitio
	if !errors.As(err, &es) {
		t.Fatalf("err = %v, se esperaba ErrDelSitio", err)
	}
	if es.Codigo != 500 {
		t.Errorf("codigo = %d", es.Codigo)
	}
}

// respuestaCKAN arma lo que devuelve el portal de datos.
const respuestaCKAN = `{
  "success": true,
  "result": {
    "metadata_modified": "2026-09-01T14:00:03.159085",
    "resources": [
      {"name": "Base Infoleg Normativa Nacional - Muestreo", "format": "CSV", "url": "https://x/muestra.csv"},
      {"name": "Base Complementaria Infoleg de Normas Modificadas", "format": "ZIP", "url": "https://x/comp.zip"},
      {"name": "Base Infoleg Normativa Nacional", "format": "ZIP", "url": "https://x/base.zip", "size": 49616300}
    ]
  }
}`

// La URL del catálogo cambia con cada publicación: se pregunta, no se escribe
// a mano. Y hay que elegir el ZIP de la base, no el de las complementarias.
func TestBuscarCatalogo(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, DatasetCatalogo) {
			t.Errorf("consulta = %q", r.URL.RawQuery)
		}
		w.Write([]byte(respuestaCKAN))
	}))
	info, err := c.BuscarCatalogo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != "https://x/base.zip" {
		t.Errorf("url = %q: se eligió el recurso equivocado", info.URL)
	}
	if info.Bytes != 49616300 {
		t.Errorf("bytes = %d", info.Bytes)
	}
	if info.Actualizado.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("actualizado = %v", info.Actualizado)
	}
}

func TestBuscarCatalogoSinZIP(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":{"resources":[{"name":"x","format":"CSV","url":"u"}]}}`))
	}))
	if _, err := c.BuscarCatalogo(context.Background()); err == nil {
		t.Error("se aceptó un dataset sin el ZIP de la base")
	}
}

func TestBuscarCatalogoDatasetDesconocido(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false}`))
	}))
	if _, err := c.BuscarCatalogo(context.Background()); err == nil {
		t.Error("se aceptó una respuesta que dice que falló")
	}
}

// zipDePrueba arma un ZIP con un CSV adentro, como el del portal.
func zipDePrueba(t *testing.T, contenido string) []byte {
	t.Helper()
	var sb strings.Builder
	z := zip.NewWriter(&sbWriter{&sb})
	f, err := z.Create("base-infoleg-normativa-nacional.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(contenido)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(sb.String())
}

type sbWriter struct{ sb *strings.Builder }

func (w *sbWriter) Write(p []byte) (int, error) { return w.sb.Write(p) }

func TestDescargarYAbrirCatalogo(t *testing.T) {
	csv := "id_norma,tipo_norma,numero_norma\n401266,Ley,27742\n"
	datos := zipDePrueba(t, csv)

	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(datos)
	}))
	destino := filepath.Join(t.TempDir(), "catalogo.zip")
	if err := c.DescargarCatalogo(context.Background(), c.baseDatos+"/base.zip", destino); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destino)
	if err != nil || info.Size() == 0 {
		t.Fatalf("el archivo no se escribió: %v", err)
	}

	lector, err := AbrirCatalogo(destino)
	if err != nil {
		t.Fatal(err)
	}
	defer lector.Close()
	leido, err := io.ReadAll(lector)
	if err != nil {
		t.Fatal(err)
	}
	if string(leido) != csv {
		t.Errorf("csv = %q", leido)
	}

	// Y el catálogo se puede leer de punta a punta desde el ZIP.
	lector2, err := AbrirCatalogo(destino)
	if err != nil {
		t.Fatal(err)
	}
	defer lector2.Close()
	var normas []Norma
	if _, err := LeerCatalogo(lector2, func(n Norma) error {
		normas = append(normas, n)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(normas) != 1 || normas[0].ID != 401266 {
		t.Errorf("normas = %+v", normas)
	}
}

func TestAbrirCatalogoSinCSV(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "vacio.zip")
	var sb strings.Builder
	z := zip.NewWriter(&sbWriter{&sb})
	if _, err := z.Create("leeme.txt"); err != nil {
		t.Fatal(err)
	}
	z.Close()
	if err := os.WriteFile(ruta, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AbrirCatalogo(ruta); err == nil {
		t.Error("se aceptó un ZIP sin CSV")
	}
}

func TestAbrirCatalogoInexistente(t *testing.T) {
	if _, err := AbrirCatalogo(filepath.Join(t.TempDir(), "no-esta.zip")); err == nil {
		t.Error("se aceptó un archivo que no existe")
	}
}

// El ritmo es global al cliente: InfoLEG es un sitio público del Estado.
func TestRespetaElIntervalo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body><p>texto</p></body></html>"))
	}))
	defer srv.Close()
	c := NuevoCliente(Opciones{Base: srv.URL, Intervalo: 60 * time.Millisecond})

	inicio := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.TraerTexto(context.Background(), 100000+i); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(inicio); d < 120*time.Millisecond {
		t.Errorf("tres pedidos tardaron %v: no se respetó el intervalo", d)
	}
}

func TestCancelacion(t *testing.T) {
	c := clienteDePrueba(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	ctx, cancelar := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelar()
	if _, err := c.TraerTexto(ctx, 401266); err == nil {
		t.Error("se esperaba un error por cancelación")
	}
}

// El portal publica tres ZIP y hay que distinguirlos por el nombre. Es fácil
// confundirlos: "modificatorias" y "modificadas" comparten el arranque, y
// tomar la primera que coincida deja las relaciones al revés.
func TestBuscarCatalogoDistingueLasTresBases(t *testing.T) {
	respuesta := `{"success":true,"result":{"metadata_modified":"2026-09-01T10:00:00.000000",
	 "resources":[
	  {"name":"Base Infoleg Normativa Nacional - Muestreo","format":"CSV","url":"https://x/muestra.csv"},
	  {"name":"Base Complementaria Infoleg de Normas Modificatorias","format":"ZIP","url":"https://x/mtorias.zip","size":300},
	  {"name":"Base Infoleg Normativa Nacional","format":"ZIP","url":"https://x/base.zip","size":100},
	  {"name":"Base Complementaria Infoleg de Normas Modificadas","format":"ZIP","url":"https://x/mdas.zip","size":200}
	 ]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respuesta))
	}))
	defer srv.Close()

	info, err := NuevoCliente(Opciones{BaseDatos: srv.URL, Intervalo: time.Nanosecond}).
		BuscarCatalogo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != "https://x/base.zip" {
		t.Errorf("la base completa quedó en %q", info.URL)
	}
	if info.Modificadas.URL != "https://x/mdas.zip" {
		t.Errorf("las modificadas quedaron en %q", info.Modificadas.URL)
	}
	if info.Modificatorias.URL != "https://x/mtorias.zip" {
		t.Errorf("las modificatorias quedaron en %q", info.Modificatorias.URL)
	}
	if info.Actualizado.IsZero() {
		t.Error("no se leyó cuándo se actualizó")
	}
}

// Y si el portal deja de publicarlas, notarum sigue sirviendo el catálogo: se
// pierden las relaciones, no todo lo demás.
func TestSinLasComplementariasElCatalogoSirveIgual(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"success":true,"result":{"metadata_modified":"2026-09-01T10:00:00.000000",
		 "resources":[{"name":"Base Infoleg Normativa Nacional","format":"ZIP","url":"https://x/base.zip"}]}}`))
	}))
	defer srv.Close()

	info, err := NuevoCliente(Opciones{BaseDatos: srv.URL, Intervalo: time.Nanosecond}).
		BuscarCatalogo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.URL == "" {
		t.Error("no encontró la base completa")
	}
	if info.Modificadas.Hay() || info.Modificatorias.Hay() {
		t.Error("inventó complementarias que el portal no publicó")
	}
}
