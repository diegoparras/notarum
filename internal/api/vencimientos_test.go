package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/contrato"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/vencimientos"
	"github.com/diegoparras/notarum/internal/vencimientos/vencimientostest"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// agendaDePruebaAPI son cien regímenes que vencen hoy, uno sin fecha fija y
// uno que sólo le toca a la terminación 8-9.
func agendaDePruebaAPI() []byte {
	hoy := boletin.HoyEnArgentina().Time.Format("02/01/2006")
	rs := vencimientostest.Muchos(100, hoy)
	rs = append(rs,
		vencimientostest.Regimen{Impuesto: "IMPUESTO AL VALOR AGREGADO", Regimen: "PRESTACIONES REALIZADAS EN EL EXTERIOR",
			Obligacion: "Determinación e ingreso dentro de los 10 días hábiles"},
		vencimientostest.Regimen{Impuesto: "IMPUESTO A LAS GANANCIAS", Obligacion: "Ingreso del anticipo", Periodo: "Septiembre/2026",
			Fechas: [][2]string{{"8-9", hoy}}},
	)
	return vencimientostest.Pagina(rs...)
}

// conAgenda levanta la API con la agenda ya bajada.
func conAgenda(t *testing.T) (*httptest.Server, *servicio.Servicio, *vencimientostest.ARCA) {
	t.Helper()
	arca := vencimientostest.NuevaARCA(t, agendaDePruebaAPI())
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConVencimientos(vencimientos.NuevoCliente(vencimientos.Opciones{URL: arca.URL}))
	if _, err := srv.SincronizarVencimientos(context.Background()); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(Nuevo(Config{Servicio: srv, Version: "test"}))
	t.Cleanup(api.Close)
	return api, srv, arca
}

func TestLaAgendaSeBuscaPorLaAPI(t *testing.T) {
	api, _, _ := conAgenda(t)
	var r ResultadoVencimientos
	if c := traerJSON(t, api, "/v1/vencimientos?texto=ganancias", &r); c != 200 {
		t.Fatalf("código %d", c)
	}
	if r.Total != 1 || r.Vencimientos[0].Impuesto != "IMPUESTO A LAS GANANCIAS" {
		t.Fatalf("resultado: %+v", r)
	}
	// Toda respuesta dice de cuándo es la agenda y qué período se preguntó.
	if r.Agenda.BajadaEn.IsZero() || r.Agenda.Desde == "" || r.Agenda.Hasta == "" {
		t.Errorf("la respuesta no dice de cuándo es: %+v", r.Agenda)
	}
}

// Lo que vence para todos también le vence a cada terminación, y lo de otra
// terminación no.
func TestHoyConCUITTraeLoDeEsaTerminacion(t *testing.T) {
	api, _, _ := conAgenda(t)
	var con9, con4 ResultadoVencimientos
	traerJSON(t, api, "/v1/vencimientos/hoy?cuit=20-12345678-9&limite=500", &con9)
	traerJSON(t, api, "/v1/vencimientos/hoy?cuit=4&limite=500", &con4)
	if con9.Total != con4.Total+1 {
		t.Errorf("con 9: %d, con 4: %d; el anticipo sólo le toca al 8-9", con9.Total, con4.Total)
	}
}

// Un CUIT mal escrito no se ignora: la respuesta sería la agenda de todos.
func TestUnCUITMalEscritoEsUnError(t *testing.T) {
	api, _, _ := conAgenda(t)
	var e map[string]any
	if c := traerJSON(t, api, "/v1/vencimientos/hoy?cuit=30-12", &e); c != 400 {
		t.Fatalf("código %d: %v", c, e)
	}
}

func TestLaPaginaDiceQueHayMas(t *testing.T) {
	api, _, _ := conAgenda(t)
	var r ResultadoVencimientos
	traerJSON(t, api, "/v1/vencimientos/proximos?dias=1&limite=10", &r)
	if len(r.Vencimientos) != 10 || !r.HayMas || r.Total <= 10 {
		t.Fatalf("página: %d de %d, hay_mas=%v", len(r.Vencimientos), r.Total, r.HayMas)
	}
}

// Sin agenda bajada, la respuesta lo dice: una lista vacía se leería como
// "no vence nada".
func TestSinAgendaNoContestaUnaListaVacia(t *testing.T) {
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConVencimientos(vencimientos.NuevoCliente(vencimientos.Opciones{URL: "http://127.0.0.1:1"}))
	api := httptest.NewServer(Nuevo(Config{Servicio: srv, Version: "test"}))
	t.Cleanup(api.Close)
	var e map[string]any
	if c := traerJSON(t, api, "/v1/vencimientos/hoy", &e); c != http.StatusServiceUnavailable {
		t.Fatalf("código %d: %v", c, e)
	}
}

func TestLosCambiosSeVenPorLaAPI(t *testing.T) {
	api, srv, arca := conAgenda(t)
	// ARCA corre el anticipo de Ganancias al 31 de diciembre.
	arca.Poner([]byte(strings.Replace(string(agendaDePruebaAPI()),
		`<td class="td-fecha" >8-9</td><td class="td-fecha" >`+boletin.HoyEnArgentina().Time.Format("02/01/2006"),
		`<td class="td-fecha" >8-9</td><td class="td-fecha" >31/12/2026`, 1)))
	if _, err := srv.SincronizarVencimientos(context.Background()); err != nil {
		t.Fatal(err)
	}
	var r struct {
		Cantidad int                   `json:"cantidad"`
		Cambios  []vencimientos.Cambio `json:"cambios"`
	}
	traerJSON(t, api, "/v1/vencimientos/cambios", &r)
	if r.Cantidad != 1 || r.Cambios[0].FechaNueva != "2026-12-31" {
		t.Fatalf("cambios: %+v", r)
	}
}

// Cada respuesta de la agenda cumple el contrato publicado.
func TestContratoDeVencimientos(t *testing.T) {
	api, _, _ := conAgenda(t)
	doc, err := (&openapi3.Loader{Context: context.Background()}).LoadFromData(contrato.JSON())
	if err != nil {
		t.Fatal(err)
	}
	doc.Servers = openapi3.Servers{&openapi3.Server{URL: api.URL}}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ruta   string
		codigo int
	}{
		{"/v1/vencimientos", 200},
		{"/v1/vencimientos?texto=ganancias&cuit=9", 200},
		{"/v1/vencimientos?sin_fecha=1", 200},
		{"/v1/vencimientos?desde=2026-13-01", 400},
		{"/v1/vencimientos/hoy", 200},
		{"/v1/vencimientos/proximos?dias=30", 200},
		{"/v1/vencimientos/proximos?dias=0", 400},
		{"/v1/vencimientos/cambios", 200},
		{"/v1/vencimientos/impuestos", 200},
	} {
		t.Run(c.ruta, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, api.URL+c.ruta, nil)
			ruta, params, err := router.FindRoute(req)
			if err != nil {
				t.Fatalf("no está en el contrato: %v", err)
			}
			res, cuerpo := pedir(t, api, c.ruta)
			if res.StatusCode != c.codigo {
				t.Fatalf("código %d, se esperaba %d: %s", res.StatusCode, c.codigo, cuerpo)
			}
			salida := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: ruta},
				Status:                 res.StatusCode, Header: res.Header, Body: http.NoBody,
				Options: &openapi3filter.Options{IncludeResponseStatus: true},
			}
			salida.SetBodyBytes(cuerpo)
			if err := openapi3filter.ValidateResponse(context.Background(), salida); err != nil {
				t.Errorf("no cumple el contrato: %v", err)
			}
		})
	}
}

// La agenda es parte de la API: con la instancia cerrada pide token, y con el
// token que un administrador crea desde /cuenta se lee.
func TestLaAgendaPideTokenEnModoCerrado(t *testing.T) {
	arca := vencimientostest.NuevaARCA(t, agendaDePruebaAPI())
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CrearUsuario("admin", "una frase larga y tranquila", cuentas.RolAdmin); err != nil {
		t.Fatal(err)
	}
	srv := servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm).
		ConVencimientos(vencimientos.NuevoCliente(vencimientos.Opciones{URL: arca.URL}))
	if _, err := srv.SincronizarVencimientos(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoCerrado
	api := httptest.NewServer(Nuevo(Config{Servicio: srv, Version: "test", Registro: reg, Politica: p}))
	t.Cleanup(api.Close)

	if res := conToken(t, api, "/v1/vencimientos/hoy", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("sin token = %d, se esperaba 401", res.StatusCode)
	}
	_, token, err := reg.CrearToken("admin", "planilla de vencimientos", cuentas.AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if res := conToken(t, api, "/v1/vencimientos/hoy", token); res.StatusCode != http.StatusOK {
		t.Errorf("con el token del admin = %d, se esperaba 200", res.StatusCode)
	}
}
