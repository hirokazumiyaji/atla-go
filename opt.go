package atla

// Opt is an optional value for partial INSERT and UPDATE column lists.
// For use by generated code.
type Opt[T any] struct {
	set bool
	v   T
}

// Some returns an Opt holding v.
// For use by generated code.
func Some[T any](v T) Opt[T] { return Opt[T]{set: true, v: v} }
