package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
)

// conPolitica levanta un servidor con cuentas y la política indicada.
func conPolitica(t *testing.T, p cuentas.Politica) (*httptest.Server, *cuentas.Registro) {
	t.Helper()
	h, _ := sitioFalso(t)
	origen := httptest.NewServer(h)
	t.Cleanup(origen.Close)

	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CrearUsuario("diego", "una frase larga y tranquila", cuentas.RolPersona); err != nil {
		t.Fatal(err)
	}

	cli := boletin.NuevoCliente(boletin.Opciones{Base: origen.URL, Intervalo: time.Millisecond})
	api := Nuevo(Config{
		Servicio: servicio.Nuevo(cli, alm), Version: "test",
		Registro: reg, Politica: p,
	})
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return srv, reg
}

func tokenDe(t *testing.T, reg *cuentas.Registro, alcance cuentas.Alcance) string {
	t.Helper()
	_, valor, err := reg.CrearToken("diego", "prueba", alcance)
	if err != nil {
		t.Fatal(err)
	}
	return valor
}

func conToken(t *testing.T, srv *httptest.Server, ruta, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+ruta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// Cada modo tiene que dejar entrar exactamente a quien dice.
func TestModosDeAcceso(t *testing.T) {
	casos := []struct {
		modo        cuentas.Modo
		api, lector int
	}{
		{cuentas.ModoAbierto, 200, 302}, // el lector redirige a la última edición
		{cuentas.ModoMixto, 401, 302},
		{cuentas.ModoCerrado, 401, 302}, // redirige, pero a /entrar
	}
	for _, c := range casos {
		t.Run(string(c.modo), func(t *testing.T) {
			p := cuentas.PoliticaPorDefecto(true)
			p.Modo = c.modo
			srv, _ := conPolitica(t, p)

			if res := conToken(t, srv, "/v1/secciones", ""); res.StatusCode != c.api {
				t.Errorf("API sin token = %d, se esperaba %d", res.StatusCode, c.api)
			}
			res := conToken(t, srv, "/", "")
			if res.StatusCode != c.lector {
				t.Errorf("lector sin sesión = %d, se esperaba %d", res.StatusCode, c.lector)
			}
			destino := res.Header.Get("Location")
			if c.modo == cuentas.ModoCerrado && destino != "/entrar" {
				t.Errorf("cerrado tendría que mandar a entrar, mandó a %q", destino)
			}
			if c.modo != cuentas.ModoCerrado && destino == "/entrar" {
				t.Errorf("%s no tendría que pedir login para leer", c.modo)
			}
		})
	}
}

// Con un token válido se entra en cualquier modo.
func TestConTokenSeEntraEnCualquierModo(t *testing.T) {
	for _, modo := range []cuentas.Modo{cuentas.ModoAbierto, cuentas.ModoMixto, cuentas.ModoCerrado} {
		p := cuentas.PoliticaPorDefecto(true)
		p.Modo = modo
		srv, reg := conPolitica(t, p)
		valor := tokenDe(t, reg, cuentas.AlcanceAPI)

		if res := conToken(t, srv, "/v1/secciones", valor); res.StatusCode != 200 {
			t.Errorf("%s: con token = %d", modo, res.StatusCode)
		}
	}
}

// Este es el defecto que motivó separar las zonas: con la API limitada a unos
// pocos pedidos por minuto, el lector tiene que seguir andando.
func TestElLimiteDeLaAPINoAhogaAlLector(t *testing.T) {
	p := cuentas.PoliticaPorDefecto(false)
	p.Modo = cuentas.ModoAbierto
	p.Anonimo = 2 // una API muy restringida
	p.Lector = 100
	srv, _ := conPolitica(t, p)

	// La API se agota enseguida, como corresponde.
	var agotada bool
	for i := 0; i < 5; i++ {
		if conToken(t, srv, "/v1/secciones", "").StatusCode == http.StatusTooManyRequests {
			agotada = true
			break
		}
	}
	if !agotada {
		t.Fatal("la API no respetó su cuota")
	}
	// Y el lector sigue respondiendo: son cuotas distintas.
	for i := 0; i < 5; i++ {
		res := conToken(t, srv, "/docs", "")
		if res.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("el lector se quedó sin cuota por culpa de la API (intento %d)", i+1)
		}
	}
}

// La salud nunca se limita: es lo que mira el orquestador para saber si el
// servicio está vivo.
func TestLaSaludNoSeLimitaNiSeCierra(t *testing.T) {
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoCerrado
	p.Anonimo = 1
	srv, _ := conPolitica(t, p)

	for i := 0; i < 10; i++ {
		if res := conToken(t, srv, "/v1/salud", ""); res.StatusCode != 200 {
			t.Fatalf("salud en el intento %d = %d", i+1, res.StatusCode)
		}
	}
}

// Los intentos de entrada tienen su propio tope, que es para frenar y no para
// repartir: no puede consumir la cuota de leer.
func TestElLoginTieneSuPropioTope(t *testing.T) {
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoAbierto
	p.Login = 3
	p.Anonimo = 100
	srv, _ := conPolitica(t, p)

	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	var frenado bool
	for i := 0; i < 6; i++ {
		res, err := cli.PostForm(srv.URL+"/entrar", map[string][]string{
			"usuario": {"diego"}, "clave": {"la que no es"},
		})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			frenado = true
			break
		}
	}
	if !frenado {
		t.Error("se pudo insistir con el login sin freno")
	}
	// Y leer sigue funcionando: el freno del login no gastó esa cuota.
	if res := conToken(t, srv, "/v1/secciones", ""); res.StatusCode != 200 {
		t.Errorf("leer después de los intentos = %d", res.StatusCode)
	}
}

// Un token revocado no se trata como anónimo: hay que decirlo.
func TestTokenRevocadoSeAvisa(t *testing.T) {
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoAbierto
	srv, reg := conPolitica(t, p)

	tok, valor, err := reg.CrearToken("diego", "para revocar", cuentas.AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RevocarToken("diego", tok.ID); err != nil {
		t.Fatal(err)
	}
	res := conToken(t, srv, "/v1/secciones", valor)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("codigo = %d, se esperaba 401 aunque el modo sea abierto", res.StatusCode)
	}
}

// La cuota de quien se identifica es propia: no la comparte con las demás
// personas que salen por la misma dirección.
func TestLaCuotaConTokenEsPropia(t *testing.T) {
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoAbierto
	p.Anonimo = 2
	p.Persona = 50
	srv, reg := conPolitica(t, p)
	valor := tokenDe(t, reg, cuentas.AlcanceAPI)

	// Se agota la cuota anónima.
	for i := 0; i < 4; i++ {
		conToken(t, srv, "/v1/secciones", "")
	}
	// Y con token se sigue pudiendo, desde la misma dirección.
	res := conToken(t, srv, "/v1/secciones", valor)
	if res.StatusCode != 200 {
		t.Errorf("con token después de agotar lo anónimo = %d", res.StatusCode)
	}
	if lim := res.Header.Get("X-RateLimit-Limit"); lim != "50" {
		t.Errorf("X-RateLimit-Limit = %q, se esperaba la cuota del rol", lim)
	}
}

// Un token de API no sirve para el MCP y viceversa.
func TestElAlcanceDelTokenSeRespeta(t *testing.T) {
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoCerrado
	srv, reg := conPolitica(t, p)

	deAPI := tokenDe(t, reg, cuentas.AlcanceAPI)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+deAPI)
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("un token de API entró al MCP: %d", res.StatusCode)
	}
}

// El error de identificación dice qué hacer, no sólo que no se puede.
func TestElErrorExplicaComoIdentificarse(t *testing.T) {
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoCerrado
	srv, _ := conPolitica(t, p)

	res := conToken(t, srv, "/v1/secciones", "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("codigo = %d", res.StatusCode)
	}
	if res.Header.Get("WWW-Authenticate") == "" {
		t.Error("falta la cabecera que dice cómo autenticarse")
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/secciones", nil)
	respuesta, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer respuesta.Body.Close()
	var e RespuestaError
	if err := json.NewDecoder(respuesta.Body).Decode(&e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.Detalle, "/cuenta") {
		t.Errorf("el error no dice dónde crear un token: %q", e.Detalle)
	}
}

func TestZonaDe(t *testing.T) {
	casos := []struct {
		metodo, ruta string
		esperada     zona
	}{
		{"GET", "/v1/salud", zonaLibre},
		{"GET", "/estatico/estilo.css", zonaLibre},
		{"POST", "/entrar", zonaLogin},
		{"GET", "/entrar", zonaLector},
		{"POST", "/mcp", zonaMCP},
		{"GET", "/v1/secciones", zonaAPI},
		{"GET", "/", zonaLector},
		{"GET", "/docs", zonaLector},
		{"GET", "/ed/primera/2026-09-01", zonaLector},
	}
	for _, c := range casos {
		r := httptest.NewRequest(c.metodo, c.ruta, nil)
		if got := zonaDe(r); got != c.esperada {
			t.Errorf("%s %s -> %v, se esperaba %v", c.metodo, c.ruta, got, c.esperada)
		}
	}
}

// La puerta para crear la primera cuenta no puede quedar detrás del gate:
// para entrar haría falta una cuenta que sólo se puede crear entrando. Pasó:
// /empezar redirigía a /entrar y /entrar a /empezar.
func TestElArranqueNuncaSeCierra(t *testing.T) {
	for _, modo := range []cuentas.Modo{cuentas.ModoAbierto, cuentas.ModoMixto, cuentas.ModoCerrado} {
		p := cuentas.PoliticaPorDefecto(true)
		p.Modo = modo
		p.Anonimo = 1 // y tampoco se limita
		// Sin ninguna cuenta creada, que es cuando el arranque hace falta.
		srv := sinCuentas(t, p)

		for i := 0; i < 5; i++ {
			res := conToken(t, srv, "/empezar", "")
			if res.StatusCode == http.StatusTooManyRequests {
				t.Errorf("%s: /empezar se quedó sin cuota en el intento %d", modo, i+1)
				break
			}
			if destino := res.Header.Get("Location"); destino == "/entrar" {
				t.Errorf("%s: /empezar manda a entrar, y entrar manda a empezar", modo)
				break
			}
		}
	}
}

// sinCuentas levanta una instancia con registro pero sin ninguna cuenta: es
// como queda una recién montada.
func sinCuentas(t *testing.T, p cuentas.Politica) *httptest.Server {
	t.Helper()
	h, _ := sitioFalso(t)
	origen := httptest.NewServer(h)
	t.Cleanup(origen.Close)

	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	cli := boletin.NuevoCliente(boletin.Opciones{Base: origen.URL, Intervalo: time.Millisecond})
	srv := httptest.NewServer(Nuevo(Config{
		Servicio: servicio.Nuevo(cli, alm), Version: "test",
		Registro: reg, Politica: p,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestZonaDelArranque(t *testing.T) {
	for _, m := range []string{"GET", "POST"} {
		if got := zonaDe(httptest.NewRequest(m, "/empezar", nil)); got != zonaLibre {
			t.Errorf("%s /empezar -> %v, tendría que ser libre", m, got)
		}
	}
}
