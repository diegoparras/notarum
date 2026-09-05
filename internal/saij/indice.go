package saij

import (
	"io"
	"sort"
	"strings"
	"sync"
)

// Indice tiene el catálogo en memoria y sabe buscarlo.
//
// Se midió con el catálogo real: 81.403 normas ocupan unos 77 MB y tardan
// 340 ms en cargarse, y una búsqueda con filtros lleva entre 4 y 25 ms —66 ms
// el peor caso, que es recorrer y ordenar las 81 mil sin ningún filtro. Es
// una pasada lineal y no una base de datos, a propósito: así la búsqueda de
// normativa provincial anda igual con los tres motores de almacenamiento, sin
// meterle tablas a ninguno.
//
// Esos 77 MB los paga sólo quien sincroniza SAIJ. Una instancia que no lo
// use no carga nada.
//
// El catálogo se publica entero y de tanto en tanto, así que no hay
// escrituras que coordinar: se arma uno nuevo y se reemplaza.
type Indice struct {
	mu      sync.RWMutex
	normas  []Norma
	buscado []string // el texto de cada norma ya normalizado, para no rehacerlo
	porID   map[string]int
}

func NuevoIndice() *Indice {
	return &Indice{porID: map[string]int{}}
}

// Cargar arma el índice leyendo un catálogo.
func (i *Indice) Cargar(r io.Reader) (int, error) {
	normas := make([]Norma, 0, 90000)
	n, err := LeerCatalogo(r, func(x Norma) error {
		normas = append(normas, x)
		return nil
	})
	if err != nil {
		return n, err
	}
	i.Reemplazar(normas)
	return n, nil
}

// Reemplazar cambia el contenido del índice de una vez.
func (i *Indice) Reemplazar(normas []Norma) {
	buscado := make([]string, len(normas))
	porID := make(map[string]int, len(normas))
	for k, n := range normas {
		buscado[k] = textoDe(n)
		porID[n.ID] = k
	}
	i.mu.Lock()
	i.normas, i.buscado, i.porID = normas, buscado, porID
	i.mu.Unlock()
}

// textoDe junta lo que se busca de una norma. El número entra para poder
// pedir "ley 6109", y la provincia para "constitución de salta".
func textoDe(n Norma) string {
	var b strings.Builder
	for _, campo := range []string{
		n.TituloResumido, n.Nombre, n.TituloSumario,
		n.Tipo, n.Numero, n.Provincia, n.ID,
	} {
		if campo != "" {
			b.WriteString(normalizar(campo))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// Normas dice cuántas hay.
func (i *Indice) Normas() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.normas)
}

// Cargado dice si hay algo con qué responder.
func (i *Indice) Cargado() bool { return i.Normas() > 0 }

// Norma trae una por su identificador.
func (i *Indice) Norma(id string) (Norma, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	k, hay := i.porID[strings.ToUpper(strings.TrimSpace(id))]
	if !hay {
		return Norma{}, false
	}
	return i.normas[k], true
}

// Consulta son los criterios de una búsqueda.
type Consulta struct {
	// Texto se parte en palabras y tienen que estar todas.
	Texto string
	// Provincia acepta el código INDEC, el nombre o el prefijo.
	Provincia string
	Tipo      string
	// Desde y Hasta son años de sanción; 0 los deja abiertos.
	Desde int
	Hasta int
	// SoloVigentes deja fuera lo derogado, caduco y las modificatorias.
	SoloVigentes bool

	Limite         int
	Desplazamiento int
}

// Resultado es una página de resultados.
type Resultado struct {
	Total  int     `json:"total"`
	Normas []Norma `json:"normas"`
	// Truncado avisa que hay más de las que entran en esta página.
	Truncado bool `json:"truncado"`
}

// LimitePorDefecto es cuántas se devuelven si no se pide otra cosa.
const LimitePorDefecto = 30

// LimiteMaximo es el techo. Sin él, una consulta sin filtros devolvería las
// 81 mil de una.
const LimiteMaximo = 200

// Buscar recorre el índice y devuelve las que cumplen, de la más nueva a la
// más vieja.
func (i *Indice) Buscar(q Consulta) *Resultado {
	i.mu.RLock()
	defer i.mu.RUnlock()

	terminos := strings.Fields(normalizar(q.Texto))
	var provincia string
	if q.Provincia != "" {
		if p, hay := BuscarProvincia(q.Provincia); hay {
			provincia = p.ID
		} else {
			// Una provincia que no existe no puede devolver todo: devuelve
			// nada, que es la respuesta correcta.
			return &Resultado{}
		}
	}
	tipo := normalizar(q.Tipo)

	var encontradas []int
	for k, n := range i.normas {
		if provincia != "" && n.ProvinciaID != provincia {
			continue
		}
		if tipo != "" && !strings.Contains(normalizar(n.Tipo), tipo) {
			continue
		}
		if q.Desde > 0 || q.Hasta > 0 {
			a := n.Anio()
			if a == 0 || (q.Desde > 0 && a < q.Desde) || (q.Hasta > 0 && a > q.Hasta) {
				continue
			}
		}
		if q.SoloVigentes && !n.Vigente() {
			continue
		}
		if len(terminos) > 0 && !tieneTodos(i.buscado[k], terminos) {
			continue
		}
		encontradas = append(encontradas, k)
	}

	// El orden: primero lo que coincide en el nombre de la norma, después lo
	// que la menciona de paso.
	//
	// Sin esto, buscar "constitución salta" devolvía primero las leyes de
	// expropiación de 2021 que nombran la Constitución en sus materias, y la
	// Constitución de Salta quedaba enterrada por ser de 1998. Dentro de cada
	// grupo sí manda la fecha, que es como se busca normativa.
	afinidad := make(map[int]int, len(encontradas))
	if len(terminos) > 0 {
		for _, k := range encontradas {
			afinidad[k] = pesoDe(i.normas[k], terminos)
			// Empatar por dónde apareció no alcanza: buscar "agua" traía
			// primero un presupuesto municipal de Bagual, que la contiene
			// adentro de otra palabra. Que arranque una palabra desempata.
			if empiezanPalabras(i.buscado[k], terminos) {
				afinidad[k]++
			}
		}
	}
	sort.SliceStable(encontradas, func(a, b int) bool {
		ka, kb := encontradas[a], encontradas[b]
		if afinidad[ka] != afinidad[kb] {
			return afinidad[ka] > afinidad[kb]
		}
		na, nb := i.normas[ka], i.normas[kb]
		if na.Fecha != nb.Fecha {
			return na.Fecha > nb.Fecha
		}
		return na.ID < nb.ID
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
	res.Normas = make([]Norma, 0, hasta-desde)
	for _, k := range encontradas[desde:hasta] {
		res.Normas = append(res.Normas, i.normas[k])
	}
	return res
}

// pesoDe dice qué tan de lleno le pega la búsqueda a una norma. Se calcula
// sólo sobre las que ya pasaron los filtros, que son pocas: guardar esto para
// las 81 mil costaría memoria a cambio de nada.
func pesoDe(n Norma, terminos []string) int {
	nombre := normalizar(n.TituloResumido + " " + n.Nombre)
	// Lo que la norma es —"Constitución Provincial de Salta"— cuenta como
	// nombre: es lo que alguien escribe cuando busca una constitución.
	queEs := normalizar(n.Tipo + " " + n.Provincia + " " + n.Numero)

	// Si la búsqueda trae el número exacto, está nombrando esta norma y no
	// mencionándola: eso gana sobre cualquier coincidencia de título.
	if n.Numero != "" && n.Numero != "0" && esNumeroDe(n.Numero, terminos) {
		return 20
	}

	// Se multiplica por dos para dejar lugar al desempate por palabra: sin
	// eso, una coincidencia de palabra en las materias pasaría por delante de
	// una del título, que casi siempre importa más.
	switch {
	case tieneTodos(nombre, terminos):
		return 6
	case tieneTodos(nombre+" "+queEs, terminos):
		return 4
	case tieneTodos(queEs, terminos):
		return 2
	}
	return 0 // aparece en las materias, que es mencionarla de paso
}

// empiezanPalabras dice si todos los términos arrancan alguna palabra del
// texto. Se mira el arranque y no la palabra entera para que "agua" siga
// encontrando "aguas" y "educacion" siga encontrando "educacional": el plural
// y los derivados son lo normal en castellano, y lo que hay que dejar atrás
// es la coincidencia por adentro.
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

// esDeLaPalabra dice si un byte sigue formando parte de la palabra anterior.
// Alcanza con mirar bytes: el texto ya viene normalizado, sin acentos.
func esDeLaPalabra(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
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
// escribe 6109, 6.109 y 0006109.
func normalizarNumero(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r != '.' && r != ' ' {
			return ""
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

// PorProvincia cuenta cuántas normas hay de cada jurisdicción, para poder
// mostrar el tamaño de lo que hay sin recorrerlo desde afuera.
func (i *Indice) PorProvincia() map[string]int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	cuenta := map[string]int{}
	for _, n := range i.normas {
		cuenta[n.ProvinciaID]++
	}
	return cuenta
}

// Tipos son los tipos de norma que hay, del más frecuente al menos.
func (i *Indice) Tipos() []ConteoTipo {
	i.mu.RLock()
	defer i.mu.RUnlock()
	cuenta := map[string]int{}
	for _, n := range i.normas {
		cuenta[n.Tipo]++
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
