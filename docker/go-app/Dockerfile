FROM golang:1.22 AS build

WORKDIR /src
COPY go.mod ./
COPY main.go ./
# Resolve deps and produce go.sum, then build a static binary.
RUN go mod tidy && CGO_ENABLED=0 go build -o /out/go-app .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/go-app /go-app
EXPOSE 8080
ENTRYPOINT ["/go-app"]
