package vencimientos

import (
	"sort"
	"strings"
	"sync"
)

// Los límites de una búsqueda.
const (
	LimitePorDefecto = 50
	// LimiteMaximo es el techo. Una agenda de un año son seis mil filas; sin
	// techo, una consulta sin filtros las devolvería todas.
	LimiteMaximo = 500
)

// Consulta son los criterios de una búsqueda en la agenda. Todo en cero trae
// lo vigente con fecha.
type Consulta struct {
	// Desde y Hasta acotan la fecha de vencimiento, en AAAA-MM-DD.
	Desde, Hasta string
	// Impuesto es el nombre, sin importar mayúsculas ni acentos.
	Impuesto string
	// Terminacion es un dígito: trae lo que le vence a esa terminación, más lo
	// que vence para todas.
	Terminacion string
	// Texto busca en impuesto, régimen, obligación, período y sujeto. Todas
	// las palabras tienen que estar; los acentos no importan.
	Texto string
	// SinFechaFija trae sólo las obligaciones de plazo relativo. La consulta
	// normal las deja afuera: no tienen fecha, así que meterlas en "qué vence
	// en septiembre" sería decir que vencen en septiembre.
	SinFechaFija bool
	// ConRetirados trae también lo que ARCA dejó de publicar.
	ConRetirados   bool
	Limite         int
	Desplazamiento int
}

// Resultado es una página de la agenda.
type Resultado struct {
	Total        int        `json:"total"`
	Vencimientos []Registro `json:"vencimientos"`
	// Truncado dice que hay más que los que se devolvieron.
	Truncado bool `json:"hay_mas"`
}

// ConteoImpuesto es un impuesto con cuántos vencimientos vigentes tiene.
type ConteoImpuesto struct {
	Impuesto
	Vencimientos int `json:"vencimientos"`
}

// Indice es la agenda en memoria. Son unos miles de filas por año: recorrerlas
// entero es más rápido que cualquier estructura que haya que mantener.
type Indice struct {
	mu        sync.RWMutex
	registros []Registro
	textos    []string // el texto normalizado de cada registro, para buscar
	cargado   bool
}

func NuevoIndice() *Indice { return &Indice{} }

// Reemplazar pone una agenda nueva.
func (ix *Indice) Reemplazar(rs []Registro) {
	textos := make([]string, len(rs))
	for i, r := range rs {
		textos[i] = normalizar(strings.Join([]string{
			r.Impuesto, r.Regimen, r.Obligacion, r.Periodo, r.Sujeto, r.TipoRegimen, r.Formularios,
		}, " "))
	}
	ix.mu.Lock()
	ix.registros, ix.textos, ix.cargado = rs, textos, true
	ix.mu.Unlock()
}

// Cargado dice si hay una agenda con qué responder.
func (ix *Indice) Cargado() bool {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.cargado
}

// Registros devuelve todo lo que hay, vigente o no. Es para guardar.
func (ix *Indice) Registros() []Registro {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return append([]Registro(nil), ix.registros...)
}

// Buscar filtra la agenda.
func (ix *Indice) Buscar(q Consulta) *Resultado {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	limite := q.Limite
	if limite <= 0 {
		limite = LimitePorDefecto
	}
	if limite > LimiteMaximo {
		limite = LimiteMaximo
	}
	desplazamiento := max(q.Desplazamiento, 0)
	palabras := strings.Fields(normalizar(q.Texto))
	impuesto := normalizar(strings.TrimSpace(q.Impuesto))

	res := &Resultado{Vencimientos: []Registro{}}
	for i, r := range ix.registros {
		if !q.ConRetirados && r.RetiradoEn != nil {
			continue
		}
		if r.SinFechaFija != q.SinFechaFija {
			continue
		}
		if q.Desde != "" && r.Fecha < q.Desde {
			continue
		}
		if q.Hasta != "" && r.Fecha > q.Hasta {
			continue
		}
		if impuesto != "" && normalizar(r.Impuesto) != impuesto {
			continue
		}
		if q.Terminacion != "" && !LeToca(r.TerminacionCUIT, q.Terminacion) {
			continue
		}
		if !contieneTodas(ix.textos[i], palabras) {
			continue
		}
		res.Total++
		if res.Total > desplazamiento && len(res.Vencimientos) < limite {
			res.Vencimientos = append(res.Vencimientos, r)
		}
	}
	res.Truncado = desplazamiento+len(res.Vencimientos) < res.Total
	return res
}

// LeToca dice si un grupo de terminaciones ("0-1-2-3", "todos") incluye a un
// dígito. Se compara dígito por dígito y no por contener el texto: un "1"
// adentro de "10" no es la terminación 1.
func LeToca(grupo, digito string) bool {
	if grupo == "todos" {
		return true
	}
	for _, d := range strings.Split(grupo, "-") {
		if strings.TrimSpace(d) == digito {
			return true
		}
	}
	return false
}

func contieneTodas(texto string, palabras []string) bool {
	for _, p := range palabras {
		if !strings.Contains(texto, p) {
			return false
		}
	}
	return true
}

// PorImpuesto cuenta los vencimientos vigentes de cada impuesto, sumando los
// del catálogo que todavía no tienen ninguno.
func (ix *Indice) PorImpuesto(catalogo []Impuesto) []ConteoImpuesto {
	ix.mu.RLock()
	cuenta := map[string]int{}
	nombres := map[string]string{}
	for _, r := range ix.registros {
		if r.RetiradoEn == nil {
			k := normalizar(r.Impuesto)
			cuenta[k]++
			nombres[k] = r.Impuesto
		}
	}
	ix.mu.RUnlock()

	var out []ConteoImpuesto
	vistos := map[string]bool{}
	for _, i := range catalogo {
		k := normalizar(i.Nombre)
		vistos[k] = true
		out = append(out, ConteoImpuesto{Impuesto: i, Vencimientos: cuenta[k]})
	}
	// La agenda puede nombrar un impuesto que el desplegable no tiene.
	for k, n := range cuenta {
		if !vistos[k] {
			out = append(out, ConteoImpuesto{Impuesto: Impuesto{Nombre: nombres[k]}, Vencimientos: n})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Nombre < out[b].Nombre })
	return out
}
