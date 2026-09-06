package alertas

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/diegoparras/notarum/internal/almacen"
)

// El guardado de las alertas.
//
// Van en el mismo almacén que todo lo demás: una alerta tiene que sobrevivir a
// un reinicio, porque quien la creó espera que siga mirando.

const (
	claveTodas = "alertas/_todas"
	prefijo    = "alertas/"
)

func claveAlerta(id string) string { return prefijo + id }
func claveDe(usuario string) string {
	return prefijo + "_de/" + strings.ToLower(strings.TrimSpace(usuario))
}

// MaximoPorCuenta es cuántas alertas puede tener una cuenta. Hay un tope
// porque cada una corre en cada actualización: sin límite, una cuenta sola
// puede volver interminable la pasada de todas.
const MaximoPorCuenta = 25

// Registro guarda y recupera alertas.
type Registro struct {
	alm almacen.Almacen
}

func NuevoRegistro(alm almacen.Almacen) *Registro { return &Registro{alm: alm} }

// Crear guarda una alerta nueva.
func (r *Registro) Crear(a Alerta) (*Alerta, error) {
	a.Dueño = strings.ToLower(strings.TrimSpace(a.Dueño))
	if a.Dueño == "" {
		return nil, errors.New("una alerta es de alguien")
	}
	if err := a.Validar(); err != nil {
		return nil, err
	}
	if len(r.De(a.Dueño)) >= MaximoPorCuenta {
		return nil, errors.New("ya tenés el máximo de alertas; borrá alguna para crear otra")
	}
	id, err := identificador()
	if err != nil {
		return nil, err
	}
	a.ID = id
	a.Creada = time.Now().UTC()
	a.Activa = true

	if err := r.guardar(&a); err != nil {
		return nil, err
	}
	if err := r.sumarAlIndice(claveDe(a.Dueño), a.ID); err != nil {
		return nil, err
	}
	if err := r.sumarAlIndice(claveTodas, a.ID); err != nil {
		return nil, err
	}
	return &a, nil
}

// Actualizar guarda los cambios de una alerta que ya existe.
func (r *Registro) Actualizar(a *Alerta) error {
	if a.ID == "" {
		return errors.New("esa alerta no tiene identificador")
	}
	return r.guardar(a)
}

func (r *Registro) guardar(a *Alerta) error {
	crudo, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return r.alm.Guardar(claveAlerta(a.ID), crudo, almacen.SinVencimiento)
}

// Leer trae una alerta por su identificador.
func (r *Registro) Leer(id string) (*Alerta, bool) {
	crudo, hay := r.alm.Leer(claveAlerta(id))
	if !hay {
		return nil, false
	}
	var a Alerta
	if err := json.Unmarshal(crudo, &a); err != nil {
		return nil, false
	}
	return &a, true
}

// De son las alertas de una cuenta, de la más nueva a la más vieja.
func (r *Registro) De(usuario string) []Alerta {
	return r.leerIndice(claveDe(usuario))
}

// Todas son las de todas las cuentas, que es lo que recorre cada pasada.
func (r *Registro) Todas() []Alerta { return r.leerIndice(claveTodas) }

func (r *Registro) leerIndice(clave string) []Alerta {
	var out []Alerta
	for _, id := range r.indice(clave) {
		if a, hay := r.Leer(id); hay {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Creada.After(out[j].Creada) })
	return out
}

// Borrar saca una alerta. Se pide el dueño para que nadie borre la de otro por
// adivinar un identificador.
func (r *Registro) Borrar(id, dueño string) error {
	a, hay := r.Leer(id)
	if !hay {
		return errors.New("esa alerta no existe")
	}
	if !strings.EqualFold(a.Dueño, strings.TrimSpace(dueño)) {
		return errors.New("esa alerta no es tuya")
	}
	if err := r.alm.Borrar(claveAlerta(id)); err != nil {
		return err
	}
	_ = r.sacarDelIndice(claveDe(a.Dueño), id)
	_ = r.sacarDelIndice(claveTodas, id)
	return nil
}

func (r *Registro) indice(clave string) []string {
	crudo, hay := r.alm.Leer(clave)
	if !hay {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(crudo, &ids); err != nil {
		return nil
	}
	return ids
}

func (r *Registro) sumarAlIndice(clave, id string) error {
	ids := r.indice(clave)
	for _, v := range ids {
		if v == id {
			return nil
		}
	}
	return r.guardarIndice(clave, append(ids, id))
}

func (r *Registro) sacarDelIndice(clave, id string) error {
	ids := r.indice(clave)
	fuera := make([]string, 0, len(ids))
	for _, v := range ids {
		if v != id {
			fuera = append(fuera, v)
		}
	}
	return r.guardarIndice(clave, fuera)
}

func (r *Registro) guardarIndice(clave string, ids []string) error {
	crudo, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return r.alm.Guardar(clave, crudo, almacen.SinVencimiento)
}

func identificador() (string, error) {
	crudo := make([]byte, 12)
	if _, err := rand.Read(crudo); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(crudo), nil
}
