package almacen

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// Recorrer todo lo guardado.
//
// El almacén sirve para guardar y recuperar por clave, que es todo lo que hace
// falta para atender pedidos. Recorrerlo entero hace falta para una sola cosa:
// pasarlo a otro motor. Por eso es una capacidad aparte y no parte del
// contrato: un almacén que no la tenga sirve igual para todo lo demás.

// Recorrible lo cumple el almacén que sabe listar lo que tiene.
type Recorrible interface {
	// Recorrer llama a hacer con cada entrada. Cortar devolviendo un error.
	Recorrer(hacer func(clave string, datos []byte) error) error
}

// ------------------------------------------------------------------- disco

func (d *Disco) Recorrer(hacer func(clave string, datos []byte) error) error {
	return filepath.WalkDir(d.raiz, func(ruta string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			return nil
		}
		rel, err := filepath.Rel(d.raiz, ruta)
		if err != nil {
			return nil
		}
		clave := strings.TrimSuffix(filepath.ToSlash(rel), ".json")
		// Se lee por la puerta de siempre: así lo vencido queda afuera y el
		// sobre se abre igual que cuando lo pide un pedido de verdad.
		datos, hay := d.Leer(clave)
		if !hay {
			return nil
		}
		return hacer(clave, datos)
	})
}

// ------------------------------------------------------------------ sqlite

func (s *SQLite) Recorrer(hacer func(clave string, datos []byte) error) error {
	// Ordenado por clave para que dos corridas den lo mismo, y sin traer todo
	// a memoria: son cientos de miles de filas.
	filas, err := s.db.Query(`SELECT clave, datos FROM entradas
		WHERE vence_en IS NULL OR vence_en > ? ORDER BY clave`, ahoraMilis())
	if err != nil {
		return err
	}
	defer filas.Close()
	for filas.Next() {
		var clave string
		var datos []byte
		if err := filas.Scan(&clave, &datos); err != nil {
			return err
		}
		if err := hacer(clave, datos); err != nil {
			return err
		}
	}
	return filas.Err()
}

// ---------------------------------------------------------------- postgres

func (p *Postgres) Recorrer(hacer func(clave string, datos []byte) error) error {
	filas, err := p.db.Query(`SELECT clave, datos FROM `+p.t("entradas")+`
		WHERE vence_en IS NULL OR vence_en > $1 ORDER BY clave`, ahoraMilis())
	if err != nil {
		return err
	}
	defer filas.Close()
	for filas.Next() {
		var clave string
		var datos []byte
		if err := filas.Scan(&clave, &datos); err != nil {
			return err
		}
		if err := hacer(clave, datos); err != nil {
			return err
		}
	}
	return filas.Err()
}

// ahoraMilis es el instante con el que se comparan los vencimientos, en la
// misma unidad en que se guardan.
func ahoraMilis() int64 { return time.Now().UnixMilli() }
