package controller

import (
	"testing"

	"system-stats/internal/platform/setup"
)

func TestSameExceptGateway(t *testing.T) {
	base := setup.DesiredState{Generation: 1, DBMode: setup.DBModeSQLite, Image: "img:1"}
	gw := base
	gw.Generation = 2
	gw.Gateway = &setup.GatewayProvision{Enabled: true, HTTPPort: 80, HTTPSPort: 443}
	if !sameExceptGateway(base, gw) {
		t.Error("gateway-only change must be recognised")
	}
	img := gw
	img.Generation = 3
	img.Image = "img:2"
	if sameExceptGateway(gw, img) {
		t.Error("image change must NOT be treated as gateway-only")
	}
	db := gw
	db.DBMode = setup.DBModePostgresManaged
	if sameExceptGateway(gw, db) {
		t.Error("db change must NOT be treated as gateway-only")
	}
}

func TestAppliedStatePersistence(t *testing.T) {
	dir := t.TempDir()
	if readAppliedState(dir) != nil {
		t.Fatal("expected nil without a file")
	}
	ds := setup.DesiredState{Generation: 7, DBMode: setup.DBModeSQLite, Gateway: &setup.GatewayProvision{Enabled: true}}
	writeAppliedState(dir, ds)
	got := readAppliedState(dir)
	if got == nil || got.Generation != 7 || got.Gateway == nil || !got.Gateway.Enabled {
		t.Fatalf("round-trip: %+v", got)
	}
}
