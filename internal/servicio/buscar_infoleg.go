package servicio

import (
	"log/slog"
	"os"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/infoleg"
)

// El buscador de normativa nacional.
//
// InfoLEG entraba sólo como enriquecimiento: al abrir un aviso del Boletín se
// veía al lado la norma actualizada. Pero las 428 mil normas no se podían
// buscar, mientras que las 81 mil provinciales sí. Esto lo empareja.
//
// Va aparte y apagado por defecto porque cuesta: se midió con el catálogo
// real y son 357 MB en memoria. Quien lo quiera lo enciende; quien no, no
// paga nada.

// claveCatalogoInfoLEG guarda el catálogo comprimido, tal como se bajó.
//
// Se guarda el zip y no el índice armado: son 50 MB contra los cientos que
// ocuparía serializar lo que está en memoria, y rearmarlo al arrancar lleva
// tres segundos.
const claveCatalogoInfoLEG = "infoleg/_catalogo.zip"

// ConBuscadorInfoLEG enciende el buscador de normativa nacional.
func (s *Servicio) ConBuscadorInfoLEG(activo bool) *Servicio {
	s.buscadorInfoLEG = activo
	return s
}

// BuscadorInfoLEGActivo dice si esta instancia puede buscar normativa
// nacional.
func (s *Servicio) BuscadorInfoLEGActivo() bool { return s.buscadorInfoLEG }

// indiceInfoLEG arma el índice la primera vez que hace falta.
func (s *Servicio) indiceInfoLEG() *infoleg.Indice {
	if !s.buscadorInfoLEG {
		return infoleg.NuevoIndice() // vacío: nadie lo pidió
	}
	s.infoUnaVez.Do(func() {
		s.infoIndice = infoleg.NuevoIndice()
		crudo, hay := s.cache.Leer(claveCatalogoInfoLEG)
		if !hay {
			return
		}
		empezo := time.Now()
		i, err := armarIndiceDesdeZip(crudo)
		if err != nil {
			slog.Error("no se pudo armar el buscador de InfoLEG", "err", err)
			return
		}
		s.infoIndice = i
		slog.Info("buscador de InfoLEG listo",
			"normas", i.Normas(), "tardo", time.Since(empezo).Round(time.Millisecond))
	})
	return s.infoIndice
}

// armarIndiceDesdeZip descomprime el catálogo guardado y arma el índice.
func armarIndiceDesdeZip(crudo []byte) (*infoleg.Indice, error) {
	// AbrirCatalogo trabaja sobre un archivo, así que el zip vuelve a disco
	// un momento: son 50 MB y evita tener el descomprimido entero en memoria
	// además del índice.
	tmp, err := os.CreateTemp("", "notarum-infoleg-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(crudo); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	lector, err := infoleg.AbrirCatalogo(tmp.Name())
	if err != nil {
		return nil, err
	}
	defer lector.Close()

	i := infoleg.NuevoIndiceCon(infoleg.NormasEsperadas)
	internar := infoleg.Internador()
	if _, err := infoleg.LeerCatalogo(lector, func(n infoleg.Norma) error {
		i.Agregar(n, internar)
		return nil
	}); err != nil {
		return nil, err
	}
	i.Cerrar()
	return i, nil
}

// BuscarNacional busca en la normativa nacional de InfoLEG.
func (s *Servicio) BuscarNacional(q infoleg.Consulta) *infoleg.Resultado {
	return s.indiceInfoLEG().Buscar(q)
}

// TiposNacionales son los tipos de norma que trae el catálogo.
func (s *Servicio) TiposNacionales() []infoleg.ConteoTipo {
	return s.indiceInfoLEG().Tipos()
}

// CatalogoNacionalCargado dice si hay con qué buscar.
func (s *Servicio) CatalogoNacionalCargado() bool { return s.indiceInfoLEG().Cargado() }

// guardarCatalogoInfoLEG deja el zip para poder rearmar el índice al
// arrancar, sin volver a bajar 50 MB.
func (s *Servicio) guardarCatalogoInfoLEG(ruta string) error {
	if !s.buscadorInfoLEG {
		return nil // sin buscador no hay para qué guardarlo
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		return err
	}
	if err := s.cache.Guardar(claveCatalogoInfoLEG, crudo, almacen.SinVencimiento); err != nil {
		return err
	}
	// Y se arma con lo recién bajado, para que la búsqueda ande sin reiniciar.
	i, err := armarIndiceDesdeZip(crudo)
	if err != nil {
		return err
	}
	s.infoUnaVez.Do(func() {})
	s.infoMu.Lock()
	s.infoIndice = i
	s.infoMu.Unlock()
	return nil
}
