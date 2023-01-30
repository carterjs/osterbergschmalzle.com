FROM golang:alpine AS builder
WORKDIR /app
ADD go.mod .
COPY . ./
RUN go build -o start

FROM alpine
WORKDIR /app
COPY --from=builder /app/start /app/start
COPY --from=builder /app/static /app/static/
COPY --from=builder /app/templates /app/templates/
CMD ["./start"]