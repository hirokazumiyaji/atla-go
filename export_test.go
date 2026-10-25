package atla

// resetClassifiers removes every classifier registered with RegisterClassifier.
func resetClassifiers() {
	classifiersMu.Lock()
	defer classifiersMu.Unlock()
	classifiers = nil
}

// NewSubqueryForTest presents sub as a single-column subquery of type T.
// Select1 produces Subquery values once projections exist.
func NewSubqueryForTest[T any](sub AnySubquery) Subquery[T] {
	return Subquery[T](sub)
}
