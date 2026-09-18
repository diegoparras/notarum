package servicio

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/alertas"
	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/saij"
)

// Buscar lo que espera una alerta.
//
// Cada fuente tiene su propia forma de buscar y su propia forma de nombrar lo
// que devuelve; acá se las lleva a una sola, que es lo que le permite a una
// alerta no saber de dónde salió lo que encontró.

// cuantasMiraUnaAlerta es cuántos resultados se traen por pasada.
//
// Las alertas miran lo más nuevo, que es lo único que puede ser una novedad:
// traer el catálogo entero para descubrir que ya se avisó de todo sería pagar
// una búsqueda enorme en cada pasada de cada alerta.
const cuantasMiraUnaAlerta = 200

// BuscarParaAlerta traduce los criterios de una alerta a su fuente.
func (s *Servicio) BuscarParaAlerta(ctx context.Context, f alertas.Fuente, c alertas.Criterios) ([]alertas.Coincidencia, error) {
	hallazgos, err := s.BuscarEnFuente(ctx, string(f), Criterios{
		Texto: c.Texto, Tipo: c.Tipo, Provincia: c.Provincia,
		Seccion: c.Seccion, SoloVigentes: c.SoloVigentes,
		// Sólo lo reciente puede ser una novedad: buscar desde 2015 en cada
		// pasada de cada alerta no agrega nada.
		UltimosDias: diasQueMiraUnaAlerta,
	}, cuantasMiraUnaAlerta)
	if err != nil {
		return nil, err
	}
	out := make([]alertas.Coincidencia, 0, len(hallazgos))
	for _, h := range hallazgos {
		out = append(out, alertas.Coincidencia{
			ID: h.ID, Titulo: h.Titulo, Detalle: h.Detalle,
			Fecha: h.Fecha, Enlace: h.Enlace,
		})
	}
	return out, nil
}

// Criterios es qué se busca, igual para las tres fuentes.
type Criterios struct {
	Texto        string
	Tipo         string
	Provincia    string
	Seccion      string
	SoloVigentes bool
	// DesdeAnio y HastaAnio acotan por año en las tres: el de sanción en la
	// normativa, el de publicación en el Boletín. Cero los deja abiertos.
	DesdeAnio, HastaAnio int
	// UltimosDias acota el Boletín a lo reciente. Lo usan las alertas, que
	// sólo pueden encontrar novedades ahí; una búsqueda común lo deja en cero
	// y mira toda la historia indexada.
	//
	// Antes esa ventana estaba escrita adentro de la búsqueda del Boletín, y
	// la usaban también /v1/todo y buscar_todo del MCP: pedirle al Boletín
	// algo de hace un mes daba cero resultados, sin avisar por qué.
	UltimosDias int
	// Desplazamiento es desde qué resultado traer, para paginar.
	Desplazamiento int
}

// PaginaDeFuente es una página de resultados de una fuente, con cuántos hay
// en total: sin el total, "10 resultados" no dice si son todos o los
// primeros diez de tres mil.
type PaginaDeFuente struct {
	Hallazgos []Hallazgo
	Total     int
}

// Hallazgo es algo encontrado, venga de donde venga. Es lo que permite juntar
// resultados de tres fuentes que no se parecen en nada.
type Hallazgo struct {
	Fuente  string `json:"fuente"`
	ID      string `json:"id"`
	Titulo  string `json:"titulo"`
	Detalle string `json:"detalle,omitempty"`
	Fecha   string `json:"fecha,omitempty"`
	// Enlace es dónde verlo en esta instancia; EnAPI, dónde pedirlo.
	Enlace string `json:"enlace"`
	EnAPI  string `json:"en_api,omitempty"`
}

// BuscarEnFuente busca en una sola de las tres.
func (s *Servicio) BuscarEnFuente(ctx context.Context, fuente string, c Criterios, limite int) ([]Hallazgo, error) {
	p, err := s.BuscarPagina(ctx, fuente, c, limite)
	return p.Hallazgos, err
}

// BuscarPagina busca en una fuente y dice además cuántos hay en total.
func (s *Servicio) BuscarPagina(ctx context.Context, fuente string, c Criterios, limite int) (PaginaDeFuente, error) {
	if limite <= 0 {
		limite = cuantasMiraUnaAlerta
	}
	if c.Desplazamiento < 0 {
		c.Desplazamiento = 0
	}
	switch alertas.Fuente(fuente) {
	case alertas.FuenteNacional:
		return s.buscarNacionalPara(c, limite)
	case alertas.FuenteProvincial:
		return s.buscarProvincialPara(c, limite)
	case alertas.FuenteBoletin:
		return s.buscarBoletinPara(ctx, c, limite)
	}
	return PaginaDeFuente{}, errors.New("no se sabe dónde mirar: " + fuente)
}

func (s *Servicio) buscarNacionalPara(c Criterios, limite int) (PaginaDeFuente, error) {
	if !s.BuscadorInfoLEGActivo() {
		return PaginaDeFuente{}, errors.New("el buscador de normativa nacional está apagado en esta instancia")
	}
	if !s.CatalogoNacionalCargado() {
		return PaginaDeFuente{}, errors.New("el catálogo de InfoLEG todavía no se bajó")
	}
	res := s.BuscarNacional(infoleg.Consulta{
		Texto: c.Texto, Tipo: c.Tipo, Desde: c.DesdeAnio, Hasta: c.HastaAnio,
		Limite: limite, Desplazamiento: c.Desplazamiento,
	})
	if res == nil {
		return PaginaDeFuente{}, nil
	}
	out := make([]Hallazgo, 0, len(res.Normas))
	for _, n := range res.Normas {
		id := int(n.ID)
		out = append(out, Hallazgo{
			Fuente:  "nacional",
			ID:      "nacional:" + strconv.Itoa(id),
			Titulo:  strings.TrimSpace(n.Tipo + " " + n.Numero),
			Detalle: n.Titulo,
			Fecha:   n.Fecha,
			Enlace:  "/norma/" + strconv.Itoa(id),
			EnAPI:   "/v1/nacional/" + strconv.Itoa(id),
		})
	}
	return PaginaDeFuente{Hallazgos: out, Total: res.Total}, nil
}

func (s *Servicio) buscarProvincialPara(c Criterios, limite int) (PaginaDeFuente, error) {
	if !s.CatalogoProvincialCargado() {
		return PaginaDeFuente{}, errors.New("la normativa provincial todavía no se bajó")
	}
	provincia := c.Provincia
	if provincia != "" {
		p, hay := saij.BuscarProvincia(provincia)
		if !hay {
			return PaginaDeFuente{}, errors.New("no se reconoce la provincia " + provincia)
		}
		provincia = p.ID
	}
	res := s.BuscarProvincial(saij.Consulta{
		Texto: c.Texto, Tipo: c.Tipo, Provincia: provincia,
		Desde: c.DesdeAnio, Hasta: c.HastaAnio,
		SoloVigentes: c.SoloVigentes, Limite: limite, Desplazamiento: c.Desplazamiento,
	})
	if res == nil {
		return PaginaDeFuente{}, nil
	}
	out := make([]Hallazgo, 0, len(res.Normas))
	for _, n := range res.Normas {
		out = append(out, Hallazgo{
			Fuente:  "provincial",
			ID:      "provincial:" + n.ID,
			Titulo:  n.Descripcion(),
			Detalle: n.Titulo(),
			Fecha:   n.Fecha,
			Enlace:  "/provincial/" + n.ID,
			EnAPI:   "/v1/provincial/" + n.ID,
		})
	}
	return PaginaDeFuente{Hallazgos: out, Total: res.Total}, nil
}

func (s *Servicio) buscarBoletinPara(ctx context.Context, c Criterios, limite int) (PaginaDeFuente, error) {
	if s.indice == nil {
		return PaginaDeFuente{}, errors.New("esta instancia no tiene índice local: " +
			"buscar en el Boletín necesita el motor sqlite o postgres")
	}
	var seccion boletin.Seccion
	if c.Seccion != "" {
		sec, err := boletin.ParseSeccion(c.Seccion)
		if err != nil {
			return PaginaDeFuente{}, err
		}
		seccion = sec
	}
	desde, hasta := rangoDelBoletin(c)

	// BuscarEnIndice pagina por número de página; el desplazamiento se
	// traduce a la página que lo contiene.
	res, err := s.BuscarEnIndice(ctx, almacen.ConsultaLocal{
		Texto: c.Texto, Seccion: seccion, Rubro: c.Tipo,
		Desde: desde, Hasta: hasta, Limite: limite,
	}, c.Desplazamiento/limite+1)
	if err != nil {
		return PaginaDeFuente{}, err
	}
	out := make([]Hallazgo, 0, len(res.Avisos))
	for _, a := range res.Avisos {
		out = append(out, Hallazgo{
			Fuente:  "boletin",
			ID:      "boletin:" + a.ID + ":" + a.Fecha.API(),
			Titulo:  strings.TrimSpace(a.Organismo + " " + a.Norma),
			Detalle: a.Sintesis,
			Fecha:   a.Fecha.API(),
			Enlace:  "/av/" + string(a.Seccion) + "/" + a.ID + "/" + a.Fecha.API(),
			EnAPI:   "/v1/avisos/" + string(a.Seccion) + "/" + a.ID + "/" + a.Fecha.API(),
		})
	}
	return PaginaDeFuente{Hallazgos: out, Total: res.Total}, nil
}

// rangoDelBoletin traduce los criterios a fechas. La ventana de las alertas
// manda; si no, los años, que en el Boletín van del primero de enero al 31
// de diciembre. Sin nada, las fechas quedan vacías y se mira todo.
func rangoDelBoletin(c Criterios) (desde, hasta boletin.Fecha) {
	if c.UltimosDias > 0 {
		hasta = boletin.HoyEnArgentina()
		return boletin.Fecha{Time: hasta.AddDate(0, 0, -c.UltimosDias)}, hasta
	}
	if c.DesdeAnio > 0 {
		desde = boletin.Fecha{Time: time.Date(c.DesdeAnio, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	if c.HastaAnio > 0 {
		hasta = boletin.Fecha{Time: time.Date(c.HastaAnio, 12, 31, 0, 0, 0, 0, time.UTC)}
	}
	return desde, hasta
}

// diasQueMiraUnaAlerta es cuánto para atrás mira una alerta del Boletín. Más
// que la frecuencia con la que corre, para que un día que falle no deje un
// agujero.
const diasQueMiraUnaAlerta = 7

// ------------------------------------------------ buscar en las tres a la vez

// EnTodo es el resultado de buscar en las tres fuentes.
//
// Van juntas porque la pregunta es una sola. Hoy hay que saber de antemano si
// lo que se busca está en el Boletín, en InfoLEG o en SAIJ, y hacer tres
// consultas: eso es pedirle a quien pregunta que conozca cómo está organizado
// el Estado antes de poder buscar.
type EnTodo struct {
	Texto  string         `json:"texto"`
	Total  int            `json:"total"`
	Por    map[string]int `json:"por_fuente"`
	Normas []Hallazgo     `json:"resultados"`
	// Totales son cuántos coinciden en cada fuente, no cuántos se
	// devolvieron: "10" no dice si son todos o los primeros diez de tres mil.
	Totales map[string]int `json:"totales"`
	// SinMirar dice qué fuente no se pudo consultar y por qué. Una fuente
	// apagada no puede pasar por "no hay nada": son cosas distintas.
	SinMirar map[string]string `json:"sin_mirar,omitempty"`
}

// BuscarEnTodo busca lo mismo en las tres fuentes.
func (s *Servicio) BuscarEnTodo(ctx context.Context, c Criterios, porFuente int) EnTodo {
	if porFuente <= 0 {
		porFuente = 10
	}
	out := EnTodo{Texto: c.Texto, Por: map[string]int{}, Totales: map[string]int{}}

	// En serie y no en paralelo: con el motor SQLite hay una sola conexión, y
	// tres consultas a la vez se hacen cola igual pero con más piezas móviles.
	for _, fuente := range []string{"boletin", "nacional", "provincial"} {
		p, err := s.BuscarPagina(ctx, fuente, c, porFuente)
		if err != nil {
			if out.SinMirar == nil {
				out.SinMirar = map[string]string{}
			}
			out.SinMirar[fuente] = err.Error()
			continue
		}
		out.Por[fuente] = len(p.Hallazgos)
		out.Totales[fuente] = p.Total
		out.Normas = append(out.Normas, p.Hallazgos...)
		out.Total += len(p.Hallazgos)
	}
	return out
}
