package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// TestContrato valida cada respuesta real del servidor contra openapi.json.
// Si el contrato y el código se separan, este test lo dice.
func TestContrato(t *testing.T) {
	cargador := &openapi3.Loader{Context: context.Background(), IsExternalRefsAllowed: false}
	doc, err := cargador.LoadFromData(contratoOpenAPI)
	if err != nil {
		t.Fatalf("openapi.json no se pudo cargar: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("openapi.json no es un contrato válido: %v", err)
	}

	srv := servidorDePrueba(t)
	// El router necesita un server que matchee la URL de prueba.
	doc.Servers = openapi3.Servers{&openapi3.Server{URL: srv.URL}}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("no se pudo armar el router del contrato: %v", err)
	}

	casos := []struct {
		nombre string
		ruta   string
		codigo int
	}{
		{"secciones", "/v1/secciones", 200},
		{"calendario", "/v1/calendario/2026/primera", 200},
		{"edicion", "/v1/ediciones/primera/2026-09-01", 200},
		{"edicion filtrada", "/v1/ediciones/primera/2026-09-01?rubro=DECRETOS", 200},
		{"edicion sin edicion", "/v1/ediciones/primera/2026-08-17", 404},
		{"rango", "/v1/ediciones/primera?desde=2026-09-01&hasta=2026-09-10", 200},
		{"aviso", "/v1/avisos/primera/346633/2026-09-01", 200},
		{"rubros", "/v1/rubros/primera", 200},
		{"buscar", "/v1/buscar?seccion=primera&texto=decreto&desde=2026-09-01&hasta=2026-09-03", 200},
		{"salud", "/v1/salud", 200},
		{"seccion invalida", "/v1/ediciones/cuarta/2026-09-01", 400},
		{"fecha invalida", "/v1/ediciones/primera/01-09-2026", 400},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+c.ruta, nil)
			if err != nil {
				t.Fatal(err)
			}
			ruta, params, err := router.FindRoute(req)
			if err != nil {
				t.Fatalf("la ruta %s no está en el contrato: %v", c.ruta, err)
			}

			res, cuerpo := pedir(t, srv, c.ruta)
			if res.StatusCode != c.codigo {
				t.Fatalf("codigo = %d, se esperaba %d (cuerpo: %s)", res.StatusCode, c.codigo, cuerpo)
			}

			entrada := &openapi3filter.RequestValidationInput{
				Request:    req,
				PathParams: params,
				Route:      ruta,
			}
			salida := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: entrada,
				Status:                 res.StatusCode,
				Header:                 res.Header,
				Body:                   http.NoBody,
				Options:                &openapi3filter.Options{IncludeResponseStatus: true},
			}
			salida.SetBodyBytes(cuerpo)

			if err := openapi3filter.ValidateResponse(context.Background(), salida); err != nil {
				t.Errorf("la respuesta no cumple el contrato:\n%v", err)
			}
		})
	}
}

// El contrato tiene que documentar todas las rutas que el servidor atiende.
func TestContratoCubreTodasLasRutas(t *testing.T) {
	cargador := &openapi3.Loader{Context: context.Background()}
	doc, err := cargador.LoadFromData(contratoOpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	documentadas := map[string]bool{}
	for ruta := range doc.Paths.Map() {
		documentadas[ruta] = true
	}
	// Las rutas del servidor, en la notación de OpenAPI.
	servidas := []string{
		"/v1/calendario/{anio}/{seccion}",
		"/v1/ediciones/{seccion}/{fecha}",
		"/v1/ediciones/{seccion}",
		"/v1/avisos/{seccion}/{id}/{fecha}",
		"/v1/anexos/{seccion}/{nro}/{id}/{fecha}",
		"/v1/rubros/{seccion}",
		"/v1/buscar",
		"/v1/secciones",
		"/v1/salud",
		"/v1/openapi.json",
	}
	for _, r := range servidas {
		if !documentadas[r] {
			t.Errorf("la ruta %s se atiende pero no está en openapi.json", r)
		}
	}
	for r := range documentadas {
		var estaServida bool
		for _, s := range servidas {
			if s == r {
				estaServida = true
			}
		}
		if !estaServida && !strings.HasPrefix(r, "/v1/openapi") {
			t.Errorf("la ruta %s está documentada pero no se atiende", r)
		}
	}
}
