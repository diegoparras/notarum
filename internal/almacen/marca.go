package almacen

import (
	"encoding/json"
	"time"
)

// La marca del almacén: sirve para saber si los datos sobrevivieron.
//
// Un contenedor sin volumen montado arranca con el almacén vacío cada vez que
// se redespliega. Todo funciona —se entra, se guarda, se ve guardado— y al
// siguiente despliegue no queda nada: las claves cargadas, los tokens, los
// catálogos bajados. Desde afuera parece que notarum "se olvidó", y no hay
// nada en ningún lado que diga que pasó.
//
// Con esto notarum lo mide en vez de suponerlo: deja una marca la primera vez
// y la busca en cada arranque. Si no la encuentra, o el almacén es nuevo de
// verdad, o lo que se guardó se perdió; las dos cosas hay que decirlas.

const claveMarca = "_instancia"

// Marca es lo que quedó anotado la primera vez que este almacén se usó.
type Marca struct {
	// Desde es cuándo se escribió por primera vez.
	Desde time.Time `json:"desde"`
	// Arranques es cuántas veces notarum encontró este almacén ya escrito.
	Arranques int `json:"arranques"`
	// Ultimo es el arranque más reciente.
	Ultimo time.Time `json:"ultimo"`
	// Nueva dice si esta marca se acaba de crear, o sea que el almacén estaba
	// vacío. No se guarda: es de este arranque.
	Nueva bool `json:"-"`
}

// Marcar anota el paso de notarum por este almacén y devuelve lo que encontró.
//
// Que devuelva Nueva no es un error por sí solo: la primera vez el almacén
// está vacío y corresponde. Es a partir de la segunda que significa que algo
// se perdió, y por eso se cuentan los arranques.
func Marcar(a Almacen) (Marca, error) {
	var m Marca
	ahora := time.Now().UTC()

	if crudo, hay := a.Leer(claveMarca); hay {
		if err := json.Unmarshal(crudo, &m); err != nil {
			// Ilegible cuenta como vacío: no se puede afirmar que sobrevivió
			// algo que no se puede leer.
			m = Marca{}
		}
	}
	if m.Desde.IsZero() {
		m = Marca{Desde: ahora, Nueva: true}
	}
	m.Arranques++
	m.Ultimo = ahora

	crudo, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	return m, a.Guardar(claveMarca, crudo, SinVencimiento)
}
