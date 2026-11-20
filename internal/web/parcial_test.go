package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// Si el Boletín declara 410 avisos y sólo entrega 100, el lector lo dice
// arriba de todo: quien lee no tiene por qué saber que existe un campo
// "parcial" en la API.
func TestUnaEdicionParcialLoDiceEnLaPagina(t *testing.T) {
	portada := fixture(t, "portada_segunda_20260901.html")
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/seccion/segunda/20260901":
			w.Write(portada)
		case strings.HasPrefix(r.URL.Path, "/seccion/actualizar/"):
			// El sitio corta acá, aunque su índice de rubros declare 410.
			w.Write([]byte(`{"hay_mas_datos":false,"html":"","sig_pag":3,"ult_rubro":"","id_rubro":null}`))
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

	res, cuerpo := pedir(t, srv, "/ed/segunda/2026-09-01")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	for _, esperado := range []string{
		"Esta edición puede estar incompleta",
		"declara 410 avisos y se pudieron leer 100",
	} {
		if !strings.Contains(cuerpo, esperado) {
			t.Errorf("la página no dice %q", esperado)
		}
	}
}
