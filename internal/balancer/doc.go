// Package balancer defines the Selector interface for choosing a backend
// per request, along with its implementations: round robin, least
// connections, bounded-load consistent hashing, and power-of-two-choices
// with EWMA latency.
package balancer
