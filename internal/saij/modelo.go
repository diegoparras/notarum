// Package saij lee la Base SAIJ de Normativa Provincial, que el Ministerio de
// Justicia publica en datos.jus.gob.ar.
//
// notarum sigue el Boletín Oficial de la Nación, así que la normativa de las
// provincias —que se publica en el boletín de cada una— le queda afuera. Esta
// base la cubre: 81 mil leyes, decretos leyes, códigos y las constituciones de
// las 24 jurisdicciones, desde 1855.
//
// De cada norma hay metadatos completos y un enlace a su ficha en SAIJ. El
// texto no: se midió sobre una muestra al azar y sólo un 7% lo tiene
// publicado, así que notarum no promete traerlo.
package saij

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Norma es una norma provincial.
type Norma struct {
	// ID es el identificador de SAIJ, del estilo LPB1000000. Es la clave: se
	// verificó que no se repite en ninguna de las 81.403 filas, mientras que
	// (provincia, tipo, número, fecha) sí se repite 285 veces.
	ID string `json:"id"`

	Provincia   string `json:"provincia"`
	ProvinciaID string `json:"provincia_id"` // el código INDEC, "06" para Buenos Aires

	Tipo   string `json:"tipo"`             // Ley, Decreto Ley, Constitución Provincial…
	Numero string `json:"numero,omitempty"` // texto y no número: hay normas sin numerar

	// Estado es lo que SAIJ dice de su vigencia, con sus palabras: "Vigente,
	// de alcance general", "Derogada", "Individual, Solo Modificatoria o Sin
	// Eficacia"… No se traduce a un booleano porque no son dos estados.
	Estado string `json:"estado,omitempty"`

	Fecha           string `json:"fecha,omitempty"`     // sanción, AAAA-MM-DD
	FechaPublicacio string `json:"publicada,omitempty"` // en el boletín de la provincia
	Nombre          string `json:"nombre,omitempty"`    // el nombre con el que se la conoce
	TituloResumido  string `json:"titulo,omitempty"`    // de qué trata, en una línea
	TituloSumario   string `json:"materias,omitempty"`  // las materias, separadas por guiones
	Digesto         string `json:"digesto,omitempty"`   // información del digesto provincial
}

// URLFicha es la página de la norma en SAIJ.
func URLFicha(id string) string {
	if id == "" {
		return ""
	}
	return "https://www.saij.gob.ar/" + id
}

// URLFicha es la página de esta norma en SAIJ.
func (n Norma) URLFicha() string { return URLFicha(n.ID) }

// Anio es el año de sanción, o 0 si la fecha no se entiende.
func (n Norma) Anio() int {
	if len(n.Fecha) < 4 {
		return 0
	}
	a, err := strconv.Atoi(n.Fecha[:4])
	if err != nil {
		return 0
	}
	return a
}

// Vigente dice si SAIJ la da por vigente. Es una lectura de conveniencia del
// estado, no un reemplazo: para saber qué le pasó a una norma hay que leer el
// estado entero, que distingue entre derogada, caduca, refundida y vetada.
func (n Norma) Vigente() bool {
	return strings.HasPrefix(strings.ToLower(n.Estado), "vigente")
}

// Materias parte el sumario, que viene como una lista de términos pegados con
// guiones: "CONSTITUCION PROVINCIAL-DECLARACIONES-DERECHOS Y GARANTIAS".
//
// La fuente no es pareja: de las 81.281 normas con sumario, 63.984 usan el
// guion y el resto pega los términos con espacios, sin nada que los separe.
// Cuando pasa eso, esto devuelve un solo elemento con todo adentro, que es lo
// único honesto: partir por espacios cortaría "CULTURA Y EDUCACION" en tres.
func (n Norma) Materias() []string {
	if n.TituloSumario == "" {
		return nil
	}
	var salida []string
	for _, m := range strings.Split(n.TituloSumario, "-") {
		if m = strings.TrimSpace(m); m != "" {
			salida = append(salida, m)
		}
	}
	return salida
}

// Titulo devuelve con qué llamarla, probando en orden lo que suele estar
// cargado. Muchas normas viejas no tienen ninguno de los tres, y entonces se
// arma uno con lo que sí hay: es preferible a mostrar un renglón vacío.
func (n Norma) Titulo() string {
	for _, t := range []string{n.TituloResumido, n.Nombre, n.TituloSumario} {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	return n.Descripcion()
}

// Descripcion arma el nombre corto de la norma: "Ley 6109 de Chaco".
func (n Norma) Descripcion() string {
	var b strings.Builder
	b.WriteString(n.Tipo)
	// El número 0 es el que usan las constituciones y algunas normas sin
	// numerar; escribirlo sería peor que omitirlo.
	if n.Numero != "" && n.Numero != "0" {
		b.WriteString(" " + n.Numero)
	}
	if n.Provincia != "" {
		b.WriteString(" de " + n.Provincia)
	}
	return b.String()
}

// Provincias son las 24 jurisdicciones, con el código INDEC que usa la base.
// Están acá para poder ofrecerlas sin tener que recorrer 81 mil filas.
var Provincias = []Provincia{
	{"02", "Ciudad Autónoma de Buenos Aires", "LPX"},
	{"06", "Buenos Aires", "LPB"},
	{"10", "Catamarca", "LPK"},
	{"14", "Córdoba", "LPO"},
	{"18", "Corrientes", "LPW"},
	{"22", "Chaco", "LPH"},
	{"26", "Chubut", "LPU"},
	{"30", "Entre Ríos", "LPE"},
	{"34", "Formosa", "LPP"},
	{"38", "Jujuy", "LPY"},
	{"42", "La Pampa", "LPL"},
	{"46", "La Rioja", "LPF"},
	{"50", "Mendoza", "LPM"},
	{"54", "Misiones", "LPN"},
	{"58", "Neuquén", "LPQ"},
	{"62", "Río Negro", "LPR"},
	{"66", "Salta", "LPA"},
	{"70", "San Juan", "LPJ"},
	{"74", "San Luis", "LPD"},
	{"78", "Santa Cruz", "LPZ"},
	{"82", "Santa Fe", "LPS"},
	{"86", "Santiago del Estero", "LPG"},
	{"90", "Tucumán", "LPT"},
	{"94", "Tierra del Fuego", "LPV"},
}

// Provincia es una jurisdicción de la base.
type Provincia struct {
	ID      string `json:"id"`      // código INDEC
	Nombre  string `json:"nombre"`  //
	Prefijo string `json:"prefijo"` // con el que empiezan sus ids en SAIJ
}

// BuscarProvincia encuentra una provincia por su código INDEC, por su nombre o
// por el prefijo de sus normas, sin fijarse en mayúsculas ni acentos.
func BuscarProvincia(que string) (Provincia, bool) {
	q := normalizar(que)
	if q == "" {
		return Provincia{}, false
	}
	for _, p := range Provincias {
		if q == p.ID || q == normalizar(p.Nombre) || q == normalizar(p.Prefijo) {
			return p, true
		}
	}
	// Los nombres largos se escriben de muchas maneras; probar por prefijo
	// deja entrar "caba", "tierra del fuego" y "santiago".
	for _, p := range Provincias {
		if strings.HasPrefix(normalizar(p.Nombre), q) {
			return p, true
		}
	}
	if q == "caba" || q == "capital federal" {
		return Provincias[0], true
	}
	return Provincia{}, false
}

// normalizar deja un texto comparable: minúsculas y sin acentos.
func normalizar(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 'á', 'à', 'ä', 'â':
			r = 'a'
		case 'é', 'è', 'ë', 'ê':
			r = 'e'
		case 'í', 'ì', 'ï', 'î':
			r = 'i'
		case 'ó', 'ò', 'ö', 'ô':
			r = 'o'
		case 'ú', 'ù', 'ü', 'û':
			r = 'u'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ParsearFecha lee las fechas de la base, que vienen en AAAA-MM-DD.
func ParsearFecha(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("fecha vacía")
	}
	return time.Parse("2006-01-02", s)
}
