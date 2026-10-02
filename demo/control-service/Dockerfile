# Build the demo control service as a static, dependency-free binary and ship it
# in a scratch image (S5.T16.4.1; the traffic-generator precedent). The program
# imports only the standard library, so it builds from this single-file package
# with no go.mod; page.html is embedded into the binary.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY main.go page.html ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /control-service main.go

FROM scratch
COPY --from=build /control-service /control-service
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/control-service"]
