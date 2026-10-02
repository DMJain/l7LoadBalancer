# Build the demo traffic generator as a static, dependency-free binary and ship
# it in a scratch image (S5.T16.2; the dummy-backend precedent). The program
# imports only the standard library, so it builds from this single-file package
# with no go.mod.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /traffic-generator main.go

FROM scratch
COPY --from=build /traffic-generator /traffic-generator
USER 65532:65532
EXPOSE 9090
ENTRYPOINT ["/traffic-generator"]
