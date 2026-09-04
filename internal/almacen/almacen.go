// Package almacen guarda lo que ya se leyó del Boletín. Hay dos formas de
// hacerlo — archivos en disco y SQLite — y quien las usa no necesita saber
// cuál está activa.
package almacen

import (
	"time"

	"github.com/diegoparras/notarum/internal/boletin"
)

// SinVencimiento marca una entrada que no caduca. Una edición pasada no cambia
// nunca: se guarda así.
const SinVencimiento time.Duration = 0

// Almacen es lo que el servicio necesita para guardar y recuperar.
type Almacen interface {
	Leer(clave string) ([]byte, bool)
	Guardar(clave string, datos []byte, ttl time.Duration) error
	Existe(clave string) bool
	Borrar(clave string) error
	Metricas() Metricas
	Cerrar() error
}

// Metricas informa el uso del almacén.
type Metricas struct {
	Motor    string `json:"motor"`
	Aciertos int64  `json:"aciertos"`
	Fallos   int64  `json:"fallos"`
	Escritos int64  `json:"escritos"`
	Entradas int64  `json:"entradas"`
	// Avisos es cuántos avisos hay en el índice de búsqueda local; sólo lo
	// informa el motor que sabe indexar.
	Avisos int64 `json:"avisos,omitempty"`
}

// Indexador lo cumple el almacén que además sabe buscar por su cuenta. El de
// disco no puede: sirve por fecha, no por texto. El de SQLite sí.
//
// Es lo que permite buscar sin pedirle nada al Boletín: más rápido, sin tope
// de rango y sin gastarle pedidos al sitio.
type Indexador interface {
	Almacen
	// IndexarEdicion guarda los avisos de una edición en el índice.
	IndexarEdicion(ed *boletin.Edicion) error
	// IndexarDetalle suma el texto completo de un aviso, para que la búsqueda
	// local llegue al cuerpo y no sólo al sumario.
	IndexarDetalle(d *boletin.Detalle) error
	// BuscarLocal busca sobre lo indexado.
	BuscarLocal(q ConsultaLocal) (*ResultadoLocal, error)
	// Cobertura dice qué días de un rango están indexados, para saber si una
	// búsqueda local puede responder con autoridad o le falta historia.
	Cobertura(sec boletin.Seccion, desde, hasta boletin.Fecha) (indexados int, err error)
}

// ConsultaLocal son los criterios de una búsqueda sobre el índice.
type ConsultaLocal struct {
	Texto          string
	Seccion        boletin.Seccion // vacía = todas
	Rubro          string
	Desde          boletin.Fecha
	Hasta          boletin.Fecha
	Limite         int
	Desplazamiento int
}

// ResultadoLocal es una página de resultados del índice.
type ResultadoLocal struct {
	Total          int             `json:"total"`
	Limite         int             `json:"limite"`
	Desplazamiento int             `json:"desplazamiento"`
	Avisos         []boletin.Aviso `json:"avisos"`
}
