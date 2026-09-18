# Build the dummy backend as a static, dependency-free binary and ship it in
# a scratch image. The service imports only the standard library, so the
# build works from this single-file package with no go.mod.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dummy-backend main.go

FROM scratch
COPY --from=build /dummy-backend /dummy-backend
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/dummy-backend"]
