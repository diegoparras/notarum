package almacen

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diegoparras/notarum/internal/boletin"

	_ "github.com/jackc/pgx/v5/stdlib" // driver en Go puro: el binario sigue estático
)

// Postgres guarda lo mismo que los otros motores, pero en una base que puede
// vivir aparte del contenedor. Es la opción para quien no quiere depender de
// un archivo en un volumen: réplicas, backups gestionados y varias instancias
// leyendo la misma base.
//
// El índice de búsqueda usa las herramientas nativas de Postgres para
// castellano: to_tsvector('spanish', ...) hace stemming de verdad —"promúlgase"
// y "promulgar" caen en la misma raíz— y unaccent saca las tildes.
type Postgres struct {
	db      *sql.DB
	esquema string
	// sinUnaccent queda en true si la base no tiene la extensión y no se pudo
	// crear. Se sigue buscando, sólo que exigiendo los acentos.
	sinUnaccent bool

	aciertos atomic.Int64
	fallos   atomic.Int64
	escritos atomic.Int64
}

// OpcionesPostgres configura la conexión.
type OpcionesPostgres struct {
	// DSN es la cadena completa, del estilo
	// postgres://usuario:clave@host:5432/base?sslmode=require
	DSN string
	// Esquema donde viven las tablas; por defecto public.
	Esquema string
	// MaxConexiones limita el pool; 0 deja el valor de la biblioteca.
	MaxConexiones int
}

// NuevoPostgres abre la base y prepara las tablas.
func NuevoPostgres(o OpcionesPostgres) (*Postgres, error) {
	if strings.TrimSpace(o.DSN) == "" {
		return nil, errors.New("falta la cadena de conexión a Postgres")
	}
	if o.Esquema == "" {
		o.Esquema = "public"
	}
	if !esIdentificadorSeguro(o.Esquema) {
		return nil, fmt.Errorf("nombre de esquema inválido: %q", o.Esquema)
	}

	db, err := sql.Open("pgx", o.DSN)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir la conexión a Postgres: %w", err)
	}
	if o.MaxConexiones > 0 {
		db.SetMaxOpenConns(o.MaxConexiones)
	}
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("no se pudo conectar a Postgres: %w", err)
	}

	p := &Postgres{db: db, esquema: o.Esquema}
	if err := p.preparar(); err != nil {
		db.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) Cerrar() error { return p.db.Close() }

// esIdentificadorSeguro evita que un nombre de esquema se cuele como SQL: los
// identificadores no se pueden pasar por parámetro.
func esIdentificadorSeguro(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func (p *Postgres) preparar() error {
	if _, err := p.db.Exec(`CREATE SCHEMA IF NOT EXISTS ` + p.esquema); err != nil {
		return fmt.Errorf("no se pudo preparar el esquema %s: %w", p.esquema, err)
	}

	// unaccent es lo que permite encontrar "ENERGÍA" escribiendo "energia".
	// En una base gestionada puede faltar el permiso para crearla: si no se
	// puede, se sigue sin ella y se avisa, en vez de no arrancar.
	if _, err := p.db.Exec(`CREATE EXTENSION IF NOT EXISTS unaccent`); err != nil {
		p.sinUnaccent = true
		slog.Warn("Postgres sin la extensión unaccent: la búsqueda va a exigir los acentos",
			"err", err, "sugerencia", "pedile a quien administre la base que corra CREATE EXTENSION unaccent")
	}

	sentencias := []string{
		`CREATE TABLE IF NOT EXISTS ` + p.t("entradas") + ` (
			clave       TEXT PRIMARY KEY,
			datos       BYTEA NOT NULL,
			guardado_en BIGINT NOT NULL,
			vence_en    BIGINT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_entradas_vence ON ` + p.t("entradas") + ` (vence_en)`,

		`CREATE TABLE IF NOT EXISTS ` + p.t("avisos") + ` (
			seccion      TEXT NOT NULL,
			id           TEXT NOT NULL,
			fecha        DATE NOT NULL,
			rubro        TEXT NOT NULL DEFAULT '',
			organismo    TEXT NOT NULL DEFAULT '',
			norma        TEXT NOT NULL DEFAULT '',
			referencia   TEXT NOT NULL DEFAULT '',
			sintesis     TEXT NOT NULL DEFAULT '',
			tiene_anexos BOOLEAN NOT NULL DEFAULT false,
			repetido     BOOLEAN NOT NULL DEFAULT false,
			suplemento   BOOLEAN NOT NULL DEFAULT false,
			url          TEXT NOT NULL DEFAULT '',
			texto        TEXT NOT NULL DEFAULT '',
			busqueda     tsvector,
			PRIMARY KEY (seccion, id, fecha)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_avisos_fecha ON ` + p.t("avisos") + ` (seccion, fecha)`,
		`CREATE INDEX IF NOT EXISTS idx_avisos_rubro ON ` + p.t("avisos") + ` (upper(rubro) text_pattern_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_avisos_busqueda ON ` + p.t("avisos") + ` USING GIN (busqueda)`,
	}
	for _, s := range sentencias {
		if _, err := p.db.Exec(s); err != nil {
			return fmt.Errorf("no se pudo preparar la base: %w", err)
		}
	}
	return nil
}

// t califica un nombre de tabla con su esquema.
func (p *Postgres) t(tabla string) string { return p.esquema + "." + tabla }

// ------------------------------------------------------------------- almacén

func (p *Postgres) Leer(clave string) ([]byte, bool) {
	if strings.TrimSpace(clave) == "" {
		p.fallos.Add(1)
		return nil, false
	}
	var datos []byte
	var vence sql.NullInt64
	err := p.db.QueryRow(
		`SELECT datos, vence_en FROM `+p.t("entradas")+` WHERE clave = $1`, clave,
	).Scan(&datos, &vence)
	if err != nil {
		p.fallos.Add(1)
		return nil, false
	}
	if vence.Valid && time.Now().UnixMilli() > vence.Int64 {
		p.fallos.Add(1)
		return nil, false
	}
	p.aciertos.Add(1)
	return datos, true
}

func (p *Postgres) Guardar(clave string, datos []byte, ttl time.Duration) error {
	if strings.TrimSpace(clave) == "" {
		return errors.New("clave de almacén vacía")
	}
	var vence any
	if ttl > 0 {
		vence = time.Now().Add(ttl).UnixMilli()
	}
	_, err := p.db.Exec(`
		INSERT INTO `+p.t("entradas")+` (clave, datos, guardado_en, vence_en)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (clave) DO UPDATE SET
			datos = EXCLUDED.datos,
			guardado_en = EXCLUDED.guardado_en,
			vence_en = EXCLUDED.vence_en`,
		clave, datos, time.Now().UnixMilli(), vence)
	if err != nil {
		return fmt.Errorf("no se pudo guardar %q: %w", clave, err)
	}
	p.escritos.Add(1)
	return nil
}

func (p *Postgres) Existe(clave string) bool {
	var vence sql.NullInt64
	err := p.db.QueryRow(`SELECT vence_en FROM `+p.t("entradas")+` WHERE clave = $1`, clave).Scan(&vence)
	if err != nil {
		return false
	}
	return !vence.Valid || time.Now().UnixMilli() <= vence.Int64
}

func (p *Postgres) Borrar(clave string) error {
	_, err := p.db.Exec(`DELETE FROM `+p.t("entradas")+` WHERE clave = $1`, clave)
	return err
}

func (p *Postgres) Metricas() Metricas {
	m := Metricas{
		Motor:    "postgres",
		Aciertos: p.aciertos.Load(),
		Fallos:   p.fallos.Load(),
		Escritos: p.escritos.Load(),
	}
	_ = p.db.QueryRow(`SELECT count(*) FROM ` + p.t("entradas")).Scan(&m.Entradas)
	_ = p.db.QueryRow(`SELECT count(*) FROM ` + p.t("avisos")).Scan(&m.Avisos)
	return m
}

// -------------------------------------------------------------------- índice

// tsvectorDe indexa el texto por dos caminos a la vez: tal como está y sin
// acentos. Se calcula al escribir y no con una columna generada porque
// unaccent no es inmutable y Postgres no la acepta en una expresión
// almacenada.
//
// Las dos ramas hacen falta porque el stemmer de castellano necesita las
// palabras bien escritas: si se le saca la tilde antes, "designación" queda
// como "designacion" y ya no reduce a la misma raíz que "designaciones". Con
// la rama sin tocar, la forma correcta sigue stemizando bien; con la rama sin
// acentos, quien escribe "energia" encuentra "ENERGÍA". Las dos juntas cubren
// bastante más que cualquiera sola, y el vector casi no crece porque la
// mayoría de las palabras dan el mismo token por los dos caminos.
func (p *Postgres) tsvectorDe(expr string) string {
	sinTocar := `to_tsvector('spanish', ` + expr + `)`
	if p.sinUnaccent {
		return sinTocar
	}
	return sinTocar + ` || to_tsvector('spanish', unaccent(` + expr + `))`
}

// expresionInsercion arma el tsvector desde los parámetros del INSERT, donde
// todavía no hay fila de la cual leer columnas.
func (p *Postgres) expresionInsercion() string {
	return p.tsvectorDe(`coalesce($1,'') || ' ' || coalesce($2,'') || ' ' || coalesce($3,'') || ' ' ||
		coalesce($4,'') || ' ' || coalesce($5,'') || ' ' || coalesce($6,'')`)
}

// expresionActualizacion arma el tsvector de una fila que ya existe: toma sus
// columnas y le suma el texto nuevo, para no perder el sumario al indexar el
// cuerpo.
func (p *Postgres) expresionActualizacion(paramTexto int) string {
	return p.tsvectorDe(fmt.Sprintf(`coalesce(organismo,'') || ' ' || coalesce(norma,'') || ' ' ||
		coalesce(referencia,'') || ' ' || coalesce(sintesis,'') || ' ' || coalesce(rubro,'') || ' ' ||
		coalesce($%d,'')`, paramTexto))
}

// consultaTexto arma el tsquery de lo que escribió una persona, por los
// mismos dos caminos con que se indexó, unidos por OR.
//
// plainto_tsquery se encarga de la puntuación y de los operadores: lo que
// escriba quien busca es texto y nunca sintaxis, así que no hay nada que
// sanear a mano ni forma de inyectar.
func (p *Postgres) consultaTexto() string {
	if p.sinUnaccent {
		return `plainto_tsquery('spanish', $%d)`
	}
	return `(plainto_tsquery('spanish', $%[1]d) || plainto_tsquery('spanish', unaccent($%[1]d)))`
}

// IndexarEdicion vuelca los avisos de una edición. Es idempotente: reindexar
// el mismo día reemplaza lo que había y conserva el texto ya bajado.
func (p *Postgres) IndexarEdicion(ed *boletin.Edicion) error {
	if ed == nil {
		return nil
	}
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	sec, fecha := string(ed.Seccion), ed.Fecha.API()

	// El texto completo cuesta un pedido por aviso: si ya se bajó, se conserva.
	textos := map[string]string{}
	filas, err := tx.Query(
		`SELECT id, texto FROM `+p.t("avisos")+` WHERE seccion = $1 AND fecha = $2 AND texto <> ''`, sec, fecha)
	if err == nil {
		for filas.Next() {
			var id, txt string
			if filas.Scan(&id, &txt) == nil {
				textos[id] = txt
			}
		}
		filas.Close()
	}

	if _, err := tx.Exec(`DELETE FROM `+p.t("avisos")+` WHERE seccion = $1 AND fecha = $2`, sec, fecha); err != nil {
		return err
	}

	inserta := `
		INSERT INTO ` + p.t("avisos") + `
			(organismo, norma, referencia, sintesis, rubro, texto,
			 seccion, id, fecha, tiene_anexos, repetido, suplemento, url, busqueda)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, ` + p.expresionInsercion() + `)`

	for _, a := range ed.Avisos {
		if _, err := tx.Exec(inserta,
			a.Organismo, a.Norma, a.Referencia, a.Sintesis, a.Rubro, textos[a.ID],
			sec, a.ID, a.Fecha.API(), a.TieneAnexos, a.Repetido, a.Suplemento, a.URL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IndexarDetalle suma el texto completo de un aviso al índice, para que la
// búsqueda llegue al cuerpo y no sólo al sumario. Si el aviso todavía no está
// indexado no hace nada: primero va la edición.
func (p *Postgres) IndexarDetalle(d *boletin.Detalle) error {
	if d == nil || d.Texto == "" {
		return nil
	}
	_, err := p.db.Exec(`
		UPDATE `+p.t("avisos")+` SET
			texto = $1,
			busqueda = `+p.expresionActualizacion(1)+`
		WHERE seccion = $2 AND id = $3 AND fecha = $4`,
		d.Texto, string(d.Seccion), d.ID, d.Fecha.API())
	return err
}

func (p *Postgres) BuscarLocal(q ConsultaLocal) (*ResultadoLocal, error) {
	limite := q.Limite
	if limite <= 0 || limite > 500 {
		limite = 50
	}
	desplazamiento := q.Desplazamiento
	if desplazamiento < 0 {
		desplazamiento = 0
	}

	var condiciones []string
	var args []any
	agregar := func(cond string, valor any) {
		args = append(args, valor)
		condiciones = append(condiciones, fmt.Sprintf(cond, len(args)))
	}

	if t := strings.TrimSpace(q.Texto); t != "" {
		agregar(`busqueda @@ `+p.consultaTexto(), t)
	}
	if q.Seccion != "" {
		agregar(`seccion = $%d`, string(q.Seccion))
	}
	if q.Rubro != "" {
		agregar(`upper(rubro) LIKE $%d`, strings.ToUpper(q.Rubro)+"%")
	}
	if !q.Desde.IsZero() {
		agregar(`fecha >= $%d`, q.Desde.API())
	}
	if !q.Hasta.IsZero() {
		agregar(`fecha <= $%d`, q.Hasta.API())
	}
	where := ""
	if len(condiciones) > 0 {
		where = " WHERE " + strings.Join(condiciones, " AND ")
	}

	res := &ResultadoLocal{Limite: limite, Desplazamiento: desplazamiento, Avisos: []boletin.Aviso{}}
	if err := p.db.QueryRow(`SELECT count(*) FROM `+p.t("avisos")+where, args...).Scan(&res.Total); err != nil {
		return nil, err
	}

	args = append(args, limite, desplazamiento)
	filas, err := p.db.Query(fmt.Sprintf(`
		SELECT seccion, id, fecha, rubro, organismo, norma, referencia,
		       sintesis, tiene_anexos, repetido, suplemento, url
		FROM %s%s
		ORDER BY fecha DESC, seccion, id
		LIMIT $%d OFFSET $%d`, p.t("avisos"), where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer filas.Close()

	for filas.Next() {
		var (
			a     boletin.Aviso
			sec   string
			fecha time.Time
		)
		if err := filas.Scan(&sec, &a.ID, &fecha, &a.Rubro, &a.Organismo, &a.Norma,
			&a.Referencia, &a.Sintesis, &a.TieneAnexos, &a.Repetido, &a.Suplemento, &a.URL); err != nil {
			return nil, err
		}
		a.Seccion = boletin.Seccion(sec)
		if f, err := boletin.ParseFecha(fecha.Format("2006-01-02")); err == nil {
			a.Fecha = f
		}
		res.Avisos = append(res.Avisos, a)
	}
	return res, filas.Err()
}

func (p *Postgres) Cobertura(sec boletin.Seccion, desde, hasta boletin.Fecha) (int, error) {
	var n int
	err := p.db.QueryRow(
		`SELECT count(DISTINCT fecha) FROM `+p.t("avisos")+`
		 WHERE seccion = $1 AND fecha >= $2 AND fecha <= $3`,
		string(sec), desde.API(), hasta.API()).Scan(&n)
	return n, err
}
