package servicio

import (
	"sort"
	"strings"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
)

// Cuánto del Boletín tiene guardado esta instancia.
//
// Sin esto, el panel dice que la base tiene tantas entradas y no dice de qué:
// una instancia con diez años de la primera sección y nada de la tercera se ve
// igual que una que tiene todo. Saber qué falta es lo que permite decidir si
// hay que llenar algo.

// CoberturaDeSeccion es lo que hay de una sección.
type CoberturaDeSeccion struct {
	Seccion   boletin.Seccion `json:"seccion"`
	Nombre    string          `json:"nombre"`
	Ediciones int             `json:"ediciones"`
	// Desde y Hasta son la primera y la última que hay guardadas.
	Desde string `json:"desde,omitempty"`
	Hasta string `json:"hasta,omitempty"`
}

// Cobertura cuenta las ediciones guardadas de cada sección.
//
// Recorre el almacén entero, así que no es algo para cada pedido: lo usa el
// panel, que se mira de vez en cuando.
func (s *Servicio) Cobertura() []CoberturaDeSeccion {
	porSeccion := map[boletin.Seccion]*CoberturaDeSeccion{}
	for _, sec := range boletin.SeccionesValidas {
		porSeccion[sec] = &CoberturaDeSeccion{Seccion: sec, Nombre: nombreDeSeccion(sec)}
	}

	r, sabe := s.cache.(almacen.Recorrible)
	if !sabe {
		return enOrden(porSeccion)
	}
	_ = r.Recorrer(func(clave string, _ []byte) error {
		// ediciones/<seccion>/<fecha>
		partes := strings.Split(clave, "/")
		if len(partes) != 3 || partes[0] != "ediciones" {
			return nil
		}
		c, hay := porSeccion[boletin.Seccion(partes[1])]
		if !hay {
			return nil
		}
		fecha := partes[2]
		c.Ediciones++
		if c.Desde == "" || fecha < c.Desde {
			c.Desde = fecha
		}
		if fecha > c.Hasta {
			c.Hasta = fecha
		}
		return nil
	})
	return enOrden(porSeccion)
}

func enOrden(m map[boletin.Seccion]*CoberturaDeSeccion) []CoberturaDeSeccion {
	out := make([]CoberturaDeSeccion, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seccion < out[j].Seccion })
	return out
}

// nombreDeSeccion dice qué trae cada una, que es lo que hace falta para saber
// si a uno le importa.
func nombreDeSeccion(s boletin.Seccion) string {
	switch s {
	case boletin.Primera:
		return "decretos, resoluciones y disposiciones"
	case boletin.Segunda:
		return "sociedades, edictos y sucesiones"
	case boletin.Tercera:
		return "licitaciones y contrataciones"
	}
	return string(s)
}
