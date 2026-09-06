package tareas

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLeerHora(t *testing.T) {
	for entrada, esperado := range map[string][2]int{
		"05:00": {5, 0}, "5:00": {5, 0}, "23:59": {23, 59},
		"00:00": {0, 0}, " 05:30 ": {5, 30},
	} {
		h, m, err := leerHora(entrada)
		if err != nil || h != esperado[0] || m != esperado[1] {
			t.Errorf("%q -> %d:%d (%v), se esperaba %d:%d", entrada, h, m, err, esperado[0], esperado[1])
		}
	}
	for _, mala := range []string{"", "5", "24:00", "05:60", "-1:00", "cinco", "05:00:00 pm"} {
		if _, _, err := leerHora(mala); err == nil {
			t.Errorf("se aceptó la hora %q", mala)
		}
	}
}

// La zona horaria tiene que existir dentro del binario: la imagen es alpine y
// no trae la base de zonas, así que sin el import de tzdata la hora se
// correría tres horas sin que nadie lo note.
func TestLaZonaArgentinaExisteEnElBinario(t *testing.T) {
	loc, err := time.LoadLocation(ZonaArgentina)
	if err != nil {
		t.Fatalf("no está la zona %s: %v", ZonaArgentina, err)
	}
	// Argentina está tres horas detrás de UTC, todo el año.
	_, offset := time.Date(2026, 1, 15, 12, 0, 0, 0, loc).Zone()
	if offset != -3*3600 {
		t.Errorf("en enero el offset es %d y tendría que ser -10800", offset)
	}
	_, offset = time.Date(2026, 7, 15, 12, 0, 0, 0, loc).Zone()
	if offset != -3*3600 {
		t.Errorf("en julio el offset es %d y tendría que ser -10800", offset)
	}
}

func TestLaProximaEsManana(t *testing.T) {
	p, err := NuevoProgramador(Nuevo(), "05:00", ZonaArgentina)
	if err != nil {
		t.Fatal(err)
	}
	// Sin trabajos no hay próxima: decir una hora sin nada que correr sería
	// inventarla.
	if !p.Proxima().IsZero() {
		t.Error("dice que tiene una próxima vuelta sin trabajos")
	}
	if err := p.Agregar(Programado{Tipo: "x", Hacer: nada}); err != nil {
		t.Fatal(err)
	}
	prox := p.Proxima()
	if prox.Hour() != 5 || prox.Minute() != 0 {
		t.Errorf("la próxima es a las %02d:%02d", prox.Hour(), prox.Minute())
	}
	if !prox.After(time.Now()) {
		t.Error("la próxima ya pasó")
	}
	// Y como mucho es mañana.
	if prox.After(time.Now().Add(25 * time.Hour)) {
		t.Errorf("la próxima es dentro de %s", time.Until(prox))
	}
}

func TestSiguienteDespuesDe(t *testing.T) {
	p, err := NuevoProgramador(Nuevo(), "05:00", ZonaArgentina)
	if err != nil {
		t.Fatal(err)
	}
	loc := p.zona
	todos := Programado{hora: 5, minuto: 0}

	// A las 3 de la mañana, la próxima es hoy a las 5.
	tresAM := time.Date(2026, 9, 5, 3, 0, 0, 0, loc)
	sig := p.siguienteDe(todos, tresAM)
	if sig.Day() != 5 || sig.Hour() != 5 {
		t.Errorf("desde las 3 AM -> %s", sig)
	}
	// A las 6, ya pasó: la próxima es mañana.
	seisAM := time.Date(2026, 9, 5, 6, 0, 0, 0, loc)
	sig = p.siguienteDe(todos, seisAM)
	if sig.Day() != 6 || sig.Hour() != 5 {
		t.Errorf("desde las 6 AM -> %s", sig)
	}
	// Justo a las 5:00 ya pasó por un instante: va mañana, y no dos veces hoy.
	justo := time.Date(2026, 9, 5, 5, 0, 0, 0, loc)
	sig = p.siguienteDe(todos, justo)
	if sig.Day() != 6 {
		t.Errorf("justo a las 5 -> %s", sig)
	}
}

// Cuando llega la hora, lanza los trabajos.
func TestDisparaALaHora(t *testing.T) {
	e := Nuevo()
	p, err := NuevoProgramador(e, "05:00", ZonaArgentina)
	if err != nil {
		t.Fatal(err)
	}
	var corrio sync.WaitGroup
	corrio.Add(2)
	for _, tipo := range []string{"infoleg", "provincial"} {
		if err := p.Agregar(Programado{Tipo: tipo, Hacer: func(context.Context, func(string)) (string, error) {
			corrio.Done()
			return "listo", nil
		}}); err != nil {
			t.Fatal(err)
		}
	}

	// Se dispara a mano en el momento de la cita, que es lo que hace el reloj
	// cuando llega la hora.
	p.correr(p.Proxima())

	listo := make(chan struct{})
	go func() { corrio.Wait(); close(listo) }()
	select {
	case <-listo:
	case <-time.After(3 * time.Second):
		t.Fatal("no corrieron los trabajos")
	}
	// Y quedó anotado quién los lanzó.
	if x := e.Estado("infoleg"); !strings.Contains(x.QuienLaLanzo, "automática") {
		t.Errorf("quien = %q", x.QuienLaLanzo)
	}
}

// Después de correr, la próxima es al día siguiente y no se repite.
func TestNoSeRepiteElMismoDia(t *testing.T) {
	e := Nuevo()
	p, _ := NuevoProgramador(e, "05:00", ZonaArgentina)
	var veces int
	var mu sync.Mutex
	if err := p.Agregar(Programado{Tipo: "infoleg", Hacer: func(context.Context, func(string)) (string, error) {
		mu.Lock()
		veces++
		mu.Unlock()
		return "", nil
	}}); err != nil {
		t.Fatal(err)
	}

	cita := p.Proxima()
	p.correr(cita)
	// Al día siguiente, y no dos veces el mismo día.
	if siguiente := p.Proxima(); !siguiente.Equal(cita.AddDate(0, 0, 1)) {
		t.Errorf("la próxima quedó en %s y la anterior era %s", siguiente, cita)
	}
	if p.Ultima().IsZero() {
		t.Error("no quedó anotado cuándo corrió")
	}
	// Esperar a que termine antes de mirar la cuenta.
	esperarA(t, e, "infoleg")
	mu.Lock()
	defer mu.Unlock()
	if veces != 1 {
		t.Errorf("corrió %d veces", veces)
	}
}

// Si el trabajo ya está corriendo —porque alguien lo lanzó a mano hace un
// rato— la automática no lo pisa.
func TestNoPisaLoQueYaCorre(t *testing.T) {
	e := Nuevo()
	p, _ := NuevoProgramador(e, "05:00", ZonaArgentina)
	seguir := make(chan struct{})
	defer close(seguir)

	// Alguien lo lanzó a mano.
	e.Lanzar("infoleg", "diego", func(context.Context, func(string)) (string, error) {
		<-seguir
		return "", nil
	})
	for e.Estado("infoleg").Estado != Corriendo {
		time.Sleep(time.Millisecond)
	}

	var automatica bool
	if err := p.Agregar(Programado{Tipo: "infoleg", Hacer: func(context.Context, func(string)) (string, error) {
		automatica = true
		return "", nil
	}}); err != nil {
		t.Fatal(err)
	}
	p.correr(time.Now().In(p.zona))
	time.Sleep(50 * time.Millisecond)

	if automatica {
		t.Error("la automática se lanzó encima de la que ya corría")
	}
	if x := e.Estado("infoleg"); x.QuienLaLanzo != "diego" {
		t.Errorf("le pisó la tarea a %q", x.QuienLaLanzo)
	}
}

func TestArrancarYFrenar(t *testing.T) {
	p, _ := NuevoProgramador(Nuevo(), "05:00", ZonaArgentina)
	p.Arrancar()
	p.Arrancar() // dos veces no arranca dos relojes
	p.Frenar()
	p.Frenar() // ni frenar dos veces explota
}

func TestHoraTextoYZona(t *testing.T) {
	p, _ := NuevoProgramador(Nuevo(), "5:07", ZonaArgentina)
	if got := p.HoraTexto(); got != "05:07" {
		t.Errorf("hora = %q", got)
	}
	if got := p.Zona(); got != ZonaArgentina {
		t.Errorf("zona = %q", got)
	}
}

func TestZonaQueNoExiste(t *testing.T) {
	if _, err := NuevoProgramador(Nuevo(), "05:00", "Marte/Olympus"); err == nil {
		t.Error("se aceptó una zona que no existe")
	}
}

func nada(context.Context, func(string)) (string, error) { return "", nil }

// Un trabajo puede correr sólo algunos días: bajar un catálogo son minutos y
// va todos los días, pero bajar el texto de una semana del Boletín son horas y
// conviene hacerlo con la máquina tranquila.
func TestUnTrabajoSemanalCorreSuDia(t *testing.T) {
	p, err := NuevoProgramador(Nuevo(), "05:00", ZonaArgentina)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Agregar(Programado{
		Tipo: "boletin", Hacer: nada,
		Hora: "04:00", Dias: []time.Weekday{time.Saturday},
	}); err != nil {
		t.Fatal(err)
	}
	trabajos := p.Trabajos()
	if len(trabajos) != 1 {
		t.Fatalf("quedaron %d trabajos", len(trabajos))
	}
	prox := trabajos[0].Proxima()
	if prox.Weekday() != time.Saturday {
		t.Errorf("la próxima cae %s", prox.Weekday())
	}
	if prox.Hour() != 4 || prox.Minute() != 0 {
		t.Errorf("la próxima es a las %02d:%02d", prox.Hour(), prox.Minute())
	}
	if !prox.After(time.Now()) || prox.After(time.Now().Add(8*24*time.Hour)) {
		t.Errorf("la próxima es %s", prox)
	}
	if got := trabajos[0].Cuando(); got != "sábado a las 04:00" {
		t.Errorf("se describe como %q", got)
	}
}

// Y los que tienen horarios distintos no se pisan: cada uno corre al suyo.
func TestCadaTrabajoCorreASuHora(t *testing.T) {
	e := Nuevo()
	p, _ := NuevoProgramador(e, "05:00", ZonaArgentina)
	var corrieron sync.Map
	for _, caso := range []struct {
		tipo string
		hora string
		dias []time.Weekday
	}{
		{"infoleg", "", nil},
		{"boletin", "04:00", []time.Weekday{time.Saturday}},
	} {
		tipo := caso.tipo
		if err := p.Agregar(Programado{
			Tipo: tipo, Hora: caso.hora, Dias: caso.dias,
			Hacer: func(context.Context, func(string)) (string, error) {
				corrieron.Store(tipo, true)
				return "", nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// A la hora del diario, el semanal no corre —salvo que ese día sea sábado
	// y ya le tocara, que no es el caso a las cinco.
	trabajos := p.Trabajos()
	var citaDelDiario time.Time
	for _, tr := range trabajos {
		if tr.Tipo == "infoleg" {
			citaDelDiario = tr.Proxima()
		}
	}
	p.correr(citaDelDiario)
	esperarA(t, e, "infoleg")

	if _, corrio := corrieron.Load("infoleg"); !corrio {
		t.Error("el diario no corrió a su hora")
	}
	if _, corrio := corrieron.Load("boletin"); corrio {
		t.Error("el semanal corrió a la hora del diario")
	}
}
