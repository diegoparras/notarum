// Package tareas corre los trabajos largos del servicio y cuenta cómo van.
//
// Sincronizar InfoLEG son 428 mil normas y varios minutos; el catálogo
// provincial, 81 mil; rellenar un año de ediciones, cientos de pedidos al
// Boletín. Nada de eso entra en un pedido HTTP: el navegador se cansa antes.
//
// Así que se lanzan acá y se pregunta después. Es lo que permite que todo se
// haga desde la interfaz en vez de por consola.
package tareas

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Estado dice en qué anda una tarea.
type Estado string

const (
	// Nunca es una tarea que no se corrió en esta instancia.
	Nunca Estado = "nunca"
	// Corriendo, ahora mismo.
	Corriendo Estado = "corriendo"
	// Terminada bien.
	Terminada Estado = "terminada"
	// Fallada: terminó con error.
	Fallada Estado = "fallada"
	// Cortada por quien la lanzó, o por el apagado del servicio.
	Cortada Estado = "cortada"
)

// Tarea es cómo le fue a un trabajo.
type Tarea struct {
	Tipo   string `json:"tipo"`
	Estado Estado `json:"estado"`
	// Avance es la última novedad que dejó la tarea: "guardando normas
	// (140.000)". Se muestra tal cual.
	Avance string `json:"avance,omitempty"`
	// Resultado es lo que dejó al terminar bien.
	Resultado string `json:"resultado,omitempty"`
	// Error es por qué falló, en palabras que sirvan para arreglarlo.
	Error string `json:"error,omitempty"`

	Empezo  time.Time `json:"empezo,omitempty"`
	Termino time.Time `json:"termino,omitempty"`
	// QuienLaLanzo queda anotado: son acciones que cambian lo que sirve la
	// instancia, y conviene saber de quién salieron.
	QuienLaLanzo string `json:"quien,omitempty"`
}

// Duracion es cuánto tardó, o cuánto lleva si sigue corriendo.
func (t Tarea) Duracion() time.Duration {
	if t.Empezo.IsZero() {
		return 0
	}
	if t.Termino.IsZero() {
		return time.Since(t.Empezo)
	}
	return t.Termino.Sub(t.Empezo)
}

// Estos son para las plantillas, que no pueden comparar un Estado con un
// texto suelto: son tipos distintos y la comparación da error en silencio.
func (t Tarea) EstaCorriendo() bool { return t.Estado == Corriendo }
func (t Tarea) SalioBien() bool     { return t.Estado == Terminada }
func (t Tarea) Fallo() bool         { return t.Estado == Fallada }
func (t Tarea) SeCorto() bool       { return t.Estado == Cortada }
func (t Tarea) NuncaCorrio() bool   { return t.Estado == Nunca || t.Estado == "" }

// ErrYaCorre es intentar lanzar algo que ya está corriendo. No es una falla:
// es la respuesta correcta a apretar el botón dos veces.
var ErrYaCorre = errors.New("esa tarea ya está corriendo")

// Ejecutor corre las tareas y guarda cómo les fue.
//
// Una de cada tipo por vez: dos sincronizaciones del mismo catálogo a la vez
// se pisarían, y no hay nada que ganar.
type Ejecutor struct {
	mu     sync.RWMutex
	tareas map[string]*Tarea
	cortar map[string]context.CancelFunc
	// esperar deja que el apagado del servicio espere a lo que esté corriendo.
	esperar sync.WaitGroup
}

func Nuevo() *Ejecutor {
	return &Ejecutor{
		tareas: map[string]*Tarea{},
		cortar: map[string]context.CancelFunc{},
	}
}

// Trabajo es lo que hace una tarea. Recibe con qué avisar cómo va; avisar
// puede llamarse cuantas veces quiera.
type Trabajo func(ctx context.Context, avisar func(string)) (resultado string, err error)

// Lanzar arranca una tarea si no hay otra igual corriendo.
func (e *Ejecutor) Lanzar(tipo, quien string, hacer Trabajo) error {
	e.mu.Lock()
	if t, hay := e.tareas[tipo]; hay && t.Estado == Corriendo {
		e.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrYaCorre, tipo)
	}
	// El contexto es propio de la tarea y no el del pedido HTTP que la lanzó:
	// si dependiera de ese, la tarea se cortaría en cuanto quien apretó el
	// botón cerrara la pestaña.
	ctx, cancelar := context.WithCancel(context.Background())
	e.tareas[tipo] = &Tarea{
		Tipo: tipo, Estado: Corriendo, Empezo: time.Now(), QuienLaLanzo: quien,
	}
	e.cortar[tipo] = cancelar
	e.esperar.Add(1)
	e.mu.Unlock()

	go func() {
		defer e.esperar.Done()
		defer cancelar()

		avisar := func(novedad string) {
			e.mu.Lock()
			if t, hay := e.tareas[tipo]; hay {
				t.Avance = novedad
			}
			e.mu.Unlock()
		}

		// Un panic en una tarea no puede llevarse el servicio puesto: se
		// anota como falla y el resto sigue andando.
		defer func() {
			if r := recover(); r != nil {
				e.terminar(tipo, Fallada, "", fmt.Sprintf("la tarea se rompió: %v", r))
			}
		}()

		resultado, err := hacer(ctx, avisar)
		switch {
		case err != nil && errors.Is(err, context.Canceled):
			e.terminar(tipo, Cortada, resultado, "")
		case err != nil:
			e.terminar(tipo, Fallada, "", err.Error())
		default:
			e.terminar(tipo, Terminada, resultado, "")
		}
	}()
	return nil
}

func (e *Ejecutor) terminar(tipo string, estado Estado, resultado, err string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, hay := e.tareas[tipo]
	if !hay {
		return
	}
	t.Estado, t.Resultado, t.Error, t.Termino = estado, resultado, err, time.Now()
	delete(e.cortar, tipo)
}

// Estado dice cómo va una tarea. Con una que nunca corrió devuelve el estado
// Nunca, que es distinto de una que corrió y no hizo nada.
func (e *Ejecutor) Estado(tipo string) Tarea {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if t, hay := e.tareas[tipo]; hay {
		return *t
	}
	return Tarea{Tipo: tipo, Estado: Nunca}
}

// Todas devuelve el estado de todo lo que corrió, por tipo.
func (e *Ejecutor) Todas() []Tarea {
	e.mu.RLock()
	defer e.mu.RUnlock()
	todas := make([]Tarea, 0, len(e.tareas))
	for _, t := range e.tareas {
		todas = append(todas, *t)
	}
	sort.Slice(todas, func(a, b int) bool { return todas[a].Tipo < todas[b].Tipo })
	return todas
}

// AlgoCorriendo dice si hay alguna en curso.
func (e *Ejecutor) AlgoCorriendo() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, t := range e.tareas {
		if t.Estado == Corriendo {
			return true
		}
	}
	return false
}

// Cortar le pide a una tarea que se detenga. Lo que haya alcanzado a hacer
// queda: las sincronizaciones guardan a medida que avanzan.
func (e *Ejecutor) Cortar(tipo string) bool {
	e.mu.Lock()
	cancelar, hay := e.cortar[tipo]
	e.mu.Unlock()
	if !hay {
		return false
	}
	cancelar()
	return true
}

// Esperar corta todo y espera a que termine. Se usa al apagar el servicio,
// para no dejar una sincronización a mitad de camino sin que nadie lo sepa.
func (e *Ejecutor) Esperar(hasta time.Duration) {
	e.mu.Lock()
	for _, cancelar := range e.cortar {
		cancelar()
	}
	e.mu.Unlock()

	listo := make(chan struct{})
	go func() {
		e.esperar.Wait()
		close(listo)
	}()
	select {
	case <-listo:
	case <-time.After(hasta):
	}
}
