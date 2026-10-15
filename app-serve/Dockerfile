# Build app-serve as a static binary, then ship it on a minimal base.
# AGENTS.md prefers the standard library; there is no cgo need, so CGO is off.
# Alpine rather than distroless because the compose healthcheck calls /healthz
# through wget, and distroless ships no shell or wget to run it with.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/app-serve ./cmd/app-serve

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=build /out/app-serve /app-serve
EXPOSE 8080
USER app
ENTRYPOINT ["/app-serve"]
