package vencimientos

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Los cuatro campos son todo el contrato con ARCA. Mal escritos, la página
// contesta 200 igual, con errores o con una agenda recortada.
func TestLaConsultaPideLaAgendaEntera(t *testing.T) {
	var recibido url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("método %s", r.Method)
		}
		cuerpo, _ := io.ReadAll(r.Body)
		recibido, _ = url.ParseQuery(string(cuerpo))
		_, _ = w.Write(fixture(t, "agenda.html"))
	}))
	defer srv.Close()

	_, err := NuevoCliente(Opciones{URL: srv.URL}).Consultar(context.Background(),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"fechaVDesde": "01/09/2026", "fechaVHasta": "31/12/2026",
		"terminacionCuit": "Todos", "impuestosSeleccionados": CodigoTodos,
	} {
		if recibido.Get(k) != v {
			t.Errorf("%s = %q, se esperaba %q", k, recibido.Get(k), v)
		}
	}
}

func TestUnaVentanaAlRevesNoSalePorLaRed(t *testing.T) {
	var pedidos int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { pedidos++ }))
	defer srv.Close()
	_, err := NuevoCliente(Opciones{URL: srv.URL}).Consultar(context.Background(),
		time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || pedidos != 0 {
		t.Fatalf("err=%v pedidos=%d", err, pedidos)
	}
}

func TestUnErrorDelSitioSeAtribuyeAARCA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := NuevoCliente(Opciones{URL: srv.URL}).Catalogo(context.Background())
	var deARCA *ErrDeARCA
	if !errorsAs(err, &deARCA) {
		t.Fatalf("el error no se atribuye a ARCA: %v", err)
	}
}

func TestLaVentanaMiraAtrasYAdelante(t *testing.T) {
	ahora := time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC)
	desde, hasta := Ventana(ahora)
	if ahora.Sub(desde) < VentanaAtras || hasta.Format(FormatoISO) != "2027-12-31" {
		t.Errorf("ventana %s..%s", desde, hasta)
	}
	_, hasta = VentanaDeRespaldo(ahora)
	if hasta.Format(FormatoISO) != "2026-12-31" {
		t.Errorf("respaldo hasta %s", hasta)
	}
}
