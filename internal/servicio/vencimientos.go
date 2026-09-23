package servicio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/vencimientos"
)

// La agenda de vencimientos de ARCA: qué obligación impositiva vence cada día,
// para qué terminación de CUIT y bajo qué norma. Y, lo que ningún calendario
// publica, qué fecha movió ARCA: sale de comparar cada bajada con la anterior.

const (
	claveEstadoVencimientos = "vencimientos/_estado"
	// La agenda y el registro de cambios van en una sola entrada: se escriben
	// juntos, así que nunca puede quedar una agenda nueva con los cambios que
	// la produjeron perdidos, ni al revés. Con cualquier motor.
	claveDatosVencimientos = "vencimientos/agenda"

	// maxCambiosGuardados acota el registro de cambios. Son unos cientos por
	// año; esto alcanza para muchos años sin que la entrada crezca sin techo.
	maxCambiosGuardados = 5000
)

// pisoDeFilas es lo mínimo que se acepta de una bajada, contra las ~6.000 de
// un año real. Una respuesta cortada a la mitad se lee perfecto y, si se
// aceptara, daría de baja todo lo que no alcanzó a traer. Rechazarla es
// ruidoso; aceptarla sería silencioso. Es variable sólo para los tests, que
// trabajan con una muestra chica.
var pisoDeFilas = 200

// datosVencimientos es lo que se guarda.
type datosVencimientos struct {
	Registros []vencimientos.Registro `json:"registros"`
	// Cambios va de lo más nuevo a lo más viejo.
	Cambios []vencimientos.Cambio `json:"cambios"`
}

// EstadoVencimientos cuenta qué se sabe de la agenda.
type EstadoVencimientos struct {
	Sincronizado   bool      `json:"sincronizado"`
	SincronizadoEn time.Time `json:"sincronizado_en,omitzero"`
	// Desde y Hasta son la ventana que se le pidió a ARCA en la última bajada.
	// Afuera de ella no se preguntó nada, así que una ausencia ahí no dice que
	// no haya vencimiento.
	Desde string `json:"desde,omitempty"`
	Hasta string `json:"hasta,omitempty"`
	// Filas son las que trajo la última bajada; Vigentes, todas las que hay.
	Filas    int                  `json:"filas"`
	Vigentes int                  `json:"vigentes"`
	Resumen  vencimientos.Resumen `json:"ultima_bajada"`
	Avisos   []string             `json:"avisos,omitempty"`
	// Impuestos es el catálogo del formulario de ARCA.
	Impuestos []vencimientos.Impuesto `json:"-"`
}

// ConVencimientos habilita la agenda de vencimientos.
func (s *Servicio) ConVencimientos(c *vencimientos.Cliente) *Servicio {
	s.venc = c
	return s
}

// VencimientosDisponible dice si esta instancia sigue la agenda de ARCA.
func (s *Servicio) VencimientosDisponible() bool { return s.venc != nil }

// EstadoVencimientos lee lo que quedó de la última bajada.
func (s *Servicio) EstadoVencimientos() EstadoVencimientos {
	var e EstadoVencimientos
	if crudo, ok := s.cache.Leer(claveEstadoVencimientos); ok {
		var guardado struct {
			EstadoVencimientos
			Impuestos []vencimientos.Impuesto `json:"impuestos"`
		}
		if json.Unmarshal(crudo, &guardado) == nil {
			e = guardado.EstadoVencimientos
			e.Impuestos = guardado.Impuestos
		}
	}
	return e
}

func (s *Servicio) guardarEstadoVencimientos(e EstadoVencimientos) {
	crudo, err := json.Marshal(struct {
		EstadoVencimientos
		Impuestos []vencimientos.Impuesto `json:"impuestos"`
	}{e, e.Impuestos})
	if err == nil {
		_ = s.cache.Guardar(claveEstadoVencimientos, crudo, almacen.SinVencimiento)
	}
}

// indiceVencimientos devuelve la agenda en memoria, cargándola de nuevo si
// otra instancia del programa —`notarum vencimientos` corrido aparte— bajó una
// más nueva. Es el mismo criterio que la normativa provincial.
func (s *Servicio) indiceVencimientos() (*vencimientos.Indice, []vencimientos.Cambio) {
	s.vencMu.RLock()
	ix, cambios, cargado, mirado := s.vencIndice, s.vencCambios, s.vencCargado, s.vencMirado
	s.vencMu.RUnlock()
	if ix != nil && ix.Cargado() && time.Since(mirado) < cadaCuantoMirar {
		return ix, cambios
	}

	s.vencMu.Lock()
	defer s.vencMu.Unlock()
	if s.vencIndice != nil && s.vencIndice.Cargado() && s.vencCargado.After(cargado) {
		return s.vencIndice, s.vencCambios
	}
	if s.vencIndice == nil {
		s.vencIndice = vencimientos.NuevoIndice()
	}
	s.vencMirado = time.Now()

	e := s.EstadoVencimientos()
	if !e.Sincronizado || (s.vencIndice.Cargado() && !e.SincronizadoEn.After(s.vencCargado)) {
		return s.vencIndice, s.vencCambios
	}
	d, err := s.leerDatosVencimientos()
	if err != nil {
		slog.Error("no se pudo leer la agenda de vencimientos guardada", "err", err)
		return s.vencIndice, s.vencCambios
	}
	s.vencIndice.Reemplazar(d.Registros)
	s.vencCambios = d.Cambios
	s.vencCargado = e.SincronizadoEn
	return s.vencIndice, s.vencCambios
}

func (s *Servicio) leerDatosVencimientos() (datosVencimientos, error) {
	var d datosVencimientos
	crudo, hay := s.cache.Leer(claveDatosVencimientos)
	if !hay {
		return d, nil
	}
	err := json.Unmarshal(crudo, &d)
	return d, err
}

// AgendaCargada dice si hay una agenda con qué responder.
func (s *Servicio) AgendaCargada() bool {
	ix, _ := s.indiceVencimientos()
	return ix.Cargado()
}

// BuscarVencimientos busca en la agenda.
func (s *Servicio) BuscarVencimientos(q vencimientos.Consulta) *vencimientos.Resultado {
	ix, _ := s.indiceVencimientos()
	return ix.Buscar(q)
}

// CambiosDeVencimientos devuelve lo que ARCA movió desde un momento, lo más
// nuevo primero. Con desde en cero, todo.
func (s *Servicio) CambiosDeVencimientos(desde time.Time, limite int) []vencimientos.Cambio {
	_, cambios := s.indiceVencimientos()
	if limite <= 0 || limite > vencimientos.LimiteMaximo {
		limite = vencimientos.LimiteMaximo
	}
	out := []vencimientos.Cambio{}
	for _, c := range cambios {
		if !desde.IsZero() && c.DetectadoEn.Before(desde) {
			break // están de lo más nuevo a lo más viejo
		}
		if len(out) == limite {
			break
		}
		out = append(out, c)
	}
	return out
}

// ImpuestosDeVencimientos es el catálogo de ARCA con cuántos vencimientos
// vigentes tiene cada impuesto.
func (s *Servicio) ImpuestosDeVencimientos() []vencimientos.ConteoImpuesto {
	ix, _ := s.indiceVencimientos()
	return ix.PorImpuesto(s.EstadoVencimientos().Impuestos)
}

// SincronizarVencimientos baja la agenda, la compara con la que había y guarda
// las dos cosas: la agenda nueva y lo que cambió.
func (s *Servicio) SincronizarVencimientos(ctx context.Context) (EstadoVencimientos, error) {
	e := s.EstadoVencimientos()
	if s.venc == nil {
		return e, errors.New("esta instancia no tiene la agenda de vencimientos configurada")
	}
	ahora := time.Now().UTC()

	// Se pide hasta fin del año que viene. ARCA sólo tiene cargado el año en
	// curso hasta que publica el siguiente, y lo dice con un error propio: ahí
	// se pide lo que hay, y el día que cargue el año nuevo entra solo.
	desde, hasta := vencimientos.Ventana(ahora)
	ag, err := s.venc.Consultar(ctx, desde, hasta)
	var avisos []string
	if errors.Is(err, vencimientos.ErrRangoNoCargado) {
		desde, hasta = vencimientos.VentanaDeRespaldo(ahora)
		avisos = append(avisos, fmt.Sprintf("ARCA todavía no cargó la agenda más allá del %s; se pidió hasta ahí", hasta.Format("02/01/2006")))
		ag, err = s.venc.Consultar(ctx, desde, hasta)
	}
	if err != nil {
		return e, err
	}
	avisos = append(avisos, ag.Avisos...)

	if n := len(ag.Vencimientos); n < pisoDeFilas || (e.Filas > 0 && n < e.Filas/2) {
		return e, fmt.Errorf("la agenda vino con %d filas (la bajada anterior tuvo %d): parece cortada y se descarta; queda la que había", n, e.Filas)
	}

	anterior, err := s.leerDatosVencimientos()
	if err != nil {
		return e, fmt.Errorf("no se pudo leer la agenda guardada, y compararla contra nada daría todo por nuevo: %w", err)
	}
	registros, cambios, res := vencimientos.Comparar(anterior.Registros, ag, ahora)

	// Lo nuevo va adelante.
	todos := append(cambios, anterior.Cambios...)
	if len(todos) > maxCambiosGuardados {
		todos = todos[:maxCambiosGuardados]
	}
	crudo, err := json.Marshal(datosVencimientos{Registros: registros, Cambios: todos})
	if err != nil {
		return e, err
	}
	if err := s.cache.Guardar(claveDatosVencimientos, crudo, almacen.SinVencimiento); err != nil {
		return e, err
	}

	// El catálogo es un accesorio: si falla, la agenda ya quedó guardada.
	impuestos := e.Impuestos
	if cat, err := s.venc.Catalogo(ctx); err != nil {
		avisos = append(avisos, "no se pudo leer el catálogo de impuestos: "+err.Error())
	} else {
		impuestos = cat
	}

	vigentes := 0
	for _, r := range registros {
		if r.Vigente() {
			vigentes++
		}
	}
	e = EstadoVencimientos{
		Sincronizado: true, SincronizadoEn: ahora,
		Desde: desde.Format(vencimientos.FormatoISO), Hasta: hasta.Format(vencimientos.FormatoISO),
		Filas: res.Filas, Vigentes: vigentes, Resumen: res, Avisos: avisos, Impuestos: impuestos,
	}
	s.guardarEstadoVencimientos(e)

	s.vencMu.Lock()
	if s.vencIndice == nil {
		s.vencIndice = vencimientos.NuevoIndice()
	}
	s.vencIndice.Reemplazar(registros)
	s.vencCambios, s.vencCargado, s.vencMirado = todos, ahora, time.Now()
	s.vencMu.Unlock()

	slog.Info("agenda de vencimientos sincronizada", "filas", res.Filas, "vigentes", vigentes,
		"altas", res.Altas, "prorrogas", res.Prorrogas, "adelantos", res.Adelantos, "bajas", res.Bajas)
	return e, nil
}
