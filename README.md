# notarum

Lector, API y herramientas MCP del **Boletín Oficial de la República Argentina**,
abiertos y de sólo lectura.

Expone como JSON el calendario de ediciones, cada edición por sección y fecha
con sus avisos, cada aviso con su texto completo, los anexos en PDF y el
catálogo de rubros. Es una caché legible del sitio oficial: una edición pasada
no cambia nunca, así que se baja una vez y se sirve para siempre.

No opina, no clasifica, no puntúa. Entrega el dato crudo y bien tipado; ese es
todo su valor, y por eso puede ser abierto y servir a cualquiera: un diario, un
estudio jurídico, un investigador, un sistema que lo consuma por programa.

Sin clave para leer. Límite de pedidos por IP. Licencia MIT.

Tres caras sobre los mismos datos:

- **Un lector web** en `/`, para leer el Boletín como se lee un diario:
  la edición del día, navegación por fecha y rubro, el aviso con sus anexos,
  búsqueda y un calendario del año.
- **Una API JSON** en `/v1`, para que lo consuma un programa.
- **Un servidor MCP** en `/mcp` y por entrada estándar, para que lo consulte
  un modelo como una herramienta más.

```bash
docker run -d -p 8080:8080 -v notarum-datos:/datos ghcr.io/diegoparras/notarum:1.0.0
open http://localhost:8080/
curl http://localhost:8080/v1/ediciones/primera/2026-09-01
```

## El lector

| Ruta | Qué muestra |
|---|---|
| `/` | la última edición publicada |
| `/ed/{sección}/{fecha}` | la edición de ese día, con sus rubros |
| `/ed/{sección}/{fecha}?rubro=DECRETOS` | la misma, filtrada |
| `/av/{sección}/{id}/{fecha}` | el aviso completo con sus anexos |
| `/buscar` | búsqueda por texto y fechas |
| `/calendario/{sección}/{año}` | qué días hubo edición |

Sigue el sistema visual del Ecosistema Escriba: crema `#fff8f3`, tinta
`#2a1b14`, cards planas de radio 18, y azul tinta `#1f5f8b` como color propio
de notarum. Se cambia en una línea, en la variable `--acento` de
[estilo.css](internal/web/estatico/estilo.css).

Todo se arma en el servidor: no hay build de JavaScript ni recursos de
terceros, y las páginas viajan embebidas en el binario. Se apaga con
`NOTARUM_SIN_WEB=1` si sólo se quiere la API.

## La API

Base `/v1`. Fechas en `AAAA-MM-DD`. Secciones: `primera`, `segunda`, `tercera`.

| Ruta | Devuelve |
|---|---|
| `GET /v1/secciones` | las secciones que esta API sabe leer |
| `GET /v1/calendario/{año}/{sección}` | `{ "fechas": [...], "con_suplemento": [...] }` |
| `GET /v1/ediciones/{sección}/{fecha}` | la edición completa con sus avisos |
| `GET /v1/ediciones/{sección}/{fecha}?rubro=DECRETOS` | la misma, filtrada por rubro |
| `GET /v1/ediciones/{sección}?desde=&hasta=` | resúmenes de un rango, sin avisos |
| `GET /v1/avisos/{sección}/{id}/{fecha}` | el aviso con `texto`, `html` y `anexos` |
| `GET /v1/anexos/{sección}/{nro}/{id}/{fecha}.pdf` | el PDF del anexo |
| `GET /v1/rubros/{sección}` | el catálogo de rubros |
| `GET /v1/buscar?sección=&texto=&desde=&hasta=` | búsqueda por texto y fecha |
| `GET /v1/buscar?…&fuente=indice` | la misma, contra el índice local |
| `GET /v1/salud` | estado del servicio y de la caché |
| `GET /v1/openapi.json` | el contrato |

### Un aviso

```json
{
  "id": "346633",
  "seccion": "primera",
  "fecha": "2026-09-01",
  "rubro": "DECRETOS",
  "organismo": "PODER EJECUTIVO",
  "norma": "Decreto 845/2026",
  "referencia": "DECTO-2026-845-APN-PTE",
  "sintesis": "Disposiciones.",
  "tiene_anexos": true,
  "repetido": false,
  "suplemento": false,
  "url": "https://www.boletinoficial.gob.ar/detalleAviso/primera/346633/20260901"
}
```

El detalle agrega `texto` (plano, párrafos separados por línea en blanco),
`html` (saneado: sin estilos ni scripts, con las tablas conservadas) y
`anexos`, cada uno con la ruta para bajar su PDF por esta misma API.

### Buscar

`/v1/buscar` toma `fuente`:

- **`indice`** busca en el índice local, sin pedirle nada al Boletín. Requiere
  el motor `sqlite` (más abajo).
- **`sitio`** consulta la búsqueda avanzada del Boletín Oficial.
- **`auto`**, por defecto, usa el índice cuando tiene historia del rango y si
  no va al sitio.

La respuesta dice cuál se usó y cuántos días del rango tiene indexados, así que
se puede saber si vio todo o parte. Los totales no son comparables entre
fuentes: el Boletín pagina de a 100 y no informa un total, mientras que el
índice sí lo sabe.

### Cosas que conviene saber

- **`repetido: true`** marca los avisos que vienen de un rubro terminado en
  `- ANTERIOR`: ya se publicaron en una edición previa. Se entregan igual para
  que quien consume decida si los cuenta.
- **`suplemento: true`** marca los avisos del suplemento del día. La portada
  normal ya los incluye: no hay que pedir nada aparte.
- **El organismo puede venir vacío** y es un dato legítimo: en el rubro `LEYES`,
  los decretos de promulgación no lo traen.
- **Los ids no son todos numéricos.** En la primera son correlativos; en la
  segunda y la tercera pueden ser alfanuméricos (`A1522579`). Tampoco hay que
  inferir la fecha de un rango de ids: los suplementos usan otro rango.
- **Un día sin edición** devuelve `404` con `"sin_edicion": true`. No es una
  falla: es un feriado.

### Errores

Cada error dice de quién es la culpa, para que quien consume sepa a quién mirar:

```json
{ "error": "el Boletín Oficial no devolvió lo esperado", "origen": "sitio" }
```

`origen` es `sitio` (el Boletín no contestó o cambió de forma), `notarum`
(falló algo propio) o `pedido` (el pedido está mal armado).

### Caché

Las respuestas traen `ETag` y `Cache-Control`. Una edición pasada se declara
inmutable por un año; la del día en curso, por cinco minutos. Mandá el `ETag`
en `If-None-Match` y vas a recibir un `304` sin cuerpo.

## Correr

### Docker

```bash
docker build -t notarum:1.0.0 .
docker run -d --name notarum -p 8080:8080 -v notarum-datos:/datos notarum:1.0.0
```

Para EasyPanel está la guía en [docs/easypanel.md](docs/easypanel.md).

### Configuración

Todo por variable de entorno (o por bandera, con el mismo nombre en minúscula):

| Variable | Por defecto | Qué hace |
|---|---|---|
| `NOTARUM_PUERTO` | `8080` | puerto HTTP |
| `NOTARUM_ALMACEN` | `disco` | `disco` o `sqlite` |
| `NOTARUM_CACHE` | `/datos/cache` | directorio de la caché, con el motor `disco` |
| `NOTARUM_DB` | `/datos/notarum.db` | archivo de la base, con el motor `sqlite` |
| `NOTARUM_POR_MINUTO` | `60` | pedidos por minuto por IP; `0` desactiva el límite |
| `NOTARUM_INTERVALO` | `500ms` | espera entre pedidos al sitio del Boletín |
| `NOTARUM_USER_AGENT` | `notarum/1.0 (+…)` | User-Agent hacia el sitio |
| `NOTARUM_LOG` | `json` | `json` o `text` |
| `NOTARUM_MCP_TOKEN` | vacío | si se pone, `/mcp` exige `Authorization: Bearer` |
| `NOTARUM_SIN_MCP` | vacío | con cualquier valor, apaga `/mcp` |
| `NOTARUM_SIN_WEB` | vacío | con cualquier valor, apaga el lector web |

### Dónde se guarda

Dos motores, la misma interfaz:

- **`disco`** (por defecto): un archivo JSON por clave. Alcanza para servir por
  fecha y no tiene dependencias.
- **`sqlite`**: un solo archivo, y además **indexa los avisos** para poder
  buscarlos sin pedirle nada al Boletín. Usa `modernc.org/sqlite`, que es Go
  puro: el binario sigue siendo estático y la imagen no crece.

El índice busca sin acentos —`energia` encuentra `SECRETARÍA DE ENERGÍA`—, sobre
el sumario de todos los avisos y sobre el cuerpo de aquellos cuyo texto ya se
bajó (con `rellenar --con-avisos`).

```bash
NOTARUM_ALMACEN=sqlite notarum servir
```

### Llenar la historia

La API baja del sitio sólo lo que le piden, y con eso alcanza para el uso
diario. Si querés que conteste rápido sobre meses anteriores, llenalos antes:

```bash
notarum rellenar --seccion primera --desde 2026-01-01
notarum rellenar --seccion todas --desde 2026-08-01 --hasta 2026-08-31
```

Recorre el calendario, baja lo que falta al mismo ritmo, saltea lo que ya está
y se puede cortar y retomar. Con `--con-avisos` baja además el texto de cada
aviso (mucho más lento).

En Docker:

```bash
docker run --rm -v notarum-datos:/datos notarum:1.0.0 \
  rellenar --seccion primera --desde 2026-01-01
```

`GET /v1/ediciones/{sección}?desde=&hasta=` devuelve los resúmenes que ya están
en la caché y lista en `faltantes` los días que todavía no se bajaron: la API no
baja un año entero adentro de un pedido HTTP.

## MCP

Para que un modelo consulte el Boletín como una herramienta. Seis: `edicion`,
`aviso`, `buscar`, `calendario`, `rubros` y `estado`.

**Por HTTP**, en la instancia desplegada: `POST /mcp` con JSON-RPC 2.0. Con
`NOTARUM_MCP_TOKEN` exige `Authorization: Bearer`; sin él queda abierto, como
el resto de la API.

**Por entrada estándar**, para un cliente local:

```bash
notarum mcp --almacen sqlite --db ~/notarum.db
```

En Claude Desktop o Claude Code, apuntando al binario:

```json
{
  "mcpServers": {
    "notarum": {
      "command": "notarum",
      "args": ["mcp", "--almacen", "sqlite", "--db", "/ruta/notarum.db"]
    }
  }
}
```

Las herramientas están pensadas para un modelo, no para una persona: una
edición se recorta a 40 avisos y lo dice, en vez de llenar la ventana de
contexto en silencio; un feriado se explica en palabras en vez de devolver un
error; y el aviso entrega el texto plano sin el HTML, que sólo gasta contexto.

## Cómo está hecho

Go sin framework: `net/http` para servir, `html/template` para las páginas,
`golang.org/x/net/html` para parsear, `bluemonday` para sanear el HTML de los
avisos y `modernc.org/sqlite` para el motor de base. Un binario estático, sin
root en la imagen final.

```
cmd/notarum        servir, rellenar y mcp
internal/boletin   lectura del sitio: cliente con ritmo y parseo
internal/almacen   dónde se guarda: archivos o SQLite, con su índice
internal/servicio  qué se lee de nuevo y qué ya está guardado
internal/api       rutas, errores, límite por IP, contrato OpenAPI
internal/mcp       herramientas para un modelo, por stdio y por HTTP
internal/web       el lector, con sus plantillas y su hoja de estilo
```

**Trato con el sitio.** Un pedido cada 500 ms por proceso, reintento con espera
exponencial en 5xx y timeouts, `User-Agent` propio con URL de contacto. El sitio
tiene protección F5: no se intentan sesiones ni paralelismo agresivo.

**Caché.** Por clave en disco (`ediciones/{sección}/{fecha}.json`, etc.). Una
edición pasada no vence nunca; la del día en curso, a los cinco minutos. Los
feriados también se guardan: no tiene sentido volver a preguntar por un día sin
edición de hace tres años.

## Pruebas

```bash
go test ./...                              # unidad, contrato, handlers y páginas
go test ./internal/boletin/ -tags red -v   # contra el sitio real
```

Los fixtures de `internal/boletin/testdata/` son HTML real del sitio, guardado
el 4/9/2026. Los tests de unidad verifican cantidades conocidas (73 avisos el
15/7/2026, 52 el 10/3/2025, 100 el 1/9/2026) y cada regla de extracción. El test
de contrato valida cada respuesta contra `openapi.json`. Los tests con la
etiqueta `red` pegan al sitio real y sirven para enterarse de que cambió de
forma antes de que lo note quien consume la API.

## Licencia

MIT. Los datos son del Boletín Oficial de la República Argentina; notarum sólo
los sirve en un formato legible por programas.
