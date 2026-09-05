package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
)

// nombreCookie es la cookie de sesión del lector.
const nombreCookie = "notarum_sesion"

// tokenVista es un token listo para poner en una tabla.
type tokenVista struct {
	cuentas.Token
	CreadoTexto    string
	UltimoUsoTexto string
	RevocadoTexto  string
}

type datosEntrar struct {
	comun
	Usuario string
	Error   string
	// Federado enciende el botón del hub.
	Federado bool
	// Explicacion dice para qué sirve entrar en esta instancia, que depende
	// del modo: no es lo mismo una abierta, donde la cuenta sólo da cuota,
	// que una cerrada, donde sin cuenta no se ve nada.
	Explicacion string
}

type datosCuenta struct {
	comun
	// Se llama Cuenta y no Yo: comun ya trae un Yo con el nombre, y un campo
	// con el mismo nombre lo tapa en la plantilla base. Cuando lo tapaba, la
	// barra imprimía el usuario entero —con el hash de la clave— en el HTML.
	Cuenta            *cuentas.Usuario
	Tokens            []tokenVista
	TokenNuevo        string
	Error             string
	Base              string
	Hoy               string
	PorMinuto         int
	PorMinutoConToken int
	Modo              string
	Explicacion       string
}

// yo devuelve quién está mirando, o nil si nadie entró.
func (s *Sitio) yo(r *http.Request) *cuentas.Usuario {
	if s.registro == nil {
		return nil
	}
	c, err := r.Cookie(nombreCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := s.registro.LeerSesion(c.Value)
	if err != nil {
		return nil
	}
	return u
}

// exigirSesion manda a entrar a quien no lo hizo.
func (s *Sitio) exigirSesion(w http.ResponseWriter, r *http.Request) *cuentas.Usuario {
	u := s.yo(r)
	if u == nil {
		http.Redirect(w, r, "/entrar", http.StatusFound)
		return nil
	}
	return u
}

func (s *Sitio) verEntrar(w http.ResponseWriter, r *http.Request) {
	if s.registro == nil {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene cuentas",
			"notarum funciona sin cuentas; se habilitan creando la primera con `notarum usuarios crear`.")
		return
	}
	if s.yo(r) != nil {
		http.Redirect(w, r, "/cuenta", http.StatusFound)
		return
	}
	d := datosEntrar{comun: s.baseCon(r, "", ""), Explicacion: s.politica.Explicacion(),
		Federado: s.Federado()}
	d.Angosto = true
	s.mostrar(w, r, "entrar", d, http.StatusOK)
}

func (s *Sitio) hacerEntrar(w http.ResponseWriter, r *http.Request) {
	if s.registro == nil {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene cuentas", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}
	usuario := r.PostFormValue("usuario")
	u, err := s.registro.Autenticar(usuario, r.PostFormValue("clave"))
	if err != nil {
		// El mismo mensaje para usuario inexistente y clave errada: la
		// diferencia serviría para averiguar qué cuentas existen.
		d := datosEntrar{comun: s.baseCon(r, "", ""), Usuario: usuario,
			Error: "Usuario o clave incorrectos.", Explicacion: s.politica.Explicacion(),
			Federado: s.Federado()}
		d.Angosto = true
		s.mostrar(w, r, "entrar", d, http.StatusUnauthorized)
		return
	}

	hasta := time.Now().Add(cuentas.DuracionSesion)
	http.SetCookie(w, &http.Cookie{
		Name:     nombreCookie,
		Value:    s.registro.FirmarSesion(u.Nombre, hasta),
		Path:     "/",
		Expires:  hasta,
		HttpOnly: true,                 // fuera del alcance de cualquier script
		SameSite: http.SameSiteLaxMode, // no viaja desde otro sitio
		Secure:   esHTTPS(r),
	})
	// 303 y no 302: después de un POST que cambió algo, el redirect tiene que
	// obligar a un GET. Con 302 el cliente puede repetir el POST.
	http.Redirect(w, r, "/cuenta", http.StatusSeeOther)
}

func (s *Sitio) salir(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: nombreCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: esHTTPS(r),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// esHTTPS dice si la conexión llegó cifrada, mirando también lo que informa el
// proxy: la cookie no puede marcarse Secure sobre http o el navegador la tira.
func esHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Sitio) verCuenta(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	s.dibujarCuenta(w, r, u, "", "", http.StatusOK)
}

func (s *Sitio) dibujarCuenta(w http.ResponseWriter, r *http.Request, u *cuentas.Usuario, tokenNuevo, errMsg string, codigo int) {
	d := datosCuenta{
		comun:             s.baseCon(r, "", ""),
		Cuenta:            u,
		TokenNuevo:        tokenNuevo,
		Error:             errMsg,
		Base:              baseVisible(r),
		Hoy:               boletin.HoyEnArgentina().API(),
		PorMinuto:         s.politica.Anonimo,
		PorMinutoConToken: s.politica.CuotaDe(u),
		Modo:              string(s.politica.Modo),
		Explicacion:       s.politica.Explicacion(),
	}
	d.Angosto = true
	for _, t := range s.registro.Tokens(u.Nombre) {
		v := tokenVista{Token: t, CreadoTexto: t.Creado.Format("2006-01-02")}
		if t.UltimoUso != nil {
			v.UltimoUsoTexto = t.UltimoUso.Format("2006-01-02")
		}
		if t.Revocado != nil {
			v.RevocadoTexto = t.Revocado.Format("2006-01-02")
		}
		d.Tokens = append(d.Tokens, v)
	}
	s.mostrar(w, r, "cuenta", d, codigo)
}

func (s *Sitio) crearToken(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}
	_, valor, err := s.registro.CrearToken(u.Nombre,
		r.PostFormValue("nombre"), cuentas.Alcance(r.PostFormValue("alcance")))
	if err != nil {
		s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
		return
	}
	// El valor se muestra una sola vez, acá.
	s.dibujarCuenta(w, r, u, valor, "", http.StatusOK)
}

func (s *Sitio) revocarToken(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	// El registro sólo revoca lo que es de esta persona: pasar el id de otro
	// no alcanza para tocarlo.
	err := s.registro.RevocarToken(u.Nombre, r.PathValue("id"))
	if err != nil && !errors.Is(err, cuentas.ErrNoExiste) {
		s.dibujarCuenta(w, r, u, "", "No se pudo revocar el token.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/cuenta", http.StatusSeeOther)
}

func primeraMayuscula(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
