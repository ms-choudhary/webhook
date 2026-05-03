FROM golang:1.24.0 AS builder

WORKDIR /app

#COPY go.mod go.sum ./
#RUN go mod download 

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o ./webhook .

FROM alpine:3.14

WORKDIR /app

COPY --from=builder /app/webhook /usr/local/bin/webhook

EXPOSE 8090

CMD ["webhook"]
