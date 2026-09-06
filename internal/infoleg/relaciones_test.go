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
