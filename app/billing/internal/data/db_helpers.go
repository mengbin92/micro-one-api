package data

import "gorm.io/gorm"
import "gorm.io/gorm/clause"

// dialectorName returns the canonical driver name for the dialector
// attached to the given *gorm.DB. We avoid a direct Dialector.Name()
// call to keep the helper testable: the value is the same regardless
// of the connection state and is stable for the lifetime of the
// process.
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
// unsupported clause on every SQLite deployment and test.
func isSQLite(driver string) bool {
	return driver == "sqlite" || driver == "sqlite3"
}

// forUpdateClause returns the dialect-appropriate SELECT ... FOR UPDATE
// clause. SQLite serialises writes through the writer transaction, so
// the explicit row lock is unnecessary and (in older versions)
// unsupported. Postgres + MySQL use the explicit "FOR UPDATE" string.
func forUpdateClause(driver string) clause.Locking {
	if isSQLite(driver) {
		return clause.Locking{}
	}
	return clause.Locking{Strength: "UPDATE"}
}
