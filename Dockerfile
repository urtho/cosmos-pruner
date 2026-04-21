# syntax=docker/dockerfile:1
FROM golang:1.25.9 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 GOFLAGS="-trimpath" \
    go build -ldflags="-s -w" -o /out/cosmos-pruner ./

FROM gcr.io/distroless/static:nonroot

LABEL org.opencontainers.image.source="https://github.com/binaryholdings/cosmos-pruner"
LABEL org.opencontainers.image.description="cosmos-pruner - prunes data history from a Cosmos SDK / CometBFT node"

COPY --from=builder --chown=nonroot:nonroot /out/cosmos-pruner /usr/local/bin/cosmos-pruner

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/cosmos-pruner"]
