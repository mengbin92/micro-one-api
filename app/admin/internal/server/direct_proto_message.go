package server

import (
	"reflect"

	"google.golang.org/protobuf/proto"
)

// Compatibility DTOs embed protobuf requests alongside legacy fields such as
// id. Promoted methods make them satisfy proto.Message, but protojson must not
// reset that wrapper or ignore its legacy fields. Decode those through jsonx.
func directProtoMessage(dst any) (proto.Message, bool) {
	message, ok := dst.(proto.Message)
	if !ok || dst == nil {
		return nil, false
	}
	typ := reflect.TypeOf(dst)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Struct {
		protoType := reflect.TypeFor[proto.Message]()
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.Anonymous && (field.Type.Implements(protoType) || reflect.PointerTo(field.Type).Implements(protoType)) {
				return nil, false
			}
		}
	}
	return message, true
}
