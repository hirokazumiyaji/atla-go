// fxgen regenerates the Postgres/MySQL fixtures in ../ with Atlas's own HCL
// marshallers. Point the replace directive at a checkout of
// https://github.com/ariga/atlas (commit ed994a77898e48377f9a9c391f2d1c49f5703b8d)
// and run: go run . ..
// It is a separate module under testdata, so the go command ignores it.
module fxgen

go 1.26.10

require ariga.io/atlas v0.0.0

replace ariga.io/atlas => ../../../../../../atlas
