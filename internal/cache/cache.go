// Package cache guarda en disco lo que ya se leyó del Boletín. Una edición
// pasada no cambia nunca: se baja una vez y se sirve para siempre.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// SinVencimiento marca una entrada que no caduca.
const SinVencimiento time.Duration = 0

type sobre struct {
	GuardadoEn time.Time       `json:"guardado_en"`
	VenceEn    *time.Time      `json:"vence_en,omitempty"`
	Datos      json.RawMessage `json:"datos"`
}

// Disco es una caché de archivos JSON bajo un directorio raíz.
type Disco struct {
	raiz     string
	aciertos atomic.Int64
	fallos   atomic.Int64
	escritos atomic.Int64
}

// NuevoDisco crea (si hace falta) el directorio raíz de la caché.
func NuevoDisco(raiz string) (*Disco, error) {
	if raiz == "" {
		return nil, errors.New("la caché necesita un directorio")
	}
	if err := os.MkdirAll(raiz, 0o755); err != nil {
		return nil, fmt.Errorf("no se pudo crear el directorio de caché %s: %w", raiz, err)
	}
	return &Disco{raiz: raiz}, nil
}

// Metricas informa el uso de la caché.
type Metricas struct {
	Aciertos int64 `json:"aciertos"`
	Fallos   int64 `json:"fallos"`
	Escritos int64 `json:"escritos"`
	Entradas int64 `json:"entradas"`
}

func (d *Disco) Metricas() Metricas {
	m := Metricas{
		Aciertos: d.aciertos.Load(),
		Fallos:   d.fallos.Load(),
		Escritos: d.escritos.Load(),
	}
	_ = filepath.WalkDir(d.raiz, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			m.Entradas++
		}
		return nil
	})
	return m
}

// ruta traduce una clave lógica a un archivo. Una clave con ".." se rechaza
// en vez de normalizarse: si alguien la construyó así, hay un error arriba y
// conviene verlo, no colapsarlo en silencio.
func (d *Disco) ruta(clave string) (string, error) {
	if clave == "" {
		return "", errors.New("clave de caché vacía")
	}
	for _, parte := range strings.Split(clave, "/") {
		if parte == ".." {
			return "", fmt.Errorf("clave de caché inválida: %q", clave)
		}
	}
	limpia := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+clave)), "/")
	if limpia == "" || limpia == "." {
		return "", fmt.Errorf("clave de caché inválida: %q", clave)
	}
	return filepath.Join(d.raiz, filepath.FromSlash(limpia)+".json"), nil
}

// Leer devuelve los datos guardados si existen y no vencieron.
func (d *Disco) Leer(clave string) ([]byte, bool) {
	ruta, err := d.ruta(clave)
	if err != nil {
		d.fallos.Add(1)
		return nil, false
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		d.fallos.Add(1)
		return nil, false
	}
	var s sobre
	if err := json.Unmarshal(crudo, &s); err != nil {
		d.fallos.Add(1)
		return nil, false
	}
	if s.VenceEn != nil && time.Now().After(*s.VenceEn) {
		d.fallos.Add(1)
		return nil, false
	}
	d.aciertos.Add(1)
	return s.Datos, true
}

// Guardar escribe los datos con un vencimiento. La escritura es atómica: se
// arma un temporal y se renombra, para que un corte no deje media entrada.
func (d *Disco) Guardar(clave string, datos []byte, ttl time.Duration) error {
	ruta, err := d.ruta(clave)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ruta), 0o755); err != nil {
		return err
	}
	s := sobre{GuardadoEn: time.Now().UTC(), Datos: json.RawMessage(datos)}
	if ttl > 0 {
		v := time.Now().Add(ttl).UTC()
		s.VenceEn = &v
	}
	crudo, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(ruta), ".tmp-*")
	if err != nil {
		return err
	}
	nombreTmp := tmp.Name()
	defer os.Remove(nombreTmp)
	if _, err := tmp.Write(crudo); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(nombreTmp, ruta); err != nil {
		return err
	}
	d.escritos.Add(1)
	return nil
}

// Existe dice si hay una entrada vigente, sin contarla como acierto.
func (d *Disco) Existe(clave string) bool {
	ruta, err := d.ruta(clave)
	if err != nil {
		return false
	}
	crudo, err := os.ReadFile(ruta)
	if err != nil {
		return false
	}
	var s sobre
	if err := json.Unmarshal(crudo, &s); err != nil {
		return false
	}
	return s.VenceEn == nil || time.Now().Before(*s.VenceEn)
}

// Borrar elimina una entrada.
func (d *Disco) Borrar(clave string) error {
	ruta, err := d.ruta(clave)
	if err != nil {
		return err
	}
	if err := os.Remove(ruta); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
