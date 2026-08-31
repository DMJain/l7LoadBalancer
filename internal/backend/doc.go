// Package backend defines the Backend struct and Registry, tracking
// per-backend state (address, connection counts, health) under concurrent
// access from the request path and health checkers.
package backend
