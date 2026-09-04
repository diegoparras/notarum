// notarum sirve el Boletín Oficial de la República Argentina como JSON.
//
//	notarum servir
//	notarum rellenar --seccion primera --desde 2024-01-01 [--hasta 2024-12-31]
//	notarum version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
	"github.com/diegoparras/notarum/internal/api"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/mcp"
	"github.com/diegoparras/notarum/internal/servicio"
)

// version se puede fijar en el build: -ldflags "-X main.version=1.2.3".
var version = "1.0.0"

func main() {
	if err := ejecutar(os.Args[1:]); err != nil {
		slog.Error("notarum terminó con error", "err", err)
		os.Exit(1)
	}
}

func ejecutar(args []string) error {
	comando := "servir"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		comando, args = args[0], args[1:]
	}
	switch comando {
	case "servir", "serve":
		return servir(args)
	case "rellenar":
		return rellenar(args)
	case "mcp":
		return servirMCP(args)
	case "version", "--version", "-v":
		fmt.Println("notarum " + version)
		return nil
	case "ayuda", "help", "--help", "-h":
		ayuda()
		return nil
	default:
		ayuda()
		return fmt.Errorf("comando desconocido: %s", comando)
	}
}

func ayuda() {
	fmt.Print(`notarum ` + version + ` — API abierta del Boletín Oficial de la República Argentina

  notarum servir
      Levanta la API. Configuración por variables de entorno o banderas:
        NOTARUM_PUERTO      (--puerto)      puerto HTTP                 [8080]
        NOTARUM_ALMACEN     (--almacen)     disco | sqlite | postgres   [disco]
        NOTARUM_CACHE       (--cache)       directorio (motor disco)    [/datos/cache]
        NOTARUM_DB          (--db)          archivo (motor sqlite)      [/datos/notarum.db]

      Con el motor postgres, la conexión sale de NOTARUM_POSTGRES_DSN, o de
      las piezas sueltas NOTARUM_POSTGRES_HOST, _PUERTO, _BASE, _USUARIO,
      _CLAVE, _SSL y _ESQUEMA.
        NOTARUM_POR_MINUTO  (--por-minuto)  pedidos por minuto por IP   [60]
        NOTARUM_INTERVALO   (--intervalo)   espera entre pedidos al sitio [500ms]
        NOTARUM_USER_AGENT  (--user-agent)  User-Agent hacia el sitio
        NOTARUM_LOG         (--log)         text | json                 [json]
        NOTARUM_MCP_TOKEN   (--mcp-token)   exige Bearer en /mcp        [abierto]
        NOTARUM_SIN_MCP     (--sin-mcp)     apaga /mcp                  [activo]
        NOTARUM_SIN_WEB     (--sin-web)     apaga el lector web         [activo]

  notarum rellenar --seccion primera --desde 2024-01-01 [--hasta 2024-12-31]
      Recorre el calendario y baja lo que falte, al mismo ritmo. Es lo que
      hace que la API conteste rápido después. Se puede cortar y retomar.
      Con el motor sqlite, además indexa los avisos para buscarlos sin
      pedirle nada al Boletín.

  notarum mcp
      Habla MCP por entrada y salida estándar, para un cliente local como
      Claude Desktop o Claude Code. La instancia levantada con "servir" ya
      expone lo mismo por HTTP en /mcp.

  notarum version
`)
}

// ---------------------------------------------------------------- configurar

func entorno(clave, porDefecto string) string {
	if v := strings.TrimSpace(os.Getenv(clave)); v != "" {
		return v
	}
	return porDefecto
}

func armarLog(formato string) {
	nivel := slog.LevelInfo
	if strings.EqualFold(entorno("NOTARUM_NIVEL_LOG", ""), "debug") {
		nivel = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: nivel}
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if formato == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

// armarAlmacen elige el motor de guardado. El de disco alcanza para servir por
// fecha; el de SQLite además indexa los avisos y permite buscar sin pedirle
// nada al Boletín.
func armarAlmacen(motor, dirCache, rutaDB string) (almacen.Almacen, error) {
	switch strings.ToLower(strings.TrimSpace(motor)) {
	case "", "disco", "archivos":
		return almacen.NuevoDisco(dirCache)
	case "sqlite", "db", "base":
		return almacen.NuevoSQLite(rutaDB)
	case "postgres", "postgresql", "postgresdb":
		return armarPostgres()
	default:
		return nil, fmt.Errorf("almacén %q desconocido: se esperaba disco, sqlite o postgres", motor)
	}
}

// armarPostgres toma la cadena de conexión entera si la hay, y si no la
// arma con las piezas sueltas, que es como se configura en un panel.
func armarPostgres() (almacen.Almacen, error) {
	dsn := entorno("NOTARUM_POSTGRES_DSN", "")
	if dsn == "" {
		var err error
		dsn, err = almacen.ArmarDSN(almacen.DatosConexion{
			Host:    entorno("NOTARUM_POSTGRES_HOST", ""),
			Puerto:  entorno("NOTARUM_POSTGRES_PUERTO", "5432"),
			Base:    entorno("NOTARUM_POSTGRES_BASE", "notarum"),
			Usuario: entorno("NOTARUM_POSTGRES_USUARIO", ""),
			Clave:   entorno("NOTARUM_POSTGRES_CLAVE", ""),
			SSLMode: entorno("NOTARUM_POSTGRES_SSL", ""),
		})
		if err != nil {
			return nil, fmt.Errorf("%w: definí NOTARUM_POSTGRES_DSN o las piezas NOTARUM_POSTGRES_HOST/BASE/USUARIO/CLAVE", err)
		}
	}
	slog.Info("conectando a Postgres", "dsn", almacen.OcultarClave(dsn))
	return almacen.NuevoPostgres(almacen.OpcionesPostgres{
		DSN:     dsn,
		Esquema: entorno("NOTARUM_POSTGRES_ESQUEMA", "public"),
	})
}

// armarServicio crea el cliente y el almacén compartidos por ambos comandos.
func armarServicio(cfg configComun) (*servicio.Servicio, func(), error) {
	alm, err := armarAlmacen(cfg.motor, cfg.dirCache, cfg.rutaDB)
	if err != nil {
		return nil, nil, err
	}
	cli := boletin.NuevoCliente(boletin.Opciones{
		UserAgent: cfg.userAgent,
		Intervalo: cfg.intervalo,
	})
	return servicio.Nuevo(cli, alm), func() { _ = alm.Cerrar() }, nil
}

// configComun es lo que ambos comandos necesitan para armar el servicio.
type configComun struct {
	motor     string
	dirCache  string
	rutaDB    string
	userAgent string
	intervalo time.Duration
}

const uaPorDefecto = "notarum/" + "1.0" + " (+https://github.com/diegoparras/notarum)"

// ------------------------------------------------------------------- servir

func servir(args []string) error {
	fs := flag.NewFlagSet("servir", flag.ContinueOnError)
	puerto := fs.String("puerto", entorno("NOTARUM_PUERTO", "8080"), "puerto HTTP")
	dirCache := fs.String("cache", entorno("NOTARUM_CACHE", "/datos/cache"), "directorio de caché (motor disco)")
	motor := fs.String("almacen", entorno("NOTARUM_ALMACEN", "disco"), "dónde guardar: disco, sqlite o postgres")
	rutaDB := fs.String("db", entorno("NOTARUM_DB", "/datos/notarum.db"), "archivo de la base (motor sqlite)")
	porMinuto := fs.String("por-minuto", entorno("NOTARUM_POR_MINUTO", "60"), "pedidos por minuto por IP (0 desactiva)")
	intervalo := fs.String("intervalo", entorno("NOTARUM_INTERVALO", "500ms"), "espera entre pedidos al sitio")
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto), "User-Agent hacia el sitio")
	formatoLog := fs.String("log", entorno("NOTARUM_LOG", "json"), "formato de log: text o json")
	tokenMCP := fs.String("mcp-token", entorno("NOTARUM_MCP_TOKEN", ""), "si se pone, /mcp exige Bearer con este token")
	sinMCP := fs.Bool("sin-mcp", entorno("NOTARUM_SIN_MCP", "") != "", "apagar el endpoint /mcp")
	sinWeb := fs.Bool("sin-web", entorno("NOTARUM_SIN_WEB", "") != "", "apagar el lector web y dejar sólo la API")
	if err := fs.Parse(args); err != nil {
		return err
	}
	armarLog(*formatoLog)

	esperaSitio, err := time.ParseDuration(*intervalo)
	if err != nil {
		return fmt.Errorf("intervalo inválido %q: %w", *intervalo, err)
	}
	limite, err := strconv.Atoi(*porMinuto)
	if err != nil {
		return fmt.Errorf("por-minuto inválido %q: %w", *porMinuto, err)
	}

	srv, cerrar, err := armarServicio(configComun{
		motor: *motor, dirCache: *dirCache, rutaDB: *rutaDB,
		userAgent: *userAgent, intervalo: esperaSitio,
	})
	if err != nil {
		return err
	}
	defer cerrar()
	manejador := api.Nuevo(api.Config{
		Servicio: srv, PorMinuto: limite, Version: version,
		TokenMCP: *tokenMCP, SinMCP: *sinMCP, SinWeb: *sinWeb,
	})

	http := &http.Server{
		Addr:              ":" + *puerto,
		Handler:           manejador,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second, // leer del sitio puede tardar
		IdleTimeout:       120 * time.Second,
	}

	ctx, cancelar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelar()

	errores := make(chan error, 1)
	go func() {
		slog.Info("notarum escuchando",
			"puerto", *puerto, "almacen", srv.Almacen().Metricas().Motor,
			"indice_local", srv.TieneIndice(), "por_minuto", limite,
			"mcp", !*sinMCP, "lector", !*sinWeb, "version", version)
		errores <- http.ListenAndServe()
	}()

	select {
	case err := <-errores:
		if err != nil && !errors.Is(err, os.ErrClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		slog.Info("apagando")
		apagado, cancelarApagado := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelarApagado()
		return http.Shutdown(apagado)
	}
}

// ---------------------------------------------------------------------- mcp

// servirMCP habla MCP por entrada y salida estándar. El log va a stderr: la
// salida estándar es del protocolo y cualquier otra cosa ahí lo rompe.
func servirMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	dirCache := fs.String("cache", entorno("NOTARUM_CACHE", "/datos/cache"), "directorio de caché (motor disco)")
	motor := fs.String("almacen", entorno("NOTARUM_ALMACEN", "disco"), "dónde guardar: disco, sqlite o postgres")
	rutaDB := fs.String("db", entorno("NOTARUM_DB", "/datos/notarum.db"), "archivo de la base (motor sqlite)")
	intervalo := fs.String("intervalo", entorno("NOTARUM_INTERVALO", "500ms"), "espera entre pedidos al sitio")
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto), "User-Agent hacia el sitio")
	if err := fs.Parse(args); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	esperaSitio, err := time.ParseDuration(*intervalo)
	if err != nil {
		return fmt.Errorf("intervalo inválido %q: %w", *intervalo, err)
	}
	srv, cerrar, err := armarServicio(configComun{
		motor: *motor, dirCache: *dirCache, rutaDB: *rutaDB,
		userAgent: *userAgent, intervalo: esperaSitio,
	})
	if err != nil {
		return err
	}
	defer cerrar()

	ctx, cancelar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelar()
	return mcp.Nuevo(srv, version).ServirStdio(ctx, os.Stdin, os.Stdout)
}

// ----------------------------------------------------------------- rellenar

func rellenar(args []string) error {
	fs := flag.NewFlagSet("rellenar", flag.ContinueOnError)
	secciones := fs.String("seccion", "primera", "secciones separadas por coma, o 'todas'")
	desdeTxt := fs.String("desde", "", "fecha inicial AAAA-MM-DD (obligatoria)")
	hastaTxt := fs.String("hasta", "", "fecha final AAAA-MM-DD (por defecto, hoy)")
	dirCache := fs.String("cache", entorno("NOTARUM_CACHE", "/datos/cache"), "directorio de caché (motor disco)")
	motor := fs.String("almacen", entorno("NOTARUM_ALMACEN", "disco"), "dónde guardar: disco, sqlite o postgres")
	rutaDB := fs.String("db", entorno("NOTARUM_DB", "/datos/notarum.db"), "archivo de la base (motor sqlite)")
	intervalo := fs.String("intervalo", entorno("NOTARUM_INTERVALO", "500ms"), "espera entre pedidos al sitio")
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto), "User-Agent hacia el sitio")
	conAvisos := fs.Bool("con-avisos", false, "bajar además el texto de cada aviso (mucho más lento)")
	formatoLog := fs.String("log", entorno("NOTARUM_LOG", "text"), "formato de log: text o json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	armarLog(*formatoLog)

	if *desdeTxt == "" {
		return errors.New("falta --desde (AAAA-MM-DD)")
	}
	desde, err := boletin.ParseFecha(*desdeTxt)
	if err != nil {
		return err
	}
	hasta := boletin.HoyEnArgentina()
	if *hastaTxt != "" {
		if hasta, err = boletin.ParseFecha(*hastaTxt); err != nil {
			return err
		}
	}
	if hasta.Before(desde.Time) {
		return fmt.Errorf("el rango está al revés: desde %s hasta %s", desde.API(), hasta.API())
	}

	var lista []boletin.Seccion
	if strings.EqualFold(*secciones, "todas") {
		lista = boletin.SeccionesValidas
	} else {
		for _, s := range strings.Split(*secciones, ",") {
			sec, err := boletin.ParseSeccion(s)
			if err != nil {
				return err
			}
			lista = append(lista, sec)
		}
	}

	esperaSitio, err := time.ParseDuration(*intervalo)
	if err != nil {
		return fmt.Errorf("intervalo inválido %q: %w", *intervalo, err)
	}
	srv, cerrar, err := armarServicio(configComun{
		motor: *motor, dirCache: *dirCache, rutaDB: *rutaDB,
		userAgent: *userAgent, intervalo: esperaSitio,
	})
	if err != nil {
		return err
	}
	defer cerrar()

	ctx, cancelar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelar()

	for _, sec := range lista {
		if err := rellenarSeccion(ctx, srv, sec, desde, hasta, *conAvisos); err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Info("cortado por pedido del usuario; lo bajado queda en la caché")
				return nil
			}
			return err
		}
	}
	return nil
}

func rellenarSeccion(ctx context.Context, srv *servicio.Servicio, sec boletin.Seccion, desde, hasta boletin.Fecha, conAvisos bool) error {
	fechas, err := srv.FechasDelAnio(ctx, sec, desde, hasta)
	if err != nil {
		return err
	}
	slog.Info("rellenando", "seccion", sec, "desde", desde.API(), "hasta", hasta.API(), "dias", len(fechas))

	var bajadas, salteadas, fallidas int
	inicio := time.Now()
	for i, f := range fechas {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if srv.TieneEdicionEnCache(sec, f) {
			salteadas++
			continue
		}
		ed, err := srv.Edicion(ctx, sec, f, "")
		switch {
		case errors.Is(err, servicio.ErrSinEdicion):
			slog.Debug("sin edición", "seccion", sec, "fecha", f.API())
		case err != nil:
			fallidas++
			slog.Warn("no se pudo bajar", "seccion", sec, "fecha", f.API(), "err", err)
			continue
		default:
			bajadas++
			slog.Info("bajada", "seccion", sec, "fecha", f.API(), "avisos", ed.Cantidad,
				"progreso", fmt.Sprintf("%d/%d", i+1, len(fechas)))
			if conAvisos {
				for _, a := range ed.Avisos {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if _, err := srv.Aviso(ctx, sec, a.ID, a.Fecha); err != nil {
						slog.Warn("no se pudo bajar el aviso", "id", a.ID, "err", err)
					}
				}
			}
		}
	}
	slog.Info("listo", "seccion", sec, "bajadas", bajadas, "ya_estaban", salteadas,
		"fallidas", fallidas, "tardo", time.Since(inicio).Round(time.Second).String())
	if fallidas > 0 {
		return fmt.Errorf("quedaron %d días sin bajar en la sección %s: volvé a correr el relleno", fallidas, sec)
	}
	return nil
}
