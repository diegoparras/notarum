package servicio

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/infoleg"
)

// El Boletín publica la norma como salió ese día; InfoLEG la mantiene al día.
// Poder mostrar las dos cosas juntas es lo que ningún sitio oficial da en un
// solo lugar, y es todo el sentido de este enriquecimiento.

const (
	claveEstadoInfoLEG = "infoleg/_estado"
	// El texto de una norma no cambia: si cambia, es otra norma.
	ttlTextoNorma = almacen.SinVencimiento
)

// EstadoInfoLEG cuenta qué se sabe del catálogo local.
type EstadoInfoLEG struct {
	Sincronizado   bool      `json:"sincronizado"`
	Normas         int       `json:"normas"`
	ConTexto       int       `json:"con_texto"`
	UltimaFechaBO  string    `json:"ultima_fecha_boletin,omitempty"`
	CatalogoAlDia  time.Time `json:"catalogo_publicado,omitempty"`
	SincronizadoEn time.Time `json:"sincronizado_en,omitempty"`
	// Relaciones dice cuántas se guardaron de las bases complementarias: qué
	// modificó a cada norma y qué modifica cada una.
	Relaciones EstadoRelaciones `json:"relaciones,omitzero"`
	// Nuevas son las que aparecieron en esta sincronización y no estaban antes.
	Nuevas int `json:"nuevas,omitempty"`
}

// InfoLEGDisponible dice si esta instancia puede enriquecer avisos.
func (s *Servicio) InfoLEGDisponible() bool { return s.infoleg != nil }

// EstadoInfoLEG lee lo que quedó de la última sincronización.
func (s *Servicio) EstadoInfoLEG() EstadoInfoLEG {
	var e EstadoInfoLEG
	if crudo, ok := s.cache.Leer(claveEstadoInfoLEG); ok {
		_ = json.Unmarshal(crudo, &e)
	}
	return e
}

// NormaDelAviso busca en el catálogo la norma que el aviso nombra.
//
// Devuelve nil sin error cuando el aviso no nombra una norma —lo habitual en
// la segunda y la tercera sección— o cuando el catálogo todavía no la tiene.
// Que falte no es una falla: InfoLEG va unos días atrás del Boletín.
func (s *Servicio) NormaDelAviso(a boletin.Aviso) *infoleg.Norma {
	ref, ok := infoleg.ParsearNorma(a.Norma)
	if !ok {
		return nil
	}
	// El Boletín no siempre pone el año en la norma; el del aviso sirve.
	if ref.Anio == 0 {
		ref.Anio = a.Fecha.Year()
	}
	clave := ref.Clave()
	if clave == "" {
		return nil
	}
	crudo, hay := s.cache.Leer(clave)
	if !hay {
		return nil
	}
	// Bajo la clave de referencia vive sólo el id: la norma entera se guarda
	// una única vez, indexada por ese id.
	var id int
	if err := json.Unmarshal(crudo, &id); err != nil {
		return nil
	}
	return s.NormaGuardada(id)
}

// NormaGuardada trae del catálogo local la norma con ese id de InfoLEG.
func (s *Servicio) NormaGuardada(id int) *infoleg.Norma {
	if id <= 0 {
		return nil
	}
	crudo, hay := s.cache.Leer(claveNorma(id))
	if !hay {
		return nil
	}
	var n infoleg.Norma
	if err := json.Unmarshal(crudo, &n); err != nil {
		return nil
	}
	return &n
}

func claveNorma(id int) string { return "infoleg/norma/" + strconv.Itoa(id) }

// TextoDeNorma trae el texto de una norma de InfoLEG, de la caché si ya se
// bajó. Devuelve infoleg.ErrSinTexto cuando InfoLEG no lo publicó.
func (s *Servicio) TextoDeNorma(ctx context.Context, id int) (*infoleg.Texto, error) {
	if s.infoleg == nil {
		return nil, errors.New("esta instancia no tiene InfoLEG configurado")
	}
	clave := "infoleg/textos/" + strconv.Itoa(id)
	if crudo, ok := s.cache.Leer(clave); ok {
		if string(crudo) == marcaSinTexto {
			return nil, infoleg.ErrSinTexto
		}
		var t infoleg.Texto
		if err := json.Unmarshal(crudo, &t); err == nil {
			return &t, nil
		}
	}
	t, err := s.infoleg.TraerTexto(ctx, id)
	if err != nil {
		if errors.Is(err, infoleg.ErrSinTexto) {
			// Que no exista también se guarda: no tiene sentido volver a
			// preguntar por una norma que InfoLEG nunca publicó.
			_ = s.cache.Guardar(clave, []byte(marcaSinTexto), ttlTextoNorma)
		}
		return nil, err
	}
	if crudo, err := json.Marshal(t); err == nil {
		_ = s.cache.Guardar(clave, crudo, ttlTextoNorma)
	}
	return t, nil
}

const marcaSinTexto = `{"sin_texto":true}`

// SincronizarInfoLEG baja el catálogo y lo guarda. Son 428 mil normas y unos
// 50 MB comprimidos, así que se lee en streaming y se escribe de a una.
//
// El progreso se informa por el callback, que puede ser nil.
func (s *Servicio) SincronizarInfoLEG(ctx context.Context, dirTrabajo string, avisar func(guardadas int)) (EstadoInfoLEG, error) {
	var e EstadoInfoLEG
	if s.infoleg == nil {
		return e, errors.New("esta instancia no tiene InfoLEG configurado")
	}

	info, err := s.infoleg.BuscarCatalogo(ctx)
	if err != nil {
		return e, err
	}
	slog.Info("bajando el catálogo de InfoLEG", "url", info.URL,
		"publicado", info.Actualizado.Format("2006-01-02"))

	if dirTrabajo == "" {
		dirTrabajo = os.TempDir()
	}
	if err := os.MkdirAll(dirTrabajo, 0o755); err != nil {
		return e, err
	}
	rutaZip := filepath.Join(dirTrabajo, "infoleg-catalogo.zip")
	defer os.Remove(rutaZip)

	if err := s.infoleg.DescargarCatalogo(ctx, info.URL, rutaZip); err != nil {
		return e, err
	}
	// Con el buscador encendido se guarda el zip para poder rearmar el índice
	// al arrancar sin volver a bajar 50 MB. Si falla, la sincronización sigue:
	// el enriquecimiento de avisos no depende de esto.
	if err := s.guardarCatalogoInfoLEG(rutaZip); err != nil {
		slog.Warn("no se pudo guardar el catálogo para el buscador", "err", err)
	}
	lector, err := infoleg.AbrirCatalogo(rutaZip)
	if err != nil {
		return e, err
	}
	defer lector.Close()

	var guardadas int
	// Los identificadores de todo lo que trae el catálogo, para saber después
	// qué apareció que no estaba. Son unos cientos de miles de números: menos
	// memoria que una sola edición del Boletín.
	vistas := make([]int, 0, infoleg.NormasEsperadas)
	leidas, err := infoleg.LeerCatalogo(lector, func(n infoleg.Norma) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		clave := n.ClaveDe()
		if clave == "" {
			return nil // sin tipo o sin número no hay con qué cruzarla
		}
		crudo, err := json.Marshal(n)
		if err != nil {
			return nil
		}
		// La norma entera, indexada por su id de InfoLEG.
		if err := s.cache.Guardar(claveNorma(n.ID), crudo, almacen.SinVencimiento); err != nil {
			return err
		}
		// Y la referencia liviana con la que la encuentra un aviso.
		if err := s.cache.Guardar(clave, []byte(strconv.Itoa(n.ID)), almacen.SinVencimiento); err != nil {
			return err
		}
		guardadas++
		vistas = append(vistas, n.ID)
		if n.TieneTexto {
			e.ConTexto++
		}
		if n.FechaBoletin > e.UltimaFechaBO {
			e.UltimaFechaBO = n.FechaBoletin
		}
		if avisar != nil && guardadas%20000 == 0 {
			avisar(guardadas)
		}
		return nil
	})
	if err != nil {
		return e, err
	}

	// Qué apareció que no estaba, para que un programa pueda pedir sólo eso
	// en vez de bajar el catálogo entero todos los días.
	e.Nuevas = len(s.registrarNovedades("nacional", idsDeNormas(vistas)))

	// Las relaciones, después del catálogo: si algo falla acá, lo principal
	// ya está guardado.
	e.Relaciones = s.sincronizarRelaciones(ctx, info, dirTrabajo, func(que string) {
		slog.Info("relaciones de InfoLEG", "paso", que)
	})

	e.Sincronizado = true
	e.Normas = guardadas
	e.CatalogoAlDia = info.Actualizado
	e.SincronizadoEn = time.Now().UTC()
	if crudo, err := json.Marshal(e); err == nil {
		_ = s.cache.Guardar(claveEstadoInfoLEG, crudo, almacen.SinVencimiento)
	}
	slog.Info("catálogo de InfoLEG sincronizado",
		"leidas", leidas, "guardadas", guardadas, "con_texto", e.ConTexto,
		"ultima_fecha_boletin", e.UltimaFechaBO,
		"relaciones", e.Relaciones.Relaciones)
	return e, nil
}
