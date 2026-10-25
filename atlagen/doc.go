// Package atlagen is the ahead-of-time code generator for atla-go.
//
// It reads a schema managed by Atlas and emits Go code that targets the atla
// runtime package. It lives in a separate module so that the runtime keeps
// zero third-party dependencies.
package atlagen
