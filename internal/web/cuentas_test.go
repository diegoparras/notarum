package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
)

const claveDePrueba = "una frase larga y tranquila"

// sitioConCuentas arma un lector con login y una cuenta ya creada.
func sitioConCuentas(t *testing.T) (*httptest.Server, *cuentas.Registro) {
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
	cli := boletin.NuevoCliente(boletin.Opciones{Intervalo: time.Millisecond})
	sitio, err := Nuevo(servicio.Nuevo(cli, alm), "test")
	if err != nil {
		t.Fatal(err)
	}
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoMixto
	sitio.ConCuentas(reg, p)
	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)
	return srv, reg
}

// navegador guarda las cookies, como haría cualquiera.
func navegador(t *testing.T) *http.Client {
	t.Helper()
	tarro, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: tarro}
}

func postear(t *testing.T, cli *http.Client, destino string, campos url.Values) (*http.Response, string) {
	t.Helper()
	res, err := cli.PostForm(destino, campos)
	if err != nil {
		t.Fatalf("POST %s: %v", destino, err)
	}
	defer res.Body.Close()
	cuerpo, _ := io.ReadAll(res.Body)
	return res, string(cuerpo)
}

func entrar(t *testing.T, srv *httptest.Server, cli *http.Client) {
	t.Helper()
	res, _ := postear(t, cli, srv.URL+"/entrar", url.Values{
		"usuario": {"diego"}, "clave": {claveDePrueba},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("no se pudo entrar: %d", res.StatusCode)
	}
	if !strings.HasSuffix(res.Request.URL.Path, "/cuenta") {
		t.Fatalf("entrar terminó en %s y no en /cuenta", res.Request.URL.Path)
	}
}

var (
	reValor   = regexp.MustCompile(`ntrm_[A-Za-z0-9_-]+`)
	reRevocar = regexp.MustCompile(`action="(/cuenta/tokens/[^"]+/revocar)"`)
)

// El recorrido entero, que es como se usa: entrar, crear un token, verlo una
// sola vez, revocarlo, y que deje de servir.
func TestRecorridoDeTokensPorLaWeb(t *testing.T) {
	srv, reg := sitioConCuentas(t)
	cli := navegador(t)
	entrar(t, srv, cli)

	res, cuerpo := postear(t, cli, srv.URL+"/cuenta/tokens", url.Values{
		"nombre": {"para el script"}, "alcance": {"api"},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("crear token: %d", res.StatusCode)
	}
	valor := reValor.FindString(cuerpo)
	if valor == "" {
		t.Fatal("el token recién creado no se mostró")
	}
	if _, u, err := reg.VerificarToken(valor, cuentas.AlcanceAPI); err != nil || u.Nombre != "diego" {
		t.Fatalf("el token mostrado no sirve: %v", err)
	}

	// El valor se muestra una sola vez y nunca más.
	res2, cuerpo2 := pedirCon(t, cli, srv.URL+"/cuenta")
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("ver cuenta: %d", res2.StatusCode)
	}
	if strings.Contains(cuerpo2, valor) {
		t.Error("el valor del token se volvió a mostrar al recargar la cuenta")
	}
	if !strings.Contains(cuerpo2, "para el script") {
		t.Error("el token no aparece en la lista")
	}

	// Revocar desde el formulario que muestra la propia página.
	destino := reRevocar.FindStringSubmatch(cuerpo2)
	if destino == nil {
		t.Fatal("la cuenta no ofrece revocar el token")
	}
	res3, _ := postear(t, cli, srv.URL+destino[1], nil)
	if res3.StatusCode != http.StatusOK {
		t.Fatalf("revocar: %d", res3.StatusCode)
	}
	// Después de un POST que cambia algo, el cliente tiene que terminar en un
	// GET: si no, recargar repite la acción.
	if res3.Request.Method != http.MethodGet {
		t.Errorf("revocar terminó en %s; tendría que redirigir a un GET", res3.Request.Method)
	}

	if _, _, err := reg.VerificarToken(valor, cuentas.AlcanceAPI); err == nil {
		t.Fatal("el token revocado sigue sirviendo")
	}
}

func pedirCon(t *testing.T, cli *http.Client, destino string) (*http.Response, string) {
	t.Helper()
	res, err := cli.Get(destino)
	if err != nil {
		t.Fatalf("GET %s: %v", destino, err)
	}
	defer res.Body.Close()
	cuerpo, _ := io.ReadAll(res.Body)
	return res, string(cuerpo)
}

// Nadie puede revocar el token de otra persona, ni sabiendo el id.
func TestNoSeRevocaElTokenAjeno(t *testing.T) {
	srv, reg := sitioConCuentas(t)
	if _, err := reg.CrearUsuario("ajena", claveDePrueba, cuentas.RolPersona); err != nil {
		t.Fatal(err)
	}
	tok, valor, err := reg.CrearToken("ajena", "el de la otra persona", cuentas.AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}

	cli := navegador(t)
	entrar(t, srv, cli) // entra diego
	postear(t, cli, srv.URL+"/cuenta/tokens/"+tok.ID+"/revocar", nil)

	if _, _, err := reg.VerificarToken(valor, cuentas.AlcanceAPI); err != nil {
		t.Errorf("se revocó el token de otra persona: %v", err)
	}
}

// Sin sesión no se ve la cuenta ni se tocan los tokens.
func TestLaCuentaPideSesion(t *testing.T) {
	srv, _ := sitioConCuentas(t)
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, ruta := range []string{"/cuenta", "/cuenta/tokens", "/cuenta/tokens/loquesea/revocar"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+ruta, nil)
		if ruta == "/cuenta" {
			req.Method = http.MethodGet
		}
		res, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/entrar" {
			t.Errorf("%s sin sesión = %d hacia %q", ruta, res.StatusCode, res.Header.Get("Location"))
		}
	}
}

// La clave equivocada no entra, y el mensaje no dice cuál de las dos cosas
// falló: eso le diría a cualquiera qué nombres existen.
func TestClaveEquivocada(t *testing.T) {
	srv, _ := sitioConCuentas(t)
	cli := navegador(t)

	res, cuerpo := postear(t, cli, srv.URL+"/entrar", url.Values{
		"usuario": {"diego"}, "clave": {"la que no es"},
	})
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("codigo = %d", res.StatusCode)
	}
	res2, cuerpo2 := postear(t, cli, srv.URL+"/entrar", url.Values{
		"usuario": {"nadie"}, "clave": {"la que no es"},
	})
	if res2.StatusCode != http.StatusUnauthorized {
		t.Errorf("codigo con usuario inexistente = %d", res2.StatusCode)
	}
	if avisoDeLaPagina(cuerpo) != avisoDeLaPagina(cuerpo2) {
		t.Errorf("el error distingue el usuario de la clave:\n  %q\n  %q",
			avisoDeLaPagina(cuerpo), avisoDeLaPagina(cuerpo2))
	}
}

var reAviso = regexp.MustCompile(`(?s)class="aviso-error"[^>]*>(.*?)<`)

func avisoDeLaPagina(html string) string {
	if m := reAviso.FindStringSubmatch(html); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// El campo de clave tiene que traer el ojito para mostrarla.
func TestElCampoDeClaveTieneOjito(t *testing.T) {
	srv, _ := sitioConCuentas(t)
	_, cuerpo := pedirCon(t, navegador(t), srv.URL+"/entrar")
	if !strings.Contains(cuerpo, "👁") {
		t.Error("falta el ojito para ver la clave")
	}
	// Y no puede haber credenciales de ejemplo a la vista.
	for _, sospecha := range []string{"admin/admin", "demo", "contraseña por defecto", "value=\"admin\""} {
		if strings.Contains(strings.ToLower(cuerpo), sospecha) {
			t.Errorf("la página de entrada muestra %q", sospecha)
		}
	}
}

// La cuenta explica en qué modo está la instancia: quien opera tiene que poder
// verlo sin leer la configuración.
func TestLaCuentaMuestraElModo(t *testing.T) {
	srv, _ := sitioConCuentas(t)
	cli := navegador(t)
	entrar(t, srv, cli)
	_, cuerpo := pedirCon(t, cli, srv.URL+"/cuenta")

	if !strings.Contains(cuerpo, "mixto") {
		t.Error("la cuenta no dice en qué modo está la instancia")
	}
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoMixto
	if !strings.Contains(cuerpo, p.Explicacion()) {
		t.Errorf("la cuenta no explica el modo: falta %q", p.Explicacion())
	}
}

// Salir borra la sesión de verdad.
func TestSalirCierraLaSesion(t *testing.T) {
	srv, _ := sitioConCuentas(t)
	cli := navegador(t)
	entrar(t, srv, cli)
	if _, err := cli.Get(srv.URL + "/salir"); err != nil {
		t.Fatal(err)
	}
	res := pedirSinSeguir(t, cli, srv.URL+"/cuenta")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/entrar" {
		t.Errorf("después de salir, /cuenta = %d hacia %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func pedirSinSeguir(t *testing.T, cli *http.Client, destino string) *http.Response {
	t.Helper()
	anterior := cli.CheckRedirect
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { cli.CheckRedirect = anterior }()
	res, err := cli.Get(destino)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// Un alcance que no existe no crea nada.
func TestAlcanceInventado(t *testing.T) {
	srv, reg := sitioConCuentas(t)
	cli := navegador(t)
	entrar(t, srv, cli)

	res, _ := postear(t, cli, srv.URL+"/cuenta/tokens", url.Values{
		"nombre": {"raro"}, "alcance": {"todo"},
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("codigo = %d, se esperaba 400", res.StatusCode)
	}
	if len(reg.Tokens("diego")) != 0 {
		t.Error("se creó un token con un alcance que no existe")
	}
}

// Ninguna página puede mostrar el hash de la clave. Pasó: datosCuenta tenía un
// campo Yo que tapaba al de comun, y la barra de navegación terminó
// imprimiendo el usuario entero —con hash y sal— en el HTML de /cuenta.
func TestNingunaPaginaFiltraLaClave(t *testing.T) {
	srv, reg := sitioConCuentas(t)
	u, err := reg.Usuario("diego")
	if err != nil {
		t.Fatal(err)
	}
	if u.Clave.Hash == "" || u.Clave.Sal == "" {
		t.Fatal("el usuario de prueba no tiene clave guardada")
	}

	cli := navegador(t)
	entrar(t, srv, cli)
	for _, ruta := range []string{"/cuenta", "/docs", "/entrar"} {
		_, cuerpo := pedirCon(t, cli, srv.URL+ruta)
		for que, secreto := range map[string]string{
			"el hash de la clave": u.Clave.Hash,
			"la sal":              u.Clave.Sal,
		} {
			if strings.Contains(cuerpo, secreto) {
				t.Errorf("%s muestra %s", ruta, que)
			}
		}
		if strings.Contains(cuerpo, "pbkdf2") {
			t.Errorf("%s nombra el algoritmo de la clave", ruta)
		}
	}
}
