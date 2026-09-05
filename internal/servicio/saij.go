package servicio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/saij"
)

// La normativa provincial. notarum sigue el Boletín Oficial de la Nación, así
// que las leyes de las provincias —que salen en el boletín de cada una— le
// quedaban afuera. La Base SAIJ que publica el Ministerio de Justicia las
// cubre, y es lo único que hay en un solo lugar para las 24 jurisdicciones.

const (
	claveEstadoSAIJ = "saij/_estado"
	// El catálogo se guarda como JSON y no como el CSV que vino: el almacén
	// guarda JSON, y así al levantarlo no hay que volver a parsear nada.
	claveCatalogoSAIJ = "saij/catalogo"
)

// EstadoSAIJ cuenta qué se sabe del catálogo provincial.
type EstadoSAIJ struct {
	Sincronizado   bool      `json:"sincronizado"`
	Normas         int       `json:"normas"`
	Provincias     int       `json:"provincias"`
	CatalogoAlDia  time.Time `json:"catalogo_publicado,omitempty"`
	SincronizadoEn time.Time `json:"sincronizado_en,omitempty"`
}

// SAIJDisponible dice si esta instancia puede consultar normativa provincial.
func (s *Servicio) SAIJDisponible() bool { return s.saij != nil }

// EstadoSAIJ lee lo que quedó de la última sincronización.
func (s *Servicio) EstadoSAIJ() EstadoSAIJ {
	var e EstadoSAIJ
	if crudo, ok := s.cache.Leer(claveEstadoSAIJ); ok {
		_ = json.Unmarshal(crudo, &e)
	}
	return e
}

// cadaCuantoMirar es cada cuánto se vuelve a preguntar por un catálogo más
// nuevo cuando ya hay uno cargado. Con el índice vacío se pregunta siempre:
// es el caso en que la respuesta cambia de "no hay nada" a "están las 81 mil",
// y esperar un minuto para notarlo sería raro.
const cadaCuantoMirar = time.Minute

// indiceSAIJ devuelve el índice, cargándolo cuando hace falta.
//
// La carga es diferida a propósito: son 77 MB y 340 ms que sólo tiene que
// pagar quien de verdad consulte normativa provincial. Una instancia que no
// la use no carga nada, ni siquiera si el catálogo está guardado.
//
// Y se releva: `notarum provincial` se corre aparte del servicio —en un
// contenedor, desde la consola— y escribe en el mismo almacén. Si el índice
// se armara una sola vez, ese catálogo recién bajado no se notaría hasta
// reiniciar, y quien lo bajó vería la misma pantalla de antes.
func (s *Servicio) indiceSAIJ() *saij.Indice {
	s.saijMu.RLock()
	indice, cargado, mirado := s.saijIndice, s.saijCargado, s.saijMirado
	s.saijMu.RUnlock()

	if indice != nil && indice.Cargado() && time.Since(mirado) < cadaCuantoMirar {
		return indice
	}

	s.saijMu.Lock()
	defer s.saijMu.Unlock()
	// Otro pedido pudo haberlo cargado mientras se esperaba el candado.
	if s.saijIndice != nil && s.saijIndice.Cargado() && s.saijCargado.After(cargado) {
		return s.saijIndice
	}
	if s.saijIndice == nil {
		s.saijIndice = saij.NuevoIndice()
	}
	s.saijMirado = time.Now()

	e := s.EstadoSAIJ()
	if !e.Sincronizado {
		return s.saijIndice
	}
	// Lo que está en memoria ya es de esta versión del catálogo.
	if s.saijIndice.Cargado() && !e.SincronizadoEn.After(s.saijCargado) {
		return s.saijIndice
	}

	crudo, hay := s.cache.Leer(claveCatalogoSAIJ)
	if !hay {
		return s.saijIndice
	}
	empezo := time.Now()
	var normas []saij.Norma
	if err := json.Unmarshal(crudo, &normas); err != nil {
		slog.Error("no se pudo leer el catálogo provincial guardado", "err", err)
		return s.saijIndice
	}
	s.saijIndice.Reemplazar(normas)
	s.saijCargado = e.SincronizadoEn
	slog.Info("catálogo provincial en memoria",
		"normas", len(normas), "tardo", time.Since(empezo))
	return s.saijIndice
}

// BuscarProvincial busca en la normativa provincial.
func (s *Servicio) BuscarProvincial(q saij.Consulta) *saij.Resultado {
	return s.indiceSAIJ().Buscar(q)
}

// NormaProvincial trae una norma por su identificador de SAIJ.
func (s *Servicio) NormaProvincial(id string) (saij.Norma, bool) {
	return s.indiceSAIJ().Norma(id)
}

// ProvinciasConNormas son las jurisdicciones y cuántas normas hay de cada una.
func (s *Servicio) ProvinciasConNormas() []ProvinciaConNormas {
	cuenta := s.indiceSAIJ().PorProvincia()
	salida := make([]ProvinciaConNormas, 0, len(saij.Provincias))
	for _, p := range saij.Provincias {
		salida = append(salida, ProvinciaConNormas{Provincia: p, Normas: cuenta[p.ID]})
	}
	return salida
}

// ProvinciaConNormas es una jurisdicción con el tamaño de lo que hay de ella.
type ProvinciaConNormas struct {
	saij.Provincia
	Normas int `json:"normas"`
}

// TiposProvinciales son los tipos de norma que trae el catálogo.
func (s *Servicio) TiposProvinciales() []saij.ConteoTipo {
	return s.indiceSAIJ().Tipos()
}

// CatalogoProvincialCargado dice si hay algo con qué responder.
func (s *Servicio) CatalogoProvincialCargado() bool { return s.indiceSAIJ().Cargado() }

// SincronizarSAIJ baja el catálogo provincial y lo guarda.
//
// A diferencia del de InfoLEG, que se guarda norma por norma para poder
// cruzarlo con los avisos, éste se guarda entero: se consulta como un
// conjunto —buscar, filtrar, contar— y no de a una norma por vez.
func (s *Servicio) SincronizarSAIJ(ctx context.Context, dirTrabajo string) (EstadoSAIJ, error) {
	e := s.EstadoSAIJ()
	if s.saij == nil {
		return e, errors.New("esta instancia no tiene la base provincial configurada")
	}

	info, err := s.saij.BuscarCatalogo(ctx)
	if err != nil {
		return e, err
	}
	// Si el portal publica lo mismo que ya está guardado, no hay por qué
	// bajar 28 MB de nuevo.
	if e.Sincronizado && !info.Modificado.IsZero() && info.Modificado.Equal(e.CatalogoAlDia) {
		slog.Info("el catálogo provincial ya está al día",
			"publicado", info.Modificado.Format("2006-01-02"))
		return e, nil
	}
	slog.Info("bajando el catálogo provincial", "url", info.URL,
		"publicado", info.Modificado.Format("2006-01-02"))

	if dirTrabajo == "" {
		dirTrabajo = os.TempDir()
	}
	if err := os.MkdirAll(dirTrabajo, 0o755); err != nil {
		return e, err
	}
	ruta := filepath.Join(dirTrabajo, "saij-normativa-provincial.csv")
	defer os.Remove(ruta)

	if err := s.saij.DescargarCatalogo(ctx, info.URL, ruta); err != nil {
		return e, err
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		return e, err
	}

	// Se lee antes de guardar: si el archivo no se entiende, es preferible
	// quedarse con el catálogo viejo que reemplazarlo por algo ilegible.
	provincias := map[string]bool{}
	var normas []saij.Norma
	leidas, err := saij.LeerCatalogo(bytes.NewReader(crudo), func(n saij.Norma) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		provincias[n.ProvinciaID] = true
		normas = append(normas, n)
		return nil
	})
	if err != nil {
		return e, err
	}
	if leidas == 0 {
		return e, errors.New("el catálogo provincial vino vacío")
	}

	guardable, err := json.Marshal(normas)
	if err != nil {
		return e, err
	}
	if err := s.cache.Guardar(claveCatalogoSAIJ, guardable, almacen.SinVencimiento); err != nil {
		return e, err
	}

	e = EstadoSAIJ{
		Sincronizado:   true,
		Normas:         leidas,
		Provincias:     len(provincias),
		CatalogoAlDia:  info.Modificado,
		SincronizadoEn: time.Now().UTC(),
	}
	if crudoEstado, err := json.Marshal(e); err == nil {
		_ = s.cache.Guardar(claveEstadoSAIJ, crudoEstado, almacen.SinVencimiento)
	}

	// El índice que ya estaba en memoria queda viejo: se reemplaza con lo
	// recién leído, sin volver a parsear ni obligar a reiniciar.
	s.saijMu.Lock()
	if s.saijIndice == nil {
		s.saijIndice = saij.NuevoIndice()
	}
	s.saijIndice.Reemplazar(normas)
	s.saijCargado = e.SincronizadoEn
	s.saijMirado = time.Now()
	s.saijMu.Unlock()

	slog.Info("catálogo provincial sincronizado", "normas", leidas, "provincias", len(provincias))
	return e, nil
}
