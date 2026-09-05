package cuentas

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// La clave del proveedor de IA de cada persona.
//
// Es de quien la carga: la pone desde su cuenta, paga lo suyo y la puede
// sacar cuando quiera. notarum la guarda cifrada porque es una credencial
// ajena que da acceso a una cuenta con saldo, y guardarla en claro sería
// pedirle a alguien que confíe más de lo razonable en un servicio que se
// levanta con un docker run.

// claveDeClaveIA es donde vive la de cada persona.
func claveDeClaveIA(usuario string) string {
	return "cuentas/ia/" + NormalizarNombre(usuario)
}

// ErrSinClaveIA es no tener ninguna cargada.
var ErrSinClaveIA = errors.New("esta cuenta no tiene cargada una clave de IA")

// ErrClaveIAIlegible es que lo guardado no se puede descifrar. Pasa cuando el
// secreto de sesión cambió: lo que se cifró con el anterior no se abre con el
// nuevo, y hay que volver a cargarla.
var ErrClaveIAIlegible = errors.New("la clave guardada no se pudo descifrar")

// claveIAGuardada es lo que queda en el almacén.
type claveIAGuardada struct {
	// Cifrada es la clave, con el nonce adelante.
	Cifrada string `json:"cifrada"`
	// Pista son los últimos caracteres, para poder reconocerla sin mostrarla.
	Pista string `json:"pista"`
	// Proveedor queda anotado por si algún día hay más de uno.
	Proveedor string `json:"proveedor"`
}

// GuardarClaveIA cifra y guarda la clave de una persona.
func (r *Registro) GuardarClaveIA(usuario, clave string) error {
	clave = strings.TrimSpace(clave)
	if clave == "" {
		return errors.New("la clave viene vacía")
	}
	// Un techo por las dudas: una clave de proveedor son decenas de
	// caracteres, no un archivo.
	if len(clave) > 512 {
		return errors.New("eso es demasiado largo para ser una clave")
	}
	u, err := r.Usuario(usuario)
	if err != nil {
		return err
	}

	cifrada, err := r.cifrar(clave)
	if err != nil {
		return err
	}
	crudo, err := json.Marshal(claveIAGuardada{
		Cifrada: cifrada, Pista: pistaDe(clave), Proveedor: "openrouter",
	})
	if err != nil {
		return err
	}
	return r.alm.Guardar(claveDeClaveIA(u.Nombre), crudo, sinVencimiento)
}

// ClaveIA devuelve la clave en claro, para usarla en un pedido.
func (r *Registro) ClaveIA(usuario string) (string, error) {
	g, err := r.claveIAGuardada(usuario)
	if err != nil {
		return "", err
	}
	clave, err := r.descifrar(g.Cifrada)
	if err != nil {
		return "", ErrClaveIAIlegible
	}
	return clave, nil
}

// PistaClaveIA son los últimos caracteres de la clave cargada, para poder
// reconocerla sin mostrarla entera.
func (r *Registro) PistaClaveIA(usuario string) (string, bool) {
	g, err := r.claveIAGuardada(usuario)
	if err != nil {
		return "", false
	}
	return g.Pista, true
}

// TieneClaveIA dice si esta cuenta cargó una.
func (r *Registro) TieneClaveIA(usuario string) bool {
	_, err := r.claveIAGuardada(usuario)
	return err == nil
}

// BorrarClaveIA la saca. Es de quien la cargó, así que se la puede llevar.
func (r *Registro) BorrarClaveIA(usuario string) error {
	return r.alm.Borrar(claveDeClaveIA(usuario))
}

func (r *Registro) claveIAGuardada(usuario string) (claveIAGuardada, error) {
	var g claveIAGuardada
	crudo, hay := r.alm.Leer(claveDeClaveIA(usuario))
	if !hay {
		return g, ErrSinClaveIA
	}
	if err := json.Unmarshal(crudo, &g); err != nil || g.Cifrada == "" {
		return g, ErrClaveIAIlegible
	}
	return g, nil
}

// pistaDe deja ver lo justo para reconocer cuál se cargó: el arranque, que en
// OpenRouter dice de qué es, y las últimas cuatro. Nada más.
func pistaDe(clave string) string {
	if len(clave) <= 12 {
		return "…" + clave[len(clave)-2:]
	}
	return clave[:8] + "…" + clave[len(clave)-4:]
}

// ------------------------------------------------------------------ cifrado

// llaveDeCifrado deriva la llave AES del secreto del registro.
//
// Se separa del que firma las sesiones con una etiqueta: la misma clave para
// dos cosas distintas es la manera de que un descuido en una arruine la otra.
func (r *Registro) llaveDeCifrado() []byte {
	suma := sha256.Sum256(append([]byte("notarum/clave-ia/v1\x00"), r.secreto...))
	return suma[:]
}

func (r *Registro) cifrar(texto string) (string, error) {
	bloque, err := aes.NewCipher(r.llaveDeCifrado())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(bloque)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("no se pudo generar el nonce: %w", err)
	}
	// GCM además autentica: si alguien toca lo guardado, no descifra en vez
	// de devolver basura.
	sellado := gcm.Seal(nonce, nonce, []byte(texto), nil)
	return base64.RawStdEncoding.EncodeToString(sellado), nil
}

func (r *Registro) descifrar(guardado string) (string, error) {
	crudo, err := base64.RawStdEncoding.DecodeString(guardado)
	if err != nil {
		return "", err
	}
	bloque, err := aes.NewCipher(r.llaveDeCifrado())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(bloque)
	if err != nil {
		return "", err
	}
	if len(crudo) < gcm.NonceSize() {
		return "", errors.New("lo guardado es más corto que un nonce")
	}
	nonce, cuerpo := crudo[:gcm.NonceSize()], crudo[gcm.NonceSize():]
	abierto, err := gcm.Open(nil, nonce, cuerpo, nil)
	if err != nil {
		return "", err
	}
	return string(abierto), nil
}
