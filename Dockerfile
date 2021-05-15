FROM golang:alpine AS builder

WORKDIR /go/src/app
COPY . .

RUN go build -o private-toot-remover .

FROM alpine
RUN apk add --no-cache ca-certificates && update-ca-certificates
COPY --from=builder /go/src/app/private-toot-remover /private-toot-remover
CMD ["/private-toot-remover"]
