package servicio

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
)

// Qué apareció desde la última vez.
//
// Un programa que consulta todos los días no quiere el catálogo entero: quiere
// lo que cambió. Sin eso hay que bajar 428 mil normas para descubrir que
// ninguna es nueva, o escribir la comparación por fuera, que es lo que termina
// haciendo cada quien por su cuenta y de una forma distinta.
//
// «Nuevo» acá quiere decir «notarum no lo había visto», y no «la norma es
// reciente». No es lo mismo: el portal agrega normas viejas todo el tiempo, y
// una ley de 1998 que aparece hoy en el catálogo es una novedad para quien lo
// sigue, aunque su fecha diga otra cosa. Filtrar por la fecha de la norma se
// las perdería sin avisar.

// diasDeNovedades es cuánta historia de novedades se guarda. Un programa que
// no consultó en tres meses tiene que bajar todo de nuevo, y está bien: es
// menos trabajo que guardar el registro para siempre.
const diasDeNovedades = 120

func claveConocidos(fuente string) string { return "novedades/" + fuente + "/_conocidos" }
func claveNovedades(fuente, dia string) string {
	return "novedades/" + fuente + "/" + dia
}
func claveDiasConNovedades(fuente string) string { return "novedades/" + fuente + "/_dias" }

// registrarNovedades compara lo que hay ahora con lo que ya se conocía y anota
// lo que apareció.
//
// Devuelve los identificadores nuevos. La lista de conocidos se guarda entera
// en una sola entrada: son unos cientos de miles de números, que ocupan menos
// que una sola edición del Boletín, y leerla una vez por sincronización cuesta
// mucho menos que preguntar por cada norma si ya estaba.
func (s *Servicio) registrarNovedades(fuente string, ahora []string) []string {
	conocidos := map[string]bool{}
	if crudo, hay := s.cache.Leer(claveConocidos(fuente)); hay {
		var lista []string
		if json.Unmarshal(crudo, &lista) == nil {
			for _, id := range lista {
				conocidos[id] = true
			}
		}
	}
	primeraVez := len(conocidos) == 0

	var nuevas []string
	for _, id := range ahora {
		if id != "" && !conocidos[id] {
			nuevas = append(nuevas, id)
		}
	}

	if crudo, err := json.Marshal(ahora); err == nil {
		if err := s.cache.Guardar(claveConocidos(fuente), crudo, almacen.SinVencimiento); err != nil {
			return nil
		}
	}
	// La primera sincronización no es una novedad: sería anunciar el catálogo
	// entero como recién aparecido, que es lo mismo que no decir nada.
	if primeraVez || len(nuevas) == 0 {
		return nil
	}
	s.anotarDelDia(fuente, nuevas)
	return nuevas
}

// anotarDelDia guarda lo nuevo bajo el día en que apareció.
func (s *Servicio) anotarDelDia(fuente string, nuevas []string) {
	dia := time.Now().UTC().Format("2006-01-02")
	// Si ya se sincronizó hoy, se suma a lo del día en vez de pisarlo.
	del := s.novedadesDelDia(fuente, dia)
	yaEsta := make(map[string]bool, len(del))
	for _, id := range del {
		yaEsta[id] = true
	}
	for _, id := range nuevas {
		if !yaEsta[id] {
			del = append(del, id)
		}
	}
	if crudo, err := json.Marshal(del); err == nil {
		_ = s.cache.Guardar(claveNovedades(fuente, dia), crudo, almacen.SinVencimiento)
	}
	s.sumarDia(fuente, dia)
}

func (s *Servicio) novedadesDelDia(fuente, dia string) []string {
	crudo, hay := s.cache.Leer(claveNovedades(fuente, dia))
	if !hay {
		return nil
	}
	var ids []string
	if json.Unmarshal(crudo, &ids) != nil {
		return nil
	}
	return ids
}

// sumarDia mantiene la lista de días con novedades, y borra los viejos.
func (s *Servicio) sumarDia(fuente, dia string) {
	dias := s.diasConNovedades(fuente)
	for _, d := range dias {
		if d == dia {
			return
		}
	}
	dias = append(dias, dia)
	sort.Strings(dias)

	corte := time.Now().UTC().AddDate(0, 0, -diasDeNovedades).Format("2006-01-02")
	quedan := make([]string, 0, len(dias))
	for _, d := range dias {
		if d < corte {
			_ = s.cache.Borrar(claveNovedades(fuente, d))
			continue
		}
		quedan = append(quedan, d)
	}
	if crudo, err := json.Marshal(quedan); err == nil {
		_ = s.cache.Guardar(claveDiasConNovedades(fuente), crudo, almacen.SinVencimiento)
	}
}

func (s *Servicio) diasConNovedades(fuente string) []string {
	crudo, hay := s.cache.Leer(claveDiasConNovedades(fuente))
	if !hay {
		return nil
	}
	var dias []string
	if json.Unmarshal(crudo, &dias) != nil {
		return nil
	}
	return dias
}

// Novedades son los identificadores que aparecieron desde un día, inclusive.
//
// Devuelve además hasta dónde llega el registro: un programa que pregunta por
// una fecha más vieja que eso tiene que saber que la respuesta está incompleta
// en vez de suponer que no pasó nada.
type Novedades struct {
	Desde string   `json:"desde"`
	IDs   []string `json:"-"`
	Total int      `json:"total"`
	Dias  []string `json:"dias,omitempty"`
	// RegistroDesde es el día más viejo del que se guarda registro.
	RegistroDesde string `json:"registro_desde,omitempty"`
	// Completo dice si el registro alcanza para contestar lo que se preguntó.
	Completo bool `json:"completo"`
}

// NovedadesDesde arma lo que apareció desde un día.
func (s *Servicio) NovedadesDesde(fuente, desde string) Novedades {
	n := Novedades{Desde: desde, Completo: true}
	dias := s.diasConNovedades(fuente)
	if len(dias) > 0 {
		n.RegistroDesde = dias[0]
		// Se guarda un registro acotado: preguntar por antes de eso no se
		// puede contestar, y hay que decirlo.
		if desde < dias[0] {
			n.Completo = false
		}
	} else {
		// Sin registro no se puede afirmar que no haya pasado nada.
		n.Completo = false
	}
	vistos := map[string]bool{}
	for _, d := range dias {
		if d < desde {
			continue
		}
		n.Dias = append(n.Dias, d)
		for _, id := range s.novedadesDelDia(fuente, d) {
			if !vistos[id] {
				vistos[id] = true
				n.IDs = append(n.IDs, id)
			}
		}
	}
	n.Total = len(n.IDs)
	return n
}

// idsDeNormas pasa una lista de números a texto, que es como se guardan para
// que las dos fuentes usen el mismo registro.
func idsDeNormas(ids []int) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.Itoa(id))
	}
	return out
}
