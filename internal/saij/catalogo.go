package saij

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// bom es la marca de orden de bytes con la que algunos CSV abren su cabecera;
// si no se saca, la primera columna queda con un nombre que no matchea.
const bom = "\ufeff"

// Las columnas se leen por nombre y no por posición: el orden puede cambiar
// entre publicaciones, y una base que se publica desde 2016 va a cambiar.
const (
	colProvincia   = "provincia_nombre"
	colProvinciaID = "provincia_id"
	colTipo        = "tipo_norma"
	colNumero      = "numero_norma"
	colEstado      = "estado_vigencia"
	colFecha       = "fecha"
	colFechaPub    = "fecha_publicacion"
	colNombre      = "nombre_norma"
	colTitResumido = "titulo_resumido"
	colTitSumario  = "titulo_sumario"
	colDigesto     = "informacion_digesto"
	colTextoAct    = "texto_actualizado"
)

// ErrFormato dice que el CSV no es el que se esperaba. Se distingue para que
// quien sincroniza sepa que el problema es de la fuente y no de la red.
var ErrFormato = errors.New("el catálogo no tiene la forma esperada")

// LeerCatalogo recorre el CSV de la base y entrega una norma por vez.
//
// Son 81 mil filas y 28 MB. Se lee en streaming y no se junta en memoria, por
// la misma razón que el catálogo de InfoLEG: quien llame decide qué guardar.
// La función se corta si el callback devuelve error.
func LeerCatalogo(r io.Reader, porCada func(Norma) error) (int, error) {
	lector := csv.NewReader(r)
	lector.ReuseRecord = true
	lector.FieldsPerRecord = -1 // alguna fila puede traer campos de más

	cabecera, err := lector.Read()
	if err != nil {
		return 0, fmt.Errorf("no se pudo leer la cabecera del catálogo: %w", err)
	}
	pos := map[string]int{}
	for i, nombre := range cabecera {
		pos[strings.TrimSpace(strings.TrimPrefix(nombre, bom))] = i
	}
	// Sin estas dos no hay norma que armar: mejor cortar acá que guardar 81
	// mil filas vacías.
	for _, obligatoria := range []string{colTextoAct, colProvincia} {
		if _, hay := pos[obligatoria]; !hay {
			return 0, fmt.Errorf("%w: falta la columna %s", ErrFormato, obligatoria)
		}
	}

	campo := func(fila []string, nombre string) string {
		i, hay := pos[nombre]
		if !hay || i >= len(fila) {
			return ""
		}
		return strings.TrimSpace(fila[i])
	}

	// Provincia, tipo y estado se repiten: hay 24, 15 y 8 valores distintos
	// en 81 mil filas. Guardar la misma cadena una sola vez y repartir esa
	// ahorra decenas de megabytes cuando el catálogo entra en memoria.
	repetidos := map[string]string{}
	unaSolaVez := func(s string) string {
		if s == "" {
			return ""
		}
		if v, hay := repetidos[s]; hay {
			return v
		}
		repetidos[s] = s
		return s
	}

	var leidas int
	for {
		fila, err := lector.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Una fila mal formada no puede tirar abajo la sincronización
			// entera: se saltea y se sigue, como con el catálogo de InfoLEG.
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				continue
			}
			return leidas, fmt.Errorf("leyendo el catálogo: %w", err)
		}

		id := identificador(campo(fila, colTextoAct))
		if id == "" {
			// Sin identificador no hay clave ni enlace: la fila no sirve.
			continue
		}
		n := Norma{
			ID:              id,
			Provincia:       unaSolaVez(campo(fila, colProvincia)),
			ProvinciaID:     unaSolaVez(campo(fila, colProvinciaID)),
			Tipo:            unaSolaVez(campo(fila, colTipo)),
			Numero:          campo(fila, colNumero),
			Estado:          unaSolaVez(campo(fila, colEstado)),
			Fecha:           campo(fila, colFecha),
			FechaPublicacio: campo(fila, colFechaPub),
			Nombre:          campo(fila, colNombre),
			TituloResumido:  campo(fila, colTitResumido),
			TituloSumario:   campo(fila, colTitSumario),
			Digesto:         campo(fila, colDigesto),
		}
		leidas++
		if err := porCada(n); err != nil {
			return leidas, err
		}
	}
	return leidas, nil
}

// identificador saca el id de SAIJ de la columna que trae el enlace, que viene
// como "www.saij.gob.ar/LPB1000000". Se guarda el id y no el enlace: la
// dirección del sitio puede cambiar, el identificador no.
func identificador(enlace string) string {
	enlace = strings.TrimSpace(enlace)
	if enlace == "" {
		return ""
	}
	if i := strings.LastIndexByte(enlace, '/'); i >= 0 {
		enlace = enlace[i+1:]
	}
	// Lo que quede tiene que parecer un identificador y no un pedazo de otra
	// cosa: letras y números, sin espacios.
	if enlace == "" || len(enlace) > 32 {
		return ""
	}
	for _, r := range enlace {
		esLetra := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		if !esLetra && !(r >= '0' && r <= '9') {
			return ""
		}
	}
	return strings.ToUpper(enlace)
}
