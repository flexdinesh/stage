package store

import "encoding/json"

// Optional is a PATCH field: Set when present in JSON, Null when JSON null.
type Optional[T any] struct {
	Set  bool
	Null bool
	V    T
}

func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		var zero T
		o.V = zero
		return nil
	}
	return json.Unmarshal(b, &o.V)
}

// Ptr returns a pointer to V, or nil when unset or null.
func (o Optional[T]) Ptr() *T {
	if !o.Set || o.Null {
		return nil
	}
	v := o.V
	return &v
}
