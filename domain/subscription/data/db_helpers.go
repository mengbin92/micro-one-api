package data

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// dialectorName returns the canonical driver name for the dialector
// attached to the given *gorm.DB. Mirrors the helper in the billing
// data layer; the two are kept package-local to avoid a cross-domain
// import.
func dialectorName(db *gorm.DB) string {
	if db == nil {
		return ""
	}
	if db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}

// isSQLite reports whether the driver is any SQLite variant. The gorm
// sqlite driver (gorm.io/driver/sqlite) reports its name as "sqlite"
// while the older mattn/go-sqlite3-backed drivers reported "sqlite3";
// both must be treated as SQLite so the FOR UPDATE guards skip the
// unsupported clause (code-review 2026-07-30 domain-L3 / billing-L3).
func isSQLite(driver string) bool {
	return driver == "sqlite" || driver == "sqlite3"
}

// forUpdateClause returns the dialect-appropriate SELECT ... FOR UPDATE
// clause.
func forUpdateClause(driver string) clause.Locking {
	if isSQLite(driver) {
		return clause.Locking{}
	}
	return clause.Locking{Strength: "UPDATE"}
}
