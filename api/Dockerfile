# Build context is the repository root, not api/.
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/sagalore-api ./api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sagalore-api /sagalore-api
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/sagalore-api"]
