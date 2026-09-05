# Deploy en EasyPanel

notarum es un binario que escucha en un puerto y guarda su caché en un
directorio. En EasyPanel eso es una **App** con un **volumen**.

## 1. Crear la App

En tu proyecto de EasyPanel, **+ Service → App**.

- **Nombre**: `notarum`

### Source

**Desde una imagen ya construida**, que es como se despliega este proyecto:

- Type: `Image`
- Image: `ghcr.io/diegoparras/notarum:1.4.0`

Fijá siempre una versión concreta en vez de `latest`: así sabés qué está
corriendo cuando algo cambia.

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

```bash
docker build -t ghcr.io/diegoparras/notarum:1.4.0 --build-arg VERSION=1.2.0 .
docker push ghcr.io/diegoparras/notarum:1.4.0
```

El `docker login ghcr.io` necesita un token con `write:packages`. Si usás el de
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
NOTARUM_LOG=json
NOTARUM_USER_AGENT=notarum/1.0 (+https://tu-dominio.example)
```

Poné en `NOTARUM_USER_AGENT` una URL de contacto real. Es un sitio público del
Estado: que sepan quién los está leyendo y cómo avisarte si molesta.

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

Botón **Deploy**. Cuando termine, entrá a `https://boletin.tu-dominio.com/` y
tendría que aparecer la última edición publicada. Y desde la consola:

```bash
curl https://boletin.tu-dominio.com/v1/salud
curl https://boletin.tu-dominio.com/v1/ediciones/primera/2026-09-01
curl -X POST https://boletin.tu-dominio.com/mcp   -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

`/v1/salud` devuelve `{"ok":true,...}` con los contadores de lecturas al sitio y
de aciertos de caché. Sirve como health check y para ver si el Boletín está
respondiendo.

## 6. Llenar la historia (opcional)

La API baja lo que le piden. Si querés que conteste rápido sobre meses
anteriores, corré el relleno una vez desde la consola de la App:

```bash
notarum rellenar --seccion primera --desde 2026-01-01 --log text
```

Va a un pedido cada 500 ms: un año de una sección son unas 250 lecturas, poco
más de dos minutos. Se puede cortar y retomar; lo bajado queda en el volumen.

Para dejarlo al día automáticamente, en EasyPanel podés agregar un **Cron** en
el mismo proyecto que corra todos los días:

```bash
notarum rellenar --seccion todas --desde $(date -d '7 days ago' +%%F) --log text
```

No es necesario: la API baja el día que le pidan. Sirve si querés que el primer
pedido de la mañana ya salga de la caché.

## 7. Conectar el MCP

La instancia desplegada habla MCP en `POST /mcp`, con JSON-RPC 2.0. Un cliente
que soporte MCP por HTTP apunta ahí; si pusiste `NOTARUM_MCP_TOKEN`, va con
`Authorization: Bearer <token>`.

Para un cliente local que sólo hable por entrada estándar, el mismo binario
sirve sin servidor:

```bash
notarum mcp --almacen sqlite --db /ruta/notarum.db
```

## Recursos

Es un servicio chico: alcanza con 256 MB de memoria y poco CPU. Lo que crece es
el volumen, y despacio: una edición de la primera sección ocupa unos 80 KB, así
que un año completo de las tres secciones ronda los 100 MB. Si bajás también el
texto de cada aviso (`--con-avisos`), contá bastante más.

## Actualizar

Cambiá la etiqueta de la imagen (o hacé push a `main` si construís desde Git) y
apretá **Deploy**. El volumen sobrevive: no se vuelve a bajar nada.
