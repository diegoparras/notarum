package infoleg

import (
	"encoding/csv"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Las relaciones entre normas: qué modificó a cada una, y qué modifica cada una.
//
// El catálogo principal trae las dos como números —"modificada por 7"— y nada
// más. Saber que una ley fue modificada sin saber por cuáles no sirve para
// nada: hay que ir a buscarlas a otro lado igual, que es lo que esto evita.
//
// El detalle viene en dos bases complementarias del mismo dataset. Cuál de las
// dos normas describen sus columnas no está documentado y los nombres
// engañan, así que se midió: en la base de "normas modificadas", un mismo
// id_norma_modificada aparece en varias filas con datos distintos, lo que
// significa que las columnas describen a la modificatoria. En la de
// "modificatorias" pasa lo simétrico. O sea que cada archivo da la otra punta
// de la relación, que es justo lo que hace falta.

// Relacion es la otra norma de una relación, con lo suyo al lado.
//
// Se guardan los datos y no sólo el identificador: así una lista de "qué
// modificó a esta ley" se muestra entera sin buscar cada norma por separado,
// que serían decenas de lecturas para dibujar una página.
type Relacion struct {
	ID        int    `json:"id"`
	Tipo      string `json:"tipo,omitempty"`
	Numero    string `json:"numero,omitempty"`
	Clase     string `json:"clase,omitempty"`
	Organismo string `json:"organismo,omitempty"`
	Fecha     string `json:"fecha_boletin,omitempty"`
	Sumario   string `json:"titulo_sumario,omitempty"`
	Titulo    string `json:"titulo_resumido,omitempty"`
}

// Sentido dice cuál de las dos bases complementarias se está leyendo.
type Sentido int

const (
	// ModificadaPor lee la base de normas modificadas: se busca por la norma
	// modificada y sale qué la modificó.
	ModificadaPor Sentido = iota
	// ModificaA lee la base de modificatorias: se busca por la norma que
	// modifica y sale qué modificó.
	ModificaA
)

func (s Sentido) columnaPropia() string {
	if s == ModificadaPor {
		return "id_norma_modificada"
	}
	return "id_norma_modificatoria"
}

func (s Sentido) columnaDeLaOtra() string {
	if s == ModificadaPor {
		return "id_norma_modificatoria"
	}
	return "id_norma_modificada"
}

// MaximoPorNorma es cuántas relaciones se guardan de cada norma.
//
// Hay un tope porque el reparto es extremo, medido contra los datos reales: de
// 103.560 normas modificadas, el promedio es 3,7 modificatorias, pero la ley
// 14250 —Convenios Colectivos de Trabajo— tiene 42.427, porque cada convenio
// homologado figura como una modificación suya. Sin tope, esa sola norma sería
// una entrada de quince megas en el almacén, una respuesta de API con cuarenta
// mil elementos y una página con cuarenta mil enlaces.
//
// Se guardan las más nuevas, que son las que alguien está mirando cuando
// pregunta qué le pasó a una norma, y se anota el total de verdad.
const MaximoPorNorma = 200

// Relaciones son las normas del otro lado, con cuántas hay en total.
type Relaciones struct {
	// Total es cuántas hay de verdad, aunque se guarden menos.
	Total int `json:"total"`
	// Normas son las que se guardaron, de la más nueva a la más vieja.
	Normas []Relacion `json:"normas"`
}

// Recortada dice si quedaron afuera. Quien pregunta tiene que poder saber que
// está viendo una parte, en vez de creer que eso es todo.
func (r Relaciones) Recortada() bool { return r.Total > len(r.Normas) }

// LeerRelaciones arma, para cada norma, la lista de las del otro lado.
//
// Devuelve un mapa y no un canal porque las filas de una misma norma no vienen
// juntas: hay que recorrer el archivo entero antes de saber la lista de
// ninguna.
func LeerRelaciones(r io.Reader, sentido Sentido) (map[int][]Relacion, error) {
	lector := csv.NewReader(r)
	lector.FieldsPerRecord = -1 // filas con una coma de más no tiran todo abajo
	lector.LazyQuotes = true

	cabecera, err := lector.Read()
	if err != nil {
		return nil, errors.New("la base complementaria vino vacía")
	}
	pos := map[string]int{}
	for i, nombre := range cabecera {
		pos[strings.TrimSpace(strings.TrimPrefix(nombre, bom))] = i
	}
	propia, hayPropia := pos[sentido.columnaPropia()]
	otra, hayOtra := pos[sentido.columnaDeLaOtra()]
	if !hayPropia || !hayOtra {
		return nil, errors.New("la base complementaria no trae las columnas de identificador: " +
			"¿cambió la publicación?")
	}
	campo := func(fila []string, nombre string) string {
		i, hay := pos[nombre]
		if !hay || i >= len(fila) {
			return ""
		}
		return strings.TrimSpace(fila[i])
	}

	relaciones := map[int][]Relacion{}
	for {
		fila, err := lector.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Una fila rota no tira abajo el archivo entero: son cientos de
			// miles y lo que se pierde es una relación, no el resto.
			var errParse *csv.ParseError
			if asParseError(err, &errParse) {
				continue
			}
			return nil, err
		}
		if propia >= len(fila) || otra >= len(fila) {
			continue
		}
		deQuien, err1 := strconv.Atoi(strings.TrimSpace(fila[propia]))
		cual, err2 := strconv.Atoi(strings.TrimSpace(fila[otra]))
		if err1 != nil || err2 != nil || deQuien <= 0 || cual <= 0 {
			continue
		}
		relaciones[deQuien] = append(relaciones[deQuien], Relacion{
			ID:        cual,
			Tipo:      campo(fila, "tipo_norma"),
			Numero:    campo(fila, "nro_norma"),
			Clase:     campo(fila, "clase_norma"),
			Organismo: campo(fila, "organismo_origen"),
			Fecha:     campo(fila, "fecha_boletin"),
			Sumario:   campo(fila, "titulo_sumario"),
			Titulo:    campo(fila, "titulo_resumido"),
		})
	}
	return relaciones, nil
}

// Recortar ordena por fecha, de la más nueva a la más vieja, y deja hasta el
// tope. Devuelve el total de verdad.
func Recortar(rs []Relacion) Relaciones {
	// Estable: dos con la misma fecha quedan en el orden en que venían, que es
	// el del archivo, en vez de en uno que cambia en cada corrida.
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Fecha > rs[j].Fecha })
	out := Relaciones{Total: len(rs), Normas: rs}
	if len(rs) > MaximoPorNorma {
		out.Normas = rs[:MaximoPorNorma]
	}
	return out
}

// Descripcion es cómo se nombra una norma relacionada.
func (r Relacion) Descripcion() string {
	partes := []string{r.Tipo}
	if r.Numero != "" {
		partes = append(partes, r.Numero)
	}
	texto := strings.TrimSpace(strings.Join(partes, " "))
	if texto == "" {
		return "Norma " + strconv.Itoa(r.ID)
	}
	return texto
}
