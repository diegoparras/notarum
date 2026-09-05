package asistente

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Los modelos que ofrece el proveedor.
//
// Escribir el nombre de un modelo a mano envejece mal: OpenRouter agrega y
// retira modelos todo el tiempo, y una lista escrita acá quedaría vieja sin
// que nadie se entere hasta que alguien elige uno que ya no existe. Se
// pregunta, y se muestra lo que hay hoy.

// Modelo es uno de los que se pueden elegir.
type Modelo struct {
	ID     string
	Nombre string
	// Contexto es cuántos tokens de entrada acepta.
	Contexto int
	// PorMillonEntrada y PorMillonSalida son el precio en dólares por millón
	// de tokens. Van así y no como vienen —por token, en notación
	// científica— porque nadie compara 0.0000004 contra 0.00000125.
	PorMillonEntrada float64
	PorMillonSalida  float64
	// Gratis es el que no cobra nada, que conviene poder ver de un vistazo.
	Gratis bool
}

// Precio escribe lo que cuesta, para mostrarlo al lado del nombre.
func (m Modelo) Precio() string {
	if m.Gratis {
		return "gratis"
	}
	return "US$" + redondear(m.PorMillonEntrada) + " / US$" + redondear(m.PorMillonSalida) + " por millón"
}

func redondear(v float64) string {
	if v < 1 {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

type modeloCrudo struct {
	ID            string `json:"id"`
	Nombre        string `json:"name"`
	ContextLength int    `json:"context_length"`
	Arquitectura  struct {
		Salidas []string `json:"output_modalities"`
	} `json:"architecture"`
	Precios struct {
		Entrada string `json:"prompt"`
		Salida  string `json:"completion"`
	} `json:"pricing"`
}

// cacheModelos guarda la lista un rato: son cientos de modelos y la misma para
// todo el mundo. Pedirla en cada carga de la página sería gastar una vuelta a
// la red por algo que cambia cada varios días.
type cacheModelos struct {
	mu      sync.Mutex
	lista   []Modelo
	traidos time.Time
}

const duraLaLista = time.Hour

var cache cacheModelos

// Modelos devuelve los que el proveedor ofrece hoy, ordenados por nombre.
//
// La clave es la de quien pregunta: el catálogo es público, pero pedirlo
// identificado es lo que corresponde y deja ver los que su cuenta habilita.
func (c *Cliente) Modelos(ctx context.Context, clave string) ([]Modelo, error) {
	cache.mu.Lock()
	if time.Since(cache.traidos) < duraLaLista && len(cache.lista) > 0 {
		lista := cache.lista
		cache.mu.Unlock()
		return lista, nil
	}
	cache.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if clave != "" {
		req.Header.Set("Authorization", "Bearer "+clave)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	crudo, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrClaveRechazada
	default:
		return nil, errors.New("el proveedor contestó " + strconv.Itoa(res.StatusCode) + " al pedir los modelos")
	}

	var cuerpo struct {
		Datos []modeloCrudo `json:"data"`
	}
	if err := json.Unmarshal(crudo, &cuerpo); err != nil {
		return nil, errors.New("no se entendió la lista de modelos del proveedor")
	}

	lista := ordenar(cuerpo.Datos)
	if len(lista) == 0 {
		return nil, errors.New("el proveedor no devolvió ningún modelo")
	}
	cache.mu.Lock()
	cache.lista, cache.traidos = lista, time.Now()
	cache.mu.Unlock()
	return lista, nil
}

// ordenar traduce y deja sólo los que sirven para esto.
func ordenar(crudos []modeloCrudo) []Modelo {
	var lista []Modelo
	for _, c := range crudos {
		if c.ID == "" || !escribeTexto(c.Arquitectura.Salidas) {
			continue
		}
		entrada := porMillon(c.Precios.Entrada)
		salida := porMillon(c.Precios.Salida)
		// Los precios negativos son el enrutador automático, que cobra lo que
		// cueste el que elija: no se puede mostrar un número que no existe.
		if entrada < 0 || salida < 0 {
			continue
		}
		nombre := c.Nombre
		if nombre == "" {
			nombre = c.ID
		}
		lista = append(lista, Modelo{
			ID: c.ID, Nombre: nombre, Contexto: c.ContextLength,
			PorMillonEntrada: entrada, PorMillonSalida: salida,
			Gratis: entrada == 0 && salida == 0,
		})
	}
	sort.Slice(lista, func(i, j int) bool {
		return strings.ToLower(lista[i].Nombre) < strings.ToLower(lista[j].Nombre)
	})
	return lista
}

// escribeTexto descarta los que devuelven imágenes o audio: acá se pide código.
func escribeTexto(salidas []string) bool {
	if len(salidas) == 0 {
		return true // el que no lo declara es de texto
	}
	for _, s := range salidas {
		if s == "text" {
			return true
		}
	}
	return false
}

// porMillon pasa el precio por token —que viene como texto, a veces en
// notación científica— a dólares por millón de tokens, que es como se compara.
func porMillon(s string) float64 {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	// Redondeado: multiplicar 0.0000008 por un millón da 0.7999999999999999,
	// y esto es un precio para mirar, no para contar centavos.
	return math.Round(v*1_000_000*1e6) / 1e6
}
