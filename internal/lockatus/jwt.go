package lockatus

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Afirmaciones es lo que el hub dice de quien entró. Los nombres son los del
// protocolo —vienen así en el token— y no se traducen: cambiarlos sería
// inventar un formato propio.
type Afirmaciones struct {
	Emisor    string `json:"iss"`
	Sujeto    string `json:"sub"`
	Audiencia any    `json:"aud"` // puede venir suelta o como lista
	Correo    string `json:"email"`
	Nombre    string `json:"name"`
	Rol       string `json:"role"`
	App       string `json:"app"`
	Org       any    `json:"org"`
	Nonce     string `json:"nonce"`
	Expira    int64  `json:"exp"`
	Emitido   int64  `json:"iat"`
}

// audiencias normaliza el claim aud, que el protocolo deja mandar de las dos
// formas: un texto o una lista.
func (a Afirmaciones) audiencias() []string {
	switch v := a.Audiencia.(type) {
	case string:
		return []string{v}
	case []any:
		var s []string
		for _, x := range v {
			if t, ok := x.(string); ok {
				s = append(s, t)
			}
		}
		return s
	}
	return nil
}

// cabecera es lo que va antes del punto: qué algoritmo y con qué clave.
type cabecera struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// clavePublica es una clave del JWKS del hub. Sólo se entienden claves RSA:
// es lo que Lockatus firma, y aceptar otra cosa sin querer sería aceptar
// cualquier firma.
type clavePublica struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Uso string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (k clavePublica) rsa() (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("clave de tipo %q: sólo se entienden claves RSA", k.Kty)
	}
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("el módulo de la clave no se pudo leer: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("el exponente de la clave no se pudo leer: %w", err)
	}
	if len(e) == 0 || len(e) > 8 {
		return nil, errors.New("el exponente de la clave tiene un largo imposible")
	}
	// Una clave más corta que 2048 bits no da garantías hoy, y aceptarla sería
	// aceptar que alguien la reemplace por una que se pueda romper.
	if len(n) < 256 {
		return nil, fmt.Errorf("la clave es de %d bits: hacen falta 2048", len(n)*8)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(new(big.Int).SetBytes(e).Int64()),
	}, nil
}

var (
	// ErrFirma es lo único que se le cuenta a quien manda un token que no
	// cierra. Adentro se distingue el motivo, para el registro del servidor.
	ErrFirma = errors.New("el token del hub no se pudo verificar")
	// ErrVencido separa el caso más común y más benigno: entrar de nuevo lo
	// arregla.
	ErrVencido = errors.New("el token del hub venció")
)

// Exigencias son las condiciones que además de la firma tiene que cumplir el
// token. Ninguna es opcional por comodidad: un token bien firmado pero para
// otra app, o de otra transacción, no sirve.
type Exigencias struct {
	Audiencia string // el slug de esta app
	Nonce     string // el de la transacción que se está cerrando
	// SinNonce se pone en el access token, que no lo lleva.
	SinNonce bool
}

// verificar comprueba la firma contra las claves del hub y después los
// claims. El orden importa: hasta que la firma no cierra, todo lo que dice el
// token lo escribió cualquiera.
func verificar(token string, claves []clavePublica, emisor string, e Exigencias, ahora time.Time) (*Afirmaciones, error) {
	partes := strings.Split(token, ".")
	if len(partes) != 3 {
		return nil, fmt.Errorf("%w: no tiene las tres partes", ErrFirma)
	}
	crudoCab, err := base64.RawURLEncoding.DecodeString(partes[0])
	if err != nil {
		return nil, fmt.Errorf("%w: la cabecera no se pudo leer", ErrFirma)
	}
	var cab cabecera
	if err := json.Unmarshal(crudoCab, &cab); err != nil {
		return nil, fmt.Errorf("%w: la cabecera no es JSON", ErrFirma)
	}
	// Sólo RS256. Aceptar el algoritmo que diga el token es el agujero clásico
	// de JWT: con "none" o con HMAC, la firma la puede armar cualquiera.
	if cab.Alg != "RS256" {
		return nil, fmt.Errorf("%w: algoritmo %q, se esperaba RS256", ErrFirma, cab.Alg)
	}

	firma, err := base64.RawURLEncoding.DecodeString(partes[2])
	if err != nil {
		return nil, fmt.Errorf("%w: la firma no se pudo leer", ErrFirma)
	}
	digesto := sha256.Sum256([]byte(partes[0] + "." + partes[1]))

	if err := probarClaves(claves, cab.Kid, digesto[:], firma); err != nil {
		return nil, err
	}

	crudoCuerpo, err := base64.RawURLEncoding.DecodeString(partes[1])
	if err != nil {
		return nil, fmt.Errorf("%w: el cuerpo no se pudo leer", ErrFirma)
	}
	var a Afirmaciones
	if err := json.Unmarshal(crudoCuerpo, &a); err != nil {
		return nil, fmt.Errorf("%w: el cuerpo no es JSON", ErrFirma)
	}
	if err := revisarClaims(a, emisor, e, ahora); err != nil {
		return nil, err
	}
	return &a, nil
}

// probarClaves busca la que dice el kid. Si el token no trae kid, se prueban
// todas: el hub puede estar rotando y tener dos publicadas.
func probarClaves(claves []clavePublica, kid string, digesto, firma []byte) error {
	var candidatas []clavePublica
	for _, k := range claves {
		if kid == "" || k.Kid == kid {
			candidatas = append(candidatas, k)
		}
	}
	if len(candidatas) == 0 {
		return fmt.Errorf("%w: el hub no publica la clave %q", ErrFirma, kid)
	}
	// Una clave que no se entiende no invalida a las demás, pero si al final
	// ninguna sirvió hay que contar por qué: "no coincide la firma" mandaría a
	// buscar el problema donde no está.
	var rechazada error
	for _, k := range candidatas {
		pub, err := k.rsa()
		if err != nil {
			rechazada = err
			continue
		}
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digesto, firma) == nil {
			return nil
		}
	}
	if rechazada != nil {
		return fmt.Errorf("%w: no se pudo usar la clave del hub: %w", ErrFirma, rechazada)
	}
	return fmt.Errorf("%w: la firma no coincide con ninguna clave del hub", ErrFirma)
}

func revisarClaims(a Afirmaciones, emisor string, e Exigencias, ahora time.Time) error {
	if a.Emisor != emisor {
		return fmt.Errorf("%w: lo emitió %q y no %q", ErrFirma, a.Emisor, emisor)
	}
	if e.Audiencia != "" {
		var esPara bool
		for _, aud := range a.audiencias() {
			if aud == e.Audiencia {
				esPara = true
				break
			}
		}
		if !esPara {
			return fmt.Errorf("%w: es para %v y no para %q", ErrFirma, a.audiencias(), e.Audiencia)
		}
	}
	if a.Expira == 0 {
		return fmt.Errorf("%w: no dice cuándo vence", ErrFirma)
	}
	// Un poco de tolerancia por los relojes, que nunca están del todo iguales.
	if ahora.After(time.Unix(a.Expira, 0).Add(tolerancia)) {
		return ErrVencido
	}
	if a.Emitido != 0 && ahora.Add(tolerancia).Before(time.Unix(a.Emitido, 0)) {
		return fmt.Errorf("%w: dice haber sido emitido en el futuro", ErrFirma)
	}
	if !e.SinNonce {
		if e.Nonce == "" {
			return fmt.Errorf("%w: no hay con qué comparar el nonce", ErrFirma)
		}
		if a.Nonce != e.Nonce {
			return fmt.Errorf("%w: el nonce es de otra transacción", ErrFirma)
		}
	}
	return nil
}

// tolerancia es lo que se le perdona a los relojes de las dos máquinas.
const tolerancia = 60 * time.Second
