# Federar notarum con Lockatus

Lockatus es el hub de identidad de la suite Escriba. Federar significa que
quien entra a notarum lo hace con la cuenta de la suite, y que los accesos se
administran en un solo lugar.

Es **opcional**. Sin configurar nada, notarum usa su login propio y no depende
de ningún hub, que es lo que corresponde a una instancia que no forma parte de
ninguna suite.

Y cuando se enciende, **se suma**: el formulario de siempre sigue estando. Eso
es a propósito. Si el hub se cae, quien tenga una cuenta local todavía puede
entrar y administrar la instancia.

## Lo que hay que hacer de los dos lados

### En el hub

Declarar la app y sus roles. En caliente:

```bash
curl -X PUT https://tu-lockatus/api/admin/apps/notarum \
  -H "Content-Type: application/json" \
  -d '{"name":"notarum","roles":["admin","persona"],
       "redirect_uris":["https://tu-notarum/entrar/lockatus/volver"]}'
```

El `redirect_uri` tiene que coincidir **exacto** con el que manda notarum: sin
barra de más, con el mismo esquema y el mismo puerto.

Después, asignarle a cada persona un rol para `notarum` en la matriz de
accesos. Quien no tenga ninguno recibe un `access_denied` y notarum se lo dice
con todas las letras, para que sepa que tiene que pedirlo y no que algo se
rompió.

### En notarum

```
NOTARUM_AUTH=federado
LOCKATUS_ISSUER=https://tu-lockatus
LOCKATUS_CLIENT_ID=notarum
LOCKATUS_REDIRECT_URI=https://tu-notarum/entrar/lockatus/volver
```

Hace falta que ya exista alguna cuenta (`notarum usuarios crear`): sin
registro no hay dónde guardar a quien entre, y notarum se niega a arrancar en
vez de mostrar un botón que no funciona.

## Los roles

El catálogo de notarum tiene dos, y el hub puede tener más:

| Rol en el hub                     | Rol en notarum |
|-----------------------------------|----------------|
| `admin`, `administrador`, `superadmin` | `admin`   |
| cualquier otro                    | `persona`      |

Lo que no se reconoce cae en el rol de menos privilegio en lugar de rebotar:
que la suite agregue un rol nuevo no tiene por qué dejar a nadie afuera, pero
tampoco puede darle a nadie más de lo que notarum sabe conceder.

**El rol lo decide el hub, en cada entrada.** Si allá se lo bajan a alguien,
la próxima vez que entre acá queda con el rol nuevo. No hay que acordarse de
tocar las dos partes.

## Las cuentas federadas

Se identifican por el correo completo, no por la parte de adelante:
`diego@una.org` y `diego@otra.org` son dos personas. Juntarlas sería darle a
una lo que es de la otra.

Quedan marcadas como externas y sin clave, así que **no se puede entrar a
ellas por el formulario**. La única puerta es el hub.

Los tokens de API y de MCP funcionan igual que en una cuenta local: se crean
desde `/cuenta` y no dependen del hub una vez creados. Eso significa que un
programa sigue andando aunque el hub esté caído, y también que revocar el
acceso en el hub **no revoca los tokens ya creados** — hay que revocarlos
desde `/cuenta`, o borrar la cuenta.

## Qué se verifica

El flujo es el de código de autorización con PKCE (S256). De la respuesta del
hub, notarum comprueba, antes de dejar entrar a nadie:

- La **firma** RS256 de los dos tokens, contra las claves que publica el hub
  en `/jwks.json`. Se rechaza cualquier otro algoritmo: aceptar el que declara
  el token es el agujero clásico de JWT.
- Que las claves tengan **al menos 2048 bits**.
- El **emisor** y la **audiencia**: un token bien firmado pero para otra app de
  la suite no sirve acá.
- El **vencimiento**, con un minuto de tolerancia por los relojes.
- El **nonce**, que ata la respuesta al pedido que la empezó.
- El **estado**, que ata la vuelta a la ida. Sin esto, cualquiera podría armar
  el enlace de vuelta y hacer entrar a otra persona con un código propio.
- Que los dos tokens hablen de **la misma persona**.

Los secretos de la transacción viajan firmados en una cookie de diez minutos,
no en la memoria del servidor: dos instancias detrás de un balanceador tienen
que poder atender la ida y la vuelta indistintamente.

## Probarlo

```bash
docker run --rm -p 8080:8080 \
  -e NOTARUM_AUTH=federado \
  -e LOCKATUS_ISSUER=http://host.docker.internal:8081 \
  -e LOCKATUS_CLIENT_ID=notarum \
  -e LOCKATUS_REDIRECT_URI=http://localhost:8080/entrar/lockatus/volver \
  -e NOTARUM_SECRETO_SESION=una-frase-larga-de-al-menos-32-caracteres \
  -v notarum-datos:/datos \
  ghcr.io/diegoparras/notarum:latest
```

Si algo falta, notarum lo dice al arrancar y no cuando alguien intenta entrar.

## Cuando no se puede entrar

Cada motivo tiene su pantalla, porque no llevan a lo mismo:

| Lo que dice                              | Qué pasó                                         |
|------------------------------------------|--------------------------------------------------|
| Lockatus no te dio acceso a esta instancia | La cuenta existe en el hub pero no tiene rol para notarum. Hay que pedirlo. |
| Se venció el intento de entrada          | Pasaron más de diez minutos, o se empezó en otro navegador. |
| Esta vuelta no corresponde a la ida      | El estado no coincide. Empezar de nuevo desde `/entrar`. |
| No se pudieron verificar los datos del hub | La firma, el emisor o el nonce no cerraron. El detalle queda en el registro del servidor. |
