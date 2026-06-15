package setup

import (
	"context"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"system-stats/internal/app/dockerenv"
)

// DBMigrationDumpFile is the config-tables dump the db-switch writes before
// recreating; the next boot imports it into the fresh database and removes it.
const DBMigrationDumpFile = "db-migrate.dump"

// PrepareDBSwitch finalizes managed-Postgres provisioning on cv, resolves the
// target DB mode, and — on a controller-driven Docker deployment — records the
// desired state so the controller recreates the app on the new (empty) engine.
//
// restartPending is true when a recreate was queued; false on native /
// managed-externally, where the caller persists DB_TYPE/DB_DSN to .env and the
// operator restarts. Either way the next boot imports the config dump.
func PrepareDBSwitch(dataDir string, cv *ConfigValues) (mode string, restartPending bool, err error) {
	if ferr := finalizeManagedPostgres(cv); ferr != nil {
		return "", false, ferr
	}
	mode = DBModeSQLite
	if strings.EqualFold(cv.DBType, "postgres") {
		if cv.DBManaged {
			mode = DBModePostgresManaged
		} else {
			mode = DBModePostgresExternal
		}
	}
	if !dockerenv.Running() || ManagedExternally() {
		return mode, false, nil
	}
	gen := 1
	if prev, _ := ReadDesiredState(dataDir); prev != nil {
		gen = prev.Generation + 1
	}
	ds := DesiredState{Generation: gen, DBMode: mode, DBDSN: AssembleDSN(cv)}
	if mode == DBModePostgresManaged {
		ds.DB = DBProvision{Name: cv.DBName, User: cv.DBUser, Password: cv.DBPassword}
	}
	if werr := WriteDesiredState(dataDir, ds); werr != nil {
		return mode, false, werr
	}
	return mode, true, nil
}

// TestPostgresDSN opens a short-lived connection to validate an external
// Postgres DSN. It never persists or migrates anything.
func TestPostgresDSN(ctx context.Context, dsn string) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return err
	}
	raw, err := db.DB()
	if err != nil {
		return err
	}
	defer raw.Close()
	return raw.PingContext(pingCtx)
}
