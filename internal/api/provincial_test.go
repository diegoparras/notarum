package api

import (
	"encoding/json"
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

// conProvincial levanta la API con el catálogo provincial ya cargado.
func conProvincial(t *testing.T) *httptest.Server {
	t.Helper()
	crudo, err := os.ReadFile(filepath.Join("..", "saij", "testdata", "normativa_provincial.csv"))
	if err != nil {
		t.Fatal(err)
	}
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/3/action/") {
			w.Write([]byte(`{"success":true,"result":{"resources":[{"format":"CSV","url":"` +
				baseDe(r) + `/x.csv","last_modified":"2026-09-01T10:00:00"}]}}`))
			return
		}
		w.Write(crudo)
	}))
	t.Cleanup(portal.Close)

	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConSAIJ(saij.NuevoCliente(saij.Opciones{Base: portal.URL}))
	if _, err := srv.SincronizarSAIJ(t.Context(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(Nuevo(Config{Servicio: srv, Version: "test"}))
	t.Cleanup(api.Close)
	return api
}

func baseDe(r *http.Request) string { return "http://" + r.Host }

// sinProvincial levanta la API sin haber bajado el catálogo.
func sinProvincial(t *testing.T) *httptest.Server {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(Nuevo(Config{
		Servicio: servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm),
		Version:  "test",
	}))
	t.Cleanup(api.Close)
	return api
}

func traerJSON(t *testing.T, srv *httptest.Server, ruta string, destino any) int {
	t.Helper()
	res, err := http.Get(srv.URL + ruta)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if destino != nil {
		if err := json.NewDecoder(res.Body).Decode(destino); err != nil {
			t.Fatalf("GET %s: no se pudo leer la respuesta: %v", ruta, err)
		}
	}
	return res.StatusCode
}

func TestAPIProvincias(t *testing.T) {
	srv := conProvincial(t)
	var provincias []struct {
		ID      string `json:"id"`
		Nombre  string `json:"nombre"`
		Prefijo string `json:"prefijo"`
		Normas  int    `json:"normas"`
	}
	if c := traerJSON(t, srv, "/v1/provincial/provincias", &provincias); c != 200 {
		t.Fatalf("codigo = %d", c)
	}
	if len(provincias) != 24 {
		t.Fatalf("provincias = %d, son 24", len(provincias))
	}
	for _, p := range provincias {
		if p.ID == "" || p.Nombre == "" || p.Prefijo == "" {
			t.Errorf("provincia incompleta: %+v", p)
		}
	}
}

func TestAPIBuscarProvincial(t *testing.T) {
	srv := conProvincial(t)
	var r struct {
		Total  int          `json:"total"`
		Pagina int          `json:"pagina"`
		Normas []saij.Norma `json:"normas"`
		HayMas bool         `json:"hay_mas"`
	}
	if c := traerJSON(t, srv, "/v1/provincial?texto=constitucion", &r); c != 200 {
		t.Fatalf("codigo = %d", c)
	}
	if r.Total == 0 {
		t.Fatal("no se encontró nada")
	}
	for _, n := range r.Normas {
		if n.ID == "" || n.Provincia == "" {
			t.Errorf("norma incompleta: %+v", n)
		}
	}
}

// Una provincia que no existe se avisa: devolver cero sin explicar deja
// pensando que no hay normas.
func TestAPIProvinciaQueNoExiste(t *testing.T) {
	srv := conProvincial(t)
	var e RespuestaError
	if c := traerJSON(t, srv, "/v1/provincial?provincia=Montevideo", &e); c != 400 {
		t.Fatalf("codigo = %d, se esperaba 400", c)
	}
	if !strings.Contains(e.Detalle+e.Error, "provincia") {
		t.Errorf("el error no habla de la provincia: %+v", e)
	}
}

func TestAPIAnioInvalido(t *testing.T) {
	srv := conProvincial(t)
	for _, malo := range []string{"desde=ayer", "hasta=99", "desde=3000"} {
		var e RespuestaError
		if c := traerJSON(t, srv, "/v1/provincial?"+malo, &e); c != 400 {
			t.Errorf("%s -> %d, se esperaba 400", malo, c)
		}
	}
	// Y un rango al revés también.
	var e RespuestaError
	if c := traerJSON(t, srv, "/v1/provincial?desde=2000&hasta=1990", &e); c != 400 {
		t.Errorf("rango al revés -> %d", c)
	}
}

func TestAPINormaProvincial(t *testing.T) {
	srv := conProvincial(t)
	var r struct {
		Normas []saij.Norma `json:"normas"`
	}
	traerJSON(t, srv, "/v1/provincial?limite=1", &r)
	if len(r.Normas) == 0 {
		t.Fatal("no hay normas")
	}
	id := r.Normas[0].ID

	var n struct {
		saij.Norma
		Ficha string `json:"ficha"`
	}
	if c := traerJSON(t, srv, "/v1/provincial/"+id, &n); c != 200 {
		t.Fatalf("codigo = %d", c)
	}
	if n.ID != id {
		t.Errorf("devolvió %s y se pidió %s", n.ID, id)
	}
	if !strings.Contains(n.Ficha, id) {
		t.Errorf("la ficha no enlaza a la norma: %q", n.Ficha)
	}
}

func TestAPINormaQueNoExiste(t *testing.T) {
	srv := conProvincial(t)
	var e RespuestaError
	if c := traerJSON(t, srv, "/v1/provincial/LPX9999999", &e); c != 404 {
		t.Fatalf("codigo = %d, se esperaba 404", c)
	}
}

// Sin catálogo, la API tiene que decir que falta bajarlo. Un 404 o una lista
// vacía harían pensar que no hay normativa provincial.
func TestAPISinCatalogo(t *testing.T) {
	srv := sinProvincial(t)
	for _, ruta := range []string{"/v1/provincial", "/v1/provincial/LPB1000000"} {
		var e RespuestaError
		c := traerJSON(t, srv, ruta, &e)
		if c != http.StatusServiceUnavailable {
			t.Errorf("%s -> %d, se esperaba 503", ruta, c)
		}
		if !strings.Contains(e.Detalle, "notarum provincial") {
			t.Errorf("%s: el error no dice cómo arreglarlo: %+v", ruta, e)
		}
	}
	// Las provincias se listan igual: son una constante.
	var p []any
	if c := traerJSON(t, srv, "/v1/provincial/provincias", &p); c != 200 {
		t.Errorf("provincias sin catálogo -> %d", c)
	}
}

func TestAPIPaginarProvincial(t *testing.T) {
	srv := conProvincial(t)
	var una, dos struct {
		Total  int          `json:"total"`
		Pagina int          `json:"pagina"`
		Normas []saij.Norma `json:"normas"`
		HayMas bool         `json:"hay_mas"`
	}
	traerJSON(t, srv, "/v1/provincial?limite=5", &una)
	traerJSON(t, srv, "/v1/provincial?limite=5&pagina=2", &dos)

	if una.Total != dos.Total {
		t.Errorf("el total cambia entre páginas: %d y %d", una.Total, dos.Total)
	}
	if dos.Pagina != 2 {
		t.Errorf("la página devuelta es %d", dos.Pagina)
	}
	if len(una.Normas) > 0 && len(dos.Normas) > 0 && una.Normas[0].ID == dos.Normas[0].ID {
		t.Error("las dos páginas empiezan igual")
	}
}

// El filtro de vigencia deja fuera lo que no está vigente.
func TestAPIVigentes(t *testing.T) {
	srv := conProvincial(t)
	var r struct {
		Normas []saij.Norma `json:"normas"`
	}
	traerJSON(t, srv, "/v1/provincial?vigentes=1&limite=200", &r)
	for _, n := range r.Normas {
		if !n.Vigente() {
			t.Errorf("%s está %q", n.ID, n.Estado)
		}
	}
}
