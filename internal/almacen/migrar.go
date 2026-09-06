package almacen

import (
	"errors"
	"fmt"
	"strings"
)

// Pasar lo guardado de un motor a otro.
//
// Cambiar de motor no debería costar volver a bajar los catálogos: son cientos
// de miles de normas y varios minutos de un portal que no es nuestro. Esto
// copia lo que ya está.
//
// Lo que no se copia es el índice de búsqueda de avisos: vive en otra tabla y
// cada motor lo arma a su manera —SQLite con FTS, Postgres con tsvector—, así
// que se rearma desde las ediciones copiadas en vez de traducirse. Traducir un
// índice entre dos motores es la clase de cosa que sale mal en silencio.

// Avance cuenta cómo va la copia, para poder mostrarlo.
type Avance struct {
	Copiadas  int
	Indexadas int
}

// Migrar copia todo lo de un almacén a otro.
//
// avisar se llama cada tanto con el avance; puede ser nil.
func Migrar(desde, hacia Almacen, avisar func(Avance)) (Avance, error) {
	var a Avance
	origen, sabe := desde.(Recorrible)
	if !sabe {
		return a, errors.New("el almacén de origen no sabe recorrer lo que tiene")
	}
	if desde == hacia {
		return a, errors.New("el origen y el destino son el mismo almacén")
	}

	lote := NuevoAcumulador(hacia)
	// Las claves de las ediciones se guardan para reindexar después: reindexar
	// mientras se copia mezclaría dos trabajos que fallan por motivos
	// distintos.
	var ediciones []string
	err := origen.Recorrer(func(clave string, datos []byte) error {
		if err := lote.Sumar(clave, datos, SinVencimiento); err != nil {
			return fmt.Errorf("copiando %q: %w", clave, err)
		}
		a.Copiadas++
		if strings.HasPrefix(clave, "ediciones/") {
			ediciones = append(ediciones, clave)
		}
		if avisar != nil && a.Copiadas%5000 == 0 {
			avisar(a)
		}
		return nil
	})
	if err != nil {
		return a, err
	}
	if err := lote.Vaciar(); err != nil {
		return a, err
	}
	return a, nil
}

// Ediciones devuelve las claves de edición que hay en un almacén, para poder
// rearmar el índice después de una migración.
func Ediciones(a Almacen) ([]string, error) {
	r, sabe := a.(Recorrible)
	if !sabe {
		return nil, errors.New("este almacén no sabe recorrer lo que tiene")
	}
	var claves []string
	err := r.Recorrer(func(clave string, _ []byte) error {
		if strings.HasPrefix(clave, "ediciones/") {
			claves = append(claves, clave)
		}
		return nil
	})
	return claves, err
}
