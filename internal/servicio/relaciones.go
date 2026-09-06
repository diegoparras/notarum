package servicio

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/infoleg"
)

// Las relaciones entre normas nacionales: qué modificó a cada una y qué
// modifica cada una.
//
// El catálogo principal trae las dos como números —"modificada por 7"— y nada
// más, que es un dato que no lleva a ningún lado: hay que ir a buscar cuáles
// igual. El detalle está en dos bases complementarias del mismo dataset, que
// se bajan con la misma sincronización.
//
// Se guardan las dos direcciones por separado y con los datos de la otra norma
// adentro. Guardar sólo los identificadores obligaría a leer decenas de normas
// para dibujar una lista, y esas lecturas se pagan en cada visita; los datos
// vienen en el mismo archivo, así que no cuesta nada tenerlos.

func claveModificadaPor(id int) string { return "infoleg/rel/modificada-por/" + strconv.Itoa(id) }
func claveModificaA(id int) string     { return "infoleg/rel/modifica-a/" + strconv.Itoa(id) }

// EstadoRelaciones dice qué se guardó de las bases complementarias.
type EstadoRelaciones struct {
	// NormasConModificatorias es a cuántas normas se les sabe qué las modificó.
	NormasConModificatorias int `json:"normas_con_modificatorias"`
	// NormasQueModifican es cuántas normas modifican a alguna otra.
	NormasQueModifican int `json:"normas_que_modifican"`
	// Relaciones es el total de pares guardados, sumando las dos direcciones.
	Relaciones int `json:"relaciones"`
}

// ModificadaPor son las normas que modificaron a ésta.
func (s *Servicio) ModificadaPor(id int) []infoleg.Relacion {
	return s.relacionesGuardadas(claveModificadaPor(id))
}

// ModificaA son las normas que ésta modificó.
func (s *Servicio) ModificaA(id int) []infoleg.Relacion {
	return s.relacionesGuardadas(claveModificaA(id))
}

func (s *Servicio) relacionesGuardadas(clave string) []infoleg.Relacion {
	crudo, hay := s.cache.Leer(clave)
	if !hay {
		return nil
	}
	var rs []infoleg.Relacion
	if err := json.Unmarshal(crudo, &rs); err != nil {
		return nil
	}
	return rs
}

// sincronizarRelaciones baja las dos bases complementarias y las guarda.
//
// No devuelve error hacia arriba: que el portal no publique las
// complementarias, o que cambien de forma, no puede impedir que el catálogo
// principal se sincronice. Se anota y se sigue.
func (s *Servicio) sincronizarRelaciones(ctx context.Context, info *infoleg.InfoCatalogo, dirTrabajo string, avisar func(string)) EstadoRelaciones {
	var e EstadoRelaciones
	for _, cual := range []struct {
		recurso infoleg.Recurso
		sentido infoleg.Sentido
		nombre  string
		clave   func(int) string
		cuenta  *int
	}{
		{info.Modificadas, infoleg.ModificadaPor, "qué modificó a cada norma", claveModificadaPor, &e.NormasConModificatorias},
		{info.Modificatorias, infoleg.ModificaA, "qué modifica cada norma", claveModificaA, &e.NormasQueModifican},
	} {
		if !cual.recurso.Hay() {
			slog.Info("el portal no publica una base complementaria", "cual", cual.nombre)
			continue
		}
		if avisar != nil {
			avisar("bajando " + cual.nombre)
		}
		normas, relaciones, err := s.guardarRelaciones(ctx, cual.recurso.URL, cual.sentido, dirTrabajo, cual.clave)
		if err != nil {
			if ctx.Err() != nil {
				return e // cortado a propósito: no es una falla de la fuente
			}
			slog.Warn("no se pudieron guardar las relaciones", "cual", cual.nombre, "err", err)
			continue
		}
		*cual.cuenta = normas
		e.Relaciones += relaciones
	}
	return e
}

func (s *Servicio) guardarRelaciones(ctx context.Context, url string, sentido infoleg.Sentido, dirTrabajo string, clave func(int) string) (normas, relaciones int, err error) {
	ruta := filepath.Join(dirTrabajo, "infoleg-relaciones-"+strconv.Itoa(int(sentido))+".zip")
	defer os.Remove(ruta)

	if err := s.infoleg.DescargarCatalogo(ctx, url, ruta); err != nil {
		return 0, 0, err
	}
	lector, err := infoleg.AbrirCatalogo(ruta)
	if err != nil {
		return 0, 0, err
	}
	defer lector.Close()

	// El archivo entero antes de guardar nada: las filas de una misma norma no
	// vienen juntas, así que no se sabe la lista de ninguna hasta el final.
	porNorma, err := infoleg.LeerRelaciones(lector, sentido)
	if err != nil {
		return 0, 0, err
	}
	for id, rs := range porNorma {
		if ctx.Err() != nil {
			return normas, relaciones, ctx.Err()
		}
		crudo, err := json.Marshal(rs)
		if err != nil {
			continue
		}
		if err := s.cache.Guardar(clave(id), crudo, almacen.SinVencimiento); err != nil {
			return normas, relaciones, err
		}
		normas++
		relaciones += len(rs)
	}
	if normas == 0 {
		return 0, 0, errors.New("la base complementaria no trajo ninguna relación")
	}
	return normas, relaciones, nil
}
