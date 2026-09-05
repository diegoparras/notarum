package cuentas

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"strings"
)

// El arranque: crear la primera cuenta sin abrir una consola.
//
// Es el problema del huevo y la gallina de cualquier servicio que se
// administra desde adentro. El panel pide una cuenta de administrador, y para
// crear la primera hacía falta entrar por consola al contenedor. Quien monta
// esto en un panel de deploy no tiene por qué hacer eso.
//
// Pero una instancia expuesta a internet no puede regalarle el administrador
// a quien pase primero. Así que la puerta pide un código que sólo ve quien
// puede leer el log del servicio —que es quien la levantó— y se cierra sola
// en cuanto existe la primera cuenta.

// claveCodigoArranque es donde vive el código mientras no haya nadie.
const claveCodigoArranque = "cuentas/_arranque"

// LargoCodigo son los caracteres del código. Con 16 de un alfabeto de 32 hay
// 80 bits: no se adivina, y se puede copiar del log sin equivocarse.
const LargoCodigo = 16

// alfabeto es base32 sin las letras que se confunden al copiarlas a mano.
var alfabeto = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

// ErrCodigoArranque es el código equivocado.
var ErrCodigoArranque = errors.New("el código de arranque no es ése")

// ErrYaHayCuentas es intentar el arranque cuando la puerta ya se cerró.
var ErrYaHayCuentas = errors.New("esta instancia ya tiene cuentas")

// CodigoDeArranque devuelve el código con el que se crea la primera cuenta,
// generándolo la primera vez.
//
// Devuelve vacío cuando ya hay cuentas: ahí no hay nada que arrancar.
func (r *Registro) CodigoDeArranque() (string, error) {
	if r.HayUsuarios() {
		return "", nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// El código se guarda para que sobreviva a un reinicio: si cambiara cada
	// vez, el que quedó anotado en el log dejaría de servir justo cuando hace
	// falta.
	if crudo, hay := r.alm.Leer(claveCodigoArranque); hay && len(crudo) > 2 {
		var guardado string
		if err := json.Unmarshal(crudo, &guardado); err == nil && guardado != "" {
			return guardado, nil
		}
	}

	b := make([]byte, 10) // 80 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	codigo := alfabeto.EncodeToString(b)[:LargoCodigo]
	crudo, err := json.Marshal(codigo)
	if err != nil {
		return "", err
	}
	if err := r.alm.Guardar(claveCodigoArranque, crudo, sinVencimiento); err != nil {
		return "", err
	}
	return codigo, nil
}

// Arrancar crea la primera cuenta, que administra.
//
// Falla si ya hay alguna: la puerta se abre una sola vez y no se vuelve a
// abrir ni borrando la cuenta que se creó.
func (r *Registro) Arrancar(nombre, clave, codigo string) (*Usuario, error) {
	if r.HayUsuarios() {
		return nil, ErrYaHayCuentas
	}
	esperado, err := r.CodigoDeArranque()
	if err != nil {
		return nil, err
	}
	// En tiempo constante: comparar con == deja medir cuánto se acertó.
	if esperado == "" || subtle.ConstantTimeCompare(
		[]byte(NormalizarCodigo(codigo)), []byte(esperado)) != 1 {
		return nil, ErrCodigoArranque
	}
	// La primera cuenta administra: es la que va a poner en marcha todo lo
	// demás.
	return r.CrearUsuario(nombre, clave, RolAdmin)
}

// NormalizarCodigo perdona cómo se copió: mayúsculas, espacios y los guiones
// que uno mete para leerlo.
func NormalizarCodigo(c string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(c)) {
		if r == ' ' || r == '-' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CodigoLegible parte el código en grupos de cuatro, para poder leerlo en voz
// alta o copiarlo del log sin perderse.
func CodigoLegible(c string) string {
	var b strings.Builder
	for i, r := range c {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}
