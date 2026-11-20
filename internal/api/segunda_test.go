package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
)

// servidorConLaSegunda levanta la API, con índice, contra un sitio que entrega
// la Segunda del 1/9/2026 como la entrega el Boletín: 100 avisos en la
// portada y los otros 310 en las páginas que le siguen.
func servidorConLaSegunda(t *testing.T) *httptest.Server {
	t.Helper()
	portada := fixture(t, "portada_segunda_20260901.html")
	paginas := map[string][]byte{}
	for p := 2; p <= 6; p++ {
		paginas[fmt.Sprint(p)] = fixture(t, fmt.Sprintf("seccion_segunda_20260901_p%d.json", p))
	}
	origen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/seccion/segunda/20260901":
			w.Write(portada)
		case "/seccion/actualizar/segunda":
			w.Write(paginas[r.URL.Query().Get("pag")])
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
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

	srv := httptest.NewServer(Nuevo(Config{Servicio: servicio.Nuevo(cli, db), PorMinuto: 0, Version: "test"}))
	t.Cleanup(srv.Close)
	return srv
}

// El síntoma del 19/11/2026: la Segunda devolvía 100 avisos exactos, todos
// societarios, y ni una convocatoria ni un edicto.
func TestLaSegundaSaleEntera(t *testing.T) {
	srv := servidorConLaSegunda(t)
	res, cuerpo := pedir(t, srv, "/v1/ediciones/segunda/2026-09-01")
	if res.StatusCode != 200 {
		t.Fatalf("codigo = %d: %s", res.StatusCode, cuerpo)
	}
	var ed boletin.Edicion
	if err := json.Unmarshal(cuerpo, &ed); err != nil {
		t.Fatal(err)
	}
	if ed.Cantidad != 410 || len(ed.Avisos) != 410 || ed.Declarados != 410 || ed.Parcial {
		t.Fatalf("cantidad=%d avisos=%d cantidad_declarada=%d parcial=%v",
			ed.Cantidad, len(ed.Avisos), ed.Declarados, ed.Parcial)
	}
	for _, rubro := range []string{
		"CONVOCATORIAS Y AV.COMERCIALES - CONVOCATORIAS",
		"CONVOCATORIAS Y AV.COMERCIALES - AVISOS COMERCIALES",
		"EDICTOS JUDICIALES - SUCESIONES",
		"EDICTOS JUDICIALES - REMATES JUDICIALES",
	} {
		if ed.PorRubro[rubro] == 0 {
			t.Errorf("falta el rubro %q", rubro)
		}
	}

	// El filtro por rubro llega a lo que antes quedaba afuera.
	_, cuerpo = pedir(t, srv, "/v1/ediciones/segunda/2026-09-01?rubro=EDICTOS%20JUDICIALES")
	var edictos boletin.Edicion
	if err := json.Unmarshal(cuerpo, &edictos); err != nil {
		t.Fatal(err)
	}
	if edictos.Cantidad != 164 {
		t.Errorf("edictos judiciales = %d, se esperaban 164", edictos.Cantidad)
	}
}

// Y la búsqueda sobre ese día: con la edición cortada el índice tenía 100
// avisos, así que en la segunda página de 50 decía hay_mas: false. Con la
// edición entera recorre los 410 y recién ahí se apaga.
func TestBuscarRecorreLaSegundaEntera(t *testing.T) {
	srv := servidorConLaSegunda(t)
	if res, _ := pedir(t, srv, "/v1/ediciones/segunda/2026-09-01"); res.StatusCode != 200 {
		t.Fatalf("no se pudo leer la edición: %d", res.StatusCode)
	}

	vistos := map[string]bool{}
	for pagina := 1; ; pagina++ {
		if pagina > 20 {
			t.Fatal("la búsqueda no termina nunca")
		}
		res, cuerpo := pedir(t, srv, fmt.Sprintf(
			"/v1/buscar?seccion=segunda&desde=2026-09-01&hasta=2026-09-01&pagina=%d", pagina))
		if res.StatusCode != 200 {
			t.Fatalf("página %d: codigo = %d: %s", pagina, res.StatusCode, cuerpo)
		}
		var b servicio.Busqueda
		if err := json.Unmarshal(cuerpo, &b); err != nil {
			t.Fatal(err)
		}
		if b.Fuente != servicio.FuenteIndice || b.Total != 410 {
			t.Fatalf("página %d: fuente = %q, total = %d", pagina, b.Fuente, b.Total)
		}
		for _, a := range b.Avisos {
			if vistos[a.ID] {
				t.Errorf("página %d: aviso repetido %s", pagina, a.ID)
			}
			vistos[a.ID] = true
		}
		if quedan := len(vistos) < 410; b.HayMas != quedan {
			t.Fatalf("página %d: hay_mas = %v con %d de 410 avisos vistos", pagina, b.HayMas, len(vistos))
		}
		if !b.HayMas {
			break
		}
	}
	if len(vistos) != 410 {
		t.Errorf("la búsqueda recorrió %d avisos, se esperaban 410", len(vistos))
	}
}
