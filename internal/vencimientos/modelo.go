// Package vencimientos lee la Agenda de Vencimientos de ARCA (ex-AFIP): el
// calendario oficial de cuándo vence cada obligación impositiva y previsional.
//
// https://seti.afip.gob.ar/av/viewVencimientos.do es un formulario de los
// primeros dos mil, sin API ni documentación. Lo que sigue se verificó contra el
// sitio real el 2026-09-22:
//
//   - La consulta es un POST de cuatro campos: fechaVDesde y fechaVHasta
//     (dd/mm/aaaa), terminacionCuit e impuestosSeleccionados. Con 999 ("TODOS")
//     y "Todos", una sola consulta trae la agenda completa del período.
//   - No hace falta sesión: el POST sin cookie contesta lo mismo que con la
//     cookie que deja el formulario.
//   - Un año entero entra en una consulta: 4,3 MB en unos 30 segundos.
//   - ARCA sólo tiene cargado hasta fin del año en curso. Más allá contesta un
//     200 con "se ingresó un rango de fecha que no está cargado". Ver
//     ErrRangoNoCargado.
//   - La página está en ISO-8859-1. Ver DeLatin1.
//   - "Información actualizada al …" NO dice cuándo cambió la agenda: es la
//     hora a la que se armó la página (dos consultas separadas por 51 segundos
//     declararon horas separadas por 51 segundos). Por eso lo que cambió se
//     averigua comparando cada bajada con la anterior. Ver Comparar.
//
// La unidad es la obligación de un grupo de terminaciones de CUIT —impuesto,
// régimen, sujeto, tipo de régimen, obligación, período y terminación— y la
// fecha es su valor. Si la fecha fuera parte de la identidad, una prórroga se
// vería como un vencimiento nuevo y otro que desapareció.
package vencimientos

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// URLPorDefecto es la dirección de la agenda. La misma sirve el formulario por
// GET y los resultados por POST.
const URLPorDefecto = "https://seti.afip.gob.ar/av/viewVencimientos.do"

// Norma es una cita legal que ARCA cuelga al lado de un dato, con su enlace a
// la biblioteca. Una fecha con su norma se puede verificar.
type Norma struct {
	Texto string `json:"texto"`
	URL   string `json:"url,omitempty"`
}

// Vencimiento es una fila de la agenda tal como la publica ARCA.
type Vencimiento struct {
	Impuesto    string `json:"impuesto"`
	Regimen     string `json:"regimen,omitempty"`
	Sujeto      string `json:"sujeto,omitempty"`
	TipoRegimen string `json:"tipo_regimen,omitempty"`
	// Obligacion es qué hay que hacer y Periodo a qué corresponde:
	// "Presentación de la declaración jurada…" y "Agosto/2026".
	Obligacion string `json:"obligacion"`
	Periodo    string `json:"periodo,omitempty"`
	// TerminacionCUIT es el grupo al que le toca esta fecha: "todos",
	// "0-1-2-3", "8-9". Vacío cuando la obligación no tiene fecha fija.
	TerminacionCUIT string `json:"terminacion_cuit"`
	// Fecha es AAAA-MM-DD, vacía cuando la obligación no tiene fecha fija. Va
	// como texto porque así se ordena y se compara como fecha sin convertir.
	Fecha string `json:"fecha,omitempty"`
	// SinFechaFija marca las obligaciones de plazo relativo: "dentro de los 10
	// días hábiles siguientes de perfeccionado el hecho imponible". Se guardan
	// igual; tirarlas sería decir que no existen.
	SinFechaFija bool    `json:"sin_fecha_fija,omitempty"`
	Formularios  string  `json:"formularios,omitempty"`
	Aplicativos  string  `json:"aplicativos,omitempty"`
	Normas       []Norma `json:"normas,omitempty"`
}

// Clave es la identidad del vencimiento: los siete campos que lo definen, sin
// la fecha. El separador es un byte que no aparece en el texto, para que dos
// identidades distintas no puedan dar la misma cadena.
func (v Vencimiento) Clave() string {
	h := sha256.New()
	for i, campo := range [...]string{
		v.Impuesto, v.Regimen, v.Sujeto, v.TipoRegimen,
		v.Obligacion, v.Periodo, v.TerminacionCUIT,
	} {
		if i > 0 {
			h.Write([]byte{0x1f})
		}
		h.Write([]byte(campo))
	}
	// Dieciséis bytes alcanzan de sobra para unos miles de filas por año y
	// dejan una clave que se puede pegar en una URL.
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// Registro es un vencimiento con lo que notarum sabe de él: desde cuándo lo
// ve, desde cuándo rige esa fecha y si ARCA lo dejó de publicar.
type Registro struct {
	Clave string `json:"clave"`
	Vencimiento
	// FechaDesde es desde cuándo ARCA publica ESTA fecha para esta
	// obligación. Se mueve sólo cuando la fecha cambia.
	FechaDesde   time.Time `json:"fecha_desde"`
	VistoPrimero time.Time `json:"visto_primero"`
	VistoUltimo  time.Time `json:"visto_ultimo"`
	// RetiradoEn es cuándo dejó de aparecer estando adentro de la ventana que
	// se consultó. El registro no se borra.
	RetiradoEn *time.Time `json:"retirado_en,omitempty"`
}

// Vigente dice si ARCA lo sigue publicando.
func (r Registro) Vigente() bool { return r.RetiradoEn == nil }

// Los tipos de cambio que se registran.
const (
	Alta        = "alta"
	Prorroga    = "prorroga"
	Adelanto    = "adelanto"
	Baja        = "baja"
	Reaparicion = "reaparicion"
)

// Cambio es algo que ARCA movió en la agenda entre dos bajadas.
type Cambio struct {
	Clave string `json:"clave"`
	Tipo  string `json:"tipo"`
	// FechaAnterior y FechaNueva son AAAA-MM-DD. Una baja no tiene nueva; un
	// alta no tiene anterior.
	FechaAnterior string `json:"fecha_anterior,omitempty"`
	FechaNueva    string `json:"fecha_nueva,omitempty"`
	// Dias es cuánto se corrió la fecha; negativo si se adelantó.
	Dias int `json:"dias,omitempty"`
	// VistoAnterior y DetectadoEn acotan cuándo ocurrió: entre esas dos
	// bajadas. ARCA no publica cuándo cambió algo, así que no se sabe más.
	VistoAnterior time.Time `json:"visto_anterior,omitzero"`
	DetectadoEn   time.Time `json:"detectado_en"`

	// Lo necesario para leer el cambio sin ir a buscar el vencimiento.
	Impuesto        string `json:"impuesto"`
	Regimen         string `json:"regimen,omitempty"`
	Obligacion      string `json:"obligacion"`
	Periodo         string `json:"periodo,omitempty"`
	TerminacionCUIT string `json:"terminacion_cuit"`
}

// Agenda es lo que devolvió una consulta.
type Agenda struct {
	// Desde y Hasta son la ventana que se pidió. Sólo adentro de ella una
	// ausencia significa algo.
	Desde, Hasta time.Time
	Vencimientos []Vencimiento
	// Avisos son rarezas de la página que no impiden usarla.
	Avisos []string
}

// Impuesto es una entrada del desplegable del formulario.
type Impuesto struct {
	Codigo string `json:"codigo"`
	Nombre string `json:"nombre"`
}

// TerminacionDeCUIT lee lo que alguien escribe para filtrar por su CUIT: el
// dígito solo o el CUIT entero, con o sin guiones. Nadie recuerda su
// terminación; recuerda su CUIT.
//
// Devuelve error en vez de ignorar lo que no entiende: un filtro que se ignora
// muestra la agenda de todos, y quien la mira cree que es la suya.
func TerminacionDeCUIT(entrada string) (string, error) {
	limpio := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, entrada)
	switch len(limpio) {
	case 0:
		if strings.TrimSpace(entrada) != "" {
			return "", fmt.Errorf("«%s» no es un CUIT ni una terminación", strings.TrimSpace(entrada))
		}
		return "", nil
	case 1:
		return limpio, nil
	case 11:
		return limpio[10:], nil
	default:
		return "", fmt.Errorf("«%s» no es ni una terminación (un dígito) ni un CUIT (once dígitos)", strings.TrimSpace(entrada))
	}
}

// ParsearFecha lee una fecha AAAA-MM-DD de un pedido y la devuelve igual,
// validada. Vacío es "sin límite".
func ParsearFecha(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if _, err := time.Parse(FormatoISO, s); err != nil {
		return "", fmt.Errorf("«%s» no es una fecha: se escribe AAAA-MM-DD", s)
	}
	return s, nil
}

// FormatoISO es el formato de las fechas de este paquete hacia afuera.
const FormatoISO = "2006-01-02"

// normalizar deja un texto comparable: minúsculas y sin acentos. Quien busca
// no escribe los acentos.
func normalizar(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range strings.ToLower(s) {
		switch c {
		case 'á', 'à', 'ä', 'â':
			c = 'a'
		case 'é', 'è', 'ë', 'ê':
			c = 'e'
		case 'í', 'ì', 'ï', 'î':
			c = 'i'
		case 'ó', 'ò', 'ö', 'ô':
			c = 'o'
		case 'ú', 'ù', 'ü', 'û':
			c = 'u'
		}
		b.WriteRune(c)
	}
	return b.String()
}
