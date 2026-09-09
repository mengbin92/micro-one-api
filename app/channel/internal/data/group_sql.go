package data

import (
	"strings"

	"gorm.io/gorm"
)

// quoteGroupColumnSQL quotes the reserved group column for the active driver.
// The predicate is a source-code constant. Values remain bound parameters;
// legacy collation and LIKE semantics are deliberately preserved for auditing.
func quoteGroupColumnSQL(db *gorm.DB, predicate string) string {
	var column strings.Builder
	db.Dialector.QuoteTo(&column, "group")
	return strings.ReplaceAll(predicate, "`group`", column.String())
}
