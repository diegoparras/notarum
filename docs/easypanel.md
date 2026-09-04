# Deploy en EasyPanel

notarum es un binario que escucha en un puerto y guarda su caché en un
directorio. En EasyPanel eso es una **App** con un **volumen**.

## 1. Crear la App

En tu proyecto de EasyPanel, **+ Service → App**.

- **Nombre**: `notarum`

### Source

Dos opciones, según de dónde salga la imagen.

**a) Desde el repositorio (EasyPanel construye)**

- Type: `Git`
- Repository: `https://github.com/diegoparras/notarum`
- Branch: `main`
- Build method: `Dockerfile`
- Dockerfile path: `Dockerfile`

**b) Desde una imagen ya construida**

- Type: `Image`
- Image: `ghcr.io/diegoparras/notarum:1.0.0`

Fijá siempre una versión concreta en vez de `latest`: así sabés qué está
corriendo cuando algo cambia.

## 2. Environment

```
NOTARUM_PUERTO=8080
NOTARUM_CACHE=/datos/cache
NOTARUM_POR_MINUTO=60
NOTARUM_INTERVALO=500ms
NOTARUM_LOG=json
NOTARUM_USER_AGENT=notarum/1.0 (+https://tu-dominio.example)
```

Poné en `NOTARUM_USER_AGENT` una URL de contacto real. Es un sitio público del
Estado: que sepan quién los está leyendo y cómo avisarte si molesta.

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

Botón **Deploy**. Cuando termine:

```bash
curl https://boletin.tu-dominio.com/v1/salud
curl https://boletin.tu-dominio.com/v1/ediciones/primera/2026-09-01
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

## Recursos

Es un servicio chico: alcanza con 128 MB de memoria y poco CPU. Lo que crece es
el volumen, y despacio: una edición de la primera sección ocupa unos 80 KB de
JSON, así que un año completo de las tres secciones ronda los 100 MB. Si bajás
también el texto de cada aviso (`--con-avisos`), contá bastante más.

## Actualizar

Cambiá la etiqueta de la imagen (o hacé push a `main` si construís desde Git) y
apretá **Deploy**. El volumen sobrevive: no se vuelve a bajar nada.
