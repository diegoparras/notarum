//go:build red

// Estos tests pegan a ARCA de verdad.
//
//	go test ./internal/vencimientos/ -tags red -v
//
// No corren en la suite normal. Sirven para enterarse de que la página cambió
// antes de que lo note quien consume.
package vencimientos

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRedLaAgendaDelMesSeLee(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancelar()
	hoy := time.Now().UTC()
	desde := time.Date(hoy.Year(), hoy.Month(), 1, 0, 0, 0, 0, time.UTC)
	ag, err := NuevoCliente(Opciones{}).Consultar(ctx, desde, desde.AddDate(0, 1, -1))
	if err != nil {
		t.Fatal(err)
	}
	if len(ag.Vencimientos) < 100 {
		t.Errorf("el mes trajo %d vencimientos", len(ag.Vencimientos))
	}
	t.Logf("%d vencimientos, %d avisos", len(ag.Vencimientos), len(ag.Avisos))
}

// ARCA no tiene cargado más allá del año en curso, y lo dice así.
func TestRedMasAllaDeLoCargadoSeReconoce(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
	defer cancelar()
	lejos := time.Date(time.Now().Year()+5, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := NuevoCliente(Opciones{}).Consultar(ctx, lejos, lejos.AddDate(0, 0, 7))
	if !errors.Is(err, ErrRangoNoCargado) {
		t.Fatalf("se esperaba ErrRangoNoCargado: %v", err)
	}
}

func TestRedElCatalogoSeLee(t *testing.T) {
	ctx, cancelar := context.WithTimeout(context.Background(), time.Minute)
	defer cancelar()
	cat, err := NuevoCliente(Opciones{}).Catalogo(ctx)
	if err != nil || len(cat) < 30 {
		t.Fatalf("%d impuestos, %v", len(cat), err)
	}
}
