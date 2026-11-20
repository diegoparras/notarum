package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// La clave generada de la cuenta que administra nunca va al log: queda en un
// archivo del directorio de datos, legible sólo por notarum.
func TestGuardarClaveInicialEnElDirectorioDeDatos(t *testing.T) {
	dir := t.TempDir()
	ruta, err := guardarClaveInicial(dir, "clave-de-prueba")
	if err != nil {
		t.Fatalf("no la guardó: %v", err)
	}
	if ruta != filepath.Join(dir, archivoClaveInicial) {
		t.Fatalf("ruta = %q, se esperaba dentro de %q", ruta, dir)
	}
	b, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "clave-de-prueba" {
		t.Fatalf("contenido = %q", b)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(ruta)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("permisos = %v, se esperaba 0600", info.Mode().Perm())
		}
	}
}

func TestGuardarClaveInicialSinDirectorioDevuelveError(t *testing.T) {
	noExiste := filepath.Join(t.TempDir(), "no", "existe")
	if _, err := guardarClaveInicial(noExiste, "x"); err == nil {
		t.Fatal("tenía que fallar: el directorio no existe")
	}
}

func TestAvisoDeClaveNuncaIncluyeLaClave(t *testing.T) {
	const clave = "Zq9-secreta-123"
	for _, args := range avisoClaveGenerada("admin", "/datos/"+archivoClaveInicial, nil) {
		if s, ok := args.(string); ok && strings.Contains(s, clave) {
			t.Fatal("el aviso contiene la clave")
		}
	}
	conError := avisoClaveGenerada("admin", "", os.ErrPermission)
	texto := ""
	for _, a := range conError {
		if s, ok := a.(string); ok {
			texto += s + " "
		}
	}
	if !strings.Contains(texto, "NOTARUM_ADMIN_CLAVE") {
		t.Fatalf("si no se pudo guardar, el aviso tiene que decir cómo entrar: %q", texto)
	}
}
