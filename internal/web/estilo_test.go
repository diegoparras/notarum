package web

import (
	"regexp"
	"strings"
	"testing"
)

// La hoja de estilos, comprobada como texto.
//
// No reemplaza a mirar la pantalla, pero sí atrapa la clase de error que la
// dejó desalineada: una regla que enumera tipos de campo y se olvida de uno, y
// una variable que no existe.

func hojaDeEstilos(t *testing.T) string {
	t.Helper()
	crudo, err := archivosEstaticos.ReadFile("estatico/estilo.css")
	if err != nil {
		t.Fatal(err)
	}
	// Sin los comentarios: lo que se explica ahí no es lo que hace el
	// navegador, y estas comprobaciones son sobre lo que hace. Un comentario
	// que menciona una variable rota para contar que estuvo rota no es un uso.
	return sinComentarios.ReplaceAllString(string(crudo), " ")
}

var sinComentarios = regexp.MustCompile(`(?s)/\*.*?\*/`)

// Toda variable que se usa tiene que estar definida.
//
// Un var() que no existe no avisa: invalida la declaración entera en silencio.
// Así los campos del armador quedaron literalmente sin borde, y desde afuera
// parecía una decisión de diseño.
func TestNoSeUsanVariablesQueNoExisten(t *testing.T) {
	css := hojaDeEstilos(t)
	definidas := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[a-z-]+)\s*:`).FindAllStringSubmatch(css, -1) {
		definidas[m[1]] = true
	}
	if len(definidas) == 0 {
		t.Fatal("no se encontró ninguna variable definida")
	}
	for _, m := range regexp.MustCompile(`var\((--[a-z-]+)\)`).FindAllStringSubmatch(css, -1) {
		if !definidas[m[1]] {
			t.Errorf("se usa %s y no está definida: un var() inválido "+
				"invalida la declaración entera, sin avisar", m[1])
		}
	}
}

// Los campos se nombran por exclusión y no por enumeración.
//
// Enumerando tipos, el que no está en la lista se queda con el estilo del
// navegador: pasó con number, con password y con textarea, y quedaban dos
// estilos distintos en la misma pantalla. Con la exclusión, un tipo nuevo entra
// solo.
func TestLosCamposSeEstilanPorExclusion(t *testing.T) {
	css := hojaDeEstilos(t)

	// La regla base tiene que existir y cubrir input, select y textarea.
	i := strings.Index(css, "input:where(")
	if i < 0 {
		t.Fatal("no está la regla base de los campos, escrita con :where()")
	}
	regla := css[i:min(i+400, len(css))]
	for _, quiero := range []string{"select", "textarea"} {
		if !strings.Contains(regla, quiero) {
			t.Errorf("la regla base no alcanza a %s", quiero)
		}
	}

	// Y no puede haber vuelto una regla que enumere tipos de texto: es lo que
	// deja afuera al siguiente.
	if regexp.MustCompile(`input\[type="text"\]\s*,\s*input\[type=`).MatchString(css) {
		t.Error("volvió una regla que enumera tipos de campo")
	}
}

// La base va con :where() para que valga lo que un selector de elemento.
//
// Escrita con :not() encadenados pesaba más que .clave-caja input y le ganaba:
// el campo de la clave perdía el espacio del ojito y el texto le pasaba por
// abajo. Una base que le gana a sus propias excepciones no es una base.
func TestLaBaseNoLeGanaALasReglasDeContexto(t *testing.T) {
	css := hojaDeEstilos(t)
	if strings.Contains(css, `input:not([type="checkbox"]):not(`) {
		t.Error("la regla base volvió a encadenar :not(), que suma especificidad " +
			"y le gana a las reglas de contexto")
	}
	// Y las reglas de contexto que dependen de eso siguen ahí.
	for _, regla := range []string{".clave-caja input", ".armar input"} {
		if !strings.Contains(css, regla) {
			t.Errorf("falta la regla de contexto %s", regla)
		}
	}
}

// Las casillas llevan el color de la marca: sin esto quedan con el celeste del
// sistema, que no es de acá.
func TestLasCasillasLlevanElColorDeLaMarca(t *testing.T) {
	css := hojaDeEstilos(t)
	if !strings.Contains(css, "accent-color: var(--acento)") {
		t.Error("las casillas no toman el color de la marca")
	}
}
