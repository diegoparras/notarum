package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/lockatus"
)

// La federación con Lockatus, el hub de identidad de la suite Escriba.
//
// Es opcional y va al lado del login propio, no en su lugar: una instancia
// que no forma parte de ninguna suite no tiene por qué depender de un hub, y
// una que sí puede querer conservar alguna cuenta local para los programas.

const (
	// nombreCookieOIDC guarda los secretos de una vuelta al hub. Es aparte de
	// la de sesión y dura poco: sólo tiene que sobrevivir el viaje.
	nombreCookieOIDC = "notarum_oidc"
	// propositoOIDC entra en la firma del sello, así que una transacción a
	// medio hacer no puede presentarse como una sesión abierta.
	propositoOIDC = "oidc"
	// duracionTransaccion es lo que se le da a alguien para entrar en el hub.
	duracionTransaccion = 10 * time.Minute
)

// ConLockatus enciende el login federado.
func (s *Sitio) ConLockatus(c *lockatus.Cliente) *Sitio {
	s.hub = c
	return s
}

// Federado dice si esta instancia delega el login.
func (s *Sitio) Federado() bool { return s.hub != nil && s.registro != nil }

// irAlHub arranca el viaje: arma los secretos, los guarda firmados en una
// cookie y manda el navegador al hub.
func (s *Sitio) irAlHub(w http.ResponseWriter, r *http.Request) {
	if !s.Federado() {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no está federada",
			"El login con Lockatus se enciende con NOTARUM_AUTH=federado.")
		return
	}
	t, err := lockatus.NuevaTransaccion(destinoSeguro(r.URL.Query().Get("volver")))
	if err != nil {
		s.fallo(w, r, http.StatusInternalServerError, "No se pudo empezar", "")
		return
	}
	crudo, err := empaquetar(t)
	if err != nil {
		s.fallo(w, r, http.StatusInternalServerError, "No se pudo empezar", "")
		return
	}
	hasta := time.Now().Add(duracionTransaccion)
	http.SetCookie(w, &http.Cookie{
		Name:     nombreCookieOIDC,
		Value:    s.registro.Sellar(propositoOIDC, crudo, hasta),
		Path:     "/",
		Expires:  hasta,
		HttpOnly: true,
		// Lax y no Strict: la vuelta del hub es una navegación desde otro
		// sitio, y con Strict la cookie no viajaría y nadie podría entrar.
		SameSite: http.SameSiteLaxMode,
		Secure:   esHTTPS(r),
	})
	http.Redirect(w, r, s.hub.URLAutorizar(t), http.StatusFound)
}

// volverDelHub cierra el viaje: comprueba que la vuelta corresponda a la ida,
// canjea el código y abre la misma sesión que abriría el login propio.
func (s *Sitio) volverDelHub(w http.ResponseWriter, r *http.Request) {
	if !s.Federado() {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no está federada", "")
		return
	}
	// La cookie de la transacción se borra pase lo que pase: se usa una vez.
	http.SetCookie(w, &http.Cookie{
		Name: nombreCookieOIDC, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: esHTTPS(r),
	})

	q := r.URL.Query()
	if motivo := q.Get("error"); motivo != "" {
		s.noEntro(w, r, motivo, q.Get("error_description"))
		return
	}

	c, err := r.Cookie(nombreCookieOIDC)
	if err != nil || c.Value == "" {
		s.noEntro(w, r, "sin_transaccion", "")
		return
	}
	crudo, err := s.registro.Abrir(propositoOIDC, c.Value)
	if err != nil {
		s.noEntro(w, r, "transaccion_invalida", "")
		return
	}
	t, err := desempaquetar(crudo)
	if err != nil {
		s.noEntro(w, r, "transaccion_invalida", "")
		return
	}
	// El estado es lo que ata esta vuelta a aquella ida: sin esta
	// comparación, cualquiera podría hacer entrar a alguien con un código
	// ajeno.
	if q.Get("state") == "" || q.Get("state") != t.Estado {
		s.noEntro(w, r, "estado_distinto", "")
		return
	}
	codigo := q.Get("code")
	if codigo == "" {
		s.noEntro(w, r, "sin_codigo", "")
		return
	}

	ident, err := s.hub.Canjear(r.Context(), codigo, t)
	if err != nil {
		if errors.Is(err, lockatus.ErrRechazado) {
			s.noEntro(w, r, "access_denied", "")
			return
		}
		// El detalle va al registro del servidor y no a la pantalla: a quien
		// está mirando no le sirve, y a quien esté probando sí.
		slog.Warn("no se pudo cerrar el login federado", "err", err)
		s.noEntro(w, r, "no_verificado", "")
		return
	}

	u, err := s.registro.EntrarFederado(ident.Correo, rolDelHub(ident.Rol))
	if err != nil {
		slog.Warn("no se pudo abrir la cuenta federada", "correo", ident.Correo, "err", err)
		s.noEntro(w, r, "cuenta_no_abierta", "")
		return
	}

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
	destino := t.Volver
	if destino == "" {
		destino = "/cuenta"
	}
	http.Redirect(w, r, destino, http.StatusSeeOther)
}

// rolDelHub traduce el rol que asigna el hub al catálogo de notarum, que
// tiene dos.
//
// Lo que no se reconoce cae en el rol de menos privilegio y no se rechaza:
// que el hub sume un rol nuevo no tiene por qué dejar a nadie afuera, pero
// tampoco puede darle a nadie más de lo que notarum sabe conceder.
func rolDelHub(rol string) cuentas.Rol {
	switch strings.ToLower(strings.TrimSpace(rol)) {
	case "admin", "administrador", "superadmin":
		return cuentas.RolAdmin
	}
	return cuentas.RolPersona
}

// noEntro explica por qué no se pudo entrar. Cada motivo tiene su frase: "no
// se pudo" a secas deja a quien lo lee sin saber si tiene que reintentar,
// pedir un acceso o avisarle a alguien.
func (s *Sitio) noEntro(w http.ResponseWriter, r *http.Request, motivo, detalle string) {
	codigo := http.StatusBadRequest
	titulo, texto := "No se pudo entrar con Lockatus", ""

	switch motivo {
	case "access_denied":
		codigo = http.StatusForbidden
		titulo = "Lockatus no te dio acceso a esta instancia"
		texto = "Tu cuenta existe en el hub, pero no tiene un rol asignado para notarum. " +
			"Pediselo a quien administre la suite."
	case "sin_transaccion", "transaccion_invalida":
		titulo = "Se venció el intento de entrada"
		texto = "Pasó demasiado tiempo, o se empezó en otro navegador. Probá de nuevo."
	case "estado_distinto":
		titulo = "Esta vuelta no corresponde a la ida"
		texto = "Empezá el login de nuevo desde acá y no desde un enlace que te hayan pasado."
	case "no_verificado":
		codigo = http.StatusBadGateway
		titulo = "No se pudieron verificar los datos del hub"
		texto = "La respuesta de Lockatus no se pudo comprobar. Si sigue pasando, es cosa de quien opera el hub."
	case "cuenta_no_abierta":
		codigo = http.StatusConflict
		titulo = "No se pudo abrir tu cuenta acá"
		texto = "Lockatus te reconoció, pero la cuenta local no se pudo crear."
	default:
		texto = "El hub cortó el intento."
		if detalle != "" {
			texto += " Dijo: " + detalle
		}
	}
	s.fallo(w, r, codigo, titulo, texto)
}

// ------------------------------------------------------------ la mochila

// La transacción va en la cookie como texto plano sellado. Se arma a mano y
// no con JSON para que el separador sea uno solo y el destino, que es lo
// único que viene de afuera, no pueda inventar campos.
func empaquetar(t lockatus.Transaccion) (string, error) {
	if strings.ContainsAny(t.Verificador+t.Estado+t.Nonce, "\n") {
		return "", errors.New("los secretos de la transacción salieron mal")
	}
	return strings.Join([]string{t.Verificador, t.Estado, t.Nonce, t.Volver}, "\n"), nil
}

func desempaquetar(crudo string) (lockatus.Transaccion, error) {
	p := strings.SplitN(crudo, "\n", 4)
	if len(p) != 4 || p[0] == "" || p[1] == "" || p[2] == "" {
		return lockatus.Transaccion{}, errors.New("la transacción está incompleta")
	}
	return lockatus.Transaccion{
		Verificador: p[0], Estado: p[1], Nonce: p[2],
		// El destino se vuelve a revisar al salir del sobre: viene de la
		// dirección con la que alguien empezó, y de ahí a la barra hay un
		// paso.
		Volver: destinoLocal(p[3]),
	}, nil
}

// destinoSeguro limpia el "volver" que llega por la dirección, y destinoLocal
// lo revisa otra vez al usarlo. Es la misma regla dos veces, a propósito: un
// redirect abierto convierte al sitio en trampolín para llevar a alguien a
// otro lado con la confianza de haber salido de acá.
func destinoSeguro(v string) string { return destinoLocal(v) }

func destinoLocal(v string) string {
	if v == "" {
		return ""
	}
	// Tiene que ser una ruta de este sitio: empezar con una barra sola. Con
	// dos barras el navegador lo lee como otro dominio.
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
		return ""
	}
	// Y nada de \ ni de control: hay navegadores que los tratan como barras.
	if strings.ContainsAny(v, "\\\n\r\t") {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return ""
	}
	return u.RequestURI()
}
