package tareas

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	// Las zonas horarias van adentro del binario: la imagen es alpine y no
	// trae la base de datos de zonas, así que sin esto "America/Argentina/
	// Buenos_Aires" no existiría y la hora se correría tres horas.
	_ "time/tzdata"
)

// El programador corre las actualizaciones solas, todos los días.
//
// Los catálogos se publican de a poco: InfoLEG suma las normas del Boletín
// con unos días de retraso, y la base provincial se republica cada tanto. Sin
// esto hay que acordarse de apretar el botón, y la instancia sirve datos
// viejos sin que nadie lo note.
//
// De madrugada a propósito: bajar 50 MB de InfoLEG y armar el índice usa la
// máquina un rato, y a las cinco de la mañana no molesta a nadie.

// ZonaArgentina es donde vive el Boletín que esto sigue.
const ZonaArgentina = "America/Argentina/Buenos_Aires"

// HoraPorDefecto es cuándo se actualiza si no se configura otra cosa.
const HoraPorDefecto = "05:00"

// Programado es un trabajo que se repite todos los días.
type Programado struct {
	// Tipo es el mismo que usa el ejecutor, así que una actualización
	// automática y una lanzada a mano no se pisan.
	Tipo string
	// Hacer es el trabajo.
	Hacer Trabajo
}

// Programador dispara los trabajos a su hora.
type Programador struct {
	ejecutor *Ejecutor
	zona     *time.Location
	hora     int
	minuto   int
	trabajos []Programado

	mu      sync.RWMutex
	ultima  time.Time // cuándo corrió por última vez
	proxima time.Time
	frenar  chan struct{}
	unaVez  sync.Once
}

// NuevoProgramador arma el programador. La hora se escribe HH:MM.
func NuevoProgramador(e *Ejecutor, horaHHMM, zona string) (*Programador, error) {
	if zona == "" {
		zona = ZonaArgentina
	}
	loc, err := time.LoadLocation(zona)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(horaHHMM) == "" {
		horaHHMM = HoraPorDefecto
	}
	h, m, err := leerHora(horaHHMM)
	if err != nil {
		return nil, err
	}
	p := &Programador{
		ejecutor: e, zona: loc, hora: h, minuto: m,
		frenar: make(chan struct{}),
	}
	p.proxima = p.siguienteDespuesDe(time.Now().In(loc))
	return p, nil
}

func leerHora(s string) (hora, minuto int, err error) {
	partes := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(partes) != 2 {
		return 0, 0, errHora(s)
	}
	hora, err = strconv.Atoi(partes[0])
	if err != nil || hora < 0 || hora > 23 {
		return 0, 0, errHora(s)
	}
	minuto, err = strconv.Atoi(partes[1])
	if err != nil || minuto < 0 || minuto > 59 {
		return 0, 0, errHora(s)
	}
	return hora, minuto, nil
}

type errHora string

func (e errHora) Error() string {
	return "la hora " + string(e) + " no se entiende: se escribe HH:MM, como 05:00"
}

// Agregar suma un trabajo a los que se corren todos los días.
func (p *Programador) Agregar(t Programado) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trabajos = append(p.trabajos, t)
}

// siguienteDespuesDe calcula cuándo toca la próxima vuelta.
func (p *Programador) siguienteDespuesDe(desde time.Time) time.Time {
	hoy := time.Date(desde.Year(), desde.Month(), desde.Day(), p.hora, p.minuto, 0, 0, p.zona)
	if hoy.After(desde) {
		return hoy
	}
	return hoy.AddDate(0, 0, 1)
}

// Arrancar pone el programador a andar. Vuelve enseguida: espera en su propia
// goroutine.
func (p *Programador) Arrancar() {
	p.unaVez.Do(func() {
		go p.esperar()
		slog.Info("actualización automática programada",
			"hora", p.HoraTexto(), "zona", p.zona.String(),
			"proxima", p.Proxima().Format(time.RFC3339))
	})
}

// esperar duerme hasta la hora y dispara. Se recalcula en cada vuelta en vez
// de sumar 24 horas: así el cambio de hora del sistema, o un contenedor que
// estuvo suspendido, no corren la cita para siempre.
func (p *Programador) esperar() {
	for {
		p.mu.RLock()
		proxima := p.proxima
		p.mu.RUnlock()

		espera := time.Until(proxima)
		if espera < 0 {
			espera = 0
		}
		// Se despierta cada tanto aunque falte mucho: si la máquina estuvo
		// dormida, un timer largo puede no dispararse cuando corresponde.
		if espera > time.Hour {
			espera = time.Hour
		}
		select {
		case <-time.After(espera):
		case <-p.frenar:
			return
		}

		ahora := time.Now().In(p.zona)
		if ahora.Before(proxima) {
			continue // todavía no
		}
		p.correr(ahora)
	}
}

func (p *Programador) correr(ahora time.Time) {
	p.mu.Lock()
	p.ultima = ahora
	p.proxima = p.siguienteDespuesDe(ahora)
	trabajos := append([]Programado(nil), p.trabajos...)
	p.mu.Unlock()

	for _, t := range trabajos {
		// Lanzar devuelve ErrYaCorre si alguien la lanzó a mano hace un rato:
		// eso no es un problema, es la razón de que exista ese error.
		if err := p.ejecutor.Lanzar(t.Tipo, "la actualización automática", t.Hacer); err != nil {
			slog.Info("no se lanzó la actualización automática", "tarea", t.Tipo, "por", err)
			continue
		}
		slog.Info("actualización automática lanzada", "tarea", t.Tipo)
	}
}

// Frenar detiene el programador.
func (p *Programador) Frenar() {
	p.unaVez.Do(func() {}) // por si nunca arrancó
	select {
	case <-p.frenar:
	default:
		close(p.frenar)
	}
}

// Proxima es cuándo toca la próxima actualización.
func (p *Programador) Proxima() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.proxima
}

// Ultima es cuándo corrió por última vez, o el cero si todavía no.
func (p *Programador) Ultima() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ultima
}

// HoraTexto es la hora configurada, para mostrarla.
func (p *Programador) HoraTexto() string {
	return leerDosDigitos(p.hora) + ":" + leerDosDigitos(p.minuto)
}

// Zona es dónde se cuenta esa hora.
func (p *Programador) Zona() string { return p.zona.String() }

// Tareas son los tipos que corre.
func (p *Programador) Tareas() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var tipos []string
	for _, t := range p.trabajos {
		tipos = append(tipos, t.Tipo)
	}
	return tipos
}

func leerDosDigitos(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
