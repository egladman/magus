# swegrade as a container, so the benchmark host needs docker and nothing else to
# grade: the eval log is piped in on stdin and the verdict comes out on stdout.
# The command has no dependencies, so it builds as its own module here rather than
# dragging the magus module graph into the image.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY *.go ./
RUN go mod init swegrade >/dev/null 2>&1 && CGO_ENABLED=0 go build -trimpath -o /swegrade .

FROM scratch
COPY --from=build /swegrade /swegrade
ENTRYPOINT ["/swegrade"]
