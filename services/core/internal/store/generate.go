// Package store is the generated storm store: one package per table, and the
// scanners and statement registry for internal/store/queries.
//
// Regenerate after changing the model or a declared statement, against a
// database migrated to this build (the model is a projection of the
// migrations, which stay the schema of record):
//
//	STORM_DSN=postgres://am_owner:owner@localhost/assessmatrix go generate ./internal/store
package store

//go:generate go run github.com/gsoultan/storm/cmd/storm generate -raw-schema live internal/store
