package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/alertas"
	"github.com/diegoparras/notarum/internal/asistente"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
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
	// HayAsistente y ClaveIA son del generador de consultas. ClaveIA es sólo
	// una pista para reconocer cuál está cargada, nunca la clave.
	HayAsistente bool
	ClaveIA      string
	// Modelos son los que ofrece el proveedor con esa clave, para elegir uno.
	Modelos []asistente.Modelo
	// ModeloIA es el elegido; vacío significa el que trae notarum.
	ModeloIA         string
	ModeloPorDefecto string
	// ErrorModelos es por qué no se pudo traer la lista, si no se pudo.
	ErrorModelos string

	// Las alertas: búsquedas guardadas que avisan cuando aparece algo nuevo.
	HayAlertas bool
	Alertas    []datosAlerta
	Fuentes    []alertas.Fuente
	Provincias []servicio.ProvinciaConNormas
	Secciones  []boletin.Seccion
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
			"El registro de cuentas está apagado.")
		return
	}
	if s.yo(r) != nil {
		http.Redirect(w, r, "/cuenta", http.StatusFound)
		return
	}
	d := datosEntrar{comun: s.baseCon(r, "", ""), Explicacion: s.vigente().Explicacion(),
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
			Error: "Usuario o clave incorrectos.", Explicacion: s.vigente().Explicacion(),
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
		PorMinuto:         s.vigente().Anonimo,
		PorMinutoConToken: s.vigente().CuotaDe(u),
		Modo:              string(s.vigente().Modo),
		Explicacion:       s.vigente().Explicacion(),
		HayAsistente:      s.PuedeAsistir(),
	}
	if pista, hay := s.registro.PistaClaveIA(u.Nombre); hay {
		d.ClaveIA = pista
		d.ModeloIA = s.registro.ModeloIA(u.Nombre)
		d.ModeloPorDefecto = asistente.ModeloPorDefecto
		// La lista se le pide al proveedor con la clave de quien mira. Si no
		// se puede, la página sale igual: elegir el modelo es una comodidad,
		// no un requisito para que la cuenta funcione.
		if s.asistente != nil {
			if clave, err := s.registro.ClaveIA(u.Nombre); err == nil {
				ctx, cancelar := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancelar()
				if ms, err := s.asistente.Modelos(ctx, clave); err == nil {
					d.Modelos = ms
				} else {
					d.ErrorModelos = explicarDelProveedor(err)
				}
			}
		}
	}
	d.HayAlertas = s.PuedeAlertar()
	if d.HayAlertas {
		d.Alertas = s.alertasDe(u.Nombre, d.Base)
		d.Fuentes = alertas.Fuentes
		d.Provincias = s.srv.ProvinciasConNormas()
		d.Secciones = boletin.SeccionesValidas
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
