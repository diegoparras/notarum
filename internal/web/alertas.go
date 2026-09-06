package web

import (
	"net/http"
	"strings"

	"github.com/diegoparras/notarum/internal/alertas"
	"github.com/diegoparras/notarum/internal/saij"
)

// Las alertas, desde la cuenta.
//
// Una búsqueda guardada que mira después de cada actualización y avisa lo
// nuevo. Es lo que convierte a notarum de algo que se consulta en algo que
// avisa: quien sigue un tema no tiene por qué acordarse de entrar a buscarlo.

// PuedeAlertar dice si esta instancia tiene las alertas encendidas.
func (s *Sitio) PuedeAlertar() bool { return s.alertas != nil && s.registro != nil }

// crearAlerta atiende el formulario.
func (s *Sitio) crearAlerta(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if !s.PuedeAlertar() {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene alertas", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}

	a := alertas.Alerta{
		Dueño:   u.Nombre,
		Nombre:  r.PostFormValue("nombre"),
		Fuente:  alertas.Fuente(strings.TrimSpace(r.PostFormValue("fuente"))),
		Webhook: r.PostFormValue("webhook"),
		Criterios: alertas.Criterios{
			Texto:        r.PostFormValue("texto"),
			Tipo:         strings.TrimSpace(r.PostFormValue("tipo")),
			Provincia:    strings.TrimSpace(r.PostFormValue("provincia")),
			Seccion:      strings.TrimSpace(r.PostFormValue("seccion")),
			SoloVigentes: r.PostFormValue("vigentes") != "",
		},
	}
	// Los criterios que no son de esa fuente se descartan en vez de guardarse
	// sin efecto: una alerta que dice "provincia: Mendoza" sobre el Boletín
	// promete un filtro que no existe.
	switch a.Fuente {
	case alertas.FuenteBoletin:
		a.Criterios.Provincia, a.Criterios.SoloVigentes = "", false
	case alertas.FuenteNacional:
		a.Criterios.Provincia, a.Criterios.Seccion, a.Criterios.SoloVigentes = "", "", false
	case alertas.FuenteProvincial:
		a.Criterios.Seccion = ""
	}

	creada, err := s.alertas.Crear(a)
	if err != nil {
		s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
		return
	}
	// Se prueba en el acto: una alerta que no coincide con nada se descubre
	// recién a la semana, cuando uno se pregunta por qué nunca avisó.
	aviso := "Alerta «" + creada.Nombre + "» creada. "
	if s.corredor != nil {
		coincidencias, err := s.corredor.Probar(r.Context(), *creada)
		switch {
		case err != nil:
			aviso += "No se pudo probar ahora: " + err.Error() + "."
		case len(coincidencias) == 0:
			aviso += "Ahora mismo no coincide con nada; va a avisar cuando aparezca algo."
		default:
			aviso += "Hoy coincide con " + conPuntos(len(coincidencias)) +
				"; de eso no se avisa, sólo de lo que aparezca de ahora en más."
		}
	}
	s.dibujarCuenta(w, r, u, aviso, "", http.StatusOK)
}

// borrarAlerta la saca.
func (s *Sitio) borrarAlerta(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if !s.PuedeAlertar() {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene alertas", "")
		return
	}
	if err := s.alertas.Borrar(r.PathValue("id"), u.Nombre); err != nil {
		s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/cuenta", http.StatusSeeOther)
}

// crearFeedDeAlerta genera la clave que abre el feed, o la da de baja.
//
// Es una clave aparte y no el token de la cuenta, porque un lector de feeds no
// manda cabeceras y la clave termina en la dirección. Ahí se filtra: por los
// registros del servidor, por el historial, por quien mire la pantalla. Por
// eso ésta abre una sola alerta y nada más, y se puede dar de baja sola.
func (s *Sitio) crearFeedDeAlerta(w http.ResponseWriter, r *http.Request) {
	u := s.exigirSesion(w, r)
	if u == nil {
		return
	}
	if !s.PuedeAlertar() {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no tiene alertas", "")
		return
	}
	a, hay := s.alertas.Leer(r.PathValue("id"))
	if !hay || !strings.EqualFold(a.Dueño, u.Nombre) {
		s.fallo(w, r, http.StatusNotFound, "Esa alerta no existe", "")
		return
	}

	if r.PostFormValue("dar_de_baja") != "" {
		a.ClaveFeed = ""
	} else {
		clave, err := alertas.NuevaClaveFeed()
		if err != nil {
			s.dibujarCuenta(w, r, u, "", "No se pudo generar la clave.", http.StatusInternalServerError)
			return
		}
		a.ClaveFeed = clave
	}
	if err := s.alertas.Actualizar(a); err != nil {
		s.dibujarCuenta(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/cuenta", http.StatusSeeOther)
}

// datosAlerta es una alerta lista para mostrar.
type datosAlerta struct {
	alertas.Alerta
	FuenteNombre     string
	CriteriosEnTexto string
	// Feed es la dirección entera, con su clave, para copiarla.
	Feed string
}

func (s *Sitio) alertasDe(usuario, base string) []datosAlerta {
	if !s.PuedeAlertar() {
		return nil
	}
	var out []datosAlerta
	for _, a := range s.alertas.De(usuario) {
		d := datosAlerta{
			Alerta:           a,
			FuenteNombre:     a.Fuente.Nombre(),
			CriteriosEnTexto: criteriosEnTexto(a.Criterios),
		}
		if a.ClaveFeed != "" {
			d.Feed = base + "/feed/" + a.ID + "?k=" + a.ClaveFeed
		}
		out = append(out, d)
	}
	return out
}

// criteriosEnTexto escribe los criterios como se leen.
func criteriosEnTexto(c alertas.Criterios) string {
	var partes []string
	if c.Texto != "" {
		partes = append(partes, "«"+c.Texto+"»")
	}
	if c.Tipo != "" {
		partes = append(partes, c.Tipo)
	}
	if c.Provincia != "" {
		if p, hay := saij.BuscarProvincia(c.Provincia); hay {
			partes = append(partes, p.Nombre)
		} else {
			partes = append(partes, c.Provincia)
		}
	}
	if c.Seccion != "" {
		partes = append(partes, "sección "+c.Seccion)
	}
	if c.SoloVigentes {
		partes = append(partes, "sólo vigentes")
	}
	return strings.Join(partes, " · ")
}
