// Package lockatus habla OIDC con Lockatus, el hub de identidad de la suite
// Escriba, para que notarum pueda delegarle el login.
//
// Es un cliente mínimo y a propósito: sólo el flujo de código de autorización
// con PKCE, que es el único que necesita una aplicación con servidor. La
// verificación de los tokens se hace acá, contra las claves que publica el
// hub, sin llamarlo de vuelta.
//
// Sin dependencias: RSA y SHA-256 están en la biblioteca estándar, y sumar un
// paquete de terceros para verificar una firma es sumar a alguien más a la
// lista de quienes pueden entrar.
package lockatus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Opciones es lo que hace falta para hablar con un hub.
type Opciones struct {
	// Emisor es la dirección del hub, tal como aparece en los tokens.
	Emisor string
	// ClienteID es el slug con el que esta instancia está declarada en el hub.
	ClienteID string
	// Vuelta es la dirección a la que el hub manda el navegador después de
	// entrar. Tiene que estar declarada igual del otro lado.
	Vuelta string
	// HTTP se puede cambiar en los tests.
	HTTP *http.Client
	// Ahora se puede cambiar en los tests.
	Ahora func() time.Time
}

// Cliente habla con un hub.
type Cliente struct {
	emisor, clienteID, vuelta string
	http                      *http.Client
	ahora                     func() time.Time

	mu     sync.Mutex
	claves []clavePublica
	leidas time.Time
}

// ErrConfiguracion dice que falta algo para poder federar. Se devuelve al
// arrancar y no en cada pedido: es un problema de quien monta el servicio.
var ErrConfiguracion = errors.New("la federación con Lockatus está incompleta")

func Nuevo(o Opciones) (*Cliente, error) {
	emisor := strings.TrimRight(strings.TrimSpace(o.Emisor), "/")
	if emisor == "" {
		return nil, fmt.Errorf("%w: falta la dirección del hub", ErrConfiguracion)
	}
	u, err := url.Parse(emisor)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%w: %q no es una dirección http(s)", ErrConfiguracion, o.Emisor)
	}
	if o.ClienteID == "" {
		return nil, fmt.Errorf("%w: falta el nombre con el que esta instancia está declarada", ErrConfiguracion)
	}
	if o.Vuelta == "" {
		return nil, fmt.Errorf("%w: falta la dirección de vuelta", ErrConfiguracion)
	}
	c := &Cliente{
		emisor: emisor, clienteID: o.ClienteID, vuelta: o.Vuelta,
		http: o.HTTP, ahora: o.Ahora,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 15 * time.Second}
	}
	if c.ahora == nil {
		c.ahora = time.Now
	}
	return c, nil
}

func (c *Cliente) Emisor() string    { return c.emisor }
func (c *Cliente) ClienteID() string { return c.clienteID }

// ------------------------------------------------------------------ PKCE

// Transaccion es lo que hay que recordar entre que se manda a alguien al hub
// y vuelve. Viaja firmada en una cookie: guardarla en memoria del servidor
// haría que dos instancias detrás de un balanceador no se entiendan.
type Transaccion struct {
	Verificador string `json:"v"`
	Estado      string `json:"e"`
	Nonce       string `json:"n"`
	// Volver es a dónde iba quien tuvo que entrar, para devolverlo ahí.
	Volver string `json:"r,omitempty"`
}

// NuevaTransaccion arma los tres secretos de una vuelta al hub.
func NuevaTransaccion(volver string) (Transaccion, error) {
	v, err := azar(32)
	if err != nil {
		return Transaccion{}, err
	}
	e, err := azar(16)
	if err != nil {
		return Transaccion{}, err
	}
	n, err := azar(16)
	if err != nil {
		return Transaccion{}, err
	}
	return Transaccion{Verificador: v, Estado: e, Nonce: n, Volver: volver}, nil
}

func azar(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("no se pudo generar azar: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// desafio es el verificador pasado por SHA-256: es lo único que viaja por el
// navegador. Quien intercepte el código no puede canjearlo sin el verificador,
// que nunca sale del servidor.
func desafio(verificador string) string {
	suma := sha256.Sum256([]byte(verificador))
	return base64.RawURLEncoding.EncodeToString(suma[:])
}

// URLAutorizar es a dónde hay que mandar el navegador para empezar.
func (c *Cliente) URLAutorizar(t Transaccion) string {
	q := url.Values{
		"client_id":             {c.clienteID},
		"redirect_uri":          {c.vuelta},
		"response_type":         {"code"},
		"scope":                 {"openid email"},
		"state":                 {t.Estado},
		"nonce":                 {t.Nonce},
		"code_challenge":        {desafio(t.Verificador)},
		"code_challenge_method": {"S256"},
	}
	return c.emisor + "/authorize?" + q.Encode()
}

// --------------------------------------------------------------- la vuelta

// Identidad es quién entró, ya verificado.
type Identidad struct {
	Sujeto string // el identificador que le da el hub
	Correo string
	Nombre string
	Rol    string // el rol que el hub le asignó para esta app
}

type respuestaToken struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TipoToken   string `json:"token_type"`
	ExpiraEn    int    `json:"expires_in"`
	Error       string `json:"error"`
	Detalle     string `json:"error_description"`
}

// ErrRechazado es cuando el hub dice que esta persona no entra: no tiene rol
// para esta app. No es una falla, es una respuesta.
var ErrRechazado = errors.New("el hub no le dio acceso a esta instancia")

// Canjear cambia el código por los tokens y los verifica. Devuelve quién
// entró, o el motivo por el que no.
func (c *Cliente) Canjear(ctx context.Context, codigo string, t Transaccion) (*Identidad, error) {
	cuerpo := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {codigo},
		"redirect_uri":  {c.vuelta},
		"client_id":     {c.clienteID},
		"code_verifier": {t.Verificador},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.emisor+"/token",
		strings.NewReader(cuerpo.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo hablar con el hub: %w", err)
	}
	defer res.Body.Close()
	crudo, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la respuesta del hub: %w", err)
	}
	var rt respuestaToken
	if err := json.Unmarshal(crudo, &rt); err != nil {
		return nil, fmt.Errorf("el hub contestó algo que no es JSON (%d)", res.StatusCode)
	}
	if rt.Error != "" {
		if rt.Error == "access_denied" {
			return nil, ErrRechazado
		}
		return nil, fmt.Errorf("el hub rechazó el canje: %s", rt.Error)
	}
	if res.StatusCode != http.StatusOK || rt.AccessToken == "" {
		return nil, fmt.Errorf("el hub no entregó un token (%d)", res.StatusCode)
	}

	claves, err := c.clavesDelHub(ctx)
	if err != nil {
		return nil, err
	}
	ahora := c.ahora()

	// El access token lleva el rol; el id_token, la identidad y el nonce que
	// ata esta respuesta al pedido que la empezó. Se verifican los dos: uno
	// solo dejaría la mitad sin comprobar.
	acceso, err := verificar(rt.AccessToken, claves, c.emisor,
		Exigencias{Audiencia: c.clienteID, SinNonce: true}, ahora)
	if err != nil {
		return nil, err
	}
	ident := &Identidad{Sujeto: acceso.Sujeto, Correo: acceso.Correo, Rol: acceso.Rol}

	if rt.IDToken != "" {
		id, err := verificar(rt.IDToken, claves, c.emisor,
			Exigencias{Audiencia: c.clienteID, Nonce: t.Nonce}, ahora)
		if err != nil {
			return nil, err
		}
		if id.Sujeto != acceso.Sujeto {
			return nil, fmt.Errorf("%w: los dos tokens hablan de personas distintas", ErrFirma)
		}
		if id.Correo != "" {
			ident.Correo = id.Correo
		}
		ident.Nombre = id.Nombre
		if ident.Rol == "" {
			ident.Rol = id.Rol
		}
	}
	if ident.Sujeto == "" {
		return nil, fmt.Errorf("%w: el token no dice de quién es", ErrFirma)
	}
	return ident, nil
}

// --------------------------------------------------------------- las claves

// vidaDeLasClaves es cuánto se guardan antes de volver a pedirlas. El hub
// puede rotar; una hora es lo que usan las demás apps de la suite.
const vidaDeLasClaves = time.Hour

func (c *Cliente) clavesDelHub(ctx context.Context) ([]clavePublica, error) {
	c.mu.Lock()
	if c.claves != nil && c.ahora().Sub(c.leidas) < vidaDeLasClaves {
		claves := c.claves
		c.mu.Unlock()
		return claves, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.emisor+"/jwks.json", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron pedir las claves del hub: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("el hub contestó %d al pedirle las claves", res.StatusCode)
	}
	var doc struct {
		Claves []clavePublica `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("las claves del hub no se pudieron leer: %w", err)
	}
	if len(doc.Claves) == 0 {
		return nil, errors.New("el hub no publica ninguna clave")
	}

	c.mu.Lock()
	c.claves, c.leidas = doc.Claves, c.ahora()
	c.mu.Unlock()
	return doc.Claves, nil
}
