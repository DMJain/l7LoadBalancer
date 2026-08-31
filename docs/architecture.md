# Architecture

_This document grows sprint by sprint. See `MILESTONES.md` for the plan._

## Overview

l7LoadBalancer is a Layer 7 HTTP load balancer built on Go's `net/http` stack. The design layers pluggable backend selection, health checking, circuit breaking, and metrics around `net/http/httputil.ReverseProxy`.

## Component map

_To be filled at end of Sprint 1 with initial architecture diagram._

## Decision index

See `docs/adr/` for architecture decision records.
