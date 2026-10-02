# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY db/queries ./db/queries
ARG APP=api
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/${APP}

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 ticker
WORKDIR /app
COPY --from=build /out/service /app/service
USER ticker
EXPOSE 8080
ENTRYPOINT ["/app/service"]
