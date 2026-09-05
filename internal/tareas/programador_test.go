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

	// A las 3 de la mañana, la próxima es hoy a las 5.
	tresAM := time.Date(2026, 9, 5, 3, 0, 0, 0, loc)
	sig := p.siguienteDespuesDe(tresAM)
	if sig.Day() != 5 || sig.Hour() != 5 {
		t.Errorf("desde las 3 AM -> %s", sig)
	}
	// A las 6, ya pasó: la próxima es mañana.
	seisAM := time.Date(2026, 9, 5, 6, 0, 0, 0, loc)
	sig = p.siguienteDespuesDe(seisAM)
	if sig.Day() != 6 || sig.Hour() != 5 {
		t.Errorf("desde las 6 AM -> %s", sig)
	}
	// Justo a las 5:00 ya pasó por un instante: va mañana, y no dos veces hoy.
	justo := time.Date(2026, 9, 5, 5, 0, 0, 0, loc)
	sig = p.siguienteDespuesDe(justo)
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
		p.Agregar(Programado{Tipo: tipo, Hacer: func(context.Context, func(string)) (string, error) {
			corrio.Done()
			return "listo", nil
		}})
	}

	// Se dispara a mano, que es lo que hace el reloj cuando llega la hora.
	p.correr(time.Now().In(p.zona))

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
	p.Agregar(Programado{Tipo: "infoleg", Hacer: func(context.Context, func(string)) (string, error) {
		mu.Lock()
		veces++
		mu.Unlock()
		return "", nil
	}})

	ahora := time.Date(2026, 9, 5, 5, 0, 0, 0, p.zona)
	p.correr(ahora)
	if p.Proxima().Day() != 6 {
		t.Errorf("la próxima quedó el día %d", p.Proxima().Day())
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
	p.Agregar(Programado{Tipo: "infoleg", Hacer: func(context.Context, func(string)) (string, error) {
		automatica = true
		return "", nil
	}})
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
