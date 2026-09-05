package tareas

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// esperarA espera a que una tarea deje de correr, sin dormir de más.
func esperarA(t *testing.T, e *Ejecutor, tipo string) Tarea {
	t.Helper()
	hasta := time.Now().Add(5 * time.Second)
	for time.Now().Before(hasta) {
		if x := e.Estado(tipo); !x.EstaCorriendo() {
			return x
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("la tarea %s no terminó", tipo)
	return Tarea{}
}

func TestUnaTareaQueTerminaBien(t *testing.T) {
	e := Nuevo()
	if got := e.Estado("infoleg").Estado; got != Nunca {
		t.Errorf("antes de correr = %q", got)
	}

	err := e.Lanzar("infoleg", "diego", func(ctx context.Context, avisar func(string)) (string, error) {
		avisar("guardando normas (100)")
		return "428.000 normas", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	x := esperarA(t, e, "infoleg")
	if x.Estado != Terminada {
		t.Errorf("estado = %q, error = %q", x.Estado, x.Error)
	}
	if x.Resultado != "428.000 normas" {
		t.Errorf("resultado = %q", x.Resultado)
	}
	if x.QuienLaLanzo != "diego" {
		t.Errorf("quien = %q", x.QuienLaLanzo)
	}
	if x.Empezo.IsZero() || x.Termino.IsZero() || x.Duracion() < 0 {
		t.Errorf("los tiempos no cierran: %+v", x)
	}
}

// El error se cuenta con sus palabras: quien mira la pantalla tiene que poder
// saber qué arreglar.
func TestUnaTareaQueFalla(t *testing.T) {
	e := Nuevo()
	e.Lanzar("provincial", "diego", func(context.Context, func(string)) (string, error) {
		return "", errors.New("el portal contestó 503")
	})
	x := esperarA(t, e, "provincial")
	if x.Estado != Fallada {
		t.Fatalf("estado = %q", x.Estado)
	}
	if !strings.Contains(x.Error, "503") {
		t.Errorf("error = %q", x.Error)
	}
}

// Un panic en una tarea no puede llevarse el servicio puesto.
func TestUnaTareaQueSeRompe(t *testing.T) {
	e := Nuevo()
	e.Lanzar("rota", "diego", func(context.Context, func(string)) (string, error) {
		var p *int
		_ = *p // adrede
		return "", nil
	})
	x := esperarA(t, e, "rota")
	if x.Estado != Fallada {
		t.Fatalf("estado = %q", x.Estado)
	}
	if !strings.Contains(x.Error, "se rompió") {
		t.Errorf("error = %q", x.Error)
	}
	// Y el ejecutor sigue sirviendo.
	if err := e.Lanzar("otra", "diego", func(context.Context, func(string)) (string, error) {
		return "listo", nil
	}); err != nil {
		t.Fatalf("después de un panic no se pudo lanzar otra: %v", err)
	}
	if x := esperarA(t, e, "otra"); x.Estado != Terminada {
		t.Errorf("la siguiente quedó en %q", x.Estado)
	}
}

// Apretar el botón dos veces no lanza dos sincronizaciones.
func TestNoSeLanzaDosVecesLoMismo(t *testing.T) {
	e := Nuevo()
	seguir := make(chan struct{})
	var corridas int
	var mu sync.Mutex

	arrancar := func() error {
		return e.Lanzar("infoleg", "diego", func(context.Context, func(string)) (string, error) {
			mu.Lock()
			corridas++
			mu.Unlock()
			<-seguir
			return "", nil
		})
	}
	if err := arrancar(); err != nil {
		t.Fatal(err)
	}
	// Esperar a que esté efectivamente corriendo.
	for e.Estado("infoleg").Estado != Corriendo {
		time.Sleep(time.Millisecond)
	}
	if err := arrancar(); !errors.Is(err, ErrYaCorre) {
		t.Fatalf("la segunda dio %v; se esperaba ErrYaCorre", err)
	}
	close(seguir)
	esperarA(t, e, "infoleg")

	mu.Lock()
	defer mu.Unlock()
	if corridas != 1 {
		t.Errorf("corrió %d veces", corridas)
	}
}

// Pero una vez terminada, se puede volver a lanzar.
func TestSePuedeVolverALanzar(t *testing.T) {
	e := Nuevo()
	for k := 0; k < 3; k++ {
		if err := e.Lanzar("infoleg", "diego", func(context.Context, func(string)) (string, error) {
			return "ok", nil
		}); err != nil {
			t.Fatalf("intento %d: %v", k, err)
		}
		esperarA(t, e, "infoleg")
	}
}

// El avance se ve mientras corre: es lo único que tiene quien mira una tarea
// de varios minutos.
func TestElAvanceSeVeMientrasCorre(t *testing.T) {
	e := Nuevo()
	aviso := make(chan struct{})
	seguir := make(chan struct{})
	e.Lanzar("infoleg", "diego", func(_ context.Context, avisar func(string)) (string, error) {
		avisar("guardando normas (20.000)")
		close(aviso)
		<-seguir
		return "listo", nil
	})
	<-aviso
	if got := e.Estado("infoleg").Avance; got != "guardando normas (20.000)" {
		t.Errorf("avance = %q", got)
	}
	close(seguir)
	esperarA(t, e, "infoleg")
}

// Cortar una tarea la detiene y lo dice, en vez de dejarla como fallada.
func TestCortarUnaTarea(t *testing.T) {
	e := Nuevo()
	corriendo := make(chan struct{})
	e.Lanzar("larga", "diego", func(ctx context.Context, _ func(string)) (string, error) {
		close(corriendo)
		<-ctx.Done()
		return "lo que alcanzó", ctx.Err()
	})
	<-corriendo
	if !e.Cortar("larga") {
		t.Fatal("no se pudo cortar")
	}
	x := esperarA(t, e, "larga")
	if x.Estado != Cortada {
		t.Errorf("estado = %q", x.Estado)
	}
	// Lo que alcanzó a hacer se cuenta: las sincronizaciones guardan a medida
	// que avanzan.
	if x.Resultado != "lo que alcanzó" {
		t.Errorf("resultado = %q", x.Resultado)
	}
	// Cortar algo que no corre no explota.
	if e.Cortar("larga") || e.Cortar("inventada") {
		t.Error("dijo haber cortado algo que no estaba corriendo")
	}
}

// La tarea no se corta porque quien la lanzó cerró la pestaña: vive por su
// cuenta.
func TestLaTareaSobreviveAlPedidoQueLaLanzo(t *testing.T) {
	e := Nuevo()
	pedido, cancelarPedido := context.WithCancel(context.Background())
	listo := make(chan struct{})

	e.Lanzar("infoleg", "diego", func(ctx context.Context, _ func(string)) (string, error) {
		cancelarPedido() // como si se cerrara la pestaña
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		close(listo)
		return "terminó igual", nil
	})
	<-pedido.Done()
	select {
	case <-listo:
	case <-time.After(3 * time.Second):
		t.Fatal("la tarea se cortó cuando se fue quien la lanzó")
	}
	if x := esperarA(t, e, "infoleg"); x.Estado != Terminada {
		t.Errorf("estado = %q", x.Estado)
	}
}

func TestTodasYAlgoCorriendo(t *testing.T) {
	e := Nuevo()
	if e.AlgoCorriendo() {
		t.Error("dice que algo corre en un ejecutor recién hecho")
	}
	if len(e.Todas()) != 0 {
		t.Error("hay tareas en un ejecutor recién hecho")
	}

	seguir := make(chan struct{})
	e.Lanzar("infoleg", "diego", func(context.Context, func(string)) (string, error) {
		<-seguir
		return "", nil
	})
	for !e.AlgoCorriendo() {
		time.Sleep(time.Millisecond)
	}
	e.Lanzar("provincial", "diego", func(context.Context, func(string)) (string, error) {
		return "ok", nil
	})
	esperarA(t, e, "provincial")

	todas := e.Todas()
	if len(todas) != 2 {
		t.Fatalf("tareas = %d", len(todas))
	}
	// Ordenadas por tipo, para que la lista no baile entre recargas.
	if todas[0].Tipo != "infoleg" || todas[1].Tipo != "provincial" {
		t.Errorf("orden = %q, %q", todas[0].Tipo, todas[1].Tipo)
	}
	close(seguir)
	esperarA(t, e, "infoleg")
	if e.AlgoCorriendo() {
		t.Error("sigue diciendo que algo corre")
	}
}

// Al apagar, se corta lo que haya y se espera un rato: mejor que dejar una
// sincronización a mitad sin que nadie lo sepa.
func TestEsperarAlApagar(t *testing.T) {
	e := Nuevo()
	termino := make(chan struct{})
	e.Lanzar("larga", "diego", func(ctx context.Context, _ func(string)) (string, error) {
		<-ctx.Done()
		close(termino)
		return "", ctx.Err()
	})
	for !e.AlgoCorriendo() {
		time.Sleep(time.Millisecond)
	}

	empezo := time.Now()
	e.Esperar(3 * time.Second)
	if time.Since(empezo) > 2*time.Second {
		t.Error("esperar tardó más de lo que la tarea tardó en cortarse")
	}
	select {
	case <-termino:
	default:
		t.Error("la tarea no se enteró del apagado")
	}
}

// Y si una tarea ignora el corte, el apagado no se queda esperando para
// siempre.
func TestElApagadoNoSeCuelga(t *testing.T) {
	e := Nuevo()
	soltar := make(chan struct{})
	defer close(soltar)
	e.Lanzar("terca", "diego", func(context.Context, func(string)) (string, error) {
		<-soltar // no mira el contexto
		return "", nil
	})
	for !e.AlgoCorriendo() {
		time.Sleep(time.Millisecond)
	}
	empezo := time.Now()
	e.Esperar(100 * time.Millisecond)
	if d := time.Since(empezo); d > time.Second {
		t.Errorf("el apagado esperó %s", d)
	}
}
