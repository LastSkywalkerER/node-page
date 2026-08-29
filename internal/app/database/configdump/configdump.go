// Package configdump exports and re-imports the small identity/config tables
// (users, hosts, connectors, cluster config) as a portable gzip+gob file.
//
// It exists for the in-app database-engine switch: those tables must survive a
// switch to a fresh empty database, while the high-churn metric history
// (cpu/memory/disk/network/docker) is DELIBERATELY left behind to start over.
// The set mirrors internal/cluster/raft's snapshot managedTables for exactly the
// same reason (folding metric history in would materialise hundreds of MB).
package configdump

import (
	"compress/gzip"
	"database/sql/driver"
	"encoding/gob"
	"fmt"
	"os"
	"time"

	"gorm.io/gorm"
)

func init() {
	// Timestamp columns scan into concrete time.Time behind an interface; gob
	// refuses to encode an unregistered concrete type without this.
	gob.Register(time.Time{})
}

// ManagedTables is the set carried across a database switch. Parents precede
// children (user_accounts before user_refresh_tokens) so inserts stay FK-safe.
var ManagedTables = []string{
	"hosts",
	"user_accounts",
	"user_refresh_tokens",
	"cluster_config",
	"peer_node_advertise",
	"cluster_join_tokens",
	"connectors",
	"gateway_routes",
}

type dumpFile struct {
	Version int
	Tables  []tableRows
}

type tableRows struct {
	Name string
	Rows []map[string]any
}

// Export writes every existing managed table to path as a gzipped gob stream.
func Export(db *gorm.DB, path string) error {
	if db == nil {
		return fmt.Errorf("configdump: nil db")
	}
	out := dumpFile{Version: 1}
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, t := range ManagedTables {
			if !tx.Migrator().HasTable(t) {
				continue
			}
			rows := []map[string]any{}
			if err := tx.Table(t).Find(&rows).Error; err != nil {
				return fmt.Errorf("dump %s: %w", t, err)
			}
			normalizeRows(rows)
			out.Tables = append(out.Tables, tableRows{Name: t, Rows: rows})
		}
		return nil
	})
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	if err := gob.NewEncoder(gz).Encode(out); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

// ImportIfPresent imports path into db (truncating each managed table first) and
// removes the file on success. Returns (false, nil) when no dump file exists.
// Safe to call on every boot.
func ImportIfPresent(db *gorm.DB, path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, fmt.Errorf("configdump: gzip: %w", err)
	}
	defer gz.Close()

	var in dumpFile
	if err := gob.NewDecoder(gz).Decode(&in); err != nil {
		return false, fmt.Errorf("configdump: decode: %w", err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		if name := tx.Dialector.Name(); name == "sqlite" || name == "sqlite3" {
			_ = tx.Exec("PRAGMA defer_foreign_keys = ON").Error
		}
		// Wipe children-first (reverse order) so a parent delete never trips an
		// FK; insert parent-first (forward) below.
		for i := len(in.Tables) - 1; i >= 0; i-- {
			t := in.Tables[i].Name
			if !tx.Migrator().HasTable(t) {
				continue
			}
			if err := tx.Exec("DELETE FROM " + t).Error; err != nil {
				return fmt.Errorf("truncate %s: %w", t, err)
			}
		}
		for _, tr := range in.Tables {
			if !tx.Migrator().HasTable(tr.Name) || len(tr.Rows) == 0 {
				continue
			}
			const chunk = 200
			for i := 0; i < len(tr.Rows); i += chunk {
				end := i + chunk
				if end > len(tr.Rows) {
					end = len(tr.Rows)
				}
				if err := tx.Table(tr.Name).Create(tr.Rows[i:end]).Error; err != nil {
					return fmt.Errorf("insert %s: %w", tr.Name, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	_ = os.Remove(path)
	return true, nil
}

// normalizeRows coerces driver-specific value types into gob-encodable ones.
func normalizeRows(rows []map[string]any) {
	for _, row := range rows {
		for k, v := range row {
			row[k] = normalizeValue(v)
		}
	}
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil, bool, string, []byte,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return v
	case time.Time:
		return t.UTC()
	case driver.Valuer:
		if dv, err := t.Value(); err == nil {
			if _, again := dv.(driver.Valuer); !again {
				return normalizeValue(dv)
			}
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
