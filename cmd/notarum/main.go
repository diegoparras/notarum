// notarum sirve el Boletín Oficial de la República Argentina como JSON.
//
//	notarum servir
//	notarum rellenar --seccion primera --desde 2024-01-01 [--hasta 2024-12-31]
//	notarum version
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
	"github.com/diegoparras/notarum/internal/asistente"
	"github.com/diegoparras/notarum/internal/boletin"
	"github.com/diegoparras/notarum/internal/cuentas"
	"github.com/diegoparras/notarum/internal/infoleg"
	"github.com/diegoparras/notarum/internal/lockatus"
	"github.com/diegoparras/notarum/internal/mcp"
	"github.com/diegoparras/notarum/internal/memoria"
	"github.com/diegoparras/notarum/internal/saij"
	"github.com/diegoparras/notarum/internal/servicio"
	"github.com/diegoparras/notarum/internal/tareas"
)

// version se puede fijar en el build: -ldflags "-X main.version=1.2.3".
var version = "1.8.0"

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
	case "infoleg":
		return sincronizarInfoLEG(args)
	case "provincial", "saij":
		return sincronizarSAIJ(args)
	case "usuarios", "usuario":
		return administrarUsuarios(args)
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
        NOTARUM_ACCESO      abierto | mixto | cerrado   [cerrado si hay cuentas]
        NOTARUM_POR_MINUTO  (--por-minuto)  cuota de quien no entró     [60]
        NOTARUM_CUOTA_PERSONA               cuota del rol persona       [600]
        NOTARUM_CUOTA_ADMIN                 cuota del rol admin         [6000]
        NOTARUM_CUOTA_LECTOR                cuota de las páginas web    [600]
        NOTARUM_CUOTA_LOGIN                 intentos de entrada         [10]
        NOTARUM_SECRETO_SESION              firma las sesiones          [se genera]
        NOTARUM_MEMORIA_MAX                 techo de memoria, 512MB o 1GB
                                            [lo que diga el contenedor]

      Los catálogos se actualizan solos todos los días. Los mismos botones del
      panel los actualizan en el momento.
        NOTARUM_ACTUALIZAR_A_LAS            hora HH:MM                  [05:00]
        NOTARUM_ZONA                        dónde se cuenta esa hora
                                            [America/Argentina/Buenos_Aires]
        NOTARUM_SIN_ACTUALIZACION_AUTOMATICA   apaga la actualización diaria

      Para delegar el login en Lockatus, el hub de identidad de la suite
      Escriba. El default es el login propio; federado los suma, no los
      reemplaza. Necesita que ya exista alguna cuenta.
        NOTARUM_AUTH        local | federado                            [local]
        LOCKATUS_ISSUER     la dirección del hub
        LOCKATUS_CLIENT_ID  el nombre con el que notarum está declarado  [notarum]
        LOCKATUS_REDIRECT_URI   https://tu-notarum/entrar/lockatus/volver

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

  notarum infoleg
      Baja el catálogo de normativa de InfoLEG y lo guarda. Con eso, cada
      aviso del Boletín muestra la norma que InfoLEG mantiene actualizada.
      Son unas 428 mil normas; se puede cortar y volver a correr.
      Se apaga con NOTARUM_SIN_INFOLEG; la parte provincial, con
      NOTARUM_SIN_SAIJ.

  notarum usuarios crear <nombre> [--rol admin|persona]
      Crea una cuenta. La clave se pide por teclado y no queda en el historial
      del shell. Mientras no exista ninguna cuenta, notarum funciona sin login
      y con todo abierto, que es como viene.

  notarum provincial
      Baja la Base SAIJ de Normativa Provincial que publica el Ministerio de
      Justicia en datos.jus.gob.ar: 81 mil leyes, decretos, códigos y las
      constituciones de las 24 provincias, desde 1855. Es lo que el Boletín
      nacional no trae. Se puede volver a correr: si el portal no publicó
      nada nuevo, no baja nada.

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
	srv := servicio.Nuevo(cli, alm)
	// InfoLEG es un accesorio: si se apaga, notarum sirve el Boletín igual.
	if entorno("NOTARUM_SIN_INFOLEG", "") == "" {
		srv = srv.ConInfoLEG(infoleg.NuevoCliente(infoleg.Opciones{
			UserAgent: cfg.userAgent,
			Intervalo: cfg.intervalo,
		}))
	}
	// La normativa provincial, lo mismo. Y no cuesta nada mientras nadie la
	// sincronice: el índice se arma la primera vez que alguien lo consulta.
	if entorno("NOTARUM_SIN_SAIJ", "") == "" {
		srv = srv.ConSAIJ(saij.NuevoCliente(saij.Opciones{UserAgent: cfg.userAgent}))
	}
	// El buscador de normativa nacional se pide: son unos 350 MB en memoria,
	// medidos con el catálogo real, y no se le imponen a quien no lo usa.
	if entorno("NOTARUM_BUSCADOR_INFOLEG", "") != "" {
		srv = srv.ConBuscadorInfoLEG(true)
		slog.Warn("el buscador de normativa nacional está encendido",
			"memoria_estimada", "unos 480 MB con el catálogo entero",
			"nota", "en un contenedor de 512 MB el proceso puede morir; conviene darle 1 GB")
	}
	return srv, func() { _ = alm.Cerrar() }, nil
}

// configComun es lo que ambos comandos necesitan para armar el servicio.
type configComun struct {
	motor     string
	dirCache  string
	rutaDB    string
	userAgent string
	intervalo time.Duration
}

// uaPorDefecto sale de la versión del binario y no de una constante aparte:
// escritas a mano se desincronizan, y este texto es con lo que los sitios
// saben quién les está pidiendo.
func uaPorDefecto() string {
	return "notarum/" + version + " (+https://github.com/diegoparras/notarum)"
}

// ------------------------------------------------------------------- servir

func servir(args []string) error {
	fs := flag.NewFlagSet("servir", flag.ContinueOnError)
	puerto := fs.String("puerto", entorno("NOTARUM_PUERTO", "8080"), "puerto HTTP")
	dirCache := fs.String("cache", entorno("NOTARUM_CACHE", "/datos/cache"), "directorio de caché (motor disco)")
	motor := fs.String("almacen", entorno("NOTARUM_ALMACEN", "disco"), "dónde guardar: disco, sqlite o postgres")
	rutaDB := fs.String("db", entorno("NOTARUM_DB", "/datos/notarum.db"), "archivo de la base (motor sqlite)")
	porMinuto := fs.String("por-minuto", entorno("NOTARUM_POR_MINUTO", "60"), "pedidos por minuto por IP (0 desactiva)")
	intervalo := fs.String("intervalo", entorno("NOTARUM_INTERVALO", "500ms"), "espera entre pedidos al sitio")
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto()), "User-Agent hacia el sitio")
	formatoLog := fs.String("log", entorno("NOTARUM_LOG", "json"), "formato de log: text o json")
	tokenMCP := fs.String("mcp-token", entorno("NOTARUM_MCP_TOKEN", ""), "si se pone, /mcp exige Bearer con este token")
	sinMCP := fs.Bool("sin-mcp", entorno("NOTARUM_SIN_MCP", "") != "", "apagar el endpoint /mcp")
	sinWeb := fs.Bool("sin-web", entorno("NOTARUM_SIN_WEB", "") != "", "apagar el lector web y dejar sólo la API")
	if err := fs.Parse(args); err != nil {
		return err
	}
	armarLog(*formatoLog)

	// Un techo para el recolector, antes que nada. Go deja crecer el heap
	// hasta el doble de lo vivo sin mirar lo que hay afuera: si la máquina
	// está justa, el pico de armar un índice termina en un OOM del que el
	// proceso ni se entera —lo mata el sistema— y desde afuera se ve como el
	// servicio que dejó de responder sin explicación. Con el techo puesto, el
	// recolector trabaja más seguido y el pico no llega.
	if techo := memoria.Ajustar(entorno("NOTARUM_MEMORIA_MAX", "")); techo > 0 {
		slog.Info("techo de memoria", "megas", techo/(1<<20),
			"de", map[bool]string{true: "la configuración", false: "el contenedor"}[entorno("NOTARUM_MEMORIA_MAX", "") != ""])
	} else {
		slog.Info("sin techo de memoria: el proceso crece hasta donde lo deje la máquina",
			"para_ponerle_uno", "NOTARUM_MEMORIA_MAX=1GB")
	}

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

	// ¿Sobrevivió lo guardado al despliegue anterior?
	//
	// Un contenedor sin volumen montado arranca vacío cada vez: todo funciona
	// —se entra, se carga una clave, se ve cargada— y al siguiente despliegue
	// no quedó nada, sin que nadie lo diga. Se mide en vez de suponerlo.
	marca, err := almacen.Marcar(srv.Almacen())
	if err != nil {
		slog.Warn("no se pudo marcar el almacén", "err", err)
	} else if marca.Nueva {
		slog.Warn("el almacén arrancó vacío",
			"nota", "si ya habías guardado cosas acá, no sobrevivieron al despliegue",
			"revisar", "que haya un volumen montado en el directorio de datos")
	} else {
		slog.Info("el almacén tiene datos de antes",
			"desde", marca.Desde.Format(time.RFC3339), "arranques", marca.Arranques)
	}

	// Las cuentas se encienden solas cuando existe alguna: mientras no haya
	// ninguna, notarum funciona abierto y sin login, como viene.
	reg, err := armarRegistro(srv.Almacen())
	if err != nil {
		return err
	}
	// La cuenta que administra sale de la configuración, como en el resto de
	// la suite: si la clave está en el entorno, esa es; si no, se genera y se
	// imprime una vez. Cambiarla en el entorno y reiniciar la vuelve a poner,
	// así que no hay forma de quedarse afuera de la propia instancia.
	generada, err := reg.AsegurarAdmin(
		entorno("NOTARUM_ADMIN_USUARIO", cuentas.UsuarioAdminPorDefecto),
		entorno("NOTARUM_ADMIN_CLAVE", ""))
	if err != nil {
		return err
	}
	if generada != "" {
		slog.Warn("se creó la cuenta que administra; esta clave se muestra una sola vez",
			"usuario", entorno("NOTARUM_ADMIN_USUARIO", cuentas.UsuarioAdminPorDefecto),
			"clave", generada,
			"para_no_verla_mas", "poné NOTARUM_ADMIN_CLAVE en la configuración")
	}
	politica, err := armarPolitica(reg.HayUsuarios(), limite)
	if err != nil {
		return err
	}
	hub, err := armarHub(reg != nil)
	if err != nil {
		return err
	}

	// El ejecutor corre lo que se lanza desde el panel: sincronizar los
	// catálogos y llenar la historia, que tardan más de lo que aguanta un
	// pedido HTTP.
	ejecutor := tareas.Nuevo()

	// Y el programador, que los corre solos todos los días. Los catálogos se
	// publican de a poco —InfoLEG suma las normas del Boletín con unos días
	// de retraso— y sin esto hay que acordarse de apretar el botón.
	programador, err := tareas.NuevoProgramador(ejecutor,
		entorno("NOTARUM_ACTUALIZAR_A_LAS", tareas.HoraPorDefecto),
		entorno("NOTARUM_ZONA", tareas.ZonaArgentina))
	if err != nil {
		return err
	}
	if entorno("NOTARUM_SIN_ACTUALIZACION_AUTOMATICA", "") == "" {
		programador.Agregar(tareas.Programado{
			Tipo:  "infoleg",
			Hacer: trabajoInfoLEG(srv),
		})
		programador.Agregar(tareas.Programado{
			Tipo:  "provincial",
			Hacer: trabajoProvincial(srv),
		})
		programador.Arrancar()
		defer programador.Frenar()
	}

	manejador := api.Nuevo(api.Config{
		Servicio: srv, PorMinuto: limite, Version: version,
		TokenMCP: *tokenMCP, SinMCP: *sinMCP, SinWeb: *sinWeb,
		Registro: reg, Politica: politica, Hub: hub,
		Tareas: ejecutor, Programador: programador, Marca: marca,
		// El asistente se enciende solo: cada persona pone su clave de
		// OpenRouter desde su cuenta, así que no hay nada que configurar acá.
		Asistente: asistente.NuevoCliente(asistente.Opciones{}),
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
			"mcp", !*sinMCP, "lector", !*sinWeb, "cuentas", reg != nil,
			"acceso", politica.Modo, "federado", hub != nil, "version", version)
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
		// Primero las tareas del panel: cortarlas y darles un momento para
		// que dejen las cosas donde las dejarían. Lo que guardaron queda, y
		// lo que faltaba se retoma al volver a lanzarlas.
		if ejecutor.AlgoCorriendo() {
			slog.Info("esperando a las tareas en curso")
			ejecutor.Esperar(10 * time.Second)
		}
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
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto()), "User-Agent hacia el sitio")
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

// -------------------------------------------------------------- usuarios

func administrarUsuarios(args []string) error {
	if len(args) == 0 || args[0] != "crear" {
		return errors.New("uso: notarum usuarios crear <nombre> [--rol admin|persona]")
	}
	args = args[1:]
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("falta el nombre: notarum usuarios crear <nombre>")
	}
	nombre := args[0]

	fs := flag.NewFlagSet("usuarios crear", flag.ContinueOnError)
	rol := fs.String("rol", "admin", "admin o persona")
	dirCache := fs.String("cache", entorno("NOTARUM_CACHE", "/datos/cache"), "directorio de caché (motor disco)")
	motor := fs.String("almacen", entorno("NOTARUM_ALMACEN", "disco"), "dónde guardar: disco, sqlite o postgres")
	rutaDB := fs.String("db", entorno("NOTARUM_DB", "/datos/notarum.db"), "archivo de la base (motor sqlite)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	armarLog("text")

	alm, err := armarAlmacen(*motor, *dirCache, *rutaDB)
	if err != nil {
		return err
	}
	defer alm.Cerrar()

	reg, err := armarRegistro(alm)
	if err != nil {
		return err
	}

	// La clave se pide por teclado: pasarla como argumento la dejaría en el
	// historial del shell y en la lista de procesos de la máquina.
	clave, err := pedirClave("Clave para " + nombre + ": ")
	if err != nil {
		return err
	}
	repetida, err := pedirClave("Repetila: ")
	if err != nil {
		return err
	}
	if clave != repetida {
		return errors.New("las claves no coinciden")
	}

	u, err := reg.CrearUsuario(nombre, clave, cuentas.Rol(*rol))
	if err != nil {
		return err
	}
	fmt.Printf("Listo: %s (%s).\nEntrá en /entrar y creá tus tokens desde /cuenta.\n", u.Nombre, u.Rol)
	return nil
}

// teclado es uno solo para todo el proceso. Un bufio.Reader nuevo por llamada
// se lleva en su buffer lo que todavía no se leyó, y la segunda lectura no
// encuentra nada.
var teclado = bufio.NewReader(os.Stdin)

// pedirClave lee una línea del teclado.
func pedirClave(mensaje string) (string, error) {
	fmt.Print(mensaje)
	linea, err := teclado.ReadString('\n')
	fmt.Println()
	if err != nil && linea == "" {
		return "", err
	}
	return strings.TrimRight(linea, "\r\n"), nil
}

// --------------------------------------------------------------- infoleg

func sincronizarInfoLEG(args []string) error {
	fs := flag.NewFlagSet("infoleg", flag.ContinueOnError)
	dirCache := fs.String("cache", entorno("NOTARUM_CACHE", "/datos/cache"), "directorio de caché (motor disco)")
	motor := fs.String("almacen", entorno("NOTARUM_ALMACEN", "disco"), "dónde guardar: disco, sqlite o postgres")
	rutaDB := fs.String("db", entorno("NOTARUM_DB", "/datos/notarum.db"), "archivo de la base (motor sqlite)")
	intervalo := fs.String("intervalo", entorno("NOTARUM_INTERVALO", "500ms"), "espera entre pedidos")
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto()), "User-Agent hacia los sitios")
	trabajo := fs.String("trabajo", entorno("NOTARUM_TRABAJO", ""), "dónde bajar el catálogo (por defecto, el temporal del sistema)")
	formatoLog := fs.String("log", entorno("NOTARUM_LOG", "text"), "formato de log: text o json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	armarLog(*formatoLog)

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

	inicio := time.Now()
	estado, err := srv.SincronizarInfoLEG(ctx, *trabajo, func(guardadas int) {
		slog.Info("guardando normas", "guardadas", guardadas)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			slog.Info("cortado por pedido del usuario; lo guardado queda")
			return nil
		}
		return err
	}
	slog.Info("listo",
		"normas", estado.Normas,
		"con_texto", estado.ConTexto,
		"hasta", estado.UltimaFechaBO,
		"tardo", time.Since(inicio).Round(time.Second).String())
	return nil
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
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto()), "User-Agent hacia el sitio")
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

// rellenarSeccion delega en el servicio, que es donde vive el recorrido: el
// panel web lanza exactamente lo mismo.
func rellenarSeccion(ctx context.Context, srv *servicio.Servicio, sec boletin.Seccion, desde, hasta boletin.Fecha, conAvisos bool) error {
	avisar := func(a servicio.Avance) {
		// Por consola alcanza con una línea por día bajado; el detalle de
		// cada uno ya lo escribe el servicio.
		if a.Dia%25 == 0 || a.Dia == a.DeDias {
			slog.Info("avanzando", "seccion", a.Seccion, "dia", a.Dia, "de", a.DeDias,
				"bajadas", a.Relleno.Bajadas, "ya_estaban", a.Relleno.YaEstaban)
		}
	}
	var err error
	if conAvisos {
		_, err = srv.RellenarConAvisos(ctx, sec, desde, hasta, avisar)
	} else {
		_, err = srv.Rellenar(ctx, sec, desde, hasta, avisar)
	}
	return err
}

// armarRegistro prepara las cuentas.
//
// El secreto firma las sesiones. Si no se configura uno, se genera al vuelo y
// se guarda junto al resto: así el login sobrevive a un reinicio sin pedirle
// nada a nadie. Definir NOTARUM_SECRETO_SESION tiene sentido cuando corren
// varias instancias contra la misma base y las sesiones tienen que valer en
// todas, o para poder rotarlo y cerrar todas las sesiones de una.
func armarRegistro(alm almacen.Almacen) (*cuentas.Registro, error) {
	if s := entorno("NOTARUM_SECRETO_SESION", ""); s != "" {
		if len(s) < 32 {
			return nil, errors.New("NOTARUM_SECRETO_SESION necesita al menos 32 caracteres")
		}
		return cuentas.NuevoRegistro(alm, []byte(s))
	}

	// Sin secreto configurado se genera uno y se guarda, para que las
	// sesiones y lo que se cifre con él sobrevivan al reinicio.
	//
	// Va en base64 y no en crudo: el almacén guarda JSON, y 32 bytes al azar
	// casi nunca lo son. Guardarlos derecho hacía que una instancia sin
	// NOTARUM_SECRETO_SESION no llegara a arrancar.
	const clave = "cuentas/_secreto"
	if crudo, hay := alm.Leer(clave); hay {
		var enTexto string
		if err := json.Unmarshal(crudo, &enTexto); err == nil {
			if secreto, err := base64.StdEncoding.DecodeString(enTexto); err == nil && len(secreto) >= 32 {
				return cuentas.NuevoRegistro(alm, secreto)
			}
		}
		// Lo guardado por una versión anterior, en crudo: si sirve, se usa y
		// se reescribe bien. Perderlo cerraría todas las sesiones abiertas y
		// dejaría ilegible lo que se haya cifrado con él.
		if len(crudo) >= 32 {
			if err := guardarSecreto(alm, clave, crudo); err == nil {
				return cuentas.NuevoRegistro(alm, crudo)
			}
		}
	}
	secreto := make([]byte, 32)
	if _, err := rand.Read(secreto); err != nil {
		return nil, err
	}
	if err := guardarSecreto(alm, clave, secreto); err != nil {
		return nil, err
	}
	return cuentas.NuevoRegistro(alm, secreto)
}

func guardarSecreto(alm almacen.Almacen, clave string, secreto []byte) error {
	crudo, err := json.Marshal(base64.StdEncoding.EncodeToString(secreto))
	if err != nil {
		return err
	}
	return alm.Guardar(clave, crudo, almacen.SinVencimiento)
}

// armarPolitica arma la política de acceso de esta instancia.
//
// Quien opera decide: una cátedra puede querer su copia abierta, un estudio la
// suya cerrada, y un organismo el lector público con la API por token. Por
// defecto se cierra en cuanto hay cuentas, y se queda abierta mientras no
// haya ninguna, porque sin cuentas no habría con qué entrar.
func armarPolitica(hayUsuarios bool, porMinuto int) (cuentas.Politica, error) {
	p := cuentas.PoliticaPorDefecto(hayUsuarios)
	if s := entorno("NOTARUM_ACCESO", ""); s != "" {
		m, err := cuentas.ParseModo(s)
		if err != nil {
			return p, err
		}
		p.Modo = m
	}
	if porMinuto > 0 {
		p.Anonimo = porMinuto
	}
	for _, c := range []struct {
		variable string
		destino  *int
	}{
		{"NOTARUM_CUOTA_PERSONA", &p.Persona},
		{"NOTARUM_CUOTA_ADMIN", &p.Admin},
		{"NOTARUM_CUOTA_LECTOR", &p.Lector},
		{"NOTARUM_CUOTA_LOGIN", &p.Login},
	} {
		if v := entorno(c.variable, ""); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return p, fmt.Errorf("%s inválido: %q", c.variable, v)
			}
			*c.destino = n
		}
	}
	return p, nil
}

// armarHub prepara la federación con Lockatus, si está pedida.
//
// Devuelve nil sin error cuando no está: el default es el login propio, y una
// instancia que no forma parte de ninguna suite no tiene por qué depender de
// un hub. Cuando sí está pedida, faltar un dato es un error de arranque y no
// algo que se descubra cuando alguien intenta entrar.
func armarHub(hayCuentas bool) (*lockatus.Cliente, error) {
	modo := strings.ToLower(strings.TrimSpace(entorno("NOTARUM_AUTH", "local")))
	switch modo {
	case "", "local":
		return nil, nil
	case "federado":
	default:
		return nil, fmt.Errorf("NOTARUM_AUTH inválido: %q; se esperaba local o federado", modo)
	}
	if !hayCuentas {
		// Sin registro no hay dónde guardar a quien entre. Se avisa acá y no
		// después, porque el botón aparecería y no funcionaría.
		return nil, errors.New("NOTARUM_AUTH=federado necesita el registro de cuentas encendido")
	}
	c, err := lockatus.Nuevo(lockatus.Opciones{
		Emisor:    entorno("LOCKATUS_ISSUER", ""),
		ClienteID: entorno("LOCKATUS_CLIENT_ID", "notarum"),
		Vuelta:    entorno("LOCKATUS_REDIRECT_URI", ""),
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// sincronizarSAIJ baja la base de normativa provincial y la guarda.
func sincronizarSAIJ(args []string) error {
	fs := flag.NewFlagSet("provincial", flag.ContinueOnError)
	dirCache := fs.String("cache", entorno("NOTARUM_CACHE", "/datos/cache"), "directorio de caché (motor disco)")
	motor := fs.String("almacen", entorno("NOTARUM_ALMACEN", "disco"), "dónde guardar: disco, sqlite o postgres")
	rutaDB := fs.String("db", entorno("NOTARUM_DB", "/datos/notarum.db"), "archivo de la base (motor sqlite)")
	userAgent := fs.String("user-agent", entorno("NOTARUM_USER_AGENT", uaPorDefecto()), "User-Agent hacia los sitios")
	trabajo := fs.String("trabajo", entorno("NOTARUM_TRABAJO", ""), "dónde bajar el catálogo (por defecto, el temporal del sistema)")
	formatoLog := fs.String("log", entorno("NOTARUM_LOG", "text"), "formato de log: text o json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	armarLog(*formatoLog)

	srv, cerrar, err := armarServicio(configComun{
		motor: *motor, dirCache: *dirCache, rutaDB: *rutaDB,
		userAgent: *userAgent, intervalo: 500 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	defer cerrar()

	ctx, cancelar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelar()

	inicio := time.Now()
	estado, err := srv.SincronizarSAIJ(ctx, *trabajo)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			slog.Info("cortado por pedido del usuario; lo que había queda como estaba")
			return nil
		}
		return err
	}
	slog.Info("listo",
		"normas", estado.Normas,
		"provincias", estado.Provincias,
		"catalogo_publicado", estado.CatalogoAlDia.Format("2006-01-02"),
		"tardo", time.Since(inicio).Round(time.Second).String())
	return nil
}

// Los trabajos que corren solos y desde el panel son los mismos: se arman acá
// una vez y los usan los dos, para que no puedan diverger.
func trabajoInfoLEG(srv *servicio.Servicio) tareas.Trabajo {
	return func(ctx context.Context, avisar func(string)) (string, error) {
		avisar("buscando el catálogo de InfoLEG")
		e, err := srv.SincronizarInfoLEG(ctx, "", func(guardadas int) {
			avisar(fmt.Sprintf("guardando normas (%d)", guardadas))
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d normas, %d con texto", e.Normas, e.ConTexto), nil
	}
}

func trabajoProvincial(srv *servicio.Servicio) tareas.Trabajo {
	return func(ctx context.Context, avisar func(string)) (string, error) {
		avisar("bajando el catálogo provincial")
		e, err := srv.SincronizarSAIJ(ctx, "")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d normas de %d jurisdicciones", e.Normas, e.Provincias), nil
	}
}
