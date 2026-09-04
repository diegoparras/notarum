package web

import (
	"archive/zip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/servicio"
)

// El aviso del fixture es el Decreto 845/2026 del 1/9/2026.
const csvInfoLEG = "id_norma,tipo_norma,numero_norma,fecha_boletin,titulo_resumido,texto_original,texto_actualizado,modificada_por,modifica_a\n" +
	"346999,Decreto,845,2026-09-01,DISPOSICIONES VARIAS,http://x/norma.htm,,2,0\n"

// csvSinTexto tiene la misma norma pero sin texto publicado, que es el caso
// de más de la mitad del catálogo.
const csvSinTexto = "id_norma,tipo_norma,numero_norma,fecha_boletin,titulo_resumido,texto_original,texto_actualizado,modificada_por,modifica_a\n" +
	"346999,Decreto,845,2026-09-01,DISPOSICIONES VARIAS,,,0,0\n"

func zipCon(t *testing.T, csv string) []byte {
	t.Helper()
	var sb strings.Builder
	z := zip.NewWriter(&sbW{&sb})
	f, err := z.Create("base.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(sb.String())
}

type sbW struct{ sb *strings.Builder }

func (w *sbW) Write(p []byte) (int, error) { return w.sb.Write(p) }

// sitioConInfoLEG levanta el lector con el enriquecimiento activo y el
// catálogo ya sincronizado.
func sitioConInfoLEG(t *testing.T, csv string) *httptest.Server {
	t.Helper()
	portada := fixture(t, "portada_primera_20260901.html")
	detalle := fixture(t, "detalle_primera_346633.html")
	cal := fixture(t, "calendario_primera_2026.json")
	catalogo := zipCon(t, csv)

	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "package_show"):
			w.Write([]byte(`{"success":true,"result":{"metadata_modified":"2026-09-01T14:00:03.159085",
				"resources":[{"name":"Base Infoleg Normativa Nacional","format":"ZIP","url":"` +
				"http://" + r.Host + `/base.zip"}]}}`))
		case p == "/base.zip":
			w.Write(catalogo)
		case strings.Contains(p, "/anexos/"):
			w.Write([]byte("<html><body><p>TEXTO ACTUALIZADO POR INFOLEG</p></body></html>"))
		case strings.HasPrefix(p, "/seccion/"):
			w.Write(portada)
		case strings.HasPrefix(p, "/detalleAviso/"):
			w.Write(detalle)
		case strings.HasPrefix(p, "/calendario/"):
			w.Write(cal)
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(origen.Close)

	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srvc := servicio.Nuevo(
		boletin.NuevoCliente(boletin.Opciones{Base: origen.URL, Intervalo: time.Millisecond}),
		alm,
	).ConInfoLEG(infoleg.NuevoCliente(infoleg.Opciones{
		Base: origen.URL, BaseDatos: origen.URL, Intervalo: time.Millisecond,
	}))

	if _, err := srvc.SincronizarInfoLEG(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}

	sitio, err := Nuevo(srvc, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv
}

// Lo que le da sentido a todo esto: junto al aviso tal como salió publicado,
// el aviso de que InfoLEG tiene la versión actualizada.
func TestAvisoMuestraLaNormaDeInfoLEG(t *testing.T) {
	srv := sitioConInfoLEG(t, csvInfoLEG)
	res, html := pedir(t, srv, "/av/primera/346633/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, esperado := range []string{
		"También en InfoLEG",
		"DISPOSICIONES VARIAS", // el título que trae el catálogo
		"tal como se publicó",  // la distinción entre las dos versiones
		"2 modificaciones posteriores",
		"/norma/346999",         // el enlace al texto actualizado
		"verNorma.do?id=346999", // y a la ficha oficial
	} {
		if !strings.Contains(html, esperado) {
			t.Errorf("la página no contiene %q", esperado)
		}
	}
}

// Si InfoLEG no publicó el texto, no puede haber un enlace que lleve a nada.
func TestAvisoConNormaSinTexto(t *testing.T) {
	srv := sitioConInfoLEG(t, csvSinTexto)
	_, html := pedir(t, srv, "/av/primera/346633/2026-09-01")

	if !strings.Contains(html, "no publicó su texto") {
		t.Error("no se explicó que InfoLEG no tiene el texto")
	}
	if strings.Contains(html, "/norma/346999") {
		t.Error("se ofreció un enlace a un texto que no existe")
	}
	// La ficha sí, que siempre existe.
	if !strings.Contains(html, "verNorma.do?id=346999") {
		t.Error("no se ofreció la ficha")
	}
}

// El texto actualizado se lee en su propia página, con el camino de vuelta.
func TestPaginaDeLaNorma(t *testing.T) {
	srv := sitioConInfoLEG(t, csvInfoLEG)
	res, html := pedir(t, srv, "/norma/346999?volver=/av/primera/346633/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, esperado := range []string{
		"TEXTO ACTUALIZADO POR INFOLEG",
		"Decreto 845",
		"mantiene actualizado",
		"/av/primera/346633/2026-09-01", // el volver
	} {
		if !strings.Contains(html, esperado) {
			t.Errorf("la página no contiene %q", esperado)
		}
	}
}

// El parámetro de vuelta no puede mandar a cualquier lado: sería un trampolín.
func TestPaginaDeLaNormaNoAceptaVolverAjeno(t *testing.T) {
	srv := sitioConInfoLEG(t, csvInfoLEG)
	for _, volver := range []string{
		"https://sitio-ajeno.example/phishing",
		"//sitio-ajeno.example",
		"/../../etc",
		"javascript:alert(1)",
	} {
		_, html := pedir(t, srv, "/norma/346999?volver="+volver)
		if strings.Contains(html, "sitio-ajeno.example") || strings.Contains(html, "javascript:") {
			t.Errorf("se aceptó el volver ajeno %q", volver)
		}
	}
}

func TestPaginaDeLaNormaIDInvalido(t *testing.T) {
	srv := sitioConInfoLEG(t, csvInfoLEG)
	for _, ruta := range []string{"/norma/abc", "/norma/0", "/norma/-3"} {
		res, _ := pedir(t, srv, ruta)
		if res.StatusCode != 404 {
			t.Errorf("%s -> codigo = %d", ruta, res.StatusCode)
		}
	}
}

// Sin InfoLEG configurado, el aviso se muestra igual y sin bloque.
func TestAvisoSinInfoLEG(t *testing.T) {
	srv := sitioDePrueba(t) // el de siempre, sin InfoLEG
	res, html := pedir(t, srv, "/av/primera/346633/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if strings.Contains(html, "También en InfoLEG") {
		t.Error("apareció el bloque de InfoLEG sin tenerlo configurado")
	}
	// Y el aviso sigue completo.
	if !strings.Contains(html, "PODER EJECUTIVO") {
		t.Error("se rompió el aviso")
	}
	// La página de la norma no existe en esta instancia.
	if res2, _ := pedir(t, srv, "/norma/346999"); res2.StatusCode != 404 {
		t.Errorf("/norma sin InfoLEG -> %d", res2.StatusCode)
	}
}

// El plural en castellano no se arma pegando un sufijo: "modificación" hace
// "modificaciones" y pierde la tilde.
func TestPluralDeModificaciones(t *testing.T) {
	uno := strings.Replace(csvInfoLEG, ",2,0\n", ",1,0\n", 1)
	_, html := pedir(t, sitioConInfoLEG(t, uno), "/av/primera/346633/2026-09-01")
	if !strings.Contains(html, "1 modificación posterior") {
		t.Error("el singular no está bien escrito")
	}
	if strings.Contains(html, "modificaciónes") || strings.Contains(html, "posteriores") {
		t.Error("se usó el plural para uno solo")
	}

	varias := strings.Replace(csvInfoLEG, ",2,0\n", ",5,0\n", 1)
	_, html2 := pedir(t, sitioConInfoLEG(t, varias), "/av/primera/346633/2026-09-01")
	if !strings.Contains(html2, "5 modificaciones posteriores") {
		t.Error("el plural no está bien escrito")
	}
	if strings.Contains(html2, "modificaciónes") {
		t.Error("quedó la tilde en el plural")
	}
}
