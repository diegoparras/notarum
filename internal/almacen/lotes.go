package almacen

import "time"

// Guardar muchas entradas de una.
//
// Sincronizar InfoLEG son 428 mil escrituras, y las relaciones agregan unas
// 200 mil más. De a una eso son cientos de miles de viajes: con SQLite, una
// transacción por escritura y su fsync; con Postgres, un viaje por la red cada
// vez. Es la diferencia entre una sincronización de minutos y una de horas, y
// es lo que hacía inviable usar Postgres para esto.
//
// El almacén que no sepa hacerlo sigue andando: GuardarLote cae en el guardado
// de a una, que es lo que hacía antes.

// Entrada es algo para guardar.
type Entrada struct {
	Clave string
	Datos []byte
	TTL   time.Duration
}

// PorLotes lo cumple el almacén que sabe guardar de a muchas.
type PorLotes interface {
	GuardarLote(entradas []Entrada) error
}

// TamañoDeLote es de a cuántas conviene guardar.
//
// Ni tan chico que no se note, ni tan grande que una transacción quede abierta
// tanto tiempo que lo demás espere: mientras el lote está en curso, con SQLite
// nadie más escribe.
const TamañoDeLote = 1000

// GuardarLote guarda muchas entradas, por lotes si el almacén sabe.
func GuardarLote(a Almacen, entradas []Entrada) error {
	if len(entradas) == 0 {
		return nil
	}
	if l, ok := a.(PorLotes); ok {
		return l.GuardarLote(entradas)
	}
	for _, e := range entradas {
		if err := a.Guardar(e.Clave, e.Datos, e.TTL); err != nil {
			return err
		}
	}
	return nil
}

// Acumulador junta entradas y las guarda cuando se llena.
//
// Es lo que permite escribir un catálogo entero sin tenerlo todo en memoria ni
// pagar un viaje por norma.
type Acumulador struct {
	alm       Almacen
	pendiente []Entrada
	tope      int
}

func NuevoAcumulador(a Almacen) *Acumulador {
	return &Acumulador{alm: a, tope: TamañoDeLote}
}

// Sumar agrega una entrada, y guarda el lote si se llenó.
func (ac *Acumulador) Sumar(clave string, datos []byte, ttl time.Duration) error {
	ac.pendiente = append(ac.pendiente, Entrada{Clave: clave, Datos: datos, TTL: ttl})
	if len(ac.pendiente) < ac.tope {
		return nil
	}
	return ac.Vaciar()
}

// Vaciar guarda lo que quedó pendiente. Hay que llamarlo al terminar: lo que
// no se vacía no se guardó.
func (ac *Acumulador) Vaciar() error {
	if len(ac.pendiente) == 0 {
		return nil
	}
	err := GuardarLote(ac.alm, ac.pendiente)
	ac.pendiente = ac.pendiente[:0]
	return err
}
