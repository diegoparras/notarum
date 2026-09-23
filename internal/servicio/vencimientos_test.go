package servicio

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/vencimientos"
)

// arcaFalsa imita el formulario: GET devuelve el formulario, POST la agenda
// que se le haya dado.
type arcaFalsa struct {
	mu        sync.Mutex
	agenda    []byte
	sinCargar bool // contesta "rango no cargado" si se pide más allá de este año
	pedidos   []string
}

func (a *arcaFalsa) servir(t *testing.T) *httptest.Server {
	t.Helper()
	formulario := leerTestdata(t, "formulario.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.Method == http.MethodGet {
			w.Write(formulario)
			return
		}
		_ = r.ParseForm()
		hasta := r.PostFormValue("fechaVHasta")
		a.pedidos = append(a.pedidos, hasta)
		// Tiene cargado hasta fin del año en curso. Se compara contra el año de
		// hoy y no contra el de "desde": en enero, la ventana arranca en el año
		// anterior.
		if a.sinCargar && !strings.HasSuffix(hasta, "/"+time.Now().UTC().Format("2006")) {
			w.Write([]byte("<h3><font color=\"red\">Lista de Errores</font></h3><ul><li>se ingres\xf3 un rango de fecha que no est\xe1 cargado</li></ul>"))
			return
		}
		w.Write(a.agenda)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func leerTestdata(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "vencimientos", "testdata", nombre))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func servicioConVencimientos(t *testing.T, alm almacen.Almacen, url string) *Servicio {
	t.Helper()
	viejo := pisoDeFilas
	pisoDeFilas = 5
	t.Cleanup(func() { pisoDeFilas = viejo })
	return Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConVencimientos(vencimientos.NuevoCliente(vencimientos.Opciones{URL: url}))
}

func almacenEnDisco(t *testing.T) almacen.Almacen {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return alm
}

func TestSincronizarVencimientos(t *testing.T) {
	arca := &arcaFalsa{agenda: leerTestdata(t, "agenda.html")}
	s := servicioConVencimientos(t, almacenEnDisco(t), arca.servir(t).URL)

	if s.AgendaCargada() {
		t.Fatal("hay agenda antes de bajarla")
	}
	e, err := s.SincronizarVencimientos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !e.Sincronizado || e.Filas == 0 || !e.Resumen.CargaInicial || len(e.Impuestos) < 30 {
		t.Fatalf("estado: %+v", e)
	}
	if len(s.CambiosDeVencimientos(time.Time{}, 0)) != 0 {
		t.Error("la carga inicial registró cambios")
	}
	if s.BuscarVencimientos(vencimientos.Consulta{Texto: "ganancias"}).Total == 0 {
		t.Error("no se encuentra lo que se acaba de bajar")
	}
}

// Es la razón de ser de todo esto: ARCA corre una fecha y queda registrado,
// también después de reiniciar.
func TestUnaProrrogaQuedaRegistradaYSobreviveAlReinicio(t *testing.T) {
	arca := &arcaFalsa{agenda: leerTestdata(t, "agenda.html")}
	url := arca.servir(t).URL
	alm := almacenEnDisco(t)
	s := servicioConVencimientos(t, alm, url)
	if _, err := s.SincronizarVencimientos(context.Background()); err != nil {
		t.Fatal(err)
	}

	arca.mu.Lock()
	arca.agenda = bytes.Replace(arca.agenda, []byte(">11/09/2026<"), []byte(">15/09/2026<"), 1)
	arca.mu.Unlock()
	e, err := s.SincronizarVencimientos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if e.Resumen.Prorrogas != 1 {
		t.Fatalf("resumen: %+v", e.Resumen)
	}

	// Otro servicio sobre el mismo almacén: como después de reiniciar.
	otro := servicioConVencimientos(t, alm, url)
	cambios := otro.CambiosDeVencimientos(time.Time{}, 0)
	if len(cambios) != 1 || cambios[0].Tipo != vencimientos.Prorroga || cambios[0].Dias != 4 {
		t.Fatalf("cambios después de reiniciar: %+v", cambios)
	}
}

// Una bajada cortada no puede reemplazar a una buena: daría de baja por
// ausencia todo lo que no trajo.
func TestUnaBajadaCortadaNoReemplazaALaBuena(t *testing.T) {
	arca := &arcaFalsa{agenda: leerTestdata(t, "agenda.html")}
	s := servicioConVencimientos(t, almacenEnDisco(t), arca.servir(t).URL)
	antes, err := s.SincronizarVencimientos(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Sólo el primer régimen de la muestra.
	arca.mu.Lock()
	partes := bytes.Split(arca.agenda, []byte(`<table class="tabla-form-vencimientos"`))
	arca.agenda = bytes.Join(partes[:2], []byte(`<table class="tabla-form-vencimientos"`))
	arca.mu.Unlock()

	if _, err := s.SincronizarVencimientos(context.Background()); err == nil {
		t.Fatal("se aceptó una bajada con una fracción de las filas")
	}
	if s.EstadoVencimientos().Filas != antes.Filas || s.EstadoVencimientos().Resumen.Bajas != 0 {
		t.Error("la bajada cortada tocó lo que había")
	}
}

// ARCA no tiene cargado el año que viene hasta que lo publica. Se pide lo que
// hay y se dice.
func TestSinElAnioQueVieneSePideLoQueHay(t *testing.T) {
	arca := &arcaFalsa{agenda: leerTestdata(t, "agenda.html"), sinCargar: true}
	s := servicioConVencimientos(t, almacenEnDisco(t), arca.servir(t).URL)
	e, err := s.SincronizarVencimientos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(arca.pedidos) != 2 {
		t.Fatalf("pedidos: %v", arca.pedidos)
	}
	if !strings.HasPrefix(e.Hasta, time.Now().Format("2006")) {
		t.Errorf("la ventana quedó hasta %s", e.Hasta)
	}
	var avisado bool
	for _, a := range e.Avisos {
		avisado = avisado || strings.Contains(a, "todavía no cargó")
	}
	if !avisado {
		t.Errorf("no se avisó el repliegue: %v", e.Avisos)
	}
}

func TestSinFuenteNoSeSincroniza(t *testing.T) {
	s := Nuevo(boletin.NuevoCliente(boletin.Opciones{}), almacenEnDisco(t))
	if s.VencimientosDisponible() {
		t.Fatal("disponible sin cliente")
	}
	if _, err := s.SincronizarVencimientos(context.Background()); err == nil {
		t.Fatal("se sincronizó sin cliente")
	}
}
