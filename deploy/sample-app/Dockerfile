# Built from the repository root so it shares the module's go.mod:
#   docker build -t sample-app:dev -f deploy/sample-app/Dockerfile .
#
# The sample app lives in the same Go module as the autoscaler rather than in its
# own. It is a deliberate choice: one `go build ./...` and one `go vet ./...` cover
# both binaries, so the workload cannot silently stop compiling while attention is
# on the controller.
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sample-app ./cmd/sample-app

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sample-app /sample-app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/sample-app"]
