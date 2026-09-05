package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/asistente"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
)

// proveedorDeMentira imita a OpenRouter y guarda lo que le mandaron.
type proveedorDeMentira struct {
	*httptest.Server
	visto         string // el cuerpo del último pedido de generación
	claveRecibida string
	codigo        int
	respuesta     string
	// antesDeContestar deja frenar al proveedor, para probar qué hace
	// notarum mientras el otro se toma su tiempo.
	antesDeContestar func()
}

const respuestaDelModelo = `{"choices":[{"message":{"content":` +
	`"curl -s https://x/v1/ediciones/primera?desde=2026-01-01"}}],` +
	`"usage":{"prompt_tokens":2500,"completion_tokens":40}}`

func nuevoProveedor(t *testing.T) *proveedorDeMentira {
	t.Helper()
	p := &proveedorDeMentira{codigo: 200, respuesta: respuestaDelModelo}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.claveRecibida = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		// El chequeo de la clave, que es otro camino.
		if strings.HasSuffix(r.URL.Path, "/key") {
			w.WriteHeader(p.codigo)
			w.Write([]byte(`{"data":{"label":"notarum"}}`))
			return
		}
		// ReadAll y no Read: Read devuelve lo que haya en el buffer, que con
		// un contexto de 10 KB es menos de lo que se mandó.
		crudo, _ := io.ReadAll(r.Body)
		p.visto = string(crudo)
		if p.antesDeContestar != nil {
			p.antesDeContestar()
		}
		w.WriteHeader(p.codigo)
		w.Write([]byte(p.respuesta))
	}))
	t.Cleanup(p.Close)
	return p
}

func sitioConAsistente(t *testing.T, prov *proveedorDeMentira) (*httptest.Server, *cuentas.Registro) {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CrearUsuario("diego", claveDePrueba, cuentas.RolPersona); err != nil {
		t.Fatal(err)
	}
	sitio, err := Nuevo(servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm), "test")
	if err != nil {
		t.Fatal(err)
	}
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoAbierto
	sitio.ConCuentas(reg, p).ConAsistente(asistente.NuevoCliente(asistente.Opciones{Base: prov.URL}))

	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv, reg
}

// El recorrido: cargar la clave, pedir la consulta, verla armada.
func TestGenerarUnaConsulta(t *testing.T) {
	prov := nuevoProveedor(t)
	srv, reg := sitioConAsistente(t, prov)
	cli := navegador(t)
	entrar(t, srv, cli)

	res, cuerpo := postear(t, cli, srv.URL+"/cuenta/clave-ia", url.Values{
		"clave_ia": {"sk-or-v1-la-clave-de-diego-0123456789"},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cargar la clave = %d", res.StatusCode)
	}
	if !reg.TieneClaveIA("diego") {
		t.Fatal("la clave no quedó guardada")
	}
	if !strings.Contains(cuerpo, "clave cargada") {
		t.Error("la cuenta no dice que hay una clave")
	}
	if strings.Contains(cuerpo, "la-clave-de-diego") {
		t.Fatal("la cuenta muestra la clave entera")
	}

	// La generación no espera al proveedor: contesta en el acto y sigue por
	// su cuenta. Un pedido HTTP colgado de un tercero termina en la página de
	// error del proxy, que no explica nada.
	res2, _ := postear(t, cli, srv.URL+"/docs/generar", url.Values{
		"pedido": {"resúmenes de un rango de fechas en n8n"},
	})
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("generar = %d", res2.StatusCode)
	}
	cuerpo2 := esperarLaConsulta(t, cli, srv)
	if !strings.Contains(cuerpo2, "/v1/ediciones/primera") {
		t.Error("no se muestra la consulta generada")
	}
	// Y dice qué costó, que lo paga quien puso la clave.
	if !strings.Contains(cuerpo2, "2500") {
		t.Error("no dice cuántos tokens costó")
	}

	// Al proveedor le llegó la clave de esta persona y el contrato de esta
	// instancia.
	if prov.claveRecibida != "sk-or-v1-la-clave-de-diego-0123456789" {
		t.Errorf("el proveedor recibió %q", prov.claveRecibida)
	}
	for _, que := range []string{"/v1/ediciones", "provincial_buscar", "AAAA-MM-DD"} {
		if !strings.Contains(prov.visto, que) {
			t.Errorf("al modelo no le llegó %q", que)
		}
	}
}

// La clave de una persona nunca aparece en ninguna página.
func TestLaClaveIANoSeMuestra(t *testing.T) {
	prov := nuevoProveedor(t)
	srv, _ := sitioConAsistente(t, prov)
	cli := navegador(t)
	entrar(t, srv, cli)

	const clave = "sk-or-v1-secretisima-0123456789abcdef"
	postear(t, cli, srv.URL+"/cuenta/clave-ia", url.Values{"clave_ia": {clave}})

	for _, ruta := range []string{"/cuenta", "/docs"} {
		_, cuerpo := pedirCon(t, cli, srv.URL+ruta)
		if strings.Contains(cuerpo, clave) {
			t.Errorf("%s muestra la clave entera", ruta)
		}
		if strings.Contains(cuerpo, "secretisima") {
			t.Errorf("%s muestra un pedazo de la clave", ruta)
		}
	}
}

// Sin clave cargada, el asistente dice qué falta en vez de fallar raro.
func TestGenerarSinClave(t *testing.T) {
	prov := nuevoProveedor(t)
	srv, _ := sitioConAsistente(t, prov)
	cli := navegador(t)
	entrar(t, srv, cli)

	res, cuerpo := postear(t, cli, srv.URL+"/docs/generar", url.Values{"pedido": {"algo"}})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(cuerpo, "cargá tu clave") {
		t.Error("no dice que falta cargar la clave")
	}
}

// Sin sesión ni se intenta: la generación la paga alguien.
func TestGenerarSinSesion(t *testing.T) {
	prov := nuevoProveedor(t)
	srv, _ := sitioConAsistente(t, prov)

	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := cli.PostForm(srv.URL+"/docs/generar", url.Values{"pedido": {"algo"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Errorf("codigo = %d, se esperaba que mande a entrar", res.StatusCode)
	}
	if prov.visto != "" {
		t.Error("se le pidió algo al proveedor sin que nadie hubiera entrado")
	}
}

// Un pedido vacío o larguísimo no se manda: lo que entra se paga por token.
func TestPedidosQueNoSeMandan(t *testing.T) {
	prov := nuevoProveedor(t)
	srv, reg := sitioConAsistente(t, prov)
	cli := navegador(t)
	entrar(t, srv, cli)
	if err := reg.GuardarClaveIA("diego", "sk-or-v1-x0123456789"); err != nil {
		t.Fatal(err)
	}
	prov.visto = ""

	for _, malo := range []string{"", "   ", strings.Repeat("a", 3000)} {
		res, _ := postear(t, cli, srv.URL+"/docs/generar", url.Values{"pedido": {malo}})
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("un pedido de %d caracteres dio %d", len(malo), res.StatusCode)
		}
	}
	if prov.visto != "" {
		t.Error("se le mandó al proveedor un pedido que no tendría que haber salido")
	}
}

// Los errores del proveedor se explican en castellano, con qué hacer.
func TestLosErroresDelProveedorSeExplican(t *testing.T) {
	casos := map[int]string{
		401: "rechazó tu clave",
		402: "no tiene saldo",
		429: "limitando los pedidos",
	}
	for codigo, esperado := range casos {
		prov := nuevoProveedor(t)
		srv, reg := sitioConAsistente(t, prov)
		cli := navegador(t)
		entrar(t, srv, cli)
		// Se guarda directo, para saltear la prueba de la clave.
		if err := reg.GuardarClaveIA("diego", "sk-or-v1-x0123456789"); err != nil {
			t.Fatal(err)
		}

		prov.codigo = codigo
		prov.respuesta = `{"error":{"message":"no"}}`
		postear(t, cli, srv.URL+"/docs/generar", url.Values{"pedido": {"algo"}})
		cuerpo := esperarLaConsulta(t, cli, srv)
		if !strings.Contains(cuerpo, esperado) {
			t.Errorf("con %d no se explica: falta %q", codigo, esperado)
		}
	}
}

// La clave se prueba antes de guardarla: mejor enterarse ahí que cuando
// alguien quiere generar algo y no entiende por qué falla.
func TestLaClaveSePruebaAlCargarla(t *testing.T) {
	prov := nuevoProveedor(t)
	prov.codigo = 401
	srv, reg := sitioConAsistente(t, prov)
	cli := navegador(t)
	entrar(t, srv, cli)

	res, cuerpo := postear(t, cli, srv.URL+"/cuenta/clave-ia", url.Values{
		"clave_ia": {"sk-or-v1-no-sirve"},
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("codigo = %d", res.StatusCode)
	}
	if !strings.Contains(cuerpo, "rechazó tu clave") {
		t.Error("no explica que la clave no sirve")
	}
	if reg.TieneClaveIA("diego") {
		t.Error("se guardó una clave que el proveedor rechaza")
	}
}

// Es de quien la cargó: se la puede llevar.
func TestSacarLaClaveIA(t *testing.T) {
	prov := nuevoProveedor(t)
	srv, reg := sitioConAsistente(t, prov)
	cli := navegador(t)
	entrar(t, srv, cli)
	postear(t, cli, srv.URL+"/cuenta/clave-ia", url.Values{"clave_ia": {"sk-or-v1-x0123456789"}})

	res, cuerpo := postear(t, cli, srv.URL+"/cuenta/clave-ia/borrar", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if reg.TieneClaveIA("diego") {
		t.Error("la clave sigue guardada")
	}
	if !strings.Contains(cuerpo, "openrouter.ai/keys") {
		t.Error("no vuelve a ofrecer cargar una")
	}
}

// El asistente vive en la documentación, que es donde alguien está leyendo
// qué rutas hay.
func TestElAsistenteEstaEnLaDocumentacion(t *testing.T) {
	prov := nuevoProveedor(t)
	srv, _ := sitioConAsistente(t, prov)

	_, sinSesion := pedirCon(t, navegador(t), srv.URL+"/docs")
	if !strings.Contains(sinSesion, "asistente") {
		t.Error("no se menciona el asistente")
	}
	// Sin sesión invita a entrar, en vez de mostrar un formulario que no
	// serviría.
	if strings.Contains(sinSesion, `action="/docs/generar"`) {
		t.Error("ofrece el formulario a quien no puede usarlo")
	}
}

// esperarLaConsulta recarga /docs hasta que la generación deja de estar
// corriendo, y devuelve la página. Es lo que hace el navegador solo, con el
// refresco de la página.
func esperarLaConsulta(t *testing.T, cli *http.Client, srv *httptest.Server) string {
	t.Helper()
	hasta := time.Now().Add(5 * time.Second)
	for time.Now().Before(hasta) {
		_, cuerpo := pedirCon(t, cli, srv.URL+"/docs")
		if !strings.Contains(cuerpo, `<span class="marca-chica">armando</span>`) {
			return cuerpo
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("la generación no terminó nunca")
	return ""
}

// El pedido HTTP no espera al proveedor.
//
// Esperándolo, el pedido quedaba colgado de un tercero: si tardaba de más, el
// navegador terminaba mostrando la página de error del proxy —"el servicio no
// responde"— cuando el servicio estaba perfecto y el que tardaba era el
// proveedor. Un error que notarum puede explicar lo tiene que mostrar notarum.
func TestGenerarContestaSinEsperarAlProveedor(t *testing.T) {
	prov := nuevoProveedor(t)
	// El proveedor tarda más de lo que aguantaría cualquier proxy.
	seguir := make(chan struct{})
	prov.antesDeContestar = func() { <-seguir }
	defer close(seguir)

	srv, reg := sitioConAsistente(t, prov)
	cli := navegador(t)
	entrar(t, srv, cli)
	if err := reg.GuardarClaveIA("diego", "sk-or-v1-x0123456789"); err != nil {
		t.Fatal(err)
	}

	empezo := time.Now()
	res, _ := postear(t, cli, srv.URL+"/docs/generar", url.Values{"pedido": {"algo"}})
	tardo := time.Since(empezo)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("generar = %d", res.StatusCode)
	}
	if tardo > 2*time.Second {
		t.Errorf("el pedido esperó %s al proveedor", tardo.Round(time.Millisecond))
	}
	// Y mientras tanto la página cuenta que está trabajando, en vez de dejar
	// a quien mira sin saber si pasó algo.
	_, cuerpo := pedirCon(t, cli, srv.URL+"/docs")
	if !strings.Contains(cuerpo, "armando") {
		t.Error("la página no dice que la consulta se está armando")
	}
}
