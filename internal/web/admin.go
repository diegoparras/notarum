package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/tareas"
)

// El panel de quien opera la instancia.
//
// Todo lo que antes había que hacer entrando por consola al contenedor
// —bajar el catálogo de InfoLEG, el de normativa provincial, llenar la
// historia del Boletín— se hace desde acá. Quien monta un servicio en un
// panel de deploy no tiene por qué abrir una terminal para ponerlo en
// marcha.

// Los tipos de tarea, que son también las claves de los botones.
const (
	tareaInfoLEG    = "infoleg"
	tareaProvincial = "provincial"
	tareaRellenar   = "rellenar"
	tareaAlertas    = "alertas"
)

type datosAdmin struct {
	comun
	Yo *cuentas.Usuario

	Almacen     string
	Entradas    int64
	Avisos      int64
	TieneIndice bool

	InfoLEG    servicio.EstadoInfoLEG
	SAIJ       servicio.EstadoSAIJ
	SAIJHay    bool
	InfoLEGHay bool

	Tareas map[string]tareas.Tarea
	// Corriendo enciende el refresco de la página: mientras algo trabaja, la
	// pantalla se actualiza sola.
	Corriendo bool

	// Automatica es la actualización de todos los días, si está encendida.
	Automatica *datosAutomatica

	// Marca dice desde cuándo guarda este almacén. Si arrancó vacío, hace
	// falta decirlo: lo que se cargue se va a perder en el próximo despliegue.
	Marca almacen.Marca

	Secciones []boletin.Seccion
	Error     string
	Aviso     string

	// La configuración de acceso, que se edita desde acá.
	Politica cuentas.Politica
	Modos    []cuentas.Modo
	// PoliticaGuardada dice si lo que rige se cambió desde el panel o viene
	// de la configuración del servicio.
	PoliticaGuardada bool
}

// exigirAdmin deja pasar sólo a quien administra. Estas acciones cambian lo
// que la instancia sirve y pueden tardar minutos.
func (s *Sitio) exigirAdmin(w http.ResponseWriter, r *http.Request) *cuentas.Usuario {
	u := s.exigirSesion(w, r)
	if u == nil {
		return nil
	}
	if u.Rol != cuentas.RolAdmin {
		s.fallo(w, r, http.StatusForbidden, "Esto es del administrador",
			"Tu cuenta puede leer y tener tokens, pero no poner en marcha las fuentes de datos.")
		return nil
	}
	return u
}

func (s *Sitio) verAdmin(w http.ResponseWriter, r *http.Request) {
	u := s.exigirAdmin(w, r)
	if u == nil {
		return
	}
	s.dibujarAdmin(w, r, u, "", "", http.StatusOK)
}

func (s *Sitio) dibujarAdmin(w http.ResponseWriter, r *http.Request, u *cuentas.Usuario, aviso, errMsg string, codigo int) {
	m := s.srv.Almacen().Metricas()
	d := datosAdmin{
		comun:       s.baseCon(r, "", ""),
		Yo:          u,
		Almacen:     m.Motor,
		Entradas:    m.Entradas,
		Avisos:      m.Avisos,
		TieneIndice: s.srv.TieneIndice(),
		InfoLEG:     s.srv.EstadoInfoLEG(),
		SAIJ:        s.srv.EstadoSAIJ(),
		SAIJHay:     s.srv.SAIJDisponible(),
		InfoLEGHay:  s.srv.InfoLEGDisponible(),
		Secciones:   boletin.SeccionesValidas,
		Aviso:       aviso,
		Error:       errMsg,
		Tareas:      map[string]tareas.Tarea{},
		Marca:       s.marca,
		Politica:    s.vigente(),
		Modos:       []cuentas.Modo{cuentas.ModoAbierto, cuentas.ModoMixto, cuentas.ModoCerrado},
	}
	if s.registro != nil {
		d.PoliticaGuardada = s.registro.HayPoliticaGuardada()
	}
	if s.tareas != nil {
		for _, t := range []string{tareaInfoLEG, tareaProvincial, tareaRellenar, tareaAlertas} {
			d.Tareas[t] = s.tareas.Estado(t)
		}
		d.Corriendo = s.tareas.AlgoCorriendo()
	}
	if s.programador != nil {
		d.Automatica = &datosAutomatica{
			Hora:    s.programador.HoraTexto(),
			Zona:    s.programador.Zona(),
			Proxima: s.programador.Proxima(),
			Ultima:  s.programador.Ultima(),
			Tareas:  s.programador.Tareas(),
		}
	}
	s.mostrar(w, r, "admin", d, codigo)
}

// lanzarTarea atiende los botones del panel.
func (s *Sitio) lanzarTarea(w http.ResponseWriter, r *http.Request) {
	u := s.exigirAdmin(w, r)
	if u == nil {
		return
	}
	if s.tareas == nil {
		s.fallo(w, r, http.StatusNotFound, "Esta instancia no corre tareas", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}

	tipo := r.PathValue("tipo")
	var trabajo tareas.Trabajo
	switch tipo {
	case tareaInfoLEG:
		trabajo = s.trabajoInfoLEG()
	case tareaProvincial:
		trabajo = s.trabajoProvincial()
	case tareaAlertas:
		if s.corredor == nil {
			s.dibujarAdmin(w, r, u, "", "Esta instancia no tiene alertas encendidas.",
				http.StatusNotFound)
			return
		}
		trabajo = s.trabajoAlertas()
	case tareaRellenar:
		var err error
		trabajo, err = s.trabajoRellenar(r)
		if err != nil {
			s.dibujarAdmin(w, r, u, "", err.Error(), http.StatusBadRequest)
			return
		}
	default:
		s.fallo(w, r, http.StatusNotFound, "No existe esa tarea", "")
		return
	}

	err := s.tareas.Lanzar(tipo, u.Nombre, trabajo)
	// Que ya esté corriendo no es una falla: es lo que pasa al apretar el
	// botón dos veces, y la página ya lo muestra.
	if err != nil && !strings.Contains(err.Error(), "ya está corriendo") {
		s.dibujarAdmin(w, r, u, "", err.Error(), http.StatusInternalServerError)
		return
	}
	// 303 para que recargar no vuelva a lanzarla.
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Sitio) cortarTarea(w http.ResponseWriter, r *http.Request) {
	u := s.exigirAdmin(w, r)
	if u == nil {
		return
	}
	if s.tareas != nil {
		s.tareas.Cortar(r.PathValue("tipo"))
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// ------------------------------------------------------------- los trabajos

func (s *Sitio) trabajoInfoLEG() tareas.Trabajo {
	return func(ctx context.Context, avisar func(string)) (string, error) {
		avisar("buscando el catálogo")
		e, err := s.srv.SincronizarInfoLEG(ctx, "", func(guardadas int) {
			avisar(fmt.Sprintf("guardando normas (%s)", conPuntos(guardadas)))
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s normas, %s con texto", conPuntos(e.Normas), conPuntos(e.ConTexto)), nil
	}
}

func (s *Sitio) trabajoProvincial() tareas.Trabajo {
	return func(ctx context.Context, avisar func(string)) (string, error) {
		avisar("bajando el catálogo provincial")
		e, err := s.srv.SincronizarSAIJ(ctx, "")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s normas de %d jurisdicciones", conPuntos(e.Normas), e.Provincias), nil
	}
}

// trabajoRellenar lee el rango del formulario antes de lanzar nada: un rango
// mal escrito tiene que avisarse en la pantalla y no quedar como una tarea
// fallada.
func (s *Sitio) trabajoRellenar(r *http.Request) (tareas.Trabajo, error) {
	sec, err := boletin.ParseSeccion(r.PostFormValue("seccion"))
	if err != nil {
		return nil, fmt.Errorf("la sección no se entiende: %w", err)
	}
	desde, err := boletin.ParseFecha(r.PostFormValue("desde"))
	if err != nil {
		return nil, fmt.Errorf("la fecha de inicio no se entiende: %w", err)
	}
	hasta, err := boletin.ParseFecha(r.PostFormValue("hasta"))
	if err != nil {
		return nil, fmt.Errorf("la fecha de fin no se entiende: %w", err)
	}
	if hasta.Before(desde.Time) {
		return nil, fmt.Errorf("el fin es anterior al inicio")
	}

	conTextos := r.PostFormValue("textos") != ""

	return func(ctx context.Context, avisar func(string)) (string, error) {
		avisar(fmt.Sprintf("recorriendo %s desde %s hasta %s", sec, desde.API(), hasta.API()))
		relleno := s.srv.Rellenar
		if conTextos {
			relleno = s.srv.RellenarConAvisos
		}
		res, err := relleno(ctx, sec, desde, hasta, func(a servicio.Avance) {
			avisar(a.Texto())
		})
		// Lo hecho se cuenta aunque haya fallado a mitad: el relleno se retoma
		// donde quedó.
		hecho := fmt.Sprintf("%s ediciones bajadas, %s ya estaban, %s días sin edición",
			conPuntos(res.Bajadas), conPuntos(res.YaEstaban), conPuntos(res.SinEdicion))
		if res.TextosBajados > 0 {
			hecho += fmt.Sprintf(", %s avisos con texto", conPuntos(res.TextosBajados))
		}
		return hecho, err
	}, nil
}

// conPuntos escribe un número como se lee en castellano: 428.000.
func conPuntos(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// haceCuanto escribe una duración como se dice: "hace 3 minutos".
func haceCuanto(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "recién"
	case d < time.Hour:
		return fmt.Sprintf("hace %d minutos", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("hace %d horas", int(d.Hours()))
	case d < 48*time.Hour:
		return "ayer"
	default:
		return fmt.Sprintf("hace %d días", int(d.Hours()/24))
	}
}

// --------------------------------------------------------- la configuración

// guardarPolitica cambia quién entra y cuánto puede pedir, sin reiniciar.
//
// Es lo que antes había que poner en variables de entorno y volver a
// desplegar. Se guarda en el almacén, así que sobrevive al reinicio y pisa a
// lo que diga el entorno.
func (s *Sitio) guardarPolitica(w http.ResponseWriter, r *http.Request) {
	u := s.exigirAdmin(w, r)
	if u == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fallo(w, r, http.StatusBadRequest, "No se entendió el formulario", "")
		return
	}
	p := s.vigente()

	modo, err := cuentas.ParseModo(r.PostFormValue("modo"))
	if err != nil {
		s.dibujarAdmin(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
		return
	}
	p.Modo = modo

	for _, c := range []struct {
		campo   string
		destino *int
	}{
		{"anonimo", &p.Anonimo},
		{"persona", &p.Persona},
		{"admin", &p.Admin},
		{"lector", &p.Lector},
		{"login", &p.Login},
	} {
		v := strings.TrimSpace(r.PostFormValue(c.campo))
		if v == "" {
			continue // lo que no se toca queda como estaba
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			s.dibujarAdmin(w, r, u, "", "«"+v+"» no es un número.", http.StatusBadRequest)
			return
		}
		*c.destino = n
	}

	if err := s.registro.FijarPolitica(p); err != nil {
		s.dibujarAdmin(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusBadRequest)
		return
	}
	slog.Info("política de acceso cambiada desde el panel",
		"quien", u.Nombre, "modo", p.Modo, "anonimo", p.Anonimo, "persona", p.Persona)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// olvidarPolitica vuelve a lo que diga la configuración del servicio.
func (s *Sitio) olvidarPolitica(w http.ResponseWriter, r *http.Request) {
	u := s.exigirAdmin(w, r)
	if u == nil {
		return
	}
	if err := s.registro.OlvidarPolitica(s.politica); err != nil {
		s.dibujarAdmin(w, r, u, "", primeraMayuscula(err.Error())+".", http.StatusInternalServerError)
		return
	}
	slog.Info("política de acceso devuelta a la del entorno", "quien", u.Nombre)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// ------------------------------------------------- la actualización automática

// datosAutomatica es lo que el panel muestra del programador: los catálogos se
// actualizan solos de madrugada, y quien opera tiene que poder verlo sin
// entrar al contenedor a leer el log.
type datosAutomatica struct {
	Hora    string
	Zona    string
	Proxima time.Time
	Ultima  time.Time
	Tareas  []string
}

// enCuanto dice cuánto falta para algo que todavía no pasó.
func enCuanto(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Until(t)
	switch {
	case d < 0:
		return "ya"
	case d < time.Minute:
		return "en menos de un minuto"
	case d < time.Hour:
		return fmt.Sprintf("en %d minutos", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("en %d horas", int(d.Hours()))
	default:
		return fmt.Sprintf("en %d días", int(d.Hours()/24))
	}
}

// cuantoVa escribe cuánto lleva algo que está corriendo. Sin esto, una tarea
// que trabaja y una que se colgó se ven igual, y no hay forma de saber si vale
// la pena seguir esperando.
func cuantoVa(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d segundos", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutos", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d horas", int(d.Hours()))
	}
}

// trabajoAlertas corre la pasada de alertas a mano, desde el panel. Es el
// mismo trabajo que corre solo todos los días después de las actualizaciones.
func (s *Sitio) trabajoAlertas() tareas.Trabajo {
	return func(ctx context.Context, avisar func(string)) (string, error) {
		r, err := s.corredor.Correr(ctx, avisar)
		if err != nil {
			return "", err
		}
		texto := fmt.Sprintf("%d alertas miradas, %d con novedades (%d en total)",
			r.Corridas, r.Avisadas, r.Novedades)
		if r.Fallaron > 0 {
			texto += fmt.Sprintf("; %d fallaron", r.Fallaron)
		}
		return texto, nil
	}
}
