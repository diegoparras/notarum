package lockatus

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// hubFalso firma de verdad, con una clave RSA propia: la única forma de
// probar que la verificación sirve es que los tokens sean auténticos cuando
// tienen que serlo.
type hubFalso struct {
	*httptest.Server
	clave *rsa.PrivateKey
	kid   string
	// lo que el hub va a contestar en /token
	acceso, id string
	errorToken string
	// pedidos guarda lo que llegó a /token, para revisarlo.
	pedidos []url.Values
	// jwksPedido cuenta cuántas veces se pidieron las claves.
	jwksPedido int
}

func nuevoHub(t *testing.T) *hubFalso {
	t.Helper()
	clave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	h := &hubFalso{clave: clave, kid: "llave-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		h.jwksPedido++
		w.Header().Set("Content-Type", "application/json")
		// h.clave y no la capturada al armar: un test puede cambiar la clave
		// del hub, y si el JWKS siguiera publicando la vieja el caso se
		// probaría solo por la firma.
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": h.kid, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(h.clave.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(h.clave.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		h.pedidos = append(h.pedidos, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		if h.errorToken != "" {
			json.NewEncoder(w).Encode(map[string]string{"error": h.errorToken})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": h.acceso, "id_token": h.id,
			"token_type": "Bearer", "expires_in": 600,
		})
	})
	h.Server = httptest.NewServer(mux)
	t.Cleanup(h.Close)
	return h
}

// firmar arma un JWT como los del hub.
func (h *hubFalso) firmar(t *testing.T, claims map[string]any) string {
	t.Helper()
	return h.firmarCon(t, map[string]any{"alg": "RS256", "kid": h.kid, "typ": "JWT"}, claims, h.clave)
}

func (h *hubFalso) firmarCon(t *testing.T, cab, claims map[string]any, con *rsa.PrivateKey) string {
	t.Helper()
	b64 := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	sinFirma := b64(cab) + "." + b64(claims)
	if con == nil {
		return sinFirma + ".firma-inventada"
	}
	suma := sha256.Sum256([]byte(sinFirma))
	firma, err := rsa.SignPKCS1v15(rand.Reader, con, crypto.SHA256, suma[:])
	if err != nil {
		t.Fatal(err)
	}
	return sinFirma + "." + base64.RawURLEncoding.EncodeToString(firma)
}

// cab arma la cabecera de un JWT.
func cab(alg, kid string) map[string]any {
	return map[string]any{"alg": alg, "kid": kid, "typ": "JWT"}
}

// claims arma un access token válido, que después cada test estropea a su
// manera.
func (h *hubFalso) claims(extra map[string]any) map[string]any {
	c := map[string]any{
		"iss": h.URL, "aud": "notarum", "sub": "42",
		"email": "diego@ejemplo.ar", "role": "admin", "app": "notarum",
		"iat": time.Now().Unix(), "exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func clienteDe(t *testing.T, h *hubFalso) *Cliente {
	t.Helper()
	c, err := Nuevo(Opciones{
		Emisor: h.URL, ClienteID: "notarum",
		Vuelta: "http://localhost:8080/entrar/lockatus/volver",
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCanjeCompleto(t *testing.T) {
	h := nuevoHub(t)
	tr, err := NuevaTransaccion("/buscar?q=algo")
	if err != nil {
		t.Fatal(err)
	}
	h.acceso = h.firmar(t, h.claims(nil))
	h.id = h.firmar(t, h.claims(map[string]any{"nonce": tr.Nonce, "name": "Diego"}))

	ident, err := clienteDe(t, h).Canjear(context.Background(), "un-codigo", tr)
	if err != nil {
		t.Fatalf("el canje falló: %v", err)
	}
	if ident.Correo != "diego@ejemplo.ar" || ident.Rol != "admin" || ident.Nombre != "Diego" {
		t.Errorf("identidad = %+v", ident)
	}

	// El verificador tiene que haber viajado, que es lo que ata el canje al
	// pedido original.
	if len(h.pedidos) != 1 {
		t.Fatalf("%d pedidos a /token", len(h.pedidos))
	}
	p := h.pedidos[0]
	if p.Get("code_verifier") != tr.Verificador {
		t.Error("el verificador no llegó al hub")
	}
	if p.Get("grant_type") != "authorization_code" || p.Get("code") != "un-codigo" {
		t.Errorf("el canje mandó %v", p)
	}
}

// Cada forma de estropear un token tiene que ser rechazada. Es lo único que
// separa una identidad verificada de la palabra de quien la manda.
func TestTokensQueNoSeAceptan(t *testing.T) {
	otraClave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	chica, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	casos := []struct {
		nombre string
		// arma el access token que va a devolver el hub
		arma func(t *testing.T, h *hubFalso) string
		// porque es parte del motivo que tiene que aparecer en el error. Que
		// falle no alcanza: si falla por otra cosa, el caso no está probado.
		porque string
	}{
		{"firmado con otra clave", func(t *testing.T, h *hubFalso) string {
			return h.firmarCon(t, cab("RS256", h.kid), h.claims(nil), otraClave)
		}, "no coincide con ninguna clave"},

		{"sin firma", func(t *testing.T, h *hubFalso) string {
			return h.firmarCon(t, cab("RS256", h.kid), h.claims(nil), nil)
		}, "no coincide con ninguna clave"},

		// Los dos clásicos: si se acepta el algoritmo que declara el token,
		// la firma la puede armar cualquiera.
		{"con alg none", func(t *testing.T, h *hubFalso) string {
			return h.firmarCon(t, cab("none", h.kid), h.claims(nil), nil)
		}, "se esperaba RS256"},

		{"con HMAC en vez de RSA", func(t *testing.T, h *hubFalso) string {
			return h.firmarCon(t, cab("HS256", h.kid), h.claims(nil), nil)
		}, "se esperaba RS256"},

		// El hub publica la chica y firma con ella: sin el piso de tamaño,
		// esto pasaría.
		{"con clave de 1024 bits", func(t *testing.T, h *hubFalso) string {
			h.clave = chica
			return h.firmarCon(t, cab("RS256", h.kid), h.claims(nil), chica)
		}, "hacen falta 2048"},

		{"con un kid que el hub no publica", func(t *testing.T, h *hubFalso) string {
			return h.firmarCon(t, cab("RS256", "otra-llave"), h.claims(nil), h.clave)
		}, "no publica la clave"},

		{"para otra app", func(t *testing.T, h *hubFalso) string {
			return h.firmar(t, h.claims(map[string]any{"aud": "selega"}))
		}, "no para"},

		{"de otro emisor", func(t *testing.T, h *hubFalso) string {
			return h.firmar(t, h.claims(map[string]any{"iss": "https://hub-que-no-es"}))
		}, "lo emitió"},

		{"vencido", func(t *testing.T, h *hubFalso) string {
			return h.firmar(t, h.claims(map[string]any{
				"exp": time.Now().Add(-2 * time.Hour).Unix(),
				"iat": time.Now().Add(-3 * time.Hour).Unix(),
			}))
		}, "venció"},

		{"sin vencimiento", func(t *testing.T, h *hubFalso) string {
			c := h.claims(nil)
			delete(c, "exp")
			return h.firmar(t, c)
		}, "no dice cuándo vence"},

		{"emitido en el futuro", func(t *testing.T, h *hubFalso) string {
			return h.firmar(t, h.claims(map[string]any{
				"iat": time.Now().Add(2 * time.Hour).Unix(),
				"exp": time.Now().Add(3 * time.Hour).Unix(),
			}))
		}, "en el futuro"},

		{"con dos puntos de menos", func(t *testing.T, h *hubFalso) string {
			return "esto-no-es-un-token"
		}, "no tiene las tres partes"},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h := nuevoHub(t)
			tr, err := NuevaTransaccion("")
			if err != nil {
				t.Fatal(err)
			}
			h.acceso = c.arma(t, h)
			h.id = "" // el caso se juega en el access token

			_, err = clienteDe(t, h).Canjear(context.Background(), "c", tr)
			if err == nil {
				t.Fatal("se aceptó un token que no tendría que valer")
			}
			if !strings.Contains(err.Error(), c.porque) {
				t.Errorf("se rechazó por otra cosa: %v; se esperaba que dijera %q", err, c.porque)
			}
		})
	}
}

// El id_token es el que ata la respuesta a esta transacción. Sin esa
// comprobación, alguien puede meter una respuesta vieja o de otra sesión.
func TestElNonceAtaLaTransaccion(t *testing.T) {
	h := nuevoHub(t)
	tr, _ := NuevaTransaccion("")
	otra, _ := NuevaTransaccion("")

	h.acceso = h.firmar(t, h.claims(nil))
	h.id = h.firmar(t, h.claims(map[string]any{"nonce": otra.Nonce}))

	_, err := clienteDe(t, h).Canjear(context.Background(), "c", tr)
	if !errors.Is(err, ErrFirma) {
		t.Fatalf("se aceptó un id_token de otra transacción: %v", err)
	}
}

func TestSinNonceEnElIDToken(t *testing.T) {
	h := nuevoHub(t)
	tr, _ := NuevaTransaccion("")
	h.acceso = h.firmar(t, h.claims(nil))
	h.id = h.firmar(t, h.claims(nil)) // sin nonce

	if _, err := clienteDe(t, h).Canjear(context.Background(), "c", tr); err == nil {
		t.Fatal("se aceptó un id_token sin nonce")
	}
}

// Los dos tokens tienen que hablar de la misma persona.
func TestLosDosTokensSonDeLaMismaPersona(t *testing.T) {
	h := nuevoHub(t)
	tr, _ := NuevaTransaccion("")
	h.acceso = h.firmar(t, h.claims(nil))
	h.id = h.firmar(t, h.claims(map[string]any{"nonce": tr.Nonce, "sub": "otro"}))

	if _, err := clienteDe(t, h).Canjear(context.Background(), "c", tr); err == nil {
		t.Fatal("se aceptaron dos tokens de personas distintas")
	}
}

// Que el hub diga que no es una respuesta, no una falla: hay que poder
// distinguirla para explicarla bien.
func TestElHubPuedeNegarElAcceso(t *testing.T) {
	h := nuevoHub(t)
	h.errorToken = "access_denied"
	tr, _ := NuevaTransaccion("")

	_, err := clienteDe(t, h).Canjear(context.Background(), "c", tr)
	if !errors.Is(err, ErrRechazado) {
		t.Fatalf("err = %v, se esperaba ErrRechazado", err)
	}
}

// El desafío que viaja es el hash del verificador, nunca el verificador.
func TestElVerificadorNoViajaPorElNavegador(t *testing.T) {
	h := nuevoHub(t)
	tr, _ := NuevaTransaccion("")
	destino := clienteDe(t, h).URLAutorizar(tr)

	if strings.Contains(destino, tr.Verificador) {
		t.Fatal("el verificador viaja en la URL: eso anula el PKCE")
	}
	u, err := url.Parse(destino)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("método = %q; con plain el PKCE no protege nada", q.Get("code_challenge_method"))
	}
	suma := sha256.Sum256([]byte(tr.Verificador))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(suma[:]) {
		t.Error("el desafío no es el hash del verificador")
	}
	for _, campo := range []string{"state", "nonce", "client_id", "redirect_uri"} {
		if q.Get(campo) == "" {
			t.Errorf("falta %s en la URL de autorización", campo)
		}
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
}

// Cada transacción tiene sus propios secretos.
func TestCadaTransaccionEsDistinta(t *testing.T) {
	vistos := map[string]bool{}
	for i := 0; i < 50; i++ {
		tr, err := NuevaTransaccion("")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{tr.Verificador, tr.Estado, tr.Nonce} {
			if vistos[s] {
				t.Fatal("se repitió un secreto de transacción")
			}
			vistos[s] = true
		}
		if len(tr.Verificador) < 43 {
			t.Errorf("el verificador tiene %d caracteres; el mínimo del protocolo es 43", len(tr.Verificador))
		}
	}
}

// Las claves se guardan: un login no puede costar dos viajes al hub cada vez.
func TestLasClavesSeGuardan(t *testing.T) {
	h := nuevoHub(t)
	cli := clienteDe(t, h)
	tr, _ := NuevaTransaccion("")
	h.acceso = h.firmar(t, h.claims(nil))
	h.id = h.firmar(t, h.claims(map[string]any{"nonce": tr.Nonce}))

	for i := 0; i < 3; i++ {
		if _, err := cli.Canjear(context.Background(), "c", tr); err != nil {
			t.Fatal(err)
		}
	}
	if h.jwksPedido != 1 {
		t.Errorf("se pidieron las claves %d veces; alcanzaba con una", h.jwksPedido)
	}
}

// Una configuración incompleta se avisa al armar el cliente, no cuando
// alguien intenta entrar.
func TestConfiguracionIncompleta(t *testing.T) {
	casos := map[string]Opciones{
		"sin emisor":     {ClienteID: "notarum", Vuelta: "http://x/v"},
		"sin cliente":    {Emisor: "https://hub", Vuelta: "http://x/v"},
		"sin vuelta":     {Emisor: "https://hub", ClienteID: "notarum"},
		"emisor raro":    {Emisor: "no-es-una-url", ClienteID: "notarum", Vuelta: "http://x/v"},
		"emisor por ftp": {Emisor: "ftp://hub", ClienteID: "notarum", Vuelta: "http://x/v"},
	}
	for nombre, o := range casos {
		if _, err := Nuevo(o); !errors.Is(err, ErrConfiguracion) {
			t.Errorf("%s: err = %v", nombre, err)
		}
	}
}

// La barra final del emisor se limpia: si no, las direcciones salen con dos.
func TestElEmisorSeNormaliza(t *testing.T) {
	c, err := Nuevo(Opciones{Emisor: "https://hub.ejemplo/", ClienteID: "notarum", Vuelta: "http://x/v"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Emisor() != "https://hub.ejemplo" {
		t.Errorf("emisor = %q", c.Emisor())
	}
	tr, _ := NuevaTransaccion("")
	if strings.Contains(c.URLAutorizar(tr), "//authorize") {
		t.Error("la dirección de autorización salió con dos barras")
	}
}
