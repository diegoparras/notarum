// Package cuentas maneja quién entra y con qué permisos.
//
// notarum es abierto: leer el Boletín no pide nada y eso no cambia. Las
// cuentas existen para otra cosa — que alguien pueda pedir un token, subir su
// límite de pedidos y usar el MCP si está reservado. Sin cuentas configuradas,
// el servicio funciona exactamente igual que antes.
package cuentas

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrCredenciales es lo único que se le dice a quien falla el login: no
	// hay que revelar si el usuario existe o si la clave estaba mal.
	ErrCredenciales = errors.New("usuario o clave incorrectos")
	ErrNoExiste     = errors.New("no existe")
	ErrYaExiste     = errors.New("ya existe")
	ErrRevocado     = errors.New("revocado")
)

// Rol define qué puede hacer alguien.
type Rol string

const (
	// RolAdmin puede administrar usuarios además de sus propios tokens.
	RolAdmin Rol = "admin"
	// RolPersona puede manejar sus tokens y nada más.
	RolPersona Rol = "persona"
)

func (r Rol) Valido() bool { return r == RolAdmin || r == RolPersona }

// Usuario es alguien que puede entrar.
type Usuario struct {
	Nombre string    `json:"nombre"`
	Rol    Rol       `json:"rol"`
	Clave  Clave     `json:"clave"`
	Creado time.Time `json:"creado"`
	// Externo marca a quien entró por un proveedor de identidad y no tiene
	// clave propia acá.
	Externo bool `json:"externo,omitempty"`
}

// Alcance dice para qué sirve un token.
type Alcance string

const (
	AlcanceAPI Alcance = "api"
	AlcanceMCP Alcance = "mcp"
)

func (a Alcance) Valido() bool { return a == AlcanceAPI || a == AlcanceMCP }

// Token es una credencial de programa. Del valor sólo se guarda su huella:
// quien lo pierde no lo recupera, lo revoca y hace otro.
type Token struct {
	ID        string     `json:"id"`
	Dueño     string     `json:"dueño"`
	Nombre    string     `json:"nombre"`
	Alcance   Alcance    `json:"alcance"`
	Huella    string     `json:"huella"`
	Prefijo   string     `json:"prefijo"` // los primeros caracteres, para reconocerlo en la lista
	Creado    time.Time  `json:"creado"`
	UltimoUso *time.Time `json:"ultimo_uso,omitempty"`
	Revocado  *time.Time `json:"revocado,omitempty"`
}

func (t Token) Activo() bool { return t.Revocado == nil }

// ---------------------------------------------------------------- claves

// Clave guarda una contraseña en forma verificable pero no reversible.
type Clave struct {
	Algoritmo   string `json:"algoritmo"`
	Sal         string `json:"sal"`
	Iteraciones int    `json:"iteraciones"`
	Hash        string `json:"hash"`
}

// String tapa el hash y la sal. Una struct se imprime sola con más facilidad
// de la que uno cree —un %v en un log, un {{.}} en una plantilla— y lo que
// salga de acá puede terminar en un archivo o en una página. Con el hash y la
// sal a la vista, la clave se puede romper sin tocar el servicio.
func (c Clave) String() string { return c.Algoritmo + ":oculta" }

// String hace lo mismo con el usuario, que lleva la clave adentro.
func (u Usuario) String() string { return u.Nombre + " (" + string(u.Rol) + ")" }

// LargoMinimoClave es el piso. Se prefiere una frase larga a una corta con
// símbolos: lo que hace fuerte a una clave es su longitud.
const LargoMinimoClave = 12

// iteracionesPorDefecto sigue la recomendación de OWASP para PBKDF2 con
// SHA-256. Se guarda en cada clave para poder subirlo sin invalidar las
// existentes.
const iteracionesPorDefecto = 600_000

// NuevaClave deriva una clave nueva con sal aleatoria.
func NuevaClave(texto string) (Clave, error) {
	if err := ValidarClave(texto); err != nil {
		return Clave{}, err
	}
	sal := make([]byte, 16)
	if _, err := rand.Read(sal); err != nil {
		return Clave{}, err
	}
	hash, err := derivar(texto, sal, iteracionesPorDefecto)
	if err != nil {
		return Clave{}, err
	}
	return Clave{
		Algoritmo:   "pbkdf2-sha256",
		Sal:         base64.RawStdEncoding.EncodeToString(sal),
		Iteraciones: iteracionesPorDefecto,
		Hash:        base64.RawStdEncoding.EncodeToString(hash),
	}, nil
}

// Verificar dice si el texto corresponde a esta clave. La comparación es de
// tiempo constante para no filtrar información por lo que tarda.
func (c Clave) Verificar(texto string) bool {
	if c.Hash == "" || c.Iteraciones <= 0 {
		return false
	}
	sal, err := base64.RawStdEncoding.DecodeString(c.Sal)
	if err != nil {
		return false
	}
	esperado, err := base64.RawStdEncoding.DecodeString(c.Hash)
	if err != nil {
		return false
	}
	calculado, err := derivar(texto, sal, c.Iteraciones)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(calculado, esperado) == 1
}

// ValidarClave rechaza lo que no sirve como clave, con un motivo que se pueda
// mostrar.
func ValidarClave(texto string) error {
	if len([]rune(texto)) < LargoMinimoClave {
		return fmt.Errorf("la clave necesita al menos %d caracteres", LargoMinimoClave)
	}
	if strings.TrimSpace(texto) == "" {
		return errors.New("la clave no puede ser sólo espacios")
	}
	return nil
}

// ---------------------------------------------------------------- nombres

// ValidarNombre acepta lo que sirve como nombre de usuario.
//
// A propósito se limita a ASCII, aunque el resto de notarum esté en castellano
// con todos sus acentos: un nombre de cuenta viaja por URLs y archivos de
// configuración, y dos nombres que se ven iguales pero se escriben distinto
// —una ñ, una á, un cirílico parecido a una a— son dos cuentas distintas para
// la máquina y la misma para quien mira.
func ValidarNombre(nombre string) error {
	n := strings.TrimSpace(nombre)
	if len(n) < 3 || len(n) > 32 {
		return errors.New("el nombre necesita entre 3 y 32 caracteres")
	}
	for _, r := range n {
		esLetra := r >= 'a' && r <= 'z'
		esDigito := r >= '0' && r <= '9'
		if !esLetra && !esDigito && r != '.' && r != '-' && r != '_' {
			return errors.New("el nombre lleva minúsculas sin acentos, números, punto, guion o guion bajo")
		}
	}
	return nil
}

// NormalizarNombre deja el nombre como se guarda, para que "Diego" y "diego"
// no sean dos cuentas distintas.
func NormalizarNombre(nombre string) string {
	return strings.ToLower(strings.TrimSpace(nombre))
}

// ----------------------------------------------------------------- tokens

// PrefijoToken abre todos los tokens, para reconocerlos de un vistazo cuando
// aparecen en un log o en un archivo de configuración ajeno.
const PrefijoToken = "ntrm_"

// GenerarToken devuelve el valor que se le muestra una única vez a la persona
// y la huella que se guarda.
//
// El valor tiene 32 bytes de azar. Con esa entropía no hace falta derivarlo
// con PBKDF2 como una clave: un SHA-256 alcanza, porque no hay diccionario
// que sirva para adivinarlo.
func GenerarToken() (valor string, huella string, prefijo string, err error) {
	crudo := make([]byte, 32)
	if _, err := rand.Read(crudo); err != nil {
		return "", "", "", err
	}
	valor = PrefijoToken + base64.RawURLEncoding.EncodeToString(crudo)
	return valor, Huella(valor), valor[:len(PrefijoToken)+6], nil
}

// Huella es lo que se guarda de un token.
func Huella(valor string) string {
	suma := sha256.Sum256([]byte(valor))
	return hex.EncodeToString(suma[:])
}

// IDNuevo arma un identificador corto para un token.
func IDNuevo() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// TokenDeCabecera saca el valor de un "Authorization: Bearer ...".
func TokenDeCabecera(cabecera string) string {
	c := strings.TrimSpace(cabecera)
	if len(c) < 7 || !strings.EqualFold(c[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(c[7:])
}

// derivar es la función de derivación, aparte para poder cambiarla en un solo
// lugar si algún día PBKDF2 deja de alcanzar.
func derivar(texto string, sal []byte, iteraciones int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, texto, sal, iteraciones, 32)
}
