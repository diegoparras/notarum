package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// sitioPelado es un lector sin nada encendido: alcanza para probar el dibujo.
func sitioPelado(t *testing.T) *Sitio {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Nuevo(servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm), "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Una plantilla que falla a mitad de camino no puede dejar salir media página.
//
// Dibujando derecho sobre la conexión, el código de éxito ya se mandó y lo que
// llega es un documento cortado: el navegador muestra algo roto y un proxy
// adelante puede terminar de romperlo. Se dibuja en memoria y recién después
// se manda, así un error es un error y no una página a medias.
func TestUnaPlantillaQueFallaNoDejaSalirMediaPagina(t *testing.T) {
	s := sitioPelado(t)
	// El campo no existe en los datos, así que falla después de escribir algo.
	s.plantilla["rota"] = template.Must(template.New("base").
		Parse(`ESTO YA SE ESCRIBIÓ{{.NoExiste}}y esto no`))

	w := httptest.NewRecorder()
	s.mostrar(w, httptest.NewRequest(http.MethodGet, "/", nil), "rota", struct{}{}, http.StatusOK)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("contestó %d: el código de éxito salió igual", w.Code)
	}
	if strings.Contains(w.Body.String(), "ESTO YA SE ESCRIBIÓ") {
		t.Error("salió el pedazo que se alcanzó a dibujar antes de fallar")
	}
}

// Y una que anda tiene que llegar entera, con su largo declarado.
func TestUnaPaginaQueAndaSaleEnteraYConSuLargo(t *testing.T) {
	s := sitioPelado(t)
	s.plantilla["sana"] = template.Must(template.New("base").Parse(`hola {{.Quien}}`))

	w := httptest.NewRecorder()
	s.mostrar(w, httptest.NewRequest(http.MethodGet, "/", nil), "sana",
		struct{ Quien string }{"mundo"}, http.StatusOK)

	if w.Body.String() != "hola mundo" {
		t.Errorf("salió %q", w.Body.String())
	}
	if largo := w.Header().Get("Content-Length"); largo != "10" {
		t.Errorf("el largo declarado es %q y la página mide 10", largo)
	}
}
