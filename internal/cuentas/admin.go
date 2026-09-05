package cuentas

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// El administrador de la instancia, que sale de la configuración.
//
// Es el mismo trato que hace Lockatus con LOCKATUS_ADMIN_PASS: si la clave
// está en el entorno, esa es; si no está, se genera una y se imprime en el
// log una sola vez. Así una instancia recién montada tiene con qué entrar sin
// que nadie abra una consola, y quien la opera no queda nunca afuera: cambiar
// la clave en el entorno y reiniciar la vuelve a poner.

// UsuarioAdminPorDefecto es el nombre de la cuenta que administra si no se
// configura otro.
const UsuarioAdminPorDefecto = "admin"

// AsegurarAdmin deja lista la cuenta que administra.
//
// Devuelve la clave sólo cuando la generó, para poder imprimirla una vez. Con
// una clave configurada devuelve vacío: ya la sabe quien la puso, y repetirla
// en el log sería dejarla ahí escrita.
func (r *Registro) AsegurarAdmin(nombre, clave string) (generada string, err error) {
	nombre = NormalizarNombre(nombre)
	if nombre == "" {
		nombre = UsuarioAdminPorDefecto
	}
	if err := ValidarNombre(nombre); err != nil {
		return "", fmt.Errorf("el usuario del administrador no sirve: %w", err)
	}

	_, err = r.Usuario(nombre)
	switch {
	case err == nil:
		// Ya existe. Si hay clave configurada se pone, que es lo que evita
		// quedarse afuera: se cambia en el entorno y se reinicia.
		if clave == "" {
			return "", nil
		}
		if err := r.PonerClave(nombre, clave); err != nil {
			return "", err
		}
		return "", nil
	case !errors.Is(err, ErrNoExiste):
		return "", err
	}

	// No existe: se crea.
	if clave == "" {
		clave, err = claveAlAzar()
		if err != nil {
			return "", err
		}
		generada = clave
	}
	if _, err := r.CrearUsuario(nombre, clave, RolAdmin); err != nil {
		return "", err
	}
	return generada, nil
}

// claveAlAzar arma una clave que nadie va a adivinar y que se puede copiar de
// un log sin equivocarse.
func claveAlAzar() (string, error) {
	b := make([]byte, 18) // 144 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	c := base64.RawURLEncoding.EncodeToString(b)
	// En grupos, para poder leerla y copiarla a mano.
	var partes []string
	for i := 0; i < len(c); i += 6 {
		fin := i + 6
		if fin > len(c) {
			fin = len(c)
		}
		partes = append(partes, c[i:fin])
	}
	return strings.Join(partes, "-"), nil
}

// PonerClave cambia la clave de una cuenta sin pedir la anterior. Es para la
// configuración del servicio, no para la interfaz: ahí se pide la que había.
func (r *Registro) PonerClave(nombre, clave string) error {
	hash, err := NuevaClave(clave)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u, err := r.Usuario(nombre)
	if err != nil {
		return err
	}
	// Una cuenta federada no tiene clave propia: ponerle una sería abrir una
	// puerta que el hub no controla.
	if u.Externo {
		return errors.New("esa cuenta entra por el proveedor de identidad")
	}
	u.Clave = hash
	return r.guardarUsuario(u)
}
