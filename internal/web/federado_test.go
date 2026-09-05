package web

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/lockatus"
	"github.com/diegoparras/notarum/internal/servicio"
)

// hub es un Lockatus de mentira que firma de verdad.
type hub struct {
	*httptest.Server
	clave *rsa.PrivateKey
	// rol es el que el hub le va a dar a quien entre; vacío = no tiene acceso.
	rol    string
	correo string
	// nonce es el que el hub va a poner en el id_token. Vacío = el de la
	// transacción, que es lo correcto.
	nonce string
	// nonceDelPedido es el que llegó en /authorize.
	nonceDelPedido string
}

func nuevoHubFalso(t *testing.T) *hub {
	t.Helper()
	clave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	h := &hub{clave: clave, rol: "persona", correo: "diego@ejemplo.ar"}
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(h.clave.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(h.clave.E)).Bytes()),
		}}})
	})
	// /authorize no muestra ninguna pantalla: manda de vuelta con un código,
	// que es lo único que le importa a notarum.
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		vuelta, _ := url.Parse(q.Get("redirect_uri"))
		v := url.Values{"state": {q.Get("state")}}
		if h.rol == "" {
			v.Set("error", "access_denied")
		} else {
			v.Set("code", "un-codigo")
			h.nonceDelPedido = q.Get("nonce")
		}
		vuelta.RawQuery = v.Encode()
		http.Redirect(w, r, vuelta.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		nonce := h.nonceDelPedido
		if h.nonce != "" {
			nonce = h.nonce
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": h.token(t, map[string]any{}),
			"id_token":     h.token(t, map[string]any{"nonce": nonce, "name": "Diego"}),
			"token_type":   "Bearer", "expires_in": 600,
		})
	})
	h.Server = httptest.NewServer(mux)
	t.Cleanup(h.Close)
	return h
}

func (h *hub) token(t *testing.T, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"iss": h.URL, "aud": "notarum", "sub": "42", "email": h.correo,
		"role": h.rol, "app": "notarum",
		"iat": time.Now().Unix(), "exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	b64 := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	sinFirma := b64(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"}) + "." + b64(claims)
	suma := sha256.Sum256([]byte(sinFirma))
	firma, err := rsa.SignPKCS1v15(rand.Reader, h.clave, crypto.SHA256, suma[:])
	if err != nil {
		t.Fatal(err)
	}
	return sinFirma + "." + base64.RawURLEncoding.EncodeToString(firma)
}

// sitioFederado arma un lector con login propio y federación encendida.
func sitioFederado(t *testing.T, h *hub) (*httptest.Server, *cuentas.Registro) {
	t.Helper()
	alm, err := almacen.NuevoDisco(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := cuentas.NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CrearUsuario("local", claveDePrueba, cuentas.RolPersona); err != nil {
		t.Fatal(err)
	}
	sitio, err := Nuevo(servicio.Nuevo(boletin.NuevoCliente(boletin.Opciones{}), alm), "test")
	if err != nil {
		t.Fatal(err)
	}
	p := cuentas.PoliticaPorDefecto(true)
	p.Modo = cuentas.ModoMixto
	sitio.ConCuentas(reg, p)

	srv := httptest.NewServer(sitio)
	t.Cleanup(srv.Close)

	cli, err := lockatus.Nuevo(lockatus.Opciones{
		Emisor: h.URL, ClienteID: "notarum",
		Vuelta: srv.URL + "/entrar/lockatus/volver",
	})
	if err != nil {
		t.Fatal(err)
	}
	sitio.ConLockatus(cli)
	return srv, reg
}

// El recorrido entero: ir al hub, volver, y quedar con sesión abierta.
func TestEntrarPorElHub(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, reg := sitioFederado(t, h)
	cli := navegador(t)

	res, err := cli.Get(srv.URL + "/entrar/lockatus")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("el recorrido terminó en %d (%s)", res.StatusCode, res.Request.URL)
	}
	// Y se terminó adentro, no de vuelta en la puerta.
	if res.Request.URL.Path != "/cuenta" {
		t.Fatalf("terminó en %s y no en /cuenta", res.Request.URL.Path)
	}

	// La cuenta quedó creada, externa y con el rol que dio el hub.
	u, err := reg.Usuario("diego@ejemplo.ar")
	if err != nil {
		t.Fatalf("no se creó la cuenta: %v", err)
	}
	if !u.Externo || u.Rol != cuentas.RolPersona {
		t.Errorf("cuenta = %+v", u)
	}
	// Y la sesión sirve.
	_, cuerpo := pedirCon(t, cli, srv.URL+"/cuenta")
	if !strings.Contains(cuerpo, "diego@ejemplo.ar") {
		t.Error("la sesión no quedó abierta a nombre de quien entró")
	}
}

// El rol del hub manda, y cambia si allá lo cambian.
func TestElRolLoDecideElHub(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, reg := sitioFederado(t, h)

	h.rol = "admin"
	if res, err := navegador(t).Get(srv.URL + "/entrar/lockatus"); err != nil {
		t.Fatal(err)
	} else {
		res.Body.Close()
	}
	if u, _ := reg.Usuario("diego@ejemplo.ar"); u.Rol != cuentas.RolAdmin {
		t.Fatalf("rol = %q", u.Rol)
	}

	// Se lo bajan en el hub: la próxima entrada tiene que reflejarlo.
	h.rol = "lector"
	if res, err := navegador(t).Get(srv.URL + "/entrar/lockatus"); err != nil {
		t.Fatal(err)
	} else {
		res.Body.Close()
	}
	if u, _ := reg.Usuario("diego@ejemplo.ar"); u.Rol != cuentas.RolPersona {
		t.Fatalf("rol = %q; el hub se lo había bajado", u.Rol)
	}
}

// Que el hub diga que no se explica, no se disimula.
func TestElHubPuedeNoDarAcceso(t *testing.T) {
	h := nuevoHubFalso(t)
	h.rol = "" // sin rol para esta app
	srv, reg := sitioFederado(t, h)

	res, err := navegador(t).Get(srv.URL + "/entrar/lockatus")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("codigo = %d, se esperaba 403", res.StatusCode)
	}
	if _, err := reg.Usuario("diego@ejemplo.ar"); err == nil {
		t.Error("se creó la cuenta de alguien a quien el hub no dejó entrar")
	}
}

// La vuelta sin la cookie de la ida no vale: es lo que impide que alguien
// arme el enlace de vuelta y haga entrar a otro con un código suyo.
func TestLaVueltaSinIdaNoVale(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, _ := sitioFederado(t, h)

	res, err := navegador(t).Get(srv.URL + "/entrar/lockatus/volver?code=un-codigo&state=inventado")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK && res.Request.URL.Path == "/cuenta" {
		t.Fatal("se abrió sesión con una vuelta que nadie había empezado")
	}
	if hayCookieDeSesion(res) {
		t.Fatal("se sembró una sesión sin haber empezado el login")
	}
}

// Y con la cookie de la ida pero otro estado, tampoco.
func TestLaVueltaConOtroEstadoNoVale(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, _ := sitioFederado(t, h)
	cli := navegadorSinSeguir(t)

	// La ida deja la cookie de transacción.
	res, err := cli.Get(srv.URL + "/entrar/lockatus")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	res2, err := cli.Get(srv.URL + "/entrar/lockatus/volver?code=un-codigo&state=otro")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusBadRequest {
		t.Errorf("codigo = %d; una vuelta con otro estado tiene que rebotar", res2.StatusCode)
	}
	if hayCookieDeSesion(res2) {
		t.Fatal("se sembró una sesión con un estado que no era el de la ida")
	}
}

// La cookie de la transacción se usa una sola vez.
func TestLaTransaccionNoSeReusa(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, _ := sitioFederado(t, h)
	cli := navegador(t)

	res, err := cli.Get(srv.URL + "/entrar/lockatus")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// La misma vuelta otra vez, ya sin cookie de transacción.
	res2, err := cli.Get(srv.URL + "/entrar/lockatus/volver?code=un-codigo&state=elmismo")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode == http.StatusOK && res2.Request.URL.Path == "/cuenta" {
		t.Fatal("la vuelta se pudo repetir")
	}
}

// Un id_token con el nonce de otra transacción no cierra el login.
func TestElNonceSeComprueba(t *testing.T) {
	h := nuevoHubFalso(t)
	h.nonce = "el-de-otra-vuelta"
	srv, reg := sitioFederado(t, h)

	res, err := navegador(t).Get(srv.URL + "/entrar/lockatus")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("codigo = %d, se esperaba 502", res.StatusCode)
	}
	if _, err := reg.Usuario("diego@ejemplo.ar"); err == nil {
		t.Error("se abrió una cuenta con un token que no se pudo verificar")
	}
}

// El "volver" es una ruta de acá y de ningún otro lado: si no, el sitio sirve
// de trampolín para llevar a alguien afuera con la confianza de haber salido
// desde acá.
func TestNoSeVuelveAOtroSitio(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, _ := sitioFederado(t, h)

	afuera := []string{
		"https://otro-sitio.example/trampa",
		"//otro-sitio.example/trampa",
		"/\\otro-sitio.example",
		"javascript:alert(1)",
		"http:/otro-sitio.example",
	}
	for _, destino := range afuera {
		cli := navegador(t)
		res, err := cli.Get(srv.URL + "/entrar/lockatus?volver=" + url.QueryEscape(destino))
		if err != nil {
			t.Fatalf("%s: %v", destino, err)
		}
		res.Body.Close()
		if res.Request.URL.Host != strings.TrimPrefix(srv.URL, "http://") {
			t.Errorf("con volver=%q se terminó en %s", destino, res.Request.URL)
		}
	}
}

// Y un destino local sí se respeta: quien tuvo que entrar vuelve a donde iba.
func TestSeVuelveADondeIba(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, _ := sitioFederado(t, h)

	res, err := navegador(t).Get(srv.URL + "/entrar/lockatus?volver=" + url.QueryEscape("/docs"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Request.URL.Path != "/docs" {
		t.Errorf("terminó en %s y no en /docs", res.Request.URL.Path)
	}
}

// Sin federación, las rutas del hub no existen y el botón no aparece.
func TestSinFederacionNoHayBoton(t *testing.T) {
	srv, _ := sitioConCuentas(t) // este no tiene hub
	_, cuerpo := pedirCon(t, navegador(t), srv.URL+"/entrar")
	if strings.Contains(cuerpo, "Lockatus") {
		t.Error("aparece el botón del hub en una instancia que no está federada")
	}
	res := pedirSinSeguir(t, navegador(t), srv.URL+"/entrar/lockatus")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("/entrar/lockatus sin federación = %d", res.StatusCode)
	}
}

// Con federación, el botón está y el login propio sigue estando: se suma una
// forma de entrar, no se reemplaza la otra.
func TestConFederacionSiguenLosDosCaminos(t *testing.T) {
	h := nuevoHubFalso(t)
	srv, _ := sitioFederado(t, h)

	_, cuerpo := pedirCon(t, navegador(t), srv.URL+"/entrar")
	if !strings.Contains(cuerpo, "Entrar con Lockatus") {
		t.Error("falta el botón del hub")
	}
	if !strings.Contains(cuerpo, `name="clave"`) {
		t.Error("desapareció el login propio")
	}

	// Y la cuenta local sigue entrando por el formulario.
	cli := navegador(t)
	res, cuerpo2 := postear(t, cli, srv.URL+"/entrar", url.Values{
		"usuario": {"local"}, "clave": {claveDePrueba},
	})
	if res.StatusCode != http.StatusOK || !strings.Contains(cuerpo2, "local") {
		t.Errorf("la cuenta local no pudo entrar: %d", res.StatusCode)
	}
}

func TestRolDelHub(t *testing.T) {
	for entrada, esperado := range map[string]cuentas.Rol{
		"admin": cuentas.RolAdmin, "ADMIN": cuentas.RolAdmin,
		"superadmin": cuentas.RolAdmin, "administrador": cuentas.RolAdmin,
		"persona": cuentas.RolPersona, "lector": cuentas.RolPersona,
		"editor": cuentas.RolPersona, "": cuentas.RolPersona,
		"un-rol-que-todavia-no-existe": cuentas.RolPersona,
	} {
		if got := rolDelHub(entrada); got != esperado {
			t.Errorf("%q -> %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

func TestDestinoLocal(t *testing.T) {
	for entrada, esperado := range map[string]string{
		"/docs": "/docs", "/buscar?q=uno+dos": "/buscar?q=uno+dos", "": "",
		"//afuera.example": "", "https://afuera.example": "", "javascript:alert(1)": "",
		"/\\afuera.example": "", "/ed/primera/2026-09-01": "/ed/primera/2026-09-01",
		"afuera.example": "", "/con\nsalto": "",
	} {
		if got := destinoLocal(entrada); got != esperado {
			t.Errorf("%q -> %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

// ------------------------------------------------------------- utilidades

func navegadorSinSeguir(t *testing.T) *http.Client {
	t.Helper()
	tarro, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: tarro, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func hayCookieDeSesion(res *http.Response) bool {
	for _, c := range res.Cookies() {
		if c.Name == nombreCookie && c.Value != "" {
			return true
		}
	}
	return false
}
