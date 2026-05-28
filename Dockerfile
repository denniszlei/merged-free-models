# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache dependencies separately. go.sum is included via glob in case it appears later.
COPY go.mod ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 GOFLAGS=-trimpath \
    go build \
      -ldflags="-s -w \
        -X github.com/denniszlei/merged-free-models/internal/version.Version=${VERSION} \
        -X github.com/denniszlei/merged-free-models/internal/version.Commit=${COMMIT} \
        -X github.com/denniszlei/merged-free-models/internal/version.Date=${DATE}" \
      -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
