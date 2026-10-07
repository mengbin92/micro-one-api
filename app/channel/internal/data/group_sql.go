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

// legacyGroupMembershipSQL matches a complete CSV token by bytes. LIKE would
// interpret legal group-key characters (% and _) as wildcards and can ignore
// case, expanding an IAM scope. Table is an owner-defined SQL identifier.
func (r *Repository) legacyGroupMembershipSQL(table string) string {
	group := quoteGroupColumnSQL(r.db, table+".`group`")
	var key strings.Builder
	r.db.Dialector.QuoteTo(&key, "key")
	quotedKey := "rg." + key.String()
	switch r.db.Dialector.Name() {
	case "mysql":
		return "LOCATE(CONCAT(',', " + quotedKey + ", ','), BINARY CONCAT(',', " + group + ", ',')) > 0"
	case "postgres":
		return "strpos(',' || " + group + " || ',', ',' || " + quotedKey + " || ',') > 0"
	default:
		return "instr(',' || " + group + " || ',', ',' || " + quotedKey + " || ',') > 0"
	}
}
