package data

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"gorm.io/gorm/schema"
)

// SQLite's consolidated billing schema historically declares timestamps as
// INTEGER, while GORM writes textual timestamps. The driver cannot scan those
// values into time.Time without an explicit serializer. Accept both historical
// epochs and driver-native timestamps without rewriting existing evidence.
type billingTimeSerializer struct{}

func init() { schema.RegisterSerializer("billing_time", billingTimeSerializer{}) }

func (billingTimeSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, value any) error {
	var stamp time.Time
	switch v := value.(type) {
	case nil:
		return field.Set(ctx, dst, nil)
	case time.Time:
		stamp = v
	case int64:
		if v != 0 {
			stamp = time.Unix(v, 0).UTC()
		}
	case []byte:
		return (billingTimeSerializer{}).Scan(ctx, field, dst, string(v))
	case string:
		if v != "" && v != "0" {
			var err error
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
				stamp, err = time.Parse(layout, v)
				if err == nil {
					break
				}
			}
			if err != nil {
				return fmt.Errorf("invalid billing timestamp: %w", err)
			}
		}
	default:
		return fmt.Errorf("unsupported billing timestamp %T", value)
	}
	return field.Set(ctx, dst, stamp)
}

func (billingTimeSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, value any) (any, error) {
	if pointer, ok := value.(*time.Time); ok {
		if pointer == nil {
			return nil, nil
		}
		return *pointer, nil
	}
	return value, nil
}
