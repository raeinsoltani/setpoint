# Build
FROM golang:1.26-alpine AS build
WORKDIR /src

# Copy manifests first so the module download layer is cached across code edits —
# client-go's dependency tree is large enough that re-downloading it on every
# build would dominate the iteration loop.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off gives a static binary that runs on a distroless base.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/autoscaler ./cmd/autoscaler

# Run
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/autoscaler /autoscaler
USER 65532:65532
EXPOSE 8000
ENTRYPOINT ["/autoscaler"]
CMD ["--config=/etc/autoscaler/config.yaml"]
