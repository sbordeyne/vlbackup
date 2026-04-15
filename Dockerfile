FROM golang:1.26.2 AS builder

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app/
ADD . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-w -s" -o vlbackup main.go

FROM scratch
WORKDIR /app/
COPY --from=builder /app/vlbackup /app/vlbackup
EXPOSE 8080

ENTRYPOINT ["/app/vlbackup"]
