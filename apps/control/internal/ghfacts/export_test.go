package ghfacts

// NewClientForTest exposes the endpoint-override constructor to this
// package's own test binaries only (internal and external). It compiles
// exclusively under `go test` and never ships: a client aimed at stub
// servers is test authority, and production code must not be able to
// redirect the fact source. This file is the only sanctioned mint for
// fixtures, per the round-10 authority finding.
var NewClientForTest = newClient
