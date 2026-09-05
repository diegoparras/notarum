package cuentas

import (
	"errors"
	"strings"
	"testing"
)

func conUsuario(t *testing.T) (*Registro, *almacenMemoria) {
	t.Helper()
	r, alm := nuevoRegistro(t)
	if _, err := r.CrearUsuario("diego", "una frase larga y tranquila", RolPersona); err != nil {
		t.Fatal(err)
	}
	return r, alm
}

func TestGuardarYUsarLaClaveIA(t *testing.T) {
	r, _ := conUsuario(t)
	const clave = "sk-or-v1-0123456789abcdef0123456789abcdef"

	if r.TieneClaveIA("diego") {
		t.Error("dice tener una clave antes de cargarla")
	}
	if err := r.GuardarClaveIA("diego", clave); err != nil {
		t.Fatal(err)
	}
	if !r.TieneClaveIA("diego") {
		t.Error("no reconoce la clave recién cargada")
	}
	vuelta, err := r.ClaveIA("diego")
	if err != nil || vuelta != clave {
		t.Fatalf("la clave volvió como %q (%v)", vuelta, err)
	}
}

// La clave no puede quedar en claro en el almacén: es una credencial ajena
// que da acceso a una cuenta con saldo.
func TestLaClaveIANoSeGuardaEnClaro(t *testing.T) {
	r, alm := conUsuario(t)
	const clave = "sk-or-v1-secretisimo0123456789abcdef"
	if err := r.GuardarClaveIA("diego", clave); err != nil {
		t.Fatal(err)
	}
	for k, v := range alm.datos {
		if strings.Contains(string(v), clave) {
			t.Fatalf("la clave está en claro en %q", k)
		}
		if strings.Contains(string(v), "secretisimo") {
			t.Fatalf("se ve un pedazo de la clave en %q", k)
		}
	}
}

// Y tampoco se muestra de vuelta: sólo lo justo para reconocer cuál se cargó.
func TestLaPistaNoAlcanzaParaUsarla(t *testing.T) {
	r, _ := conUsuario(t)
	const clave = "sk-or-v1-0123456789abcdef0123456789abcdef"
	r.GuardarClaveIA("diego", clave)

	pista, hay := r.PistaClaveIA("diego")
	if !hay {
		t.Fatal("no hay pista")
	}
	if pista == clave {
		t.Fatal("la pista es la clave entera")
	}
	if len(pista) > len(clave)/2 {
		t.Errorf("la pista muestra demasiado: %q", pista)
	}
	// Deja reconocerla, eso sí.
	if !strings.HasSuffix(pista, "cdef") {
		t.Errorf("la pista no deja reconocer cuál es: %q", pista)
	}
}

// Dos personas tienen la suya, y una no ve la de la otra.
func TestCadaCuentaTieneLaSuya(t *testing.T) {
	r, _ := conUsuario(t)
	if _, err := r.CrearUsuario("ajena", "otra frase larga y tranquila", RolPersona); err != nil {
		t.Fatal(err)
	}
	r.GuardarClaveIA("diego", "sk-or-v1-la-de-diego-0123456789")
	r.GuardarClaveIA("ajena", "sk-or-v1-la-de-ajena-0123456789")

	deDiego, _ := r.ClaveIA("diego")
	deAjena, _ := r.ClaveIA("ajena")
	if deDiego == deAjena {
		t.Fatal("las dos cuentas comparten la clave")
	}
	if !strings.Contains(deDiego, "diego") || !strings.Contains(deAjena, "ajena") {
		t.Errorf("se cruzaron: %q y %q", deDiego, deAjena)
	}
}

// Es de quien la cargó: se la puede llevar.
func TestBorrarLaClaveIA(t *testing.T) {
	r, _ := conUsuario(t)
	r.GuardarClaveIA("diego", "sk-or-v1-0123456789abcdef")
	if err := r.BorrarClaveIA("diego"); err != nil {
		t.Fatal(err)
	}
	if r.TieneClaveIA("diego") {
		t.Error("sigue estando después de borrarla")
	}
	if _, err := r.ClaveIA("diego"); !errors.Is(err, ErrSinClaveIA) {
		t.Errorf("err = %v", err)
	}
}

// Si alguien toca lo guardado, no descifra: mejor eso que devolver basura que
// después se manda a un proveedor.
func TestUnaClaveManoseadaNoSeAbre(t *testing.T) {
	r, alm := conUsuario(t)
	r.GuardarClaveIA("diego", "sk-or-v1-0123456789abcdef")

	clave := claveDeClaveIA("diego")
	crudo, _ := alm.Leer(clave)
	manoseado := strings.Replace(string(crudo), "cifrada\":\"", "cifrada\":\"A", 1)
	alm.Guardar(clave, []byte(manoseado), 0)

	if _, err := r.ClaveIA("diego"); err == nil {
		t.Fatal("se abrió una clave manoseada")
	}
}

// Con otro secreto no se abre lo cifrado con el anterior: hay que volver a
// cargarla, y el error tiene que decirlo.
func TestConOtroSecretoHayQueVolverACargarla(t *testing.T) {
	alm := nuevoAlmacen()
	uno, err := NuevoRegistro(alm, []byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	uno.CrearUsuario("diego", "una frase larga y tranquila", RolPersona)
	uno.GuardarClaveIA("diego", "sk-or-v1-0123456789abcdef")

	otro, err := NuevoRegistro(alm, []byte(strings.Repeat("b", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otro.ClaveIA("diego"); !errors.Is(err, ErrClaveIAIlegible) {
		t.Errorf("err = %v, se esperaba que dijera que no se puede descifrar", err)
	}
}

// El mismo texto cifrado dos veces da distinto: si diera igual, se podría
// saber quiénes cargaron la misma clave.
func TestCifrarDosVecesDaDistinto(t *testing.T) {
	r, _ := conUsuario(t)
	uno, err := r.cifrar("la misma cosa")
	if err != nil {
		t.Fatal(err)
	}
	otro, _ := r.cifrar("la misma cosa")
	if uno == otro {
		t.Fatal("cifrar dos veces lo mismo da lo mismo")
	}
	// Y las dos se abren igual.
	for _, c := range []string{uno, otro} {
		abierto, err := r.descifrar(c)
		if err != nil || abierto != "la misma cosa" {
			t.Errorf("descifrar = %q, %v", abierto, err)
		}
	}
}

func TestClavesIAQueNoSeAceptan(t *testing.T) {
	r, _ := conUsuario(t)
	for _, mala := range []string{"", "   ", strings.Repeat("x", 600)} {
		if err := r.GuardarClaveIA("diego", mala); err == nil {
			t.Errorf("se aceptó una clave de %d caracteres", len(mala))
		}
	}
	// Y una cuenta que no existe tampoco.
	if err := r.GuardarClaveIA("nadie", "sk-or-v1-0123456789"); err == nil {
		t.Error("se guardó una clave para una cuenta que no existe")
	}
}
