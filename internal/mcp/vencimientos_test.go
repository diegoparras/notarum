package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/vencimientos"
	"github.com/diegoparras/notarum/internal/vencimientos/vencimientostest"
)

// conAgenda arma el servidor MCP con la agenda bajada: cien regímenes que
// vencen hoy y un anticipo de Ganancias que sólo le toca al 8-9.
func conAgenda(t *testing.T) (*Servidor, *servicio.Servicio, *vencimientostest.ARCA) {
	t.Helper()
	hoy := boletin.HoyEnArgentina().Time.Format("02/01/2006")
	rs := append(vencimientostest.Muchos(100, hoy), vencimientostest.Regimen{
		Impuesto: "IMPUESTO A LAS GANANCIAS", Obligacion: "Ingreso del anticipo", Periodo: "Septiembre/2026",
		Fechas: [][2]string{{"8-9", hoy}},
	})
	arca := vencimientostest.NuevaARCA(t, vencimientostest.Pagina(rs...))
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConVencimientos(vencimientos.NuevoCliente(vencimientos.Opciones{URL: arca.URL}))
	if _, err := srv.SincronizarVencimientos(context.Background()); err != nil {
		t.Fatal(err)
	}
	return Nuevo(srv, "test"), srv, arca
}

type respuestaVencimientos struct {
	Agenda struct {
		BajadaEn string `json:"bajada_en"`
		Periodo  string `json:"periodo_consultado"`
	} `json:"agenda"`
	Total        int              `json:"total"`
	HayMas       bool             `json:"hay_mas"`
	Aviso        string           `json:"aviso"`
	Vencimientos []map[string]any `json:"vencimientos"`
}

func TestLasHerramientasDeVencimientosSeListan(t *testing.T) {
	s, _, _ := conAgenda(t)
	res := llamar(t, s, "tools/list", nil)
	if res == nil || res.Error != nil {
		t.Fatalf("res = %+v", res)
	}
	faltan := map[string]bool{"vencimientos_proximos": true, "vencimientos_buscar": true,
		"vencimientos_cambios": true, "vencimientos_impuestos": true}
	for _, h := range res.Result.(map[string]any)["tools"].([]Herramienta) {
		delete(faltan, h.Nombre)
	}
	if len(faltan) > 0 {
		t.Errorf("no se listan: %v", faltan)
	}
}

// Toda respuesta dice de cuándo es la agenda: el modelo tiene que poder citarla.
func TestLosProximosDicenDeCuandoEsLaAgenda(t *testing.T) {
	s, _, _ := conAgenda(t)
	var r respuestaVencimientos
	decodificar(t, llamarHerramienta(t, s, "vencimientos_proximos", map[string]any{"cuit": "20-12345678-9"}), &r)
	if r.Agenda.BajadaEn == "" || r.Agenda.Periodo == "" {
		t.Errorf("no dice de cuándo es la agenda: %+v", r.Agenda)
	}
	// 100 regímenes × el grupo 7-8-9 + el anticipo del 8-9.
	if r.Total != 101 {
		t.Errorf("total %d, se esperaban 101", r.Total)
	}
	// El límite para un modelo es cien: la lista viene cortada y lo dice.
	if !r.HayMas || !strings.Contains(r.Aviso, "de 101") {
		t.Errorf("la lista cortada no lo dice: hay_mas=%v aviso=%q", r.HayMas, r.Aviso)
	}
}

func TestUnCUITMalEscritoSeLeExplicaAlModelo(t *testing.T) {
	s, _, _ := conAgenda(t)
	r := llamarHerramienta(t, s, "vencimientos_proximos", map[string]any{"cuit": "30-12"})
	if !r.EsError || !strings.Contains(r.Contenido[0].Texto, "CUIT") {
		t.Fatalf("respuesta: %+v", r)
	}
}

func TestLaBusquedaFiltraPorTexto(t *testing.T) {
	s, _, _ := conAgenda(t)
	var r respuestaVencimientos
	decodificar(t, llamarHerramienta(t, s, "vencimientos_buscar", map[string]any{"texto": "anticipo"}), &r)
	if r.Total != 1 || r.Vencimientos[0]["impuesto"] != "IMPUESTO A LAS GANANCIAS" {
		t.Fatalf("resultado: %+v", r)
	}
}

// Sin cambios, se dice por qué: una lista vacía se leería como "ARCA no movió
// nada".
func TestSinCambiosSeExplicaPorQue(t *testing.T) {
	s, _, _ := conAgenda(t)
	var r struct {
		Aviso   string           `json:"aviso"`
		Cambios []map[string]any `json:"cambios"`
	}
	decodificar(t, llamarHerramienta(t, s, "vencimientos_cambios", nil), &r)
	if len(r.Cambios) != 0 || !strings.Contains(r.Aviso, "segunda bajada") {
		t.Fatalf("respuesta: %+v", r)
	}
}

func TestUnaProrrogaLlegaAlModelo(t *testing.T) {
	s, srv, arca := conAgenda(t)
	hoy := boletin.HoyEnArgentina().Time.Format("02/01/2006")
	rs := append(vencimientostest.Muchos(100, hoy), vencimientostest.Regimen{
		Impuesto: "IMPUESTO A LAS GANANCIAS", Obligacion: "Ingreso del anticipo", Periodo: "Septiembre/2026",
		Fechas: [][2]string{{"8-9", "31/12/2026"}},
	})
	arca.Poner(vencimientostest.Pagina(rs...))
	if _, err := srv.SincronizarVencimientos(context.Background()); err != nil {
		t.Fatal(err)
	}
	var r struct {
		Cambios []vencimientos.Cambio `json:"cambios"`
	}
	decodificar(t, llamarHerramienta(t, s, "vencimientos_cambios", nil), &r)
	if len(r.Cambios) != 1 || r.Cambios[0].FechaNueva != "2026-12-31" || r.Cambios[0].VistoAnterior.IsZero() {
		t.Fatalf("cambios: %+v", r.Cambios)
	}
}

// Sin agenda bajada, la herramienta no contesta "no vence nada".
func TestSinAgendaNoSeContestaQueNoVenceNada(t *testing.T) {
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConVencimientos(vencimientos.NuevoCliente(vencimientos.Opciones{URL: "http://127.0.0.1:1"}))
	r := llamarHerramienta(t, Nuevo(srv, "test"), "vencimientos_proximos", nil)
	if !r.EsError || !strings.Contains(r.Contenido[0].Texto, "no significa que no venza nada") {
		t.Fatalf("respuesta: %+v", r)
	}
}
