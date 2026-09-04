// Package infoleg lee la Base de Normativa Nacional de InfoLEG y trae el texto
// de una norma.
//
// InfoLEG no tiene API ni RSS. Se llega por dos puertas:
//
//   - El catálogo se publica como CSV en el CKAN de datos.jus.gob.ar, y trae
//     el mapeo de cada norma a su id, más la fecha del Boletín en que salió.
//   - El texto vive en una ruta que se calcula a partir de ese id, en carpetas
//     de a cinco mil.
//
// De las 428.380 normas del catálogo, sólo el 44% tiene texto publicado. El
// campo texto_original del CSV dice cuáles, así que se puede saber si hay
// texto sin pedirle nada a InfoLEG.
package infoleg

import (
	"fmt"
	"strconv"
	"strings"
)

// BaseSitio es el origen del que se lee el texto de las normas.
const BaseSitio = "https://servicios.infoleg.gob.ar/infolegInternet"

// TamañoCarpeta es de a cuántos ids agrupa InfoLEG sus archivos.
const TamañoCarpeta = 5000

// Norma es una entrada del catálogo de normativa nacional.
type Norma struct {
	ID             int    `json:"id"`
	Tipo           string `json:"tipo"`
	Numero         string `json:"numero"`
	Clase          string `json:"clase,omitempty"`
	Organismo      string `json:"organismo,omitempty"`
	FechaSancion   string `json:"fecha_sancion,omitempty"`
	FechaBoletin   string `json:"fecha_boletin,omitempty"`
	NumeroBoletin  string `json:"numero_boletin,omitempty"`
	PaginaBoletin  string `json:"pagina_boletin,omitempty"`
	TituloResumido string `json:"titulo_resumido,omitempty"`
	TituloSumario  string `json:"titulo_sumario,omitempty"`
	TextoResumido  string `json:"texto_resumido,omitempty"`
	Observaciones  string `json:"observaciones,omitempty"`
	// TieneTexto sale de que el catálogo traiga una URL de texto original.
	// Es fiable: donde el catálogo no la trae, InfoLEG no publicó el texto.
	TieneTexto bool `json:"tiene_texto"`
	// TieneTextoActualizado marca las normas consolidadas, que son pocas.
	TieneTextoActualizado bool `json:"tiene_texto_actualizado"`
	// ModificadaPor y ModificaA son cuentas, no listas: el detalle está en las
	// bases complementarias del mismo dataset.
	ModificadaPor int `json:"modificada_por"`
	ModificaA     int `json:"modifica_a"`
}

// URLTexto arma la dirección del texto de una norma a partir de su id.
//
// InfoLEG guarda los archivos en carpetas de a cinco mil, nombradas por el
// rango que contienen: la norma 401266 vive en la carpeta 400000-404999. No
// hay índice ni API que lo diga; se calcula.
func URLTexto(id int) string {
	if id <= 0 {
		return ""
	}
	base := (id / TamañoCarpeta) * TamañoCarpeta
	return fmt.Sprintf("%s/anexos/%d-%d/%d/norma.htm", BaseSitio, base, base+TamañoCarpeta-1, id)
}

// URLFicha es la página de la norma en InfoLEG, para mandar a una persona.
func URLFicha(id int) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/verNorma.do?id=%d", BaseSitio, id)
}

// URLTexto y URLFicha de la propia norma.
func (n Norma) URLTexto() string {
	if !n.TieneTexto {
		return ""
	}
	return URLTexto(n.ID)
}

func (n Norma) URLFicha() string { return URLFicha(n.ID) }

// Anio devuelve el año en que la norma salió en el Boletín.
func (n Norma) Anio() int {
	if len(n.FechaBoletin) >= 4 {
		if a, err := strconv.Atoi(n.FechaBoletin[:4]); err == nil {
			return a
		}
	}
	if len(n.FechaSancion) >= 4 {
		if a, err := strconv.Atoi(n.FechaSancion[:4]); err == nil {
			return a
		}
	}
	return 0
}

// Referencia identifica una norma como la nombra el Boletín: tipo, número y
// año. Es la llave con la que se cruza un aviso con el catálogo.
type Referencia struct {
	Tipo   string
	Numero string
	Anio   int
}

func (r Referencia) String() string {
	if r.Anio > 0 {
		return fmt.Sprintf("%s %s/%d", r.Tipo, r.Numero, r.Anio)
	}
	return fmt.Sprintf("%s %s", r.Tipo, r.Numero)
}

// tiposConocidos son los que el Boletín nombra y el catálogo entiende. La
// clave está normalizada (sin acentos, en minúscula) y el valor es como lo
// escribe InfoLEG.
var tiposConocidos = map[string]string{
	"decreto":                 "Decreto",
	"ley":                     "Ley",
	"resolucion":              "Resolución",
	"resolucion general":      "Resolución General",
	"resolucion conjunta":     "Resolución Conjunta",
	"resolucion sintetizada":  "Resolución",
	"disposicion":             "Disposición",
	"disposicion sintetizada": "Disposición",
	"decision administrativa": "Decisión Administrativa",
	"comunicacion":            "Comunicación",
	"acordada":                "Acordada",
	"acta":                    "Acta",
	"laudo":                   "Laudo",
	"convenio":                "Convenio",
}

// ParsearNorma convierte lo que el Boletín escribe en el campo "norma" de un
// aviso —"Decreto 845/2026", "Resolución 210/2026"— en una referencia con la
// que buscar en el catálogo.
//
// Devuelve false cuando el texto no nombra una norma reconocible, que es lo
// habitual en la segunda y la tercera sección.
func ParsearNorma(texto string) (Referencia, bool) {
	limpio := strings.TrimSpace(texto)
	if limpio == "" {
		return Referencia{}, false
	}

	// El número siempre viene después del tipo, con la forma 845/2026 o 845.
	barra := strings.LastIndex(limpio, "/")
	var anio int
	resto := limpio
	if barra > 0 {
		if a, err := strconv.Atoi(strings.TrimSpace(limpio[barra+1:])); err == nil && a > 1800 && a < 2200 {
			anio = a
			resto = strings.TrimSpace(limpio[:barra])
		}
	}

	campos := strings.Fields(resto)
	if len(campos) < 2 {
		return Referencia{}, false
	}
	numero := campos[len(campos)-1]
	if !esNumero(numero) {
		return Referencia{}, false
	}
	nombreTipo := strings.Join(campos[:len(campos)-1], " ")

	tipo, ok := tiposConocidos[normalizar(nombreTipo)]
	if !ok {
		return Referencia{}, false
	}
	return Referencia{Tipo: tipo, Numero: strings.TrimLeft(numero, "0"), Anio: anio}, true
}

func esNumero(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizar saca acentos y pasa a minúscula, para que "Resolución" y
// "RESOLUCION" den la misma clave.
func normalizar(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 'á', 'à', 'ä', 'â':
			sb.WriteRune('a')
		case 'é', 'è', 'ë', 'ê':
			sb.WriteRune('e')
		case 'í', 'ì', 'ï', 'î':
			sb.WriteRune('i')
		case 'ó', 'ò', 'ö', 'ô':
			sb.WriteRune('o')
		case 'ú', 'ù', 'ü', 'û':
			sb.WriteRune('u')
		case 'ñ':
			sb.WriteRune('n')
		default:
			sb.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}
