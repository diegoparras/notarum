package almacen

import (
	"fmt"
	"net/url"
	"strings"
)

// DatosConexion son las piezas sueltas de una conexión a Postgres, para quien
// prefiere configurarlas por separado en vez de armar la cadena a mano. Es el
// mismo estilo que usa n8n con sus DB_POSTGRESDB_*.
type DatosConexion struct {
	Host    string
	Puerto  string
	Base    string
	Usuario string
	Clave   string
	SSLMode string // disable, require, verify-full…
}

// ArmarDSN convierte las piezas sueltas en una cadena de conexión.
//
// La clave se escapa: una contraseña con # o / rompe la cadena si se pega a
// mano, y es de los errores más molestos de diagnosticar porque el mensaje que
// devuelve el driver no lo dice.
func ArmarDSN(d DatosConexion) (string, error) {
	if strings.TrimSpace(d.Host) == "" {
		return "", fmt.Errorf("falta el host de Postgres")
	}
	if strings.TrimSpace(d.Base) == "" {
		return "", fmt.Errorf("falta el nombre de la base de Postgres")
	}
	if d.Puerto == "" {
		d.Puerto = "5432"
	}
	if d.SSLMode == "" {
		// En una red interna de EasyPanel o de compose no hay TLS entre
		// contenedores; quien exponga la base afuera debería pedir require.
		d.SSLMode = "disable"
	}

	u := &url.URL{
		Scheme: "postgres",
		Host:   d.Host + ":" + d.Puerto,
		Path:   "/" + d.Base,
	}
	if d.Usuario != "" {
		if d.Clave != "" {
			u.User = url.UserPassword(d.Usuario, d.Clave)
		} else {
			u.User = url.User(d.Usuario)
		}
	}
	q := url.Values{}
	q.Set("sslmode", d.SSLMode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OcultarClave deja una cadena de conexión mostrable en un log o en una
// pantalla de estado, sin la contraseña.
func OcultarClave(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, hay := u.User.Password(); hay {
		u.User = url.UserPassword(u.User.Username(), "···")
	}
	return u.String()
}
