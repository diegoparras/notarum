# Deploy en EasyPanel

notarum es un binario que escucha en un puerto y guarda su caché en un
directorio. En EasyPanel eso es una **App** con un **volumen**.

## 1. Crear la App

En tu proyecto de EasyPanel, **+ Service → App**.

- **Nombre**: `notarum`

### Source

**Desde una imagen ya construida**, que es como se despliega este proyecto:

- Type: `Image`
- Image: `ghcr.io/diegoparras/notarum:1.8.1`

Fijá siempre una versión concreta en vez de `latest`: así sabés qué está
corriendo cuando algo cambia. Las versiones publicadas están en
`github.com/diegoparras/notarum/pkgs/container/notarum`, y la que corre una
instancia la dice su `/v1/salud`.

Un paquete recién publicado en ghcr.io nace **privado**. Para que EasyPanel
pueda bajarlo hay dos caminos:

- Hacerlo público, que es lo razonable para un proyecto MIT: en
  `github.com/users/diegoparras/packages` → notarum → *Package settings* →
  *Change visibility* → Public.
- O dejarlo privado y cargar en EasyPanel unas **Registry credentials**: usuario
  de GitHub y un token con `read:packages`.

Como alternativa, EasyPanel también puede construir desde el repositorio
(Type: `Git`, Build method: `Dockerfile`), pero eso requiere que el código esté
en GitHub.

### Publicar una versión nueva

La imagen se publica sola. Cada cambio que entra en `main` y pasa las pruebas
sale a ghcr.io con dos etiquetas: `latest` y el número de versión.

Ese número se lee de `var version` en `cmd/notarum/main.go`. Para que una
versión nueva tenga número propio hay que subirlo ahí —y en el `ARG VERSION`
del `Dockerfile`, que es el valor para quien construye a mano— en el mismo
cambio. Si no se sube, la imagen nueva sale con el número de la anterior y la
pisa: desde afuera ya no se distingue cuál de las dos está corriendo (el CI lo
avisa con un warning en la corrida).

En el mismo cambio va su sección `## [X.Y.Z]` arriba de todo en
`CHANGELOG.md`, con lo que cambia escrito para quien lo usa. Sin ella no se
publica nada: lo frenan los tests (también adentro del build de la imagen), el
CI y `publicar.sh`. Con ella, el CI crea el Release `vX.Y.Z` con esas notas y
etiqueta la imagen con la versión (`org.opencontainers.image.version`).

Para confirmar qué tomó un deploy: el historial de *Implementaciones* de
EasyPanel siempre dice "Deploy service", así que la versión se mira en los
**registros** del servicio (Resumen → Registros). Lo primero que escribe el
contenedor al arrancar es un cartel con `NOTARUM vX.Y.Z  (fecha)` y un renglón
por cambio, de 80 columnas como máximo.

Para publicar a mano, sin pasar por `main`:

```bash
bash publicar.sh
```

Lee la versión del mismo lugar y publica lo que haya en la carpeta. El
`docker login ghcr.io` necesita un token con `write:packages`. Si usás el de
la CLI de GitHub, el permiso se amplía una sola vez:

```bash
gh auth refresh -h github.com -s write:packages
gh auth token | docker login ghcr.io -u diegoparras --password-stdin
```

## 2. Environment

```
NOTARUM_PUERTO=8080
NOTARUM_ALMACEN=sqlite
NOTARUM_DB=/datos/notarum.db
NOTARUM_POR_MINUTO=60
NOTARUM_INTERVALO=500ms
NOTARUM_TOPE_SITIO=100
NOTARUM_LOG=json
NOTARUM_USER_AGENT=notarum/1.0 (+https://tu-dominio.example)
NOTARUM_ADMIN_CLAVE=una-clave-larga-para-el-panel
```

Poné en `NOTARUM_USER_AGENT` una URL de contacto real. Es un sitio público del
Estado: que sepan quién los está leyendo y cómo avisarte si molesta.

`NOTARUM_ADMIN_CLAVE` es la clave de la cuenta `admin`, la que entra al panel
en `/admin`. Si no la definís, notarum genera una y la imprime en el log al
arrancar, una sola vez: ponerla acá ahorra ir a buscarla.

`NOTARUM_TOPE_SITIO` es cuántos pedidos sostenidos le hace notarum al Boletín
cada cinco minutos. El sitio corta alrededor de los 140 desde una misma
dirección y contesta 429 —es lo que se midió, no un límite que el Boletín
documente—, así que 100 deja margen: salen 20 seguidos y, agotados, uno cada
tres segundos. `NOTARUM_INTERVALO` es sólo la espera mínima entre dos pedidos;
con medio segundo y sin tope saldrían 600 en esos cinco minutos. Los dos
valores de arriba son los que trae por defecto y no hace falta tocarlos.

`NOTARUM_ALMACEN=sqlite` es lo que conviene en un servidor: guarda todo en un
archivo e indexa los avisos, con lo que la búsqueda del lector responde sin
pedirle nada al Boletín. Con `disco` funciona igual pero la búsqueda siempre
va al sitio.

Si querés reservar el MCP, agregá `NOTARUM_MCP_TOKEN` con un secreto: `/mcp`
va a exigir `Authorization: Bearer`. Para apagar piezas están
`NOTARUM_SIN_MCP=1` y `NOTARUM_SIN_WEB=1`.

## 3. Volumes

**+ Add volume → Volume**

- Name: `notarum-datos`
- Mount path: `/datos`

Sin esto, cada redeploy vuelve a bajar todo del Boletín. Con esto, lo bajado
queda: una edición pasada no cambia nunca.

## 4. Domains & Proxy

**+ Add domain**

- Host: `boletin.tu-dominio.com`
- Port: `8080`
- HTTPS: activado (EasyPanel gestiona el certificado)

notarum ya lee `X-Forwarded-For`, así que el límite por IP cuenta al cliente
real y no al proxy.

## 5. Deploy

Botón **Deploy**. Cuando termine, entrá a `https://boletin.tu-dominio.com/`.

Una instancia nueva arranca **cerrada**: la cuenta `admin` existe desde el
primer arranque, y habiendo cuentas el acceso por defecto pide entrar. Te va a
llevar a `/entrar`; entrás con `admin` y la clave de `NOTARUM_ADMIN_CLAVE`, y
tendría que aparecer la última edición publicada.

Si la querés abierta, se cambia desde el panel, en `/admin` → **Quién entra, y
cuánto puede pedir**, sin volver a desplegar: `abierto` deja leer a cualquiera,
`mixto` deja público el lector y pide token para la API y el MCP. También se
puede fijar con `NOTARUM_ACCESO`.

Desde afuera, `/v1/salud` contesta siempre, esté como esté el acceso:

```bash
curl https://boletin.tu-dominio.com/v1/salud
```

El resto de la API, con la instancia cerrada o mixta, pide un token. Se crean
desde `/cuenta`:

```bash
curl -H "Authorization: Bearer $TOKEN" https://boletin.tu-dominio.com/v1/ediciones/primera/2026-09-01
```

`/v1/salud` devuelve `{"ok":true,...}` con la versión que está corriendo y los
contadores de lecturas al sitio y de aciertos de caché. Sirve como health
check y para ver si el Boletín está respondiendo.

También dice si el Boletín está limitando a la instancia: `sitio.limitados`
cuenta las veces que contestó 429 y, mientras dura la pausa que sigue a uno,
`sitio.en_pausa_hasta` dice hasta cuándo notarum no le va a pedir nada. En ese
rato lo que ya está guardado se sigue sirviendo, y lo que no, contesta 503 con
`Retry-After`.

## 6. Llenar la historia (opcional)

La API baja lo que le piden. Si querés que conteste rápido sobre meses
anteriores, llenalos desde el panel: entrá a `/admin` con la cuenta `admin`,
y en **El Boletín Oficial** elegí la sección y el rango de fechas y apretá
**Llenar**. Corre en segundo plano y la pantalla muestra cómo va; se puede
cerrar la página y volver después.

Va al ritmo que el Boletín tolera, que es el tope de más arriba: un año de una
sección son al menos unas 250 lecturas —una por día con edición, y más las
ediciones que vienen en varias páginas—, así que no baja de unos doce minutos.
Con la casilla **con el texto de cada aviso** es un pedido por aviso y son
horas. Se puede cortar y retomar; lo bajado queda en el volumen.

Si el Boletín sigue contestando 429 después de que notarum esperó y reintentó,
el relleno se corta ahí en vez de insistir con cada día que falta, y lo dice.
No se pierde nada: se vuelve a lanzar más tarde y sigue donde quedó.

No hace falta un Cron para mantenerlo al día. notarum baja solo la semana del
Boletín —las tres secciones, de lunes a viernes, con el texto de los avisos—
los sábados a las 4, y actualiza los catálogos todos los días a las 5. Las
horas se cambian con `NOTARUM_BOLETIN_A_LAS` y `NOTARUM_ACTUALIZAR_A_LAS`, y
`NOTARUM_SIN_ACTUALIZACION_AUTOMATICA=1` lo apaga.

Lo mismo se puede hacer por consola, para quien prefiera automatizarlo:

```bash
notarum rellenar --seccion primera --desde 2026-01-01 --log text
```

## 7. Conectar el MCP

La instancia desplegada habla MCP en `POST /mcp`, con JSON-RPC 2.0. Un cliente
que soporte MCP por HTTP apunta ahí; si pusiste `NOTARUM_MCP_TOKEN`, va con
`Authorization: Bearer <token>`.

Con la instancia cerrada o mixta el MCP pide identificarse igual que la API:
el token es uno de MCP, que se crea desde `/cuenta`.

```bash
curl -X POST https://boletin.tu-dominio.com/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Para un cliente local que sólo hable por entrada estándar, el mismo binario
sirve sin servidor:

```bash
notarum mcp --almacen sqlite --db /ruta/notarum.db
```

## Recursos

Es un servicio chico: alcanza con 256 MB de memoria y poco CPU. Lo que crece es
el volumen, y despacio: una edición de la primera sección ocupa unos 80 KB, así
que un año completo de las tres secciones ronda los 100 MB. Si bajás también el
texto de cada aviso, contá bastante más: la bajada semanal lo hace sola.

La memoria cambia si encendés la búsqueda de normativa nacional
(`NOTARUM_BUSCADOR_INFOLEG=1`): esa pide 1 GB.

## Actualizar

Cambiá la etiqueta de la imagen (o hacé push a `main` si construís desde Git) y
apretá **Deploy**. El volumen sobrevive: no se vuelve a bajar nada.
