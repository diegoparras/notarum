package cuentas

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Almacen es lo que el registro necesita para persistir. Lo cumple cualquiera
// de los motores de notarum, así que las cuentas viven donde vive el resto.
type Almacen interface {
	Leer(clave string) ([]byte, bool)
	Guardar(clave string, datos []byte, ttl time.Duration) error
	Existe(clave string) bool
	Borrar(clave string) error
}

// sinVencimiento vale lo mismo que en el paquete almacen: no caduca.
const sinVencimiento time.Duration = 0

// DuracionSesion es cuánto vale una sesión del navegador antes de pedir la
// clave de nuevo.
const DuracionSesion = 30 * 24 * time.Hour

// Registro guarda usuarios y tokens, y firma las sesiones.
type Registro struct {
	alm     Almacen
	secreto []byte

	// mu ordena las operaciones que leen y escriben en dos pasos, como crear
	// un usuario o revocar un token: el almacén no tiene transacciones.
	mu sync.Mutex
}

// NuevoRegistro arma el registro. El secreto firma las cookies de sesión: si
// cambia, todas las sesiones abiertas dejan de valer, que es justamente lo que
// se quiere al rotarlo.
func NuevoRegistro(alm Almacen, secreto []byte) (*Registro, error) {
	if alm == nil {
		return nil, errors.New("el registro necesita dónde guardar")
	}
	if len(secreto) < 32 {
		return nil, errors.New("el secreto de sesión necesita al menos 32 bytes")
	}
	return &Registro{alm: alm, secreto: secreto}, nil
}

func claveUsuario(nombre string) string { return "cuentas/usuarios/" + nombre }
func claveToken(huella string) string   { return "cuentas/tokens/" + huella }
func claveIndiceTokens(dueño string) string {
	return "cuentas/indice-tokens/" + dueño
}

// -------------------------------------------------------------- usuarios

// HayUsuarios dice si alguien creó alguna cuenta. Mientras no haya ninguna,
// notarum se comporta como siempre: sin login y todo abierto.
func (r *Registro) HayUsuarios() bool {
	return r.alm.Existe("cuentas/_hay")
}

// CrearUsuario da de alta una cuenta.
func (r *Registro) CrearUsuario(nombre, clave string, rol Rol) (*Usuario, error) {
	nombre = NormalizarNombre(nombre)
	if err := ValidarNombre(nombre); err != nil {
		return nil, err
	}
	if !rol.Valido() {
		return nil, fmt.Errorf("rol inválido: %q", rol)
	}
	hash, err := NuevaClave(clave)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.alm.Existe(claveUsuario(nombre)) {
		return nil, fmt.Errorf("el usuario %q %w", nombre, ErrYaExiste)
	}
	u := &Usuario{Nombre: nombre, Rol: rol, Clave: hash, Creado: time.Now().UTC()}
	if err := r.guardarUsuario(u); err != nil {
		return nil, err
	}
	// La marca de que ya hay cuentas: es lo que enciende el login.
	if err := r.alm.Guardar("cuentas/_hay", []byte("true"), sinVencimiento); err != nil {
		return nil, err
	}
	return u, nil
}

func (r *Registro) guardarUsuario(u *Usuario) error {
	crudo, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return r.alm.Guardar(claveUsuario(u.Nombre), crudo, sinVencimiento)
}

// Usuario trae una cuenta por su nombre.
func (r *Registro) Usuario(nombre string) (*Usuario, error) {
	crudo, hay := r.alm.Leer(claveUsuario(NormalizarNombre(nombre)))
	if !hay {
		return nil, ErrNoExiste
	}
	var u Usuario
	if err := json.Unmarshal(crudo, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Autenticar verifica nombre y clave.
//
// Cuando el usuario no existe igual se deriva una clave, para que fallar por
// usuario inexistente y fallar por clave incorrecta tarden lo mismo: si no, el
// tiempo de respuesta revela qué cuentas existen.
func (r *Registro) Autenticar(nombre, clave string) (*Usuario, error) {
	u, err := r.Usuario(nombre)
	if err != nil || u.Externo {
		_, _ = derivar(clave, []byte("relleno-de-tiempo-constante"), iteracionesPorDefecto)
		return nil, ErrCredenciales
	}
	if !u.Clave.Verificar(clave) {
		return nil, ErrCredenciales
	}
	return u, nil
}

// CambiarClave reemplaza la clave de una cuenta.
func (r *Registro) CambiarClave(nombre, actual, nueva string) error {
	u, err := r.Autenticar(nombre, actual)
	if err != nil {
		return err
	}
	hash, err := NuevaClave(nueva)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u.Clave = hash
	return r.guardarUsuario(u)
}

// ---------------------------------------------------------------- tokens

// CrearToken hace un token nuevo y devuelve su valor, que es la única vez que
// se puede ver.
func (r *Registro) CrearToken(dueño, nombre string, alcance Alcance) (*Token, string, error) {
	dueño = NormalizarNombre(dueño)
	if _, err := r.Usuario(dueño); err != nil {
		return nil, "", err
	}
	nombre = strings.TrimSpace(nombre)
	if nombre == "" {
		return nil, "", errors.New("ponele un nombre al token, para saber cuál revocar después")
	}
	if len(nombre) > 60 {
		return nil, "", errors.New("el nombre del token es muy largo")
	}
	if !alcance.Valido() {
		return nil, "", fmt.Errorf("alcance inválido: %q", alcance)
	}

	valor, huella, prefijo, err := GenerarToken()
	if err != nil {
		return nil, "", err
	}
	id, err := IDNuevo()
	if err != nil {
		return nil, "", err
	}
	t := &Token{
		ID: id, Dueño: dueño, Nombre: nombre, Alcance: alcance,
		Huella: huella, Prefijo: prefijo, Creado: time.Now().UTC(),
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	crudo, err := json.Marshal(t)
	if err != nil {
		return nil, "", err
	}
	if err := r.alm.Guardar(claveToken(huella), crudo, sinVencimiento); err != nil {
		return nil, "", err
	}
	if err := r.agregarAlIndice(dueño, huella); err != nil {
		return nil, "", err
	}
	return t, valor, nil
}

// agregarAlIndice mantiene la lista de tokens de cada persona: el almacén
// busca por clave exacta, no sabe recorrer.
func (r *Registro) agregarAlIndice(dueño, huella string) error {
	huellas := r.indice(dueño)
	for _, h := range huellas {
		if h == huella {
			return nil
		}
	}
	huellas = append(huellas, huella)
	crudo, err := json.Marshal(huellas)
	if err != nil {
		return err
	}
	return r.alm.Guardar(claveIndiceTokens(dueño), crudo, sinVencimiento)
}

func (r *Registro) indice(dueño string) []string {
	crudo, hay := r.alm.Leer(claveIndiceTokens(dueño))
	if !hay {
		return nil
	}
	var huellas []string
	if err := json.Unmarshal(crudo, &huellas); err != nil {
		return nil
	}
	return huellas
}

// Tokens lista los tokens de una persona, incluidos los revocados: saber que
// algo se revocó y cuándo es parte de la información.
func (r *Registro) Tokens(dueño string) []Token {
	dueño = NormalizarNombre(dueño)
	var out []Token
	for _, huella := range r.indice(dueño) {
		if t := r.tokenPorHuella(huella); t != nil {
			out = append(out, *t)
		}
	}
	return out
}

func (r *Registro) tokenPorHuella(huella string) *Token {
	crudo, hay := r.alm.Leer(claveToken(huella))
	if !hay {
		return nil
	}
	var t Token
	if err := json.Unmarshal(crudo, &t); err != nil {
		return nil
	}
	return &t
}

// VerificarToken valida el valor que llegó en un pedido.
//
// Devuelve el token y su dueño. Anota el último uso, que es lo que permite
// darse cuenta de cuáles ya no se usan y conviene revocar.
func (r *Registro) VerificarToken(valor string, alcance Alcance) (*Token, *Usuario, error) {
	if valor == "" {
		return nil, nil, ErrNoExiste
	}
	t := r.tokenPorHuella(Huella(valor))
	if t == nil {
		return nil, nil, ErrNoExiste
	}
	if !t.Activo() {
		return nil, nil, ErrRevocado
	}
	if alcance != "" && t.Alcance != alcance {
		return nil, nil, fmt.Errorf("este token es para %s, no para %s", t.Alcance, alcance)
	}
	u, err := r.Usuario(t.Dueño)
	if err != nil {
		// El dueño ya no existe: el token no vale más.
		return nil, nil, ErrNoExiste
	}
	r.anotarUso(t)
	return t, u, nil
}

// anotarUso guarda cuándo se usó por última vez, a lo sumo una vez por hora:
// no vale la pena escribir en cada pedido.
func (r *Registro) anotarUso(t *Token) {
	ahora := time.Now().UTC()
	if t.UltimoUso != nil && ahora.Sub(*t.UltimoUso) < time.Hour {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.UltimoUso = &ahora
	if crudo, err := json.Marshal(t); err == nil {
		_ = r.alm.Guardar(claveToken(t.Huella), crudo, sinVencimiento)
	}
}

// RevocarToken deja un token sin efecto. No se borra: queda el registro de que
// existió y de cuándo se dio de baja.
func (r *Registro) RevocarToken(dueño, id string) error {
	dueño = NormalizarNombre(dueño)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, huella := range r.indice(dueño) {
		t := r.tokenPorHuella(huella)
		if t == nil || t.ID != id {
			continue
		}
		if !t.Activo() {
			return nil // revocar dos veces es inocuo
		}
		ahora := time.Now().UTC()
		t.Revocado = &ahora
		crudo, err := json.Marshal(t)
		if err != nil {
			return err
		}
		return r.alm.Guardar(claveToken(huella), crudo, sinVencimiento)
	}
	return ErrNoExiste
}

// -------------------------------------------------------------- sesiones

// FirmarSesion arma el valor de la cookie: el nombre, hasta cuándo vale, y una
// firma que ata las dos cosas.
func (r *Registro) FirmarSesion(nombre string, hasta time.Time) string {
	cuerpo := nombre + "|" + strconv.FormatInt(hasta.Unix(), 10)
	return cuerpo + "|" + r.firma(cuerpo)
}

// LeerSesion valida la cookie y devuelve de quién es.
func (r *Registro) LeerSesion(valor string) (*Usuario, error) {
	partes := strings.Split(valor, "|")
	if len(partes) != 3 {
		return nil, ErrCredenciales
	}
	cuerpo := partes[0] + "|" + partes[1]
	// La firma se compara antes que nada: hasta la fecha viene de afuera.
	if !hmac.Equal([]byte(r.firma(cuerpo)), []byte(partes[2])) {
		return nil, ErrCredenciales
	}
	vence, err := strconv.ParseInt(partes[1], 10, 64)
	if err != nil || time.Now().Unix() > vence {
		return nil, ErrCredenciales
	}
	return r.Usuario(partes[0])
}

func (r *Registro) firma(cuerpo string) string {
	m := hmac.New(sha256.New, r.secreto)
	m.Write([]byte(cuerpo))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// EntrarFederado da por buena una identidad que verificó un proveedor de
// identidad y devuelve la cuenta local que le corresponde, creándola si es la
// primera vez.
//
// El rol viene del hub y se pisa en cada entrada: la matriz de accesos de allá
// es la que manda, así que quitarle un rol a alguien tiene efecto la próxima
// vez que entra y no hay que acordarse de tocar también acá.
//
// La cuenta queda marcada como externa y sin clave: no se puede entrar a ella
// por el formulario, sólo por el hub.
func (r *Registro) EntrarFederado(correo string, rol Rol) (*Usuario, error) {
	correo = NormalizarNombre(correo)
	if err := ValidarNombreExterno(correo); err != nil {
		return nil, err
	}
	if !rol.Valido() {
		return nil, fmt.Errorf("rol inválido: %q", rol)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if crudo, hay := r.alm.Leer(claveUsuario(correo)); hay {
		var u Usuario
		if err := json.Unmarshal(crudo, &u); err != nil {
			return nil, fmt.Errorf("la cuenta de %q está ilegible: %w", correo, err)
		}
		// Una cuenta local con este nombre no puede existir —no se acepta la
		// arroba al crearlas—, pero si alguna vez existiera, dejar que el hub
		// la tomara sería regalarla.
		if !u.Externo {
			return nil, fmt.Errorf("%q ya es una cuenta local: %w", correo, ErrYaExiste)
		}
		if u.Rol != rol {
			u.Rol = rol
			if err := r.guardarUsuario(&u); err != nil {
				return nil, err
			}
		}
		return &u, nil
	}

	u := &Usuario{Nombre: correo, Rol: rol, Creado: time.Now().UTC(), Externo: true}
	if err := r.guardarUsuario(u); err != nil {
		return nil, err
	}
	if err := r.alm.Guardar("cuentas/_hay", []byte("true"), sinVencimiento); err != nil {
		return nil, err
	}
	return u, nil
}

// ------------------------------------------------------------- transacciones

// Sellar firma un texto que va a dar una vuelta por afuera y volver —hoy, los
// secretos del login federado, que viajan en una cookie.
//
// El propósito entra en la firma: un sello hecho para una cosa no vale para
// otra, así que una transacción a medio hacer no puede presentarse como una
// sesión abierta.
func (r *Registro) Sellar(proposito, cuerpo string, hasta time.Time) string {
	base := proposito + "|" + strconv.FormatInt(hasta.Unix(), 10) + "|" +
		base64.RawURLEncoding.EncodeToString([]byte(cuerpo))
	return base + "|" + r.firma(base)
}

// Abrir devuelve lo que se selló, si la firma cierra, el propósito es el
// mismo y todavía no venció.
func (r *Registro) Abrir(proposito, valor string) (string, error) {
	partes := strings.Split(valor, "|")
	if len(partes) != 4 {
		return "", ErrCredenciales
	}
	base := strings.Join(partes[:3], "|")
	if !hmac.Equal([]byte(r.firma(base)), []byte(partes[3])) {
		return "", ErrCredenciales
	}
	if partes[0] != proposito {
		return "", ErrCredenciales
	}
	vence, err := strconv.ParseInt(partes[1], 10, 64)
	if err != nil || time.Now().Unix() > vence {
		return "", ErrCredenciales
	}
	cuerpo, err := base64.RawURLEncoding.DecodeString(partes[2])
	if err != nil {
		return "", ErrCredenciales
	}
	return string(cuerpo), nil
}
