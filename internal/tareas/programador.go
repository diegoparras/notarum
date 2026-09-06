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

// Programado es un trabajo que se repite.
//
// Cada uno con su horario, y no todos al mismo: bajar un catálogo son minutos
// y se hace todos los días, pero bajar el texto de una semana entera del
// Boletín son horas y conviene hacerlo con la máquina tranquila. Meterlos en
// el mismo horario obligaría a elegir el peor de los dos.
type Programado struct {
	// Tipo es el mismo que usa el ejecutor, así que una actualización
	// automática y una lanzada a mano no se pisan.
	Tipo string
	// Hacer es el trabajo.
	Hacer Trabajo
	// Hora en HH:MM. Vacío usa la del programador.
	Hora string
	// Dias limita a esos días de la semana. Vacío es todos los días.
	Dias []time.Weekday
	// Que es cómo se llama esto en la pantalla.
	Que string

	hora, minuto int
	proxima      time.Time
	ultima       time.Time
}

// Cuando escribe cuándo corre, para mostrarlo.
func (p Programado) Cuando() string {
	cuando := p.HoraTexto()
	if len(p.Dias) == 0 {
		return "todos los días a las " + cuando
	}
	var nombres []string
	for _, d := range p.Dias {
		nombres = append(nombres, nombreDeDia(d))
	}
	return strings.Join(nombres, ", ") + " a las " + cuando
}

// HoraTexto es la hora de este trabajo.
func (p Programado) HoraTexto() string {
	return leerDosDigitos(p.hora) + ":" + leerDosDigitos(p.minuto)
}

// Proxima es cuándo le toca.
func (p Programado) Proxima() time.Time { return p.proxima }

// Ultima es cuándo corrió por última vez, o el cero si nunca.
func (p Programado) Ultima() time.Time { return p.ultima }

// leTocaEl dice si este trabajo corre ese día.
func (p Programado) leTocaEl(dia time.Weekday) bool {
	if len(p.Dias) == 0 {
		return true
	}
	for _, d := range p.Dias {
		if d == dia {
			return true
		}
	}
	return false
}

var diasEnCastellano = [...]string{"domingo", "lunes", "martes", "miércoles",
	"jueves", "viernes", "sábado"}

func nombreDeDia(d time.Weekday) string { return diasEnCastellano[int(d)%7] }

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

// NuevoProgramador arma el programador. La hora se escribe HH:MM y es la que
// usan los trabajos que no traigan la suya.
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

// Agregar suma un trabajo. Si no trae hora propia usa la del programador; si
// la trae mal escrita, devuelve error en vez de correr a una hora inventada.
func (p *Programador) Agregar(t Programado) error {
	t.hora, t.minuto = p.hora, p.minuto
	if strings.TrimSpace(t.Hora) != "" {
		h, m, err := leerHora(t.Hora)
		if err != nil {
			return err
		}
		t.hora, t.minuto = h, m
	}
	if t.Que == "" {
		t.Que = t.Tipo
	}
	t.proxima = p.siguienteDe(t, time.Now().In(p.zona))

	p.mu.Lock()
	defer p.mu.Unlock()
	p.trabajos = append(p.trabajos, t)
	p.recalcularProxima()
	return nil
}

// siguienteDe calcula cuándo le toca a un trabajo, respetando sus días.
func (p *Programador) siguienteDe(t Programado, desde time.Time) time.Time {
	cita := time.Date(desde.Year(), desde.Month(), desde.Day(), t.hora, t.minuto, 0, 0, p.zona)
	if !cita.After(desde) {
		cita = cita.AddDate(0, 0, 1)
	}
	// Hasta siete días para adelante: con siete alcanza para encontrar
	// cualquier día de la semana, y sin tope un trabajo con días imposibles
	// dejaría el cálculo dando vueltas para siempre.
	for i := 0; i < 7; i++ {
		if t.leTocaEl(cita.Weekday()) {
			return cita
		}
		cita = cita.AddDate(0, 0, 1)
	}
	return cita
}

// recalcularProxima deja en proxima la más cercana de todas. Se llama con el
// candado tomado.
func (p *Programador) recalcularProxima() {
	var proxima time.Time
	for _, t := range p.trabajos {
		if proxima.IsZero() || t.proxima.Before(proxima) {
			proxima = t.proxima
		}
	}
	p.proxima = proxima
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

// correr lanza los trabajos a los que ya les tocaba.
func (p *Programador) correr(ahora time.Time) {
	p.mu.Lock()
	var vencidos []Programado
	for i := range p.trabajos {
		if p.trabajos[i].proxima.After(ahora) {
			continue // a éste todavía no le toca
		}
		p.trabajos[i].ultima = ahora
		p.trabajos[i].proxima = p.siguienteDe(p.trabajos[i], ahora)
		vencidos = append(vencidos, p.trabajos[i])
	}
	if len(vencidos) > 0 {
		p.ultima = ahora
	}
	p.recalcularProxima()
	p.mu.Unlock()

	for _, t := range vencidos {
		// Lanzar devuelve ErrYaCorre si alguien la lanzó a mano hace un rato:
		// eso no es un problema, es la razón de que exista ese error.
		if err := p.ejecutor.Lanzar(t.Tipo, "la actualización automática", t.Hacer); err != nil {
			slog.Info("no se lanzó la actualización automática", "tarea", t.Tipo, "por", err)
			continue
		}
		slog.Info("actualización automática lanzada", "tarea", t.Tipo, "cuando", t.Cuando())
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

// Trabajos son los que corre, con su horario y su próxima vuelta.
func (p *Programador) Trabajos() []Programado {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Programado(nil), p.trabajos...)
}

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
