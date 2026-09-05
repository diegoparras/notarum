package asistente

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// proveedorFalso imita a OpenRouter.
func proveedorFalso(t *testing.T, codigo int, cuerpo string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	var visto *http.Request
	var recibido []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recibido, _ = io.ReadAll(r.Body)
		visto = r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(codigo)
		w.Write([]byte(cuerpo))
	}))
	t.Cleanup(srv.Close)
	return srv, visto, &recibido
}

const respuestaOK = `{"choices":[{"message":{"role":"assistant",
  "content":"curl https://notarum.ejemplo.ar/v1/ediciones/primera?desde=2026-01-01\u0026hasta=2026-01-31"}}],
  "usage":{"prompt_tokens":2500,"completion_tokens":80}}`

func TestGenerar(t *testing.T) {
	srv, _, recibido := proveedorFalso(t, 200, respuestaOK)
	c := NuevoCliente(Opciones{Base: srv.URL, Sitio: "https://notarum.ejemplo.ar"})

	g, err := c.Generar(context.Background(), "sk-or-v1-loquesea", "",
		"sos parte de notarum", "resúmenes de un rango de fechas en curl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Texto, "curl") {
		t.Errorf("texto = %q", g.Texto)
	}
	if g.TokensEntrada != 2500 || g.TokensSalida != 80 {
		t.Errorf("tokens = %d/%d", g.TokensEntrada, g.TokensSalida)
	}
	if g.Modelo != ModeloPorDefecto {
		t.Errorf("modelo = %q", g.Modelo)
	}

	// Lo que se le mandó: el sistema y el pedido, con temperatura baja.
	var p struct {
		Modelo      string  `json:"model"`
		Temperatura float64 `json:"temperature"`
		Mensajes    []struct {
			Rol       string `json:"role"`
			Contenido string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(*recibido, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Mensajes) != 2 || p.Mensajes[0].Rol != "system" || p.Mensajes[1].Rol != "user" {
		t.Errorf("mensajes = %+v", p.Mensajes)
	}
	if p.Temperatura > 0.3 {
		t.Errorf("temperatura = %v; se quiere la consulta correcta, no una variada", p.Temperatura)
	}
}

// Cada error del proveedor se distingue: no es lo mismo una clave mal puesta
// que quedarse sin saldo, y quien lo lee tiene que saber qué hacer.
func TestLosErroresDelProveedorSeDistinguen(t *testing.T) {
	casos := []struct {
		codigo   int
		esperado error
	}{
		{401, ErrClaveRechazada},
		{403, ErrClaveRechazada},
		{402, ErrSinSaldo},
		{429, ErrProveedorOcupado},
	}
	for _, c := range casos {
		srv, _, _ := proveedorFalso(t, c.codigo, `{"error":{"message":"no"}}`)
		_, err := NuevoCliente(Opciones{Base: srv.URL}).Generar(
			context.Background(), "sk-or-v1-x", "", "sistema", "pedido")
		if !errors.Is(err, c.esperado) {
			t.Errorf("%d -> %v, se esperaba %v", c.codigo, err, c.esperado)
		}
	}
}

// Sin clave ni se sale a la red.
func TestSinClaveNoSePide(t *testing.T) {
	var llamadas int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas++
	}))
	t.Cleanup(srv.Close)

	_, err := NuevoCliente(Opciones{Base: srv.URL}).Generar(
		context.Background(), "  ", "", "sistema", "pedido")
	if !errors.Is(err, ErrClaveRechazada) {
		t.Errorf("err = %v", err)
	}
	if llamadas != 0 {
		t.Error("salió a la red sin tener clave")
	}
}

// Una respuesta vacía no puede pasar por buena: se mostraría un cuadro en
// blanco sin decir que falló.
func TestUnaRespuestaVaciaEsUnError(t *testing.T) {
	for _, cuerpo := range []string{`{"choices":[]}`, `{"choices":[{"message":{"content":"  "}}]}`, `{}`} {
		srv, _, _ := proveedorFalso(t, 200, cuerpo)
		_, err := NuevoCliente(Opciones{Base: srv.URL}).Generar(
			context.Background(), "sk-or-v1-x", "", "s", "p")
		if err == nil {
			t.Errorf("se dio por buena la respuesta %s", cuerpo)
		}
	}
}

// La clave viaja en la cabecera y no en la URL, donde quedaría en los logs
// de cualquier proxy.
func TestLaClaveVaEnLaCabecera(t *testing.T) {
	var autorizacion, url string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		autorizacion = r.Header.Get("Authorization")
		url = r.URL.String()
		w.Write([]byte(respuestaOK))
	}))
	t.Cleanup(srv.Close)

	NuevoCliente(Opciones{Base: srv.URL}).Generar(
		context.Background(), "sk-or-v1-secreta", "", "s", "p")

	if autorizacion != "Bearer sk-or-v1-secreta" {
		t.Errorf("autorización = %q", autorizacion)
	}
	if strings.Contains(url, "secreta") {
		t.Errorf("la clave viajó en la URL: %q", url)
	}
}

func TestProbarLaClave(t *testing.T) {
	srv, _, _ := proveedorFalso(t, 200, `{"data":{"label":"notarum"}}`)
	if err := NuevoCliente(Opciones{Base: srv.URL}).Probar(context.Background(), "sk-or-v1-x"); err != nil {
		t.Errorf("una clave buena dio %v", err)
	}
	mala, _, _ := proveedorFalso(t, 401, `{"error":{"message":"no"}}`)
	if err := NuevoCliente(Opciones{Base: mala.URL}).Probar(context.Background(), "sk-or-v1-x"); !errors.Is(err, ErrClaveRechazada) {
		t.Errorf("una clave mala dio %v", err)
	}
}

// Si el proveedor se cuelga, el pedido se corta: quien está esperando en una
// página no aguanta un minuto largo.
func TestElProveedorQueNoContesta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()
	_, err := NuevoCliente(Opciones{Base: srv.URL}).Generar(ctx, "sk-or-v1-x", "", "s", "p")
	if err == nil {
		t.Fatal("no cortó")
	}
}
