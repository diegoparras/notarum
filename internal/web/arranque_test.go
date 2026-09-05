package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/tareas"
)

// sitioSinCuentas es una instancia recién montada: con registro y sin nadie.
func sitioSinCuentas(t *testing.T) (*httptest.Server, *cuentas.Registro) {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	sitio, err := Nuevo(servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm), "test")
	if err != nil {
		t.Fatal(err)
	}
	sitio.ConCuentas(reg, cuentas.PoliticaPorDefecto(false)).ConTareas(tareas.Nuevo())
	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv, reg
}

// El recorrido: llegar a una instancia sin cuentas, poner el código y quedar
// adentro con el panel abierto.
func TestArrancarDesdeElNavegador(t *testing.T) {
	srv, reg := sitioSinCuentas(t)
	codigo, err := reg.CodigoDeArranque()
	if err != nil {
		t.Fatal(err)
	}

	res, cuerpo := pedirCon(t, navegador(t), srv.URL+"/empezar")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/empezar = %d", res.StatusCode)
	}
	if !strings.Contains(cuerpo, "código de arranque") {
		t.Error("no explica de dónde sacar el código")
	}
	// Y dice dónde encontrarlo sin abrir una consola.
	if !strings.Contains(cuerpo, "log del servicio") {
		t.Error("no dice dónde está el código")
	}

	cli := navegador(t)
	res2, _ := postear(t, cli, srv.URL+"/empezar", url.Values{
		"codigo": {cuentas.CodigoLegible(codigo)}, "usuario": {"diego"},
		"clave": {claveDePrueba}, "clave2": {claveDePrueba},
	})
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("crear la primera cuenta = %d", res2.StatusCode)
	}
	// Queda con la sesión abierta y en el panel, que es lo que sigue.
	if res2.Request.URL.Path != "/admin" {
		t.Errorf("terminó en %s y no en el panel", res2.Request.URL.Path)
	}
	u, err := reg.Usuario("diego")
	if err != nil || u.Rol != cuentas.RolAdmin {
		t.Errorf("la cuenta quedó como %+v (%v)", u, err)
	}
}

// Con el código equivocado no se crea nada, y el mensaje dice dónde buscarlo
// sin decir cuánto se acertó.
func TestArrancarConElCodigoEquivocado(t *testing.T) {
	srv, reg := sitioSinCuentas(t)
	cli := navegador(t)

	res, cuerpo := postear(t, cli, srv.URL+"/empezar", url.Values{
		"codigo": {"NOESESTE"}, "usuario": {"diego"},
		"clave": {claveDePrueba}, "clave2": {claveDePrueba},
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(cuerpo, "log del servicio") {
		t.Error("el error no dice dónde está el código")
	}
	if reg.HayUsuarios() {
		t.Fatal("se creó la cuenta igual")
	}
}

// Las dos claves tienen que coincidir: no hay forma de recuperarla.
func TestLasDosClavesTienenQueSerIguales(t *testing.T) {
	srv, reg := sitioSinCuentas(t)
	codigo, _ := reg.CodigoDeArranque()

	res, cuerpo := postear(t, navegador(t), srv.URL+"/empezar", url.Values{
		"codigo": {codigo}, "usuario": {"diego"},
		"clave": {claveDePrueba}, "clave2": {"otra frase larga y tranquila"},
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(cuerpo, "no son iguales") {
		t.Error("no explica que las claves no coinciden")
	}
	if reg.HayUsuarios() {
		t.Error("se creó la cuenta con dos claves distintas")
	}
}

// Con cuentas ya creadas, la puerta se cierra y manda a entrar.
func TestConCuentasElArranqueSeCierra(t *testing.T) {
	srv, _ := sitioConCuentas(t) // este ya tiene una cuenta
	res := pedirSinSeguir(t, navegador(t), srv.URL+"/empezar")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/entrar" {
		t.Errorf("/empezar con cuentas = %d hacia %q", res.StatusCode, res.Header.Get("Location"))
	}
}

// Y sin cuentas, /entrar manda a arrancar en vez de dar un 404 que no dice
// qué hacer.
func TestSinCuentasEntrarMandaAArrancar(t *testing.T) {
	srv, _ := sitioSinCuentas(t)
	res := pedirSinSeguir(t, navegador(t), srv.URL+"/entrar")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/empezar" {
		t.Errorf("/entrar sin cuentas = %d hacia %q", res.StatusCode, res.Header.Get("Location"))
	}
}

// Los dos campos de clave tienen su ojito: escribir a ciegas una frase larga
// dos veces es la mejor forma de trabarse afuera.
func TestLosDosCamposDeClaveTienenOjito(t *testing.T) {
	srv, _ := sitioSinCuentas(t)
	_, cuerpo := pedirCon(t, navegador(t), srv.URL+"/empezar")
	if n := strings.Count(cuerpo, `class="ojo"`); n != 2 {
		t.Errorf("hay %d ojitos y son dos campos de clave", n)
	}
	if strings.Contains(strings.ToLower(cuerpo), "admin/admin") {
		t.Error("muestra credenciales de ejemplo")
	}
}
