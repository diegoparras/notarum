package web

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/cuentas"
)

// El arranque: crear la primera cuenta desde el navegador.
//
// Sin esto, la primera cuenta se creaba por consola y el panel quedaba
// inalcanzable para quien montara la instancia en un panel de deploy. La
// puerta pide el código que el servicio imprime en su log al arrancar —que lo
// ve quien lo levantó— y se cierra sola en cuanto existe una cuenta.

type datosArranque struct {
	comun
	Usuario string
	Error   string
	// Listo se enciende cuando la cuenta quedó creada.
	Listo bool
}

// hayQueArrancar dice si esta instancia está esperando su primera cuenta.
func (s *Sitio) hayQueArrancar() bool {
	return s.registro != nil && !s.registro.HayUsuarios()
}

func (s *Sitio) verArranque(w http.ResponseWriter, r *http.Request) {
	if s.registro == nil {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene cuentas",
			"El registro de cuentas está apagado.")
		return
	}
	if !s.hayQueArrancar() {
		// La puerta ya se cerró: quien llegue acá es porque quiere entrar.
		http.Redirect(w, r, "/entrar", http.StatusFound)
		return
	}
	d := datosArranque{comun: s.baseCon(r, "", "")}
	d.Angosto = true
	s.mostrar(w, r, "arranque", d, http.StatusOK)
}

func (s *Sitio) hacerArranque(w http.ResponseWriter, r *http.Request) {
	if s.registro == nil || !s.hayQueArrancar() {
		http.Redirect(w, r, "/entrar", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}
	nombre := r.PostFormValue("usuario")
	clave := r.PostFormValue("clave")

	fallar := func(msg string) {
		d := datosArranque{comun: s.baseCon(r, "", ""), Usuario: nombre, Error: msg}
		d.Angosto = true
		s.mostrar(w, r, "arranque", d, http.StatusBadRequest)
	}
	// Se pide dos veces porque no se puede recuperar: si quedó con un error
	// de tipeo, la única salida es borrar el almacén.
	if clave != r.PostFormValue("clave2") {
		fallar("Las dos claves no son iguales.")
		return
	}

	u, err := s.registro.Arrancar(nombre, clave, r.PostFormValue("codigo"))
	switch {
	case errors.Is(err, cuentas.ErrCodigoArranque):
		// Sin detalle de cuánto se acertó, y anotado: es el único intento de
		// quedarse con una instancia nueva que existe.
		slog.Warn("código de arranque equivocado", "ip", IPDe(r), "usuario", nombre)
		fallar("El código de arranque no es ése. Está en el log del servicio, en la línea que dice «primera cuenta».")
		return
	case errors.Is(err, cuentas.ErrYaHayCuentas):
		http.Redirect(w, r, "/entrar", http.StatusSeeOther)
		return
	case err != nil:
		fallar(primeraMayuscula(err.Error()) + ".")
		return
	}
	slog.Info("primera cuenta creada", "usuario", u.Nombre, "ip", IPDe(r))

	// Queda con la sesión abierta: acaba de demostrar que puede leer el log
	// del servicio, y pedirle que entre de nuevo no agrega nada.
	hasta := time.Now().Add(cuentas.DuracionSesion)
	http.SetCookie(w, &http.Cookie{
		Name:     nombreCookie,
		Value:    s.registro.FirmarSesion(u.Nombre, hasta),
		Path:     "/",
		Expires:  hasta,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   esHTTPS(r),
	})
	// Al panel, que es lo que sigue: poner en marcha las fuentes.
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// IPDe dice de dónde vino un pedido, mirando primero lo que ponen los proxys.
//
// EasyPanel mete Traefik adelante, así que RemoteAddr es el del proxy y no el
// de quien pide. Sin esto, todos los pedidos vendrían de la misma dirección y
// el límite por IP sería un límite para todos juntos.
func IPDe(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			v = v[:i]
		}
		if ip := strings.TrimSpace(v); ip != "" {
			return ip
		}
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
