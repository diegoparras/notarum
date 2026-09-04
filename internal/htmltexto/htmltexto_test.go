package htmltexto

import "strings"

import "testing"

func TestSaneaLoPeligroso(t *testing.T) {
	crudo := `<p>Visto<script>alert(1)</script> el expediente</p>
	          <style>p{color:red}</style>
	          <p onclick="robar()" style="color:red">y considerando</p>
	          <a href="javascript:mal()">un enlace</a>`
	limpio := Sanear(crudo)
	for _, prohibido := range []string{"<script", "<style", "onclick", "javascript:", "alert(1)", "color:red"} {
		if strings.Contains(limpio, prohibido) {
			t.Errorf("quedó %q en %q", prohibido, limpio)
		}
	}
	// Y el texto sobrevive.
	for _, esperado := range []string{"Visto", "el expediente", "y considerando"} {
		if !strings.Contains(limpio, esperado) {
			t.Errorf("se perdió %q", esperado)
		}
	}
}

// Las tablas son contenido en un texto legal, no decoración.
func TestConservaTablas(t *testing.T) {
	crudo := `<table><tr><th>Cargo</th><th>Nombre</th></tr>
	          <tr><td colspan="2">Director</td></tr></table>`
	limpio := Sanear(crudo)
	for _, esperado := range []string{"<table", "<tr", "<th", "<td", "colspan", "Cargo", "Director"} {
		if !strings.Contains(limpio, esperado) {
			t.Errorf("se perdió %q en %q", esperado, limpio)
		}
	}
}

// El Boletín manda html y body anidados adentro del cuerpo del aviso.
func TestSacaHTMLyBodyAnidados(t *testing.T) {
	crudo := `<p><style>x{}</style><html><body><p>Ciudad de Buenos Aires</p></body></html></p>`
	limpio := Sanear(crudo)
	if strings.Contains(limpio, "<html") || strings.Contains(limpio, "<body") {
		t.Errorf("quedaron los anidados: %q", limpio)
	}
	if !strings.Contains(limpio, "Ciudad de Buenos Aires") {
		t.Errorf("se perdió el texto: %q", limpio)
	}
}

func TestAPlano(t *testing.T) {
	plano := APlano(`<p>Primer párrafo</p><p>Segundo párrafo</p>`)
	if plano != "Primer párrafo\n\nSegundo párrafo" {
		t.Errorf("= %q", plano)
	}
}

func TestAPlanoSaltosYCeldas(t *testing.T) {
	if got := APlano(`<p>una<br>dos</p>`); got != "una\ndos" {
		t.Errorf("br = %q", got)
	}
	got := APlano(`<table><tr><td>a</td><td>b</td></tr></table>`)
	if !strings.Contains(got, "a\tb") {
		t.Errorf("celdas = %q", got)
	}
}

// Los espacios no separables del HTML no pueden llegar al texto plano.
func TestAPlanoNormalizaEspacios(t *testing.T) {
	got := APlano("<p>Ley N° 22.520   y    sus  modificatorias</p>")
	if strings.Contains(got, " ") {
		t.Errorf("quedó un espacio no separable: %q", got)
	}
	if got != "Ley N° 22.520 y sus modificatorias" {
		t.Errorf("= %q", got)
	}
}

func TestAPlanoIgnoraScriptYStyle(t *testing.T) {
	got := APlano(`<p>texto</p><script>var x=1</script><style>p{}</style>`)
	if strings.Contains(got, "var x") || strings.Contains(got, "p{}") {
		t.Errorf("= %q", got)
	}
}

func TestDesdeLatin1(t *testing.T) {
	// "Cámara" tal como lo manda InfoLEG: la á es un solo byte 0xE1.
	crudo := []byte{'C', 0xe1, 'm', 'a', 'r', 'a'}
	if got := DesdeLatin1(crudo); got != "Cámara" {
		t.Errorf("= %q, se esperaba Cámara", got)
	}
	// Todo el juego de acentos que aparece en los textos legales.
	pares := map[byte]rune{
		0xc1: 'Á', 0xc9: 'É', 0xcd: 'Í', 0xd1: 'Ñ', 0xda: 'Ú',
		0xe1: 'á', 0xe9: 'é', 0xed: 'í', 0xf1: 'ñ', 0xf3: 'ó', 0xfa: 'ú',
		0xb0: '°', 0xaa: 'ª', 0xba: 'º',
	}
	for b, esperado := range pares {
		if got := DesdeLatin1([]byte{b}); got != string(esperado) {
			t.Errorf("byte %#x = %q, se esperaba %q", b, got, string(esperado))
		}
	}
}

// El tramo donde windows-1252 se aparta de latin-1: son los signos de
// puntuación que aparecen en documentos hechos en Windows.
func TestDesdeLatin1TramoWindows(t *testing.T) {
	casos := map[byte]string{
		0x93: `“`, 0x94: `”`, 0x96: "–", 0x97: "—", 0x92: `’`,
		0x81: "", // indefinido: se descarta en vez de meter basura
	}
	for b, esperado := range casos {
		if got := DesdeLatin1([]byte{b}); got != esperado {
			t.Errorf("byte %#x = %q, se esperaba %q", b, got, esperado)
		}
	}
}

func TestDesdeLatin1NoTocaASCII(t *testing.T) {
	ascii := "Decreto 845/2026 - DECTO-2026-845-APN-PTE (art. 1°)"
	sinGrado := strings.ReplaceAll(ascii, "°", "")
	if got := DesdeLatin1([]byte(sinGrado)); got != sinGrado {
		t.Errorf("= %q", got)
	}
}

func TestVacios(t *testing.T) {
	if Sanear("") != "" {
		t.Error("Sanear vacío")
	}
	if APlano("") != "" {
		t.Error("APlano vacío")
	}
	if DesdeLatin1(nil) != "" {
		t.Error("DesdeLatin1 nil")
	}
}
