package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/vencimientos"
	"github.com/diegoparras/notarum/internal/vencimientos/vencimientostest"
)

func regimenesDePrueba() []vencimientostest.Regimen {
	hoy := boletin.HoyEnArgentina().Time.Format("02/01/2006")
	return append(vencimientostest.Muchos(100, hoy), vencimientostest.Regimen{
		Impuesto: "IMPUESTO A LAS GANANCIAS", Obligacion: "Ingreso del anticipo", Periodo: "Septiembre/2026",
		Fechas: [][2]string{{"8-9", hoy}},
	})
}

// sitioConAgenda levanta el lector con la agenda bajada.
func sitioConAgenda(t *testing.T, conAgenda bool) (*httptest.Server, *servicio.Servicio, *vencimientostest.ARCA) {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	arca := vencimientostest.NuevaARCA(t, vencimientostest.Pagina(regimenesDePrueba()...))
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConVencimientos(vencimientos.NuevoCliente(vencimientos.Opciones{URL: arca.URL}))
	if conAgenda {
		if _, err := srv.SincronizarVencimientos(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	sitio, err := Nuevo(srv, "test")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(sitio)
	t.Cleanup(s.Close)
	return s, srv, arca
}

func TestVencimientosSeDibuja(t *testing.T) {
	s, _, _ := sitioConAgenda(t, true)
	res, cuerpo := pedir(t, s, "/vencimientos?texto=anticipo")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("código %d", res.StatusCode)
	}
	for _, esperado := range []string{"IMPUESTO A LAS GANANCIAS", "Ingreso del anticipo", "vence hoy", "terminación 8-9", "Agenda bajada de ARCA"} {
		if !strings.Contains(cuerpo, esperado) {
			t.Errorf("la página no muestra %q", esperado)
		}
	}
}

// Un CUIT mal escrito se dice: ignorarlo mostraría la agenda de todos.
func TestUnCUITMalEscritoSeDiceEnLaPagina(t *testing.T) {
	s, _, _ := sitioConAgenda(t, true)
	_, cuerpo := pedir(t, s, "/vencimientos?cuit=30-12")
	if !strings.Contains(cuerpo, "No se pudo buscar") {
		t.Error("un CUIT mal escrito se ignoró en silencio")
	}
}

func TestLaTerminacionFiltra(t *testing.T) {
	s, _, _ := sitioConAgenda(t, true)
	_, con4 := pedir(t, s, "/vencimientos?cuit=20-12345678-4&texto=anticipo")
	if strings.Contains(con4, "Ingreso del anticipo") {
		t.Error("la terminación 4 muestra lo que sólo le toca al 8-9")
	}
	_, con9 := pedir(t, s, "/vencimientos?cuit=9&texto=anticipo")
	if !strings.Contains(con9, "Ingreso del anticipo") {
		t.Error("la terminación 9 no muestra su anticipo")
	}
}

// Sin agenda bajada, la página lo dice en vez de mostrar "no vence nada".
func TestSinAgendaLaPaginaLoDice(t *testing.T) {
	s, _, _ := sitioConAgenda(t, false)
	_, cuerpo := pedir(t, s, "/vencimientos")
	if !strings.Contains(cuerpo, "todavía no bajó la agenda") {
		t.Error("una agenda que nunca se bajó se muestra como vacía")
	}
}

func TestLosCambiosSeMuestran(t *testing.T) {
	s, srv, arca := sitioConAgenda(t, true)
	rs := regimenesDePrueba()
	rs[len(rs)-1].Fechas = [][2]string{{"8-9", "31/12/2026"}}
	arca.Poner(vencimientostest.Pagina(rs...))
	if _, err := srv.SincronizarVencimientos(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, cuerpo := pedir(t, s, "/vencimientos")
	if !strings.Contains(cuerpo, "prorroga") || !strings.Contains(cuerpo, "al 31/12/2026") {
		t.Error("la prórroga no se ve en la página")
	}
}
