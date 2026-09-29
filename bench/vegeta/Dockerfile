# Pinned vegeta load generator, built for the host architecture (S5.T4-infra).
#
# The popular `peterevans/vegeta` image is amd64-only: on an arm64 host (Apple
# Silicon) it runs under emulation, so vegeta's own CPU ceiling — not the LB's —
# would cap every throughput number. Building from a pinned source tag gives a
# native-arch binary, and the alpine runtime supplies the /bin/sleep the compose
# service uses to idle until a run.
FROM golang:1.25-alpine AS build
RUN go install github.com/tsenart/vegeta/v12@v12.13.0

FROM alpine:3.20
COPY --from=build /go/bin/vegeta /usr/local/bin/vegeta
ENTRYPOINT ["vegeta"]
