// Package proxy wires the load balancer's request-handling path around
// net/http/httputil.ReverseProxy, delegating backend selection to the
// balancer package and health/circuit gating to their respective packages.
package proxy
