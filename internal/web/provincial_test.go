package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/saij"
	"github.com/diegoparras/notarum/internal/servicio"
)

// sitioProvincial levanta el lector con el catálogo provincial cargado.
func sitioProvincial(t *testing.T, conCatalogo bool) *httptest.Server {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm)

	if conCatalogo {
		crudo, err := os.ReadFile(filepath.Join("..", "saij", "testdata", "normativa_provincial.csv"))
		if err != nil {
			t.Fatal(err)
		}
		portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/3/action/") {
				w.Write([]byte(`{"success":true,"result":{"resources":[{"format":"CSV","url":"http://` +
					r.Host + `/x.csv","last_modified":"2026-09-01T10:00:00"}]}}`))
				return
			}
			w.Write(crudo)
		}))
		t.Cleanup(portal.Close)
		srv = srv.ConSAIJ(saij.NuevoCliente(saij.Opciones{Base: portal.URL}))
		if _, err := srv.SincronizarSAIJ(t.Context(), t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}

	sitio, err := Nuevo(srv, "test")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(sitio)
	t.Cleanup(s.Close)
	return s
}

func TestProvincialSeDibuja(t *testing.T) {
	srv := sitioProvincial(t, true)
	res, cuerpo := pedir(t, srv, "/provincial")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, que := range []string{"Normativa provincial", "todas las provincias", "sólo vigentes"} {
		if !strings.Contains(cuerpo, que) {
			t.Errorf("falta %q", que)
		}
	}
	// El desplegable trae las 24 con su conteo.
	if !strings.Contains(cuerpo, "Buenos Aires (") {
		t.Error("el desplegable no muestra cuántas normas hay de cada provincia")
	}
}

// Los filtros se conservan al volver: el desplegable tiene que mostrar lo que
// se eligió, y no reiniciarse en cada búsqueda.
func TestLosFiltrosSeConservan(t *testing.T) {
	srv := sitioProvincial(t, true)
	_, cuerpo := pedir(t, srv, "/provincial?provincia=22&vigentes=1&texto=ley")

	if !strings.Contains(cuerpo, `value="22" selected`) {
		t.Error("la provincia elegida no queda marcada")
	}
	if !strings.Contains(cuerpo, `value="1" checked`) {
		t.Error("el filtro de vigencia no queda marcado")
	}
	if !strings.Contains(cuerpo, `value="ley"`) {
		t.Error("el texto buscado no queda en la caja")
	}
}

// El nombre de la provincia también sirve, y se traduce al código para que el
// desplegable la muestre elegida.
func TestLaProvinciaSeAceptaPorNombre(t *testing.T) {
	srv := sitioProvincial(t, true)
	_, cuerpo := pedir(t, srv, "/provincial?provincia=Chaco")
	if !strings.Contains(cuerpo, `value="22" selected`) {
		t.Error("no se resolvió Chaco a su código")
	}
}

func TestProvinciaQueNoExisteSeExplica(t *testing.T) {
	srv := sitioProvincial(t, true)
	_, cuerpo := pedir(t, srv, "/provincial?provincia=Montevideo")
	if !strings.Contains(cuerpo, "No se reconoce la provincia") {
		t.Error("no se explica que la provincia no existe")
	}
}

func TestNormaProvincialSeDibuja(t *testing.T) {
	srv := sitioProvincial(t, true)
	// La constitución de Buenos Aires está en el fixture.
	res, cuerpo := pedir(t, srv, "/provincial/LPB1000000")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, que := range []string{"Buenos Aires", "LPB1000000", "Materias", "Verla en SAIJ"} {
		if !strings.Contains(cuerpo, que) {
			t.Errorf("falta %q en la ficha", que)
		}
	}
	// El enlace tiene que ir a SAIJ, con rel noopener por abrirse en otra
	// pestaña.
	if !strings.Contains(cuerpo, "https://www.saij.gob.ar/LPB1000000") {
		t.Error("falta el enlace a SAIJ")
	}
	if !strings.Contains(cuerpo, `rel="noopener"`) {
		t.Error("el enlace externo no lleva rel=noopener")
	}
	// Y dice por qué no muestra el texto, en vez de dejar un hueco.
	if !strings.Contains(cuerpo, "no su texto") {
		t.Error("no explica por qué no está el texto")
	}
}

func TestNormaProvincialQueNoExiste(t *testing.T) {
	srv := sitioProvincial(t, true)
	res, _ := pedir(t, srv, "/provincial/LPX9999999")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("codigo = %d", res.StatusCode)
	}
}

// Sin catálogo, la página explica qué falta hacer en vez de mostrar una lista
// vacía que parecería un error.
func TestProvincialSinCatalogo(t *testing.T) {
	srv := sitioProvincial(t, false)
	res, cuerpo := pedir(t, srv, "/provincial")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(cuerpo, "notarum provincial") {
		t.Error("no dice cómo bajar el catálogo")
	}
	// Y no muestra un formulario que no serviría para nada.
	if strings.Contains(cuerpo, "sólo vigentes") {
		t.Error("muestra los filtros sin tener con qué filtrar")
	}
}

// Pasar de página conserva los filtros: perderlos sería empezar de cero.
func TestPaginarConservaLosFiltros(t *testing.T) {
	srv := sitioProvincial(t, true)
	_, cuerpo := pedir(t, srv, "/provincial?provincia=66&pagina=2")

	if !strings.Contains(cuerpo, "provincia=66") {
		t.Error("los enlaces de página perdieron la provincia")
	}
	if !strings.Contains(cuerpo, "anteriores") {
		t.Error("en la página 2 no hay cómo volver")
	}
}

// El enlace de la barra de arriba lleva a la normativa provincial.
func TestSeEnlazaDesdeElSitio(t *testing.T) {
	srv := sitioProvincial(t, true)
	_, cuerpo := pedir(t, srv, "/provincial")
	if !strings.Contains(cuerpo, `href="/provincial"`) {
		t.Error("la barra de arriba no enlaza a la normativa provincial")
	}
}

// Una lista vacía dice qué probar, en vez de dejar la página en blanco.
func TestSinResultadosSeExplica(t *testing.T) {
	srv := sitioProvincial(t, true)
	_, cuerpo := pedir(t, srv, "/provincial?texto=estonoexisteenningunaparte")
	if !strings.Contains(cuerpo, "No hay normas con esos criterios") {
		t.Error("no se explica que no hubo resultados")
	}
}

// Dos años al revés son un descuido y no un error: se dan vuelta.
func TestAniosAlReves(t *testing.T) {
	srv := sitioProvincial(t, true)
	res, cuerpo := pedir(t, srv, "/provincial?desde=2000&hasta=1990")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if strings.Contains(cuerpo, "No se pudo buscar") {
		t.Error("dos años al revés se trataron como un error")
	}
}

func TestAnioDe(t *testing.T) {
	for entrada, esperado := range map[string]int{
		"1994": 1994, "2026": 2026, "": 0, "ayer": 0, "99": 0, "3000": 0, " 1994 ": 1994,
	} {
		if got := anioDe(entrada); got != esperado {
			t.Errorf("%q -> %d, se esperaba %d", entrada, got, esperado)
		}
	}
}
