package web

import (
	"html"
	"html/template"
	"net/http"
	"regexp"

	"github.com/diegoparras/notarum"
	"github.com/diegoparras/notarum/internal/novedades"
)

// Novedades: qué cambió en cada versión, del CHANGELOG embebido en el
// binario. El lector no usa JavaScript, así que en vez de un modal hay una
// página (/novedades, en el menú y en el pie) y un aviso arriba de todo que
// aparece UNA vez por navegador después de una actualización: una cookie
// recuerda la última versión que se vio, y sólo guarda eso, un número de
// versión.

// cookieVersionVista guarda la última versión de notarum que vio el navegador.
const cookieVersionVista = "notarum_version_vista"

// reVersion es lo único que se acepta como versión vista (cookie o ?desde=).
var reVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

var (
	reNegrita = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reCodigo  = regexp.MustCompile("`([^`]+)`")
)

// notaHTML pasa un ítem del CHANGELOG a HTML: primero escapa todo y recién
// después convierte **negrita** y `código`, así nada del texto se interpreta
// como marcado.
func notaHTML(s string) template.HTML {
	t := html.EscapeString(s)
	t = reNegrita.ReplaceAllString(t, "<strong>$1</strong>")
	t = reCodigo.ReplaceAllString(t, "<code>$1</code>")
	return template.HTML(t)
}

// novedadesDesde devuelve lo que cambió después de `vista` hasta la versión
// que corre; vacío si `vista` no es una versión, si es la actual o si es
// posterior (una vuelta atrás no es una actualización).
func (s *Sitio) novedadesDesde(vista string) []novedades.Version {
	if !reVersion.MatchString(vista) || novedades.Comparar(vista, s.version) >= 0 {
		return nil
	}
	return novedades.Desde(novedades.Parsear(notarum.Changelog), vista, s.version)
}

// avisoDeVersion arma el aviso de "se actualizó" a partir de la cookie.
func (s *Sitio) avisoDeVersion(r *http.Request) (string, []novedades.Version) {
	c, err := r.Cookie(cookieVersionVista)
	if err != nil {
		return "", nil
	}
	vs := s.novedadesDesde(c.Value)
	if len(vs) == 0 {
		return "", nil
	}
	return c.Value, vs
}

// recordarVersion deja en el navegador la versión que corre, si no la tenía:
// es lo que hace que el aviso salga una sola vez.
func (s *Sitio) recordarVersion(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieVersionVista); err == nil && c.Value == s.version {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieVersionVista,
		Value:    s.version,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   esHTTPS(r),
	})
}

type datosNovedades struct {
	comun
	// Desde es la versión desde la que se muestra (?desde=); vacío, todo el
	// historial hasta la versión que corre.
	Desde     string
	Versiones []novedades.Version
}

func (s *Sitio) verNovedades(w http.ResponseWriter, r *http.Request) {
	d := datosNovedades{comun: s.baseCon(r, "", "")}
	d.Angosto = true
	// Esta página ya muestra lo que cambió: el aviso de arriba repetiría lo
	// mismo.
	d.AvisoDesde, d.AvisoVersiones = "", nil
	if desde := r.URL.Query().Get("desde"); reVersion.MatchString(desde) {
		d.Desde = desde
		d.Versiones = s.novedadesDesde(desde)
	} else {
		d.Versiones = novedades.Hasta(novedades.Parsear(notarum.Changelog), s.version)
	}
	s.mostrar(w, r, "novedades", d, http.StatusOK)
}
