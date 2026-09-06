// Package alertas avisa cuando aparece algo nuevo que coincide con lo que
// alguien está esperando.
//
// Es la diferencia entre un servicio que se consulta y uno que avisa. Quien
// sigue un tema —un organismo, una expropiación, una materia— hoy tiene que
// acordarse de entrar a buscar. Con esto, notarum mira después de cada
// actualización y manda lo nuevo a donde le digan.
//
// Sólo lo nuevo: una alerta que repite todos los días lo mismo se ignora a la
// semana, y entonces no sirve para nada. Por eso cada una recuerda qué ya
// avisó.
package alertas

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Fuente es dónde mira una alerta.
type Fuente string

const (
	// FuenteBoletin mira los avisos del Boletín Oficial.
	FuenteBoletin Fuente = "boletin"
	// FuenteNacional mira el catálogo de InfoLEG.
	FuenteNacional Fuente = "nacional"
	// FuenteProvincial mira la base SAIJ de las provincias.
	FuenteProvincial Fuente = "provincial"
)

// Fuentes son las que se pueden elegir, en el orden en que se muestran.
var Fuentes = []Fuente{FuenteBoletin, FuenteNacional, FuenteProvincial}

func (f Fuente) Valida() bool {
	for _, v := range Fuentes {
		if f == v {
			return true
		}
	}
	return false
}

// Nombre es cómo se llama esta fuente en la interfaz.
func (f Fuente) Nombre() string {
	switch f {
	case FuenteBoletin:
		return "Boletín Oficial"
	case FuenteNacional:
		return "normativa nacional"
	case FuenteProvincial:
		return "normativa provincial"
	}
	return string(f)
}

// Criterios es qué se está esperando.
type Criterios struct {
	Texto string `json:"texto"`
	Tipo  string `json:"tipo,omitempty"`
	// Provincia sólo aplica a la fuente provincial.
	Provincia string `json:"provincia,omitempty"`
	// Seccion sólo aplica al Boletín.
	Seccion string `json:"seccion,omitempty"`
	// SoloVigentes deja fuera lo derogado, en la provincial.
	SoloVigentes bool `json:"solo_vigentes,omitempty"`
}

// Coincidencia es algo que apareció y coincide.
type Coincidencia struct {
	// ID identifica esto dentro de su fuente. Es con lo que se sabe si ya se
	// avisó: sin un identificador estable, cada corrida avisaría de nuevo.
	ID      string `json:"id"`
	Titulo  string `json:"titulo"`
	Detalle string `json:"detalle,omitempty"`
	Fecha   string `json:"fecha,omitempty"`
	// Enlace es dónde verlo en esta instancia.
	Enlace string `json:"enlace"`
}

// Alerta es una búsqueda guardada que avisa cuando aparece algo nuevo.
type Alerta struct {
	ID        string    `json:"id"`
	Dueño     string    `json:"dueno"`
	Nombre    string    `json:"nombre"`
	Fuente    Fuente    `json:"fuente"`
	Criterios Criterios `json:"criterios"`
	// Webhook es a dónde mandar lo nuevo. Vacío significa que sólo se muestra en
	// la cuenta.
	Webhook string    `json:"webhook,omitempty"`
	Activa  bool      `json:"activa"`
	Creada  time.Time `json:"creada"`

	// Lo que quedó de la última pasada.
	UltimaCorrida time.Time `json:"ultima_corrida,omitzero"`
	UltimoAviso   time.Time `json:"ultimo_aviso,omitzero"`
	Avisados      int       `json:"avisados"`
	Error         string    `json:"error,omitempty"`
	// Vistos son los identificadores de lo ya avisado. Se recorta: una alerta
	// que coincide con medio catálogo no es una alerta, y guardarle decenas de
	// miles de identificadores sería sostener el error.
	Vistos []string `json:"vistos,omitempty"`
	// Ultimas son las últimas coincidencias, para poder verlas en la cuenta
	// sin depender de que el webhook haya andado.
	Ultimas []Coincidencia `json:"ultimas,omitempty"`
	// ClaveFeed abre el feed de esta alerta, si se pidió uno.
	//
	// Es una clave aparte y no el token de la cuenta, y va en la dirección
	// porque un lector de feeds no manda cabeceras. Eso es una concesión: una
	// clave en una dirección se filtra por los registros y por el historial
	// del navegador. Por eso sólo abre esta alerta —no la cuenta, no la API,
	// no las otras alertas— y se puede dar de baja sin tocar nada más.
	ClaveFeed string `json:"clave_feed,omitempty"`
}

// NuevaClaveFeed genera la clave que abre el feed de esta alerta.
func NuevaClaveFeed() (string, error) {
	crudo := make([]byte, 24)
	if _, err := rand.Read(crudo); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(crudo), nil
}

// AbreElFeed compara la clave sin filtrar por el tiempo que tarda.
func (a *Alerta) AbreElFeed(clave string) bool {
	if a.ClaveFeed == "" || clave == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a.ClaveFeed), []byte(clave)) == 1
}

// MaximoVistos es cuántos identificadores recuerda una alerta.
//
// No es un límite de rendimiento: es lo que define qué es una alerta. Una
// búsqueda que coincide con miles de cosas no avisa nada útil, y conviene
// decirlo al crearla en vez de mandar mil correos.
const MaximoVistos = 2000

// MaximasUltimas es cuántas coincidencias se guardan para mirar después.
const MaximasUltimas = 20

// Validar revisa que una alerta tenga sentido antes de guardarla.
func (a *Alerta) Validar() error {
	a.Nombre = strings.TrimSpace(a.Nombre)
	if a.Nombre == "" {
		return errors.New("ponele un nombre, para reconocerla después")
	}
	if len(a.Nombre) > 80 {
		return errors.New("el nombre es muy largo")
	}
	if !a.Fuente.Valida() {
		return errors.New("elegí dónde mirar: el Boletín, la normativa nacional o la provincial")
	}
	a.Criterios.Texto = strings.TrimSpace(a.Criterios.Texto)
	if a.Criterios.Texto == "" && a.Criterios.Tipo == "" && a.Criterios.Provincia == "" {
		// Sin ningún criterio la alerta coincide con todo, y avisar de todo es
		// lo mismo que no avisar.
		return errors.New("poné al menos un texto, un tipo o una provincia: " +
			"una alerta que coincide con todo no avisa nada")
	}
	if len(a.Criterios.Texto) > 200 {
		return errors.New("el texto a buscar es muy largo")
	}
	a.Webhook = strings.TrimSpace(a.Webhook)
	if a.Webhook != "" {
		if err := ValidarWebhook(a.Webhook); err != nil {
			return err
		}
	}
	return nil
}

// Novedades separa lo que no se avisó todavía.
//
// Devuelve además la lista de vistos actualizada. Se guarda lo que coincide
// ahora y no todo lo que coincidió alguna vez: si algo deja de coincidir y
// vuelve, volver a avisarlo es lo correcto.
func (a *Alerta) Novedades(coincidencias []Coincidencia) (nuevas []Coincidencia, vistos []string) {
	yaVisto := make(map[string]bool, len(a.Vistos))
	for _, v := range a.Vistos {
		yaVisto[v] = true
	}
	vistos = make([]string, 0, len(coincidencias))
	for _, c := range coincidencias {
		if c.ID == "" {
			continue
		}
		if len(vistos) < MaximoVistos {
			vistos = append(vistos, c.ID)
		}
		if !yaVisto[c.ID] {
			nuevas = append(nuevas, c)
		}
	}
	// La primera vez no se avisa de nada: si no, estrenar una alerta sobre un
	// tema viejo mandaría de golpe todo lo que existe desde 1993.
	if a.UltimaCorrida.IsZero() {
		return nil, vistos
	}
	return nuevas, vistos
}
