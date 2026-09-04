package infoleg

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// bom es la marca de orden de bytes con la que el CSV abre su cabecera; si no
// se saca, la primera columna queda con un nombre que no matchea.
const bom = "\ufeff"

// columnas del CSV de la Base de Normativa Nacional. Se leen por nombre y no
// por posición: el orden puede cambiar entre publicaciones.
const (
	colID          = "id_norma"
	colTipo        = "tipo_norma"
	colNumero      = "numero_norma"
	colClase       = "clase_norma"
	colOrganismo   = "organismo_origen"
	colSancion     = "fecha_sancion"
	colNumBoletin  = "numero_boletin"
	colFechaBol    = "fecha_boletin"
	colPagBoletin  = "pagina_boletin"
	colTitResumido = "titulo_resumido"
	colTitSumario  = "titulo_sumario"
	colTexResumido = "texto_resumido"
	colObserv      = "observaciones"
	colTextoOrig   = "texto_original"
	colTextoAct    = "texto_actualizado"
	colModificada  = "modificada_por"
	colModifica    = "modifica_a"
)

// LeerCatalogo recorre el CSV de la base y entrega una norma por vez.
//
// El catálogo tiene 428 mil filas y 256 MB: se lee en streaming y no se junta
// en memoria. La función se corta si el callback devuelve error.
func LeerCatalogo(r io.Reader, porCada func(Norma) error) (int, error) {
	lector := csv.NewReader(r)
	lector.ReuseRecord = true
	lector.FieldsPerRecord = -1 // alguna fila trae campos de más

	cabecera, err := lector.Read()
	if err != nil {
		return 0, fmt.Errorf("no se pudo leer la cabecera del catálogo: %w", err)
	}
	pos := map[string]int{}
	for i, nombre := range cabecera {
		pos[strings.TrimSpace(strings.TrimPrefix(nombre, bom))] = i
	}
	if _, hay := pos[colID]; !hay {
		return 0, fmt.Errorf("el catálogo no tiene la columna %s: ¿cambió el formato?", colID)
	}

	campo := func(fila []string, nombre string) string {
		i, hay := pos[nombre]
		if !hay || i >= len(fila) {
			return ""
		}
		return strings.TrimSpace(fila[i])
	}

	var leidas int
	for {
		fila, err := lector.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Una fila rota no puede tirar abajo el catálogo entero.
			var errParse *csv.ParseError
			if ok := asParseError(err, &errParse); ok {
				continue
			}
			return leidas, err
		}
		id, err := strconv.Atoi(campo(fila, colID))
		if err != nil || id <= 0 {
			continue
		}
		n := Norma{
			ID:                    id,
			Tipo:                  campo(fila, colTipo),
			Numero:                strings.TrimLeft(campo(fila, colNumero), "0"),
			Clase:                 campo(fila, colClase),
			Organismo:             campo(fila, colOrganismo),
			FechaSancion:          campo(fila, colSancion),
			FechaBoletin:          campo(fila, colFechaBol),
			NumeroBoletin:         campo(fila, colNumBoletin),
			PaginaBoletin:         campo(fila, colPagBoletin),
			TituloResumido:        campo(fila, colTitResumido),
			TituloSumario:         campo(fila, colTitSumario),
			TextoResumido:         campo(fila, colTexResumido),
			Observaciones:         campo(fila, colObserv),
			TieneTexto:            campo(fila, colTextoOrig) != "",
			TieneTextoActualizado: campo(fila, colTextoAct) != "",
		}
		n.ModificadaPor, _ = strconv.Atoi(campo(fila, colModificada))
		n.ModificaA, _ = strconv.Atoi(campo(fila, colModifica))

		leidas++
		if err := porCada(n); err != nil {
			return leidas, err
		}
	}
	return leidas, nil
}

func asParseError(err error, destino **csv.ParseError) bool {
	pe, ok := err.(*csv.ParseError)
	if ok {
		*destino = pe
	}
	return ok
}
