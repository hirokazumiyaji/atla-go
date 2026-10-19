package atla

// resetClassifiers removes every classifier registered with RegisterClassifier.
func resetClassifiers() {
	classifiersMu.Lock()
	defer classifiersMu.Unlock()
	classifiers = nil
}
