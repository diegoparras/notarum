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
	switch f {
	case alertas.FuenteNacional:
		return s.alertaNacional(c)
	case alertas.FuenteProvincial:
		return s.alertaProvincial(c)
	case alertas.FuenteBoletin:
		return s.alertaBoletin(ctx, c)
	}
	return nil, errors.New("no se sabe dónde mirar: " + string(f))
}

func (s *Servicio) alertaNacional(c alertas.Criterios) ([]alertas.Coincidencia, error) {
	if !s.BuscadorInfoLEGActivo() {
		return nil, errors.New("el buscador de normativa nacional está apagado en esta instancia")
	}
	if !s.CatalogoNacionalCargado() {
		return nil, errors.New("el catálogo de InfoLEG todavía no se bajó")
	}
	res := s.BuscarNacional(infoleg.Consulta{
		Texto: c.Texto, Tipo: c.Tipo, Limite: cuantasMiraUnaAlerta,
	})
	if res == nil {
		return nil, nil
	}
	out := make([]alertas.Coincidencia, 0, len(res.Normas))
	for _, n := range res.Normas {
		id := int(n.ID)
		out = append(out, alertas.Coincidencia{
			ID:      "nacional:" + strconv.Itoa(id),
			Titulo:  strings.TrimSpace(n.Tipo + " " + n.Numero),
			Detalle: n.Titulo,
			Fecha:   n.Fecha,
			Enlace:  "/norma/" + strconv.Itoa(id),
		})
	}
	return out, nil
}

func (s *Servicio) alertaProvincial(c alertas.Criterios) ([]alertas.Coincidencia, error) {
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
		SoloVigentes: c.SoloVigentes, Limite: cuantasMiraUnaAlerta,
	})
	if res == nil {
		return nil, nil
	}
	out := make([]alertas.Coincidencia, 0, len(res.Normas))
	for _, n := range res.Normas {
		out = append(out, alertas.Coincidencia{
			ID:      "provincial:" + n.ID,
			Titulo:  n.Descripcion(),
			Detalle: n.Titulo(),
			Fecha:   n.Fecha,
			Enlace:  "/provincial/" + n.ID,
		})
	}
	return out, nil
}

func (s *Servicio) alertaBoletin(ctx context.Context, c alertas.Criterios) ([]alertas.Coincidencia, error) {
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
		Desde: desde, Hasta: hasta, Limite: cuantasMiraUnaAlerta,
	}, 1)
	if err != nil {
		return nil, err
	}
	out := make([]alertas.Coincidencia, 0, len(res.Avisos))
	for _, a := range res.Avisos {
		out = append(out, alertas.Coincidencia{
			ID:      "boletin:" + a.ID + ":" + a.Fecha.API(),
			Titulo:  strings.TrimSpace(a.Organismo + " " + a.Norma),
			Detalle: a.Sintesis,
			Fecha:   a.Fecha.API(),
			Enlace:  "/av/" + string(a.Seccion) + "/" + a.ID + "/" + a.Fecha.API(),
		})
	}
	return out, nil
}

// diasQueMiraUnaAlerta es cuánto para atrás mira una alerta del Boletín. Más
// que la frecuencia con la que corre, para que un día que falle no deje un
// agujero.
const diasQueMiraUnaAlerta = 7
