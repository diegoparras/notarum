package infoleg

import (
	"strings"
	"testing"
)

// Las columnas de detalle describen a la otra norma de la relación, no a la
// propia. No está documentado y los nombres de los archivos engañan, así que
// se midió contra los datos publicados: en la base de normas modificadas, un
// mismo id_norma_modificada aparece en filas con datos distintos.
const baseModificadas = `"id_norma_modificada","id_norma_modificatoria","tipo_norma","nro_norma","clase_norma","organismo_origen","fecha_boletin","titulo_sumario","titulo_resumido"
"365331","374703","Resolución","247","","SECRETARIA DE GESTION Y EMPLEO PUBLICO","2022-11-11","JEFATURA DE GABINETE","ORDEN DE MERITO - APROBACION"
"365331","380000","Decreto","12","","PODER EJECUTIVO NACIONAL","2023-01-05","JEFATURA DE GABINETE","OTRA COSA"
"365284","372676","Disposición","540","","DIRECCION DE NORMATIVA LABORAL","2022-10-05","CONVENCIONES","S.A.T.T.S.A.I.D."
`

func TestLeerLasQueModificaronAUnaNorma(t *testing.T) {
	rel, err := LeerRelaciones(strings.NewReader(baseModificadas), ModificadaPor)
	if err != nil {
		t.Fatal(err)
	}
	// Dos normas modificaron a la 365331, y cada una con sus propios datos.
	de365331 := rel[365331]
	if len(de365331) != 2 {
		t.Fatalf("la 365331 quedó con %d relaciones", len(de365331))
	}
	if de365331[0].ID != 374703 || de365331[1].ID != 380000 {
		t.Errorf("los identificadores quedaron %d y %d", de365331[0].ID, de365331[1].ID)
	}
	// Y los datos son los de la que modificó, que es el dato que falta.
	if de365331[0].Tipo != "Resolución" || de365331[0].Numero != "247" {
		t.Errorf("la primera quedó como %+v", de365331[0])
	}
	if de365331[0].Descripcion() != "Resolución 247" {
		t.Errorf("se describe como %q", de365331[0].Descripcion())
	}
	if len(rel[365284]) != 1 {
		t.Errorf("la 365284 quedó con %d", len(rel[365284]))
	}
}

// El otro sentido se lee del otro archivo, y la norma propia es la otra
// columna: leerlo con el sentido equivocado daría el índice al revés.
func TestElSentidoCambiaCualEsLaNormaPropia(t *testing.T) {
	const baseModificatorias = `"id_norma_modificatoria","id_norma_modificada","tipo_norma","nro_norma","fecha_boletin"
"374703","365331","Resolución","95","2022-05-26"
"374703","365400","Resolución","96","2022-06-01"
`
	rel, err := LeerRelaciones(strings.NewReader(baseModificatorias), ModificaA)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel[374703]) != 2 {
		t.Fatalf("la 374703 modifica a %d normas", len(rel[374703]))
	}
	if rel[374703][0].ID != 365331 {
		t.Errorf("la primera modificada es %d", rel[374703][0].ID)
	}
	// Y no quedó indexado por la modificada, que sería el índice al revés.
	if len(rel[365331]) != 0 {
		t.Error("quedó indexado por la norma equivocada")
	}
}

// Una fila rota no puede tirar abajo un archivo de cientos de miles: lo que se
// pierde es una relación, no el resto.
func TestUnaFilaRotaNoTiraElArchivo(t *testing.T) {
	const conBasura = `"id_norma_modificada","id_norma_modificatoria","tipo_norma"
"1","2","Ley"
"","","Ley"
"no es un numero","4","Ley"
"5","6","Decreto"
`
	rel, err := LeerRelaciones(strings.NewReader(conBasura), ModificadaPor)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel) != 2 || len(rel[1]) != 1 || len(rel[5]) != 1 {
		t.Errorf("quedaron %d normas con relaciones: %v", len(rel), rel)
	}
}

// Si el portal cambia las columnas, hay que enterarse acá y no servir un
// índice vacío como si estuviera todo bien.
func TestSinLasColumnasEsperadasSeAvisa(t *testing.T) {
	const otraCosa = `"a","b"
"1","2"
`
	if _, err := LeerRelaciones(strings.NewReader(otraCosa), ModificadaPor); err == nil {
		t.Error("se aceptó un archivo sin las columnas de identificador")
	}
}

// El reparto es extremo, medido contra los datos publicados: el promedio son
// 3,7 modificatorias por norma, pero la ley 14250 —convenios colectivos— tiene
// 42.427, porque cada convenio homologado figura como una modificación suya.
// Sin tope, esa sola norma sería quince megas en una entrada del almacén y una
// página con cuarenta mil enlaces.
func TestSeGuardanLasMasNuevasYSeCuentanTodas(t *testing.T) {
	var muchas []Relacion
	for i := 0; i < MaximoPorNorma+50; i++ {
		muchas = append(muchas, Relacion{
			ID: i, Fecha: "2020-01-" + dosDigitos(1+i%28),
		})
	}
	r := Recortar(muchas)
	if r.Total != MaximoPorNorma+50 {
		t.Errorf("el total quedó en %d y hay %d", r.Total, len(muchas))
	}
	if len(r.Normas) != MaximoPorNorma {
		t.Errorf("se guardaron %d", len(r.Normas))
	}
	if !r.Recortada() {
		t.Error("no avisa que quedaron afuera")
	}
	// De la más nueva a la más vieja: cuando alguien pregunta qué le pasó a
	// una ley, lo último es lo que está buscando.
	for i := 1; i < len(r.Normas); i++ {
		if r.Normas[i-1].Fecha < r.Normas[i].Fecha {
			t.Fatalf("quedaron desordenadas en %d: %s antes que %s",
				i, r.Normas[i-1].Fecha, r.Normas[i].Fecha)
		}
	}
}

// Y lo que entra sin recortar no dice que se recortó.
func TestLoQueEntraNoSeMarcaComoRecortado(t *testing.T) {
	r := Recortar([]Relacion{{ID: 1, Fecha: "2020-01-01"}, {ID: 2, Fecha: "2021-01-01"}})
	if r.Recortada() || r.Total != 2 || len(r.Normas) != 2 {
		t.Errorf("quedó %+v", r)
	}
	if r.Normas[0].ID != 2 {
		t.Error("no quedó primero el más nuevo")
	}
}

func dosDigitos(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
