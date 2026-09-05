package cuentas

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// almacenMemoria es el mínimo que el registro necesita; los motores reales ya
// tienen su propia suite de conformidad.
type almacenMemoria struct {
	mu    sync.Mutex
	datos map[string][]byte
}

func nuevoAlmacen() *almacenMemoria {
	return &almacenMemoria{datos: map[string][]byte{}}
}

func (a *almacenMemoria) Leer(clave string) ([]byte, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.datos[clave]
	return v, ok
}

func (a *almacenMemoria) Guardar(clave string, datos []byte, _ time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	copia := make([]byte, len(datos))
	copy(copia, datos)
	a.datos[clave] = copia
	return nil
}

func (a *almacenMemoria) Existe(clave string) bool {
	_, ok := a.Leer(clave)
	return ok
}

func (a *almacenMemoria) Borrar(clave string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.datos, clave)
	return nil
}

func nuevoRegistro(t *testing.T) (*Registro, *almacenMemoria) {
	t.Helper()
	alm := nuevoAlmacen()
	r, err := NuevoRegistro(alm, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return r, alm
}

// Mientras no haya cuentas, notarum se comporta como siempre.
func TestSinUsuariosNoHayLogin(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if r.HayUsuarios() {
		t.Error("dice que hay usuarios sin haber creado ninguno")
	}
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	if !r.HayUsuarios() {
		t.Error("no se encendió el login al crear la primera cuenta")
	}
}

func TestCrearYAutenticar(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	u, err := r.Autenticar("diego", "una frase larga y tranquila")
	if err != nil {
		t.Fatal(err)
	}
	if u.Rol != RolAdmin {
		t.Errorf("rol = %q", u.Rol)
	}
	// Mayúsculas y espacios no crean otra cuenta.
	if _, err := r.Autenticar("  DIEGO ", "una frase larga y tranquila"); err != nil {
		t.Errorf("el nombre no se normalizó al entrar: %v", err)
	}
}

// Un usuario que no existe y una clave equivocada dan el mismo error: si
// difirieran, se podría averiguar qué cuentas existen.
func TestElErrorNoRevelaSiLaCuentaExiste(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	_, errClave := r.Autenticar("diego", "la clave equivocada de acá")
	_, errUsuario := r.Autenticar("nadie", "la clave equivocada de acá")

	if !errors.Is(errClave, ErrCredenciales) || !errors.Is(errUsuario, ErrCredenciales) {
		t.Fatalf("errores distintos: clave=%v usuario=%v", errClave, errUsuario)
	}
	if errClave.Error() != errUsuario.Error() {
		t.Errorf("los mensajes difieren:\n  clave:   %v\n  usuario: %v", errClave, errUsuario)
	}
}

func TestNoSeRepitenNombres(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"diego", "DIEGO", " diego "} {
		if _, err := r.CrearUsuario(n, "otra frase larga distinta", RolPersona); !errors.Is(err, ErrYaExiste) {
			t.Errorf("crear %q dio %v", n, err)
		}
	}
}

func TestCrearUsuarioValida(t *testing.T) {
	r, _ := nuevoRegistro(t)
	casos := []struct {
		nombre, clave string
		rol           Rol
	}{
		{"ab", "una frase larga y tranquila", RolAdmin},           // nombre corto
		{"diego", "corta", RolAdmin},                              // clave corta
		{"diego", "una frase larga y tranquila", "jefe"},          // rol inventado
		{"Diego Parras", "una frase larga y tranquila", RolAdmin}, // nombre con espacio
	}
	for _, c := range casos {
		if _, err := r.CrearUsuario(c.nombre, c.clave, c.rol); err == nil {
			t.Errorf("se aceptó %+v", c)
		}
	}
}

func TestCambiarClave(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	// Con la clave actual equivocada no se cambia nada.
	if err := r.CambiarClave("diego", "la que no es amiga", "otra frase larga nueva"); err == nil {
		t.Error("se cambió la clave sin saber la actual")
	}
	if err := r.CambiarClave("diego", "una frase larga y tranquila", "otra frase larga nueva"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Autenticar("diego", "otra frase larga nueva"); err != nil {
		t.Error("la clave nueva no anda")
	}
	if _, err := r.Autenticar("diego", "una frase larga y tranquila"); err == nil {
		t.Error("la clave vieja sigue andando")
	}
	// Una clave nueva que no cumple el mínimo se rechaza.
	if err := r.CambiarClave("diego", "otra frase larga nueva", "corta"); err == nil {
		t.Error("se aceptó una clave nueva corta")
	}
}

// ---------------------------------------------------------------- tokens

func TestCrearYVerificarToken(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	tok, valor, err := r.CrearToken("diego", "mi script", AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if valor == "" || tok.Huella == "" {
		t.Fatal("no se generó el token")
	}
	verificado, u, err := r.VerificarToken(valor, AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if verificado.ID != tok.ID || u.Nombre != "diego" {
		t.Errorf("verificado = %+v, usuario = %+v", verificado, u)
	}
}

// El valor del token no puede quedar guardado: sólo su huella.
func TestElValorDelTokenNoSeGuarda(t *testing.T) {
	r, alm := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	_, valor, err := r.CrearToken("diego", "mi script", AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	alm.mu.Lock()
	defer alm.mu.Unlock()
	for clave, datos := range alm.datos {
		if strings.Contains(string(datos), valor) {
			t.Fatalf("el token quedó guardado en claro en %q", clave)
		}
	}
}

func TestTokenConAlcanceEquivocado(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	_, valor, err := r.CrearToken("diego", "para la api", AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.VerificarToken(valor, AlcanceMCP); err == nil {
		t.Error("un token de api sirvió para el mcp")
	}
	// Sin pedir alcance, sirve igual.
	if _, _, err := r.VerificarToken(valor, ""); err != nil {
		t.Errorf("sin alcance debería valer: %v", err)
	}
}

func TestTokenRevocado(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	tok, valor, err := r.CrearToken("diego", "mi script", AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RevocarToken("diego", tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.VerificarToken(valor, AlcanceAPI); !errors.Is(err, ErrRevocado) {
		t.Errorf("err = %v, se esperaba ErrRevocado", err)
	}
	// Revocar dos veces es inocuo.
	if err := r.RevocarToken("diego", tok.ID); err != nil {
		t.Errorf("revocar dos veces dio %v", err)
	}
	// Y el token sigue en la lista, marcado.
	tokens := r.Tokens("diego")
	if len(tokens) != 1 || tokens[0].Activo() {
		t.Errorf("tokens = %+v", tokens)
	}
}

// Nadie puede revocar el token de otro.
func TestNoSeRevocaElTokenAjeno(t *testing.T) {
	r, _ := nuevoRegistro(t)
	for _, n := range []string{"diego", "ajena"} {
		if _, err := r.CrearUsuario(n, "una frase larga y tranquila", RolPersona); err != nil {
			t.Fatal(err)
		}
	}
	tok, valor, err := r.CrearToken("diego", "mi script", AlcanceAPI)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RevocarToken("ajena", tok.ID); !errors.Is(err, ErrNoExiste) {
		t.Errorf("se pudo revocar un token ajeno: %v", err)
	}
	if _, _, err := r.VerificarToken(valor, AlcanceAPI); err != nil {
		t.Errorf("el token quedó revocado por alguien ajeno: %v", err)
	}
}

func TestTokenesSoloDeSuDueño(t *testing.T) {
	r, _ := nuevoRegistro(t)
	for _, n := range []string{"diego", "ajena"} {
		if _, err := r.CrearUsuario(n, "una frase larga y tranquila", RolPersona); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.CrearToken("diego", "uno", AlcanceAPI); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CrearToken("diego", "dos", AlcanceMCP); err != nil {
		t.Fatal(err)
	}
	if n := len(r.Tokens("diego")); n != 2 {
		t.Errorf("diego tiene %d tokens", n)
	}
	if n := len(r.Tokens("ajena")); n != 0 {
		t.Errorf("ajena ve %d tokens que no son suyos", n)
	}
}

func TestCrearTokenValida(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CrearToken("nadie", "x", AlcanceAPI); err == nil {
		t.Error("se creó un token para un usuario inexistente")
	}
	if _, _, err := r.CrearToken("diego", "", AlcanceAPI); err == nil {
		t.Error("se creó un token sin nombre")
	}
	if _, _, err := r.CrearToken("diego", "x", "cualquiera"); err == nil {
		t.Error("se creó un token con un alcance inventado")
	}
	if _, _, err := r.CrearToken("diego", strings.Repeat("x", 100), AlcanceAPI); err == nil {
		t.Error("se aceptó un nombre larguísimo")
	}
}

func TestTokenInventado(t *testing.T) {
	r, _ := nuevoRegistro(t)
	for _, valor := range []string{"", "ntrm_inventado", "cualquier cosa", PrefijoToken} {
		if _, _, err := r.VerificarToken(valor, AlcanceAPI); err == nil {
			t.Errorf("se aceptó el token %q", valor)
		}
	}
}

// -------------------------------------------------------------- sesiones

func TestSesionSeFirmaYSeLee(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	cookie := r.FirmarSesion("diego", time.Now().Add(DuracionSesion))
	u, err := r.LeerSesion(cookie)
	if err != nil {
		t.Fatal(err)
	}
	if u.Nombre != "diego" {
		t.Errorf("usuario = %q", u.Nombre)
	}
}

// Una cookie forjada o alterada no puede dejar entrar a nadie.
func TestSesionForjada(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CrearUsuario("ajena", "una frase larga y tranquila", RolPersona); err != nil {
		t.Fatal(err)
	}
	valida := r.FirmarSesion("ajena", time.Now().Add(time.Hour))
	partes := strings.Split(valida, "|")

	forjadas := []string{
		"",
		"diego",
		"diego|9999999999",                     // sin firma
		"diego|9999999999|firmainventada",      // firma inventada
		"diego|" + partes[1] + "|" + partes[2], // la firma de otro usuario
		"ajena|9999999999|" + partes[2],        // fecha cambiada
		valida + "x",                           // firma alterada
	}
	for _, c := range forjadas {
		if u, err := r.LeerSesion(c); err == nil {
			t.Errorf("la cookie %q dejó entrar como %q", c, u.Nombre)
		}
	}
}

func TestSesionVencida(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	vencida := r.FirmarSesion("diego", time.Now().Add(-time.Minute))
	if _, err := r.LeerSesion(vencida); err == nil {
		t.Error("una sesión vencida dejó entrar")
	}
}

// Cambiar el secreto invalida todas las sesiones abiertas: es lo que se busca
// al rotarlo.
func TestRotarElSecretoCierraLasSesiones(t *testing.T) {
	alm := nuevoAlmacen()
	viejo, err := NuevoRegistro(alm, []byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := viejo.CrearUsuario("diego", "una frase larga y tranquila", RolAdmin); err != nil {
		t.Fatal(err)
	}
	cookie := viejo.FirmarSesion("diego", time.Now().Add(time.Hour))

	nuevo, err := NuevoRegistro(alm, []byte(strings.Repeat("b", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nuevo.LeerSesion(cookie); err == nil {
		t.Error("la sesión sobrevivió al cambio de secreto")
	}
	// Pero la cuenta sigue estando.
	if _, err := nuevo.Autenticar("diego", "una frase larga y tranquila"); err != nil {
		t.Errorf("la cuenta no sobrevivió: %v", err)
	}
}

func TestRegistroNecesitaUnSecretoDecente(t *testing.T) {
	alm := nuevoAlmacen()
	for _, s := range [][]byte{nil, []byte("corto"), []byte(strings.Repeat("x", 31))} {
		if _, err := NuevoRegistro(alm, s); err == nil {
			t.Errorf("se aceptó un secreto de %d bytes", len(s))
		}
	}
	if _, err := NuevoRegistro(nil, []byte(strings.Repeat("x", 32))); err == nil {
		t.Error("se aceptó un registro sin almacén")
	}
}

// ------------------------------------------------------- login federado

func TestEntrarFederadoCreaYActualiza(t *testing.T) {
	r, _ := nuevoRegistro(t)

	u, err := r.EntrarFederado("Diego@Ejemplo.AR", RolPersona)
	if err != nil {
		t.Fatal(err)
	}
	if u.Nombre != "diego@ejemplo.ar" {
		t.Errorf("nombre = %q; tendría que normalizarse", u.Nombre)
	}
	if !u.Externo {
		t.Error("la cuenta no quedó marcada como externa")
	}
	if u.Clave.Hash != "" {
		t.Error("una cuenta federada no tiene por qué tener clave")
	}

	// La segunda vuelta es la misma cuenta, con el rol que diga el hub: allá
	// se administran los accesos.
	u2, err := r.EntrarFederado("diego@ejemplo.ar", RolAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if u2.Rol != RolAdmin {
		t.Errorf("rol = %q; el hub lo había subido a admin", u2.Rol)
	}
	guardado, err := r.Usuario("diego@ejemplo.ar")
	if err != nil || guardado.Rol != RolAdmin {
		t.Errorf("el rol nuevo no se guardó: %+v %v", guardado, err)
	}
	// Y bajarlo también tiene efecto: si no, quitar un acceso en el hub no
	// alcanzaría.
	if _, err := r.EntrarFederado("diego@ejemplo.ar", RolPersona); err != nil {
		t.Fatal(err)
	}
	if g, _ := r.Usuario("diego@ejemplo.ar"); g.Rol != RolPersona {
		t.Errorf("rol = %q; el hub se lo había bajado", g.Rol)
	}
}

// Dos correos con la misma parte de adelante son dos personas distintas.
func TestDosDominiosSonDosPersonas(t *testing.T) {
	r, _ := nuevoRegistro(t)
	uno, err := r.EntrarFederado("diego@una.org", RolPersona)
	if err != nil {
		t.Fatal(err)
	}
	otro, err := r.EntrarFederado("diego@otra.org", RolPersona)
	if err != nil {
		t.Fatal(err)
	}
	if uno.Nombre == otro.Nombre {
		t.Fatalf("las dos cuentas quedaron en %q", uno.Nombre)
	}
}

// El hub no puede quedarse con una cuenta local.
func TestElHubNoSePuedeQuedarConUnaCuentaLocal(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolPersona); err != nil {
		t.Fatal(err)
	}
	// Un nombre local no lleva arroba, así que esto ni siquiera es un nombre
	// externo válido; que falle es lo correcto por partida doble.
	if _, err := r.EntrarFederado("diego", RolAdmin); err == nil {
		t.Fatal("el hub tomó una cuenta local")
	}
	u, _ := r.Usuario("diego")
	if u.Externo || u.Rol != RolPersona {
		t.Errorf("la cuenta local quedó tocada: %+v", u)
	}
}

// Por el formulario no se entra a una cuenta federada: no tiene clave, y
// aceptar la vacía sería dejarla abierta.
func TestUnaCuentaFederadaNoEntraPorElFormulario(t *testing.T) {
	r, _ := nuevoRegistro(t)
	if _, err := r.EntrarFederado("diego@ejemplo.ar", RolAdmin); err != nil {
		t.Fatal(err)
	}
	for _, intento := range []string{"", " ", "una frase larga y tranquila"} {
		if _, err := r.Autenticar("diego@ejemplo.ar", intento); err == nil {
			t.Errorf("se entró a una cuenta federada con la clave %q", intento)
		}
	}
}

func TestCorreosQueNoSeAceptan(t *testing.T) {
	r, _ := nuevoRegistro(t)
	for _, malo := range []string{"", "diego", "@ejemplo.ar", "diego@", "a@b@c",
		"diego ejemplo@x.ar", "die\ngo@ejemplo.ar", "<script>@x.ar", "diego@ejem plo.ar"} {
		if _, err := r.EntrarFederado(malo, RolPersona); err == nil {
			t.Errorf("se aceptó el correo %q", malo)
		}
	}
	// Los espacios de los bordes sí se perdonan: son un descuido, no otro
	// correo.
	if _, err := r.EntrarFederado("  diego@ejemplo.ar \n", RolPersona); err != nil {
		t.Errorf("no se aceptó un correo con espacios al costado: %v", err)
	}
}

// ------------------------------------------------------------- sellado

func TestSellarYAbrir(t *testing.T) {
	r, _ := nuevoRegistro(t)
	dentro := `{"v":"un verificador","e":"un estado"}`
	sello := r.Sellar("oidc", dentro, time.Now().Add(time.Minute))

	vuelta, err := r.Abrir("oidc", sello)
	if err != nil || vuelta != dentro {
		t.Fatalf("abrir = %q, %v", vuelta, err)
	}
	// El contenido no viaja a la vista, pero tampoco es un secreto: lo que
	// importa es que no se pueda cambiar.
	if _, err := r.Abrir("oidc", sello[:len(sello)-1]+"X"); err == nil {
		t.Error("se abrió un sello con la firma cambiada")
	}
}

// Un sello hecho para una cosa no vale para otra: si valiera, una transacción
// a medio hacer podría presentarse como una sesión abierta.
func TestUnSelloNoSirveParaOtroProposito(t *testing.T) {
	r, _ := nuevoRegistro(t)
	sello := r.Sellar("oidc", "diego", time.Now().Add(time.Minute))
	if _, err := r.Abrir("sesion", sello); err == nil {
		t.Fatal("un sello de transacción pasó por uno de sesión")
	}
	if _, err := r.LeerSesion(sello); err == nil {
		t.Fatal("un sello de transacción pasó por una cookie de sesión")
	}
}

func TestUnSelloVencido(t *testing.T) {
	r, _ := nuevoRegistro(t)
	sello := r.Sellar("oidc", "diego", time.Now().Add(-time.Second))
	if _, err := r.Abrir("oidc", sello); err == nil {
		t.Fatal("se abrió un sello vencido")
	}
}
