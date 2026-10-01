package xdb

import (
	"database/sql/driver"
	"fmt"
	"strconv"
)

// Flag owns the persistent shape of numeric booleans. It also reads historical
// boolean columns used by snapshots and test schemas; domains keep plain bool.
type Flag int32

func BoolInt(value bool) Flag {
	if value {
		return 1
	}
	return 0
}
func (f Flag) Value() (driver.Value, error) { return int64(f), nil }
func (f *Flag) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*f = 0
	case bool:
		*f = BoolInt(v)
	case int64:
		*f = Flag(v)
	case int32:
		*f = Flag(v)
	case []byte:
		return f.Scan(string(v))
	case string:
		if v == "true" || v == "false" {
			*f = BoolInt(v == "true")
			return nil
		}
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return err
		}
		*f = Flag(n)
	default:
		return fmt.Errorf("invalid numeric flag %T", value)
	}
	return nil
}
func (Flag) GormDataType() string { return "integer" }
