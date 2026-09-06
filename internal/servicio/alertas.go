package servicio

import (
	"context"
	"errors"
	"strconv"
	"strings"

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
	if limite <= 0 {
		limite = cuantasMiraUnaAlerta
	}
	switch alertas.Fuente(fuente) {
	case alertas.FuenteNacional:
		return s.buscarNacionalPara(c, limite)
	case alertas.FuenteProvincial:
		return s.buscarProvincialPara(c, limite)
	case alertas.FuenteBoletin:
		return s.buscarBoletinPara(ctx, c, limite)
	}
	return nil, errors.New("no se sabe dónde mirar: " + fuente)
}

func (s *Servicio) buscarNacionalPara(c Criterios, limite int) ([]Hallazgo, error) {
	if !s.BuscadorInfoLEGActivo() {
		return nil, errors.New("el buscador de normativa nacional está apagado en esta instancia")
	}
	if !s.CatalogoNacionalCargado() {
		return nil, errors.New("el catálogo de InfoLEG todavía no se bajó")
	}
	res := s.BuscarNacional(infoleg.Consulta{
		Texto: c.Texto, Tipo: c.Tipo, Limite: limite,
	})
	if res == nil {
		return nil, nil
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
	return out, nil
}

func (s *Servicio) buscarProvincialPara(c Criterios, limite int) ([]Hallazgo, error) {
	if !s.CatalogoProvincialCargado() {
		return nil, errors.New("la normativa provincial todavía no se bajó")
	}
	provincia := c.Provincia
	if provincia != "" {
		p, hay := saij.BuscarProvincia(provincia)
		if !hay {
			return nil, errors.New("no se reconoce la provincia " + provincia)
		}
		provincia = p.ID
	}
	res := s.BuscarProvincial(saij.Consulta{
		Texto: c.Texto, Tipo: c.Tipo, Provincia: provincia,
		SoloVigentes: c.SoloVigentes, Limite: limite,
	})
	if res == nil {
		return nil, nil
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
	return out, nil
}

func (s *Servicio) buscarBoletinPara(ctx context.Context, c Criterios, limite int) ([]Hallazgo, error) {
	if s.indice == nil {
		return nil, errors.New("esta instancia no tiene índice local: " +
			"las alertas del Boletín necesitan el motor sqlite o postgres")
	}
	var seccion boletin.Seccion
	if c.Seccion != "" {
		sec, err := boletin.ParseSeccion(c.Seccion)
		if err != nil {
			return nil, err
		}
		seccion = sec
	}
	// Los últimos días, que es donde puede haber una novedad. El índice tiene
	// lo que se haya llenado; buscar desde 2015 en cada pasada no agrega nada.
	hasta := boletin.HoyEnArgentina()
	desde := boletin.Fecha{Time: hasta.AddDate(0, 0, -diasQueMiraUnaAlerta)}

	res, err := s.BuscarEnIndice(ctx, almacen.ConsultaLocal{
		Texto: c.Texto, Seccion: seccion, Rubro: c.Tipo,
		Desde: desde, Hasta: hasta, Limite: limite,
	}, 1)
	if err != nil {
		return nil, err
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
	return out, nil
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
	// SinMirar dice qué fuente no se pudo consultar y por qué. Una fuente
	// apagada no puede pasar por "no hay nada": son cosas distintas.
	SinMirar map[string]string `json:"sin_mirar,omitempty"`
}

// BuscarEnTodo busca lo mismo en las tres fuentes.
func (s *Servicio) BuscarEnTodo(ctx context.Context, c Criterios, porFuente int) EnTodo {
	if porFuente <= 0 {
		porFuente = 10
	}
	out := EnTodo{Texto: c.Texto, Por: map[string]int{}}

	// En serie y no en paralelo: con el motor SQLite hay una sola conexión, y
	// tres consultas a la vez se hacen cola igual pero con más piezas móviles.
	for _, fuente := range []string{"boletin", "nacional", "provincial"} {
		hallazgos, err := s.BuscarEnFuente(ctx, fuente, c, porFuente)
		if err != nil {
			if out.SinMirar == nil {
				out.SinMirar = map[string]string{}
			}
			out.SinMirar[fuente] = err.Error()
			continue
		}
		out.Por[fuente] = len(hallazgos)
		out.Normas = append(out.Normas, hallazgos...)
		out.Total += len(hallazgos)
	}
	return out
}
