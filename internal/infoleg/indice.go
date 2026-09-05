package infoleg

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// El índice de normativa nacional.
//
// InfoLEG son 428 mil normas: cinco veces el catálogo provincial. Guardarlas
// enteras en memoria costaría cientos de megabytes, así que acá entra sólo lo
// que hace falta para filtrar y listar —el resto de cada norma sigue en el
// almacén y se lee cuando alguien la abre—, y de los títulos se guarda una
// sola cadena, la normalizada para buscar, en vez de dos.

// EnIndice es una norma recortada a lo que se necesita para encontrarla.
type EnIndice struct {
	ID     int32
	Tipo   string // internado: hay una docena de valores
	Numero string
	// Anio es el de sanción. Se guarda como número para filtrar por rango sin
	// parsear la fecha en cada comparación.
	Anio int16
	// Fecha es la de sanción, tal cual, para mostrarla.
	Fecha string
	// Titulo es de qué trata, para la lista de resultados.
	Titulo string
	// TieneTexto dice si InfoLEG publicó el texto.
	TieneTexto bool
	// buscado es el título, el sumario, el tipo y el número ya normalizados y
	// pegados. Es lo único que se recorre al buscar.
	buscado string
}

// Indice tiene las normas nacionales y sabe buscarlas.
type Indice struct {
	mu     sync.RWMutex
	normas []EnIndice
	porID  map[int32]int
}

func NuevoIndice() *Indice { return NuevoIndiceCon(0) }

// NuevoIndiceCon reserva lugar para todas de entrada.
//
// Con 428 mil normas, dejar que el slice crezca solo cuesta decenas de
// megabytes: cada vez que se llena, Go reserva el doble y copia, y el pico es
// mucho más alto que lo que termina ocupando.
func NuevoIndiceCon(capacidad int) *Indice {
	i := &Indice{porID: map[int32]int{}}
	if capacidad > 0 {
		i.normas = make([]EnIndice, 0, capacidad)
	}
	return i
}

// NormasEsperadas es de qué tamaño arrancar. El catálogo venía en 428 mil;
// pasarse un poco no cuesta casi nada y quedarse corto cuesta una copia.
const NormasEsperadas = 450000

// Agregar suma una norma al índice que se está armando. No es seguro llamarlo
// desde varias goroutines: se usa mientras se arma, con una sola.
func (i *Indice) Agregar(n Norma, internar func(string) string) {
	if n.ID <= 0 {
		return
	}
	e := EnIndice{
		ID:         int32(n.ID),
		Tipo:       internar(n.Tipo),
		Numero:     n.Numero,
		Fecha:      n.FechaSancion,
		Titulo:     n.TituloResumido,
		TieneTexto: n.TieneTexto,
		buscado:    textoDe(n),
	}
	if len(n.FechaSancion) >= 4 {
		if a, err := strconv.Atoi(n.FechaSancion[:4]); err == nil {
			e.Anio = int16(a)
		}
	}
	// Sin título no hay qué mostrar; el sumario suele tener algo.
	if e.Titulo == "" {
		e.Titulo = primeraFrase(n.TituloSumario)
	}
	i.normas = append(i.normas, e)
}

// Cerrar arma el índice por id, una vez que están todas.
func (i *Indice) Cerrar() {
	porID := make(map[int32]int, len(i.normas))
	for k, n := range i.normas {
		porID[n.ID] = k
	}
	i.mu.Lock()
	i.porID = porID
	i.mu.Unlock()
}

// textoDe junta lo que se busca. El sumario entra acá y no se guarda aparte:
// es el campo más pesado del catálogo y sólo sirve para encontrar.
func textoDe(n Norma) string {
	var b strings.Builder
	for _, campo := range []string{
		n.TituloResumido, n.TituloSumario, n.Tipo, n.Numero, n.Organismo,
	} {
		if campo != "" {
			b.WriteString(normalizarTexto(campo))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// primeraFrase corta el sumario en el primer guion: es una lista de materias
// pegadas, y la primera alcanza para saber de qué trata.
func primeraFrase(s string) string {
	if i := strings.IndexByte(s, '-'); i > 0 {
		return s[:i]
	}
	if len(s) > 90 {
		return s[:90]
	}
	return s
}

// normalizarTexto deja un texto comparable: minúsculas, sin acentos, y sin
// los puntos que separan los miles de un número.
//
// Lo del punto importa: la misma ley se escribe 24240 y 24.240, y sin esto
// quien busca "ley 24.240" no encuentra nada.
func normalizarTexto(s string) string {
	r := []rune(strings.ToLower(s))
	var b strings.Builder
	b.Grow(len(s))
	for i, c := range r {
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
		case '.':
			// Sólo el punto que está entre dígitos: el del final de una
			// oración tiene que quedar.
			if i > 0 && i+1 < len(r) && esDigito(r[i-1]) && esDigito(r[i+1]) {
				continue
			}
		}
		b.WriteRune(c)
	}
	return b.String()
}

func esDigito(r rune) bool { return r >= '0' && r <= '9' }

func (i *Indice) Normas() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.normas)
}

func (i *Indice) Cargado() bool { return i.Normas() > 0 }

// Consulta son los criterios de una búsqueda.
type Consulta struct {
	Texto string
	Tipo  string
	// Desde y Hasta son años de sanción.
	Desde, Hasta int
	// SoloConTexto deja fuera las que InfoLEG no publicó.
	SoloConTexto bool

	Limite         int
	Desplazamiento int
}

// Resultado es una página de resultados.
type Resultado struct {
	Total    int        `json:"total"`
	Normas   []EnIndice `json:"-"`
	Truncado bool       `json:"hay_mas"`
}

const (
	LimitePorDefecto = 30
	LimiteMaximo     = 200
)

// Buscar recorre el índice, de la más nueva a la más vieja.
func (i *Indice) Buscar(q Consulta) *Resultado {
	i.mu.RLock()
	defer i.mu.RUnlock()

	terminos := strings.Fields(normalizarTexto(q.Texto))
	tipo := normalizarTexto(strings.TrimSpace(q.Tipo))

	var encontradas []int
	for k := range i.normas {
		n := &i.normas[k]
		if tipo != "" && !strings.Contains(normalizarTexto(n.Tipo), tipo) {
			continue
		}
		if q.Desde > 0 && (n.Anio == 0 || int(n.Anio) < q.Desde) {
			continue
		}
		if q.Hasta > 0 && (n.Anio == 0 || int(n.Anio) > q.Hasta) {
			continue
		}
		if q.SoloConTexto && !n.TieneTexto {
			continue
		}
		if len(terminos) > 0 && !tieneTodos(n.buscado, terminos) {
			continue
		}
		encontradas = append(encontradas, k)
	}

	// Igual que en la normativa provincial: primero lo que se llama así, y
	// después lo que lo menciona de paso.
	afinidad := make(map[int]int, len(encontradas))
	if len(terminos) > 0 {
		for _, k := range encontradas {
			afinidad[k] = pesoDe(&i.normas[k], terminos)
		}
	}
	sort.SliceStable(encontradas, func(a, b int) bool {
		ka, kb := encontradas[a], encontradas[b]
		if afinidad[ka] != afinidad[kb] {
			return afinidad[ka] > afinidad[kb]
		}
		na, nb := &i.normas[ka], &i.normas[kb]
		if na.Fecha != nb.Fecha {
			return na.Fecha > nb.Fecha
		}
		return na.ID > nb.ID
	})

	limite := q.Limite
	if limite <= 0 {
		limite = LimitePorDefecto
	}
	if limite > LimiteMaximo {
		limite = LimiteMaximo
	}
	desde := q.Desplazamiento
	if desde < 0 {
		desde = 0
	}

	res := &Resultado{Total: len(encontradas)}
	if desde >= len(encontradas) {
		return res
	}
	hasta := desde + limite
	if hasta > len(encontradas) {
		hasta = len(encontradas)
	}
	res.Truncado = hasta < len(encontradas)
	for _, k := range encontradas[desde:hasta] {
		res.Normas = append(res.Normas, i.normas[k])
	}
	return res
}

func pesoDe(n *EnIndice, terminos []string) int {
	nombre := normalizarTexto(n.Titulo)
	queEs := normalizarTexto(n.Tipo + " " + n.Numero)

	// Si alguno de los términos es el número de esta norma, la búsqueda la
	// está nombrando y no mencionando. Pasó con "ley 24240": primero salía la
	// 27250, que la modifica y la nombra en su título, y la 24240 —cuyo
	// título es "REGIMEN LEGAL"— quedaba abajo.
	if n.Numero != "" && esNumeroDe(n.Numero, terminos) {
		return 20
	}

	peso := 0
	switch {
	case tieneTodos(nombre, terminos):
		peso = 6
	case tieneTodos(nombre+" "+queEs, terminos):
		peso = 4
	case tieneTodos(queEs, terminos):
		peso = 2
	}
	// Lo que arranca una palabra pesa más que lo que coincide por adentro.
	if empiezanPalabras(n.buscado, terminos) {
		peso++
	}
	return peso
}

// esNumeroDe dice si la búsqueda trae el número exacto de esta norma.
func esNumeroDe(numero string, terminos []string) bool {
	n := normalizarNumero(numero)
	if n == "" {
		return false
	}
	for _, t := range terminos {
		if normalizarNumero(t) == n {
			return true
		}
	}
	return false
}

// normalizarNumero saca los puntos y los ceros de adelante: la misma ley se
// escribe 24240, 24.240 y 024240.
func normalizarNumero(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r != '.' && r != ' ' {
			return "" // no es un número
		}
	}
	return strings.TrimLeft(b.String(), "0")
}

func tieneTodos(texto string, terminos []string) bool {
	for _, t := range terminos {
		if !strings.Contains(texto, t) {
			return false
		}
	}
	return true
}

func empiezanPalabras(texto string, terminos []string) bool {
	for _, t := range terminos {
		if !empiezaPalabraCon(texto, t) {
			return false
		}
	}
	return true
}

func empiezaPalabraCon(texto, termino string) bool {
	desde := 0
	for {
		i := strings.Index(texto[desde:], termino)
		if i < 0 {
			return false
		}
		i += desde
		if i == 0 || !esDeLaPalabra(texto[i-1]) {
			return true
		}
		desde = i + 1
	}
}

func esDeLaPalabra(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// Tipos son los tipos de norma que hay, del más frecuente al menos.
func (i *Indice) Tipos() []ConteoTipo {
	i.mu.RLock()
	defer i.mu.RUnlock()
	cuenta := map[string]int{}
	for k := range i.normas {
		cuenta[i.normas[k].Tipo]++
	}
	tipos := make([]ConteoTipo, 0, len(cuenta))
	for t, c := range cuenta {
		tipos = append(tipos, ConteoTipo{Tipo: t, Normas: c})
	}
	sort.Slice(tipos, func(a, b int) bool {
		if tipos[a].Normas != tipos[b].Normas {
			return tipos[a].Normas > tipos[b].Normas
		}
		return tipos[a].Tipo < tipos[b].Tipo
	})
	return tipos
}

// ConteoTipo es cuántas normas hay de un tipo.
type ConteoTipo struct {
	Tipo   string `json:"tipo"`
	Normas int    `json:"normas"`
}

// Internador devuelve una función que guarda cada cadena repetida una sola
// vez. Con 428 mil normas y una docena de tipos, es la diferencia entre
// guardar doce cadenas y guardar cuatrocientas mil.
func Internador() func(string) string {
	vistas := map[string]string{}
	return func(s string) string {
		if s == "" {
			return ""
		}
		if v, hay := vistas[s]; hay {
			return v
		}
		vistas[s] = s
		return s
	}
}
