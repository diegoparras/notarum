// Package almacen guarda lo que ya se leyó del Boletín. Hay dos formas de
// hacerlo — archivos en disco y SQLite — y quien las usa no necesita saber
// cuál está activa.
package almacen

import (
	"encoding/base64"
	"encoding/json"
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

// Guardar y leer bytes que no son JSON.
//
// El almacén envuelve lo que guarda en un sobre JSON, así que lo que se le pasa
// tiene que ser JSON válido. Pasarle un ZIP, o unos bytes al azar, falla con un
// "invalid character" que no dice qué hacer, y en un camino donde el error se
// anota y se sigue, la función queda rota sin que nadie se entere. Ya pasó tres
// veces: con el secreto de sesión, con el catálogo provincial y con el ZIP de
// InfoLEG, que dejó el buscador de normativa nacional sin funcionar nunca.
//
// Estas dos funciones lo dicen en el nombre: si lo que se guarda no es JSON, se
// usan éstas.

// GuardarBytes guarda datos arbitrarios, codificados para que entren en el
// sobre JSON del almacén.
func GuardarBytes(a Almacen, clave string, datos []byte, ttl time.Duration) error {
	crudo, err := json.Marshal(base64.StdEncoding.EncodeToString(datos))
	if err != nil {
		return err
	}
	return a.Guardar(clave, crudo, ttl)
}

// LeerBytes recupera lo guardado con GuardarBytes.
func LeerBytes(a Almacen, clave string) ([]byte, bool) {
	crudo, hay := a.Leer(clave)
	if !hay {
		return nil, false
	}
	var enTexto string
	if err := json.Unmarshal(crudo, &enTexto); err != nil {
		return nil, false
	}
	datos, err := base64.StdEncoding.DecodeString(enTexto)
	if err != nil {
		return nil, false
	}
	return datos, true
}
