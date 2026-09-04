package almacen

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/diegoparras/notarum/internal/boletin"

	_ "modernc.org/sqlite" // driver en Go puro: el binario sigue estático
)

// SQLite guarda todo en un archivo y, además, indexa los avisos para poder
// buscarlos sin pedirle nada al Boletín.
type SQLite struct {
	db   *sql.DB
	ruta string

	aciertos atomic.Int64
	fallos   atomic.Int64
	escritos atomic.Int64
}

const esquema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS entradas (
  clave       TEXT PRIMARY KEY,
  datos       BLOB NOT NULL,
  guardado_en INTEGER NOT NULL,
  vence_en    INTEGER
);
CREATE INDEX IF NOT EXISTS idx_entradas_vence ON entradas(vence_en);

CREATE TABLE IF NOT EXISTS avisos (
  seccion      TEXT NOT NULL,
  id           TEXT NOT NULL,
  fecha        TEXT NOT NULL,
  rubro        TEXT NOT NULL,
  organismo    TEXT NOT NULL,
  norma        TEXT NOT NULL,
  referencia   TEXT NOT NULL,
  sintesis     TEXT NOT NULL,
  tiene_anexos INTEGER NOT NULL,
  repetido     INTEGER NOT NULL,
  suplemento   INTEGER NOT NULL,
  url          TEXT NOT NULL,
  texto        TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (seccion, id, fecha)
);
CREATE INDEX IF NOT EXISTS idx_avisos_fecha ON avisos(seccion, fecha);
CREATE INDEX IF NOT EXISTS idx_avisos_rubro ON avisos(rubro);

-- El índice de texto va aparte, con la fila de avisos como referencia.
-- remove_diacritics 2 es lo que hace que "promulgase" encuentre "Promúlgase".
CREATE VIRTUAL TABLE IF NOT EXISTS avisos_fts USING fts5(
  organismo, norma, referencia, sintesis, rubro, texto,
  content='',
  tokenize="unicode61 remove_diacritics 2"
);
CREATE TABLE IF NOT EXISTS fts_filas (
  rowid   INTEGER PRIMARY KEY,
  seccion TEXT NOT NULL,
  id      TEXT NOT NULL,
  fecha   TEXT NOT NULL,
  UNIQUE (seccion, id, fecha)
);
`

// NuevoSQLite abre (o crea) la base en la ruta indicada.
func NuevoSQLite(ruta string) (*SQLite, error) {
	if strings.TrimSpace(ruta) == "" {
		return nil, errors.New("la base necesita una ruta de archivo")
	}
	if dir := filepath.Dir(ruta); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("no se pudo crear el directorio de la base %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", ruta+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir la base %s: %w", ruta, err)
	}
	// SQLite escribe mejor con una sola conexión de escritura.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(esquema); err != nil {
		db.Close()
		return nil, fmt.Errorf("no se pudo preparar la base %s: %w", ruta, err)
	}
	return &SQLite{db: db, ruta: ruta}, nil
}

func (s *SQLite) Cerrar() error { return s.db.Close() }

// ------------------------------------------------------------------- almacén

func (s *SQLite) Leer(clave string) ([]byte, bool) {
	var datos []byte
	var vence sql.NullInt64
	err := s.db.QueryRow(
		`SELECT datos, vence_en FROM entradas WHERE clave = ?`, clave,
	).Scan(&datos, &vence)
	if err != nil {
		s.fallos.Add(1)
		return nil, false
	}
	if vence.Valid && time.Now().UnixMilli() > vence.Int64 {
		s.fallos.Add(1)
		return nil, false
	}
	s.aciertos.Add(1)
	return datos, true
}

func (s *SQLite) Guardar(clave string, datos []byte, ttl time.Duration) error {
	if strings.TrimSpace(clave) == "" {
		return errors.New("clave de almacén vacía")
	}
	var vence any
	if ttl > 0 {
		vence = time.Now().Add(ttl).UnixMilli()
	}
	_, err := s.db.Exec(`
		INSERT INTO entradas (clave, datos, guardado_en, vence_en) VALUES (?, ?, ?, ?)
		ON CONFLICT(clave) DO UPDATE SET datos = excluded.datos,
			guardado_en = excluded.guardado_en, vence_en = excluded.vence_en`,
		clave, datos, time.Now().UnixMilli(), vence)
	if err != nil {
		return fmt.Errorf("no se pudo guardar %q: %w", clave, err)
	}
	s.escritos.Add(1)
	return nil
}

func (s *SQLite) Existe(clave string) bool {
	var vence sql.NullInt64
	err := s.db.QueryRow(`SELECT vence_en FROM entradas WHERE clave = ?`, clave).Scan(&vence)
	if err != nil {
		return false
	}
	return !vence.Valid || time.Now().UnixMilli() <= vence.Int64
}

func (s *SQLite) Borrar(clave string) error {
	_, err := s.db.Exec(`DELETE FROM entradas WHERE clave = ?`, clave)
	return err
}

func (s *SQLite) Metricas() Metricas {
	m := Metricas{
		Motor:    "sqlite",
		Aciertos: s.aciertos.Load(),
		Fallos:   s.fallos.Load(),
		Escritos: s.escritos.Load(),
	}
	_ = s.db.QueryRow(`SELECT count(*) FROM entradas`).Scan(&m.Entradas)
	_ = s.db.QueryRow(`SELECT count(*) FROM avisos`).Scan(&m.Avisos)
	return m
}

// -------------------------------------------------------------------- índice

// IndexarEdicion vuelca los avisos de una edición al índice. Es idempotente:
// reindexar el mismo día reemplaza lo que había, no lo duplica.
func (s *SQLite) IndexarEdicion(ed *boletin.Edicion) error {
	if ed == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	fecha, sec := ed.Fecha.API(), string(ed.Seccion)

	// El texto completo cuesta un pedido por aviso: si ya se bajó, se conserva
	// al reindexar el día.
	textos := map[string]string{}
	if filasTxt, err := tx.Query(
		`SELECT id, texto FROM avisos WHERE seccion = ? AND fecha = ? AND texto <> ''`, sec, fecha); err == nil {
		for filasTxt.Next() {
			var id, txt string
			if filasTxt.Scan(&id, &txt) == nil {
				textos[id] = txt
			}
		}
		filasTxt.Close()
	}

	// Sacar primero lo de ese día, para que un aviso retirado no quede colgado.
	filas, err := tx.Query(`SELECT rowid FROM fts_filas WHERE seccion = ? AND fecha = ?`, sec, fecha)
	if err != nil {
		return err
	}
	var viejos []int64
	for filas.Next() {
		var id int64
		if err := filas.Scan(&id); err != nil {
			filas.Close()
			return err
		}
		viejos = append(viejos, id)
	}
	filas.Close()
	for _, rowid := range viejos {
		// En una tabla FTS5 externa, borrar es insertar la fila con 'delete'.
		if _, err := tx.Exec(
			`INSERT INTO avisos_fts(avisos_fts, rowid, organismo, norma, referencia, sintesis, rubro, texto)
			 VALUES ('delete', ?, '', '', '', '', '', '')`, rowid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM fts_filas WHERE seccion = ? AND fecha = ?`, sec, fecha); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM avisos WHERE seccion = ? AND fecha = ?`, sec, fecha); err != nil {
		return err
	}

	insAviso, err := tx.Prepare(`
		INSERT INTO avisos (seccion, id, fecha, rubro, organismo, norma, referencia,
		                    sintesis, tiene_anexos, repetido, suplemento, url, texto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insAviso.Close()

	for _, a := range ed.Avisos {
		if _, err := insAviso.Exec(sec, a.ID, a.Fecha.API(), a.Rubro, a.Organismo,
			a.Norma, a.Referencia, a.Sintesis, a.TieneAnexos, a.Repetido, a.Suplemento,
			a.URL, textos[a.ID]); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO fts_filas (seccion, id, fecha) VALUES (?, ?, ?)`,
			sec, a.ID, a.Fecha.API())
		if err != nil {
			return err
		}
		rowid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO avisos_fts (rowid, organismo, norma, referencia, sintesis, rubro, texto)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			rowid, a.Organismo, a.Norma, a.Referencia, a.Sintesis, a.Rubro, textos[a.ID]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IndexarDetalle suma el texto completo de un aviso al índice, para que la
// búsqueda local llegue tan lejos como la del sitio y no sólo al sumario.
// Si el aviso todavía no está indexado, no hace nada: primero va la edición.
func (s *SQLite) IndexarDetalle(d *boletin.Detalle) error {
	if d == nil || d.Texto == "" {
		return nil
	}
	sec, fecha := string(d.Seccion), d.Fecha.API()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var rowid int64
	err = tx.QueryRow(`SELECT rowid FROM fts_filas WHERE seccion = ? AND id = ? AND fecha = ?`,
		sec, d.ID, fecha).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE avisos SET texto = ? WHERE seccion = ? AND id = ? AND fecha = ?`,
		d.Texto, sec, d.ID, fecha); err != nil {
		return err
	}
	// En FTS5 externa una fila se actualiza borrándola y volviéndola a poner.
	var org, norma, ref, sin, rubro string
	if err := tx.QueryRow(
		`SELECT organismo, norma, referencia, sintesis, rubro FROM avisos
		 WHERE seccion = ? AND id = ? AND fecha = ?`, sec, d.ID, fecha,
	).Scan(&org, &norma, &ref, &sin, &rubro); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO avisos_fts(avisos_fts, rowid, organismo, norma, referencia, sintesis, rubro, texto)
		 VALUES ('delete', ?, '', '', '', '', '', '')`, rowid); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO avisos_fts (rowid, organismo, norma, referencia, sintesis, rubro, texto)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rowid, org, norma, ref, sin, rubro, d.Texto); err != nil {
		return err
	}
	return tx.Commit()
}

// BuscarLocal busca sobre el índice. Sin texto es un listado filtrado; con
// texto, una búsqueda de palabras completas o por prefijo.
func (s *SQLite) BuscarLocal(q ConsultaLocal) (*ResultadoLocal, error) {
	limite := q.Limite
	if limite <= 0 || limite > 500 {
		limite = 50
	}
	desplazamiento := q.Desplazamiento
	if desplazamiento < 0 {
		desplazamiento = 0
	}

	var (
		desde, hasta string
		condiciones  []string
		args         []any
	)
	if !q.Desde.IsZero() {
		desde = q.Desde.API()
	}
	if !q.Hasta.IsZero() {
		hasta = q.Hasta.API()
	}

	base := `FROM avisos a`
	consulta := prepararFTS(q.Texto)
	if consulta != "" {
		base += `
		 JOIN fts_filas f ON f.seccion = a.seccion AND f.id = a.id AND f.fecha = a.fecha
		 JOIN avisos_fts x ON x.rowid = f.rowid`
		condiciones = append(condiciones, `avisos_fts MATCH ?`)
		args = append(args, consulta)
	}
	if q.Seccion != "" {
		condiciones = append(condiciones, `a.seccion = ?`)
		args = append(args, string(q.Seccion))
	}
	if q.Rubro != "" {
		condiciones = append(condiciones, `upper(a.rubro) LIKE ?`)
		args = append(args, strings.ToUpper(q.Rubro)+"%")
	}
	if desde != "" {
		condiciones = append(condiciones, `a.fecha >= ?`)
		args = append(args, desde)
	}
	if hasta != "" {
		condiciones = append(condiciones, `a.fecha <= ?`)
		args = append(args, hasta)
	}
	where := ""
	if len(condiciones) > 0 {
		where = " WHERE " + strings.Join(condiciones, " AND ")
	}

	res := &ResultadoLocal{Limite: limite, Desplazamiento: desplazamiento, Avisos: []boletin.Aviso{}}
	if err := s.db.QueryRow(`SELECT count(*) `+base+where, args...).Scan(&res.Total); err != nil {
		// Una consulta FTS que el motor rechaza no es una falla del servicio:
		// es que el texto no formaba una búsqueda. Se devuelve vacío.
		if esErrorDeConsultaFTS(err) {
			return res, nil
		}
		return nil, err
	}

	filas, err := s.db.Query(`
		SELECT a.seccion, a.id, a.fecha, a.rubro, a.organismo, a.norma, a.referencia,
		       a.sintesis, a.tiene_anexos, a.repetido, a.suplemento, a.url `+
		base+where+` ORDER BY a.fecha DESC, a.seccion, a.id LIMIT ? OFFSET ?`,
		append(args, limite, desplazamiento)...)
	if err != nil {
		if esErrorDeConsultaFTS(err) {
			res.Total = 0
			return res, nil
		}
		return nil, err
	}
	defer filas.Close()

	for filas.Next() {
		var (
			a     boletin.Aviso
			sec   string
			fecha string
		)
		if err := filas.Scan(&sec, &a.ID, &fecha, &a.Rubro, &a.Organismo, &a.Norma,
			&a.Referencia, &a.Sintesis, &a.TieneAnexos, &a.Repetido, &a.Suplemento, &a.URL); err != nil {
			return nil, err
		}
		a.Seccion = boletin.Seccion(sec)
		if f, err := boletin.ParseFecha(fecha); err == nil {
			a.Fecha = f
		}
		res.Avisos = append(res.Avisos, a)
	}
	return res, filas.Err()
}

// Cobertura cuenta los días distintos indexados de una sección en un rango.
func (s *SQLite) Cobertura(sec boletin.Seccion, desde, hasta boletin.Fecha) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT count(DISTINCT fecha) FROM avisos WHERE seccion = ? AND fecha >= ? AND fecha <= ?`,
		string(sec), desde.API(), hasta.API()).Scan(&n)
	return n, err
}

// prepararFTS convierte lo que escribió una persona en una consulta FTS5
// segura. FTS5 tiene su propia sintaxis (comillas, AND/OR/NOT, NEAR, *) y un
// texto cualquiera la rompe: acá cada palabra se entrecomilla, con lo que se
// busca literalmente, y se exigen todas.
func prepararFTS(texto string) string {
	campos := strings.FieldsFunc(texto, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	})
	var palabras []string
	for _, c := range campos {
		c = strings.Trim(c, "-")
		if c == "" {
			continue
		}
		palabras = append(palabras, `"`+c+`"`)
	}
	if len(palabras) == 0 {
		return ""
	}
	return strings.Join(palabras, " AND ")
}

func esErrorDeConsultaFTS(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "fts5") || strings.Contains(m, "malformed match")
}
