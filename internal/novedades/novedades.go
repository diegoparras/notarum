// Package novedades lee el CHANGELOG.md (una sola fuente de verdad) y lo
// muestra en tres lugares: el cartel que se imprime PRIMERO al levantar el
// contenedor -lo que se ve en los registros del deploy en EasyPanel-, el
// aviso de novedades del lector web después de una actualización y las notas en
// markdown que imprime `notarum notas` (el CI y publicar.sh no publican sin
// ellas).
//
// Formato esperado (Keep a Changelog):
//
//	## [X.Y.Z] - AAAA-MM-DD
//	### Sección
//	- ítem (las líneas siguientes con sangría continúan el ítem)
package novedades

import (
	"regexp"
	"strconv"
	"strings"
)

// Seccion es un "### Título" con sus ítems, tal cual vienen (con markdown).
type Seccion struct {
	Titulo string   `json:"titulo"`
	Items  []string `json:"items"`
}

// Version es un "## [X.Y.Z] - AAAA-MM-DD". Fecha queda vacía si no la tiene.
type Version struct {
	Version   string    `json:"version"`
	Fecha     string    `json:"fecha"`
	Secciones []Seccion `json:"secciones"`
}

var (
	reVersion  = regexp.MustCompile(`^## \[(\d+\.\d+\.\d+)\](?:\s*-\s*(\d{4}-\d{2}-\d{2}))?`)
	reSeccion  = regexp.MustCompile(`^### (.+)`)
	reItem     = regexp.MustCompile(`^- (.*)`)
	reSigue    = regexp.MustCompile(`^\s+\S`)
	reNegrita  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reCodigo   = regexp.MustCompile("`([^`]+)`")
	reEspacios = regexp.MustCompile(`\s+`)
)

// Parsear devuelve las versiones en el orden del archivo (la más nueva
// primero). Las secciones sin ítems se descartan. Nunca falla: lo que no
// entiende lo saltea.
func Parsear(texto string) []Version {
	vs := []Version{}
	var v *Version
	var sec *Seccion
	for _, linea := range strings.Split(strings.ReplaceAll(texto, "\r\n", "\n"), "\n") {
		if m := reVersion.FindStringSubmatch(linea); m != nil {
			vs = append(vs, Version{Version: m[1], Fecha: m[2], Secciones: []Seccion{}})
			v = &vs[len(vs)-1]
			sec = nil
			continue
		}
		if v == nil {
			continue
		}
		if strings.HasPrefix(linea, "## ") {
			v, sec = nil, nil
			continue
		}
		if m := reSeccion.FindStringSubmatch(linea); m != nil {
			v.Secciones = append(v.Secciones, Seccion{Titulo: strings.TrimSpace(m[1]), Items: []string{}})
			sec = &v.Secciones[len(v.Secciones)-1]
			continue
		}
		if sec == nil {
			continue
		}
		if m := reItem.FindStringSubmatch(linea); m != nil {
			sec.Items = append(sec.Items, strings.TrimSpace(m[1]))
			continue
		}
		if reSigue.MatchString(linea) && len(sec.Items) > 0 {
			sec.Items[len(sec.Items)-1] += " " + strings.TrimSpace(linea)
		}
	}
	for i := range vs {
		con := []Seccion{}
		for _, s := range vs[i].Secciones {
			if len(s.Items) > 0 {
				con = append(con, s)
			}
		}
		vs[i].Secciones = con
	}
	return vs
}

// Comparar compara dos versiones X.Y.Z como números (0.10.0 > 0.3.0).
func Comparar(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

// Desde devuelve las versiones posteriores a `vista` y hasta `actual`
// inclusive. Sin `vista` (primera vez en ese navegador): solo la actual.
func Desde(vs []Version, vista, actual string) []Version {
	out := []Version{}
	for _, v := range vs {
		if vista == "" {
			if v.Version == actual {
				out = append(out, v)
			}
			continue
		}
		if Comparar(v.Version, vista) > 0 && Comparar(v.Version, actual) <= 0 {
			out = append(out, v)
		}
	}
	return out
}

// Hasta devuelve todas las versiones hasta `actual` inclusive (el historial).
func Hasta(vs []Version, actual string) []Version {
	out := []Version{}
	for _, v := range vs {
		if Comparar(v.Version, actual) <= 0 {
			out = append(out, v)
		}
	}
	return out
}

// Buscar devuelve la sección de una versión puntual.
func Buscar(vs []Version, version string) (Version, bool) {
	for _, v := range vs {
		if v.Version == version {
			return v, true
		}
	}
	return Version{}, false
}

func plano(s string) string {
	return reCodigo.ReplaceAllString(reNegrita.ReplaceAllString(s, "$1"), "$1")
}

// LineasLog da una línea por ítem, sin markdown, para el log del contenedor.
func LineasLog(v Version) []string {
	out := []string{}
	for _, s := range v.Secciones {
		for _, i := range s.Items {
			out = append(out, s.Titulo+": "+plano(i))
		}
	}
	return out
}

// cortar parte un texto en renglones de hasta `ancho` columnas; el primero
// lleva `pre` y los demás `sangria`. Una palabra más larga que el ancho queda
// sola en su renglón (no se parte).
func cortar(texto string, ancho int, pre, sangria string) []string {
	out := []string{}
	linea, vacia := pre, true
	for _, palabra := range reEspacios.Split(strings.TrimSpace(texto), -1) {
		if palabra == "" {
			continue
		}
		if !vacia && len([]rune(linea))+1+len([]rune(palabra)) > ancho {
			out = append(out, linea)
			linea, vacia = sangria, true
		}
		if !vacia {
			linea += " "
		}
		linea += palabra
		vacia = false
	}
	return append(out, linea)
}

// Cartel es lo que se imprime PRIMERO al levantar: versión, fecha y qué
// cambia, cortado a `ancho` columnas para leerlo sin scroll lateral en los
// registros de EasyPanel. Sin CHANGELOG o sin la sección de esta versión lo
// dice, y nada más: nunca frena el arranque.
func Cartel(app, version string, vs []Version, ancho int) []string {
	raya := strings.Repeat("=", min(ancho, 60))
	v, ok := Buscar(vs, version)
	titulo := app + " v" + version
	if ok && v.Fecha != "" {
		titulo += "  (" + v.Fecha + ")"
	}
	lineas := []string{raya, titulo}
	if !ok || len(v.Secciones) == 0 {
		lineas = append(lineas, "Sin notas para esta version en el CHANGELOG.")
	} else {
		lineas = append(lineas, "Que cambia en esta version:")
		for _, l := range LineasLog(v) {
			lineas = append(lineas, cortar(l, ancho, "- ", "    ")...)
		}
	}
	return append(lineas, raya)
}

// NotasMarkdown arma las notas de una versión en markdown (para publicar).
func NotasMarkdown(v Version) string {
	partes := []string{}
	for _, s := range v.Secciones {
		b := "### " + s.Titulo
		for _, i := range s.Items {
			b += "\n- " + i
		}
		partes = append(partes, b)
	}
	return strings.Join(partes, "\n\n") + "\n"
}
