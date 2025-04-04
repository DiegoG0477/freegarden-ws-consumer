FROM golang:1.23.8-alpine3.21 AS builder

WORKDIR /app

# Copiar dependencias primero
COPY go.mod go.sum ./
RUN go mod download

# Copiar todo el código fuente
COPY src/ ./src

# Compilar
RUN CGO_ENABLED=0 GOOS=linux go build -o api ./main.go

# Runtime stage
FROM alpine:latest
WORKDIR /app

# Copiar binario y archivos necesarios
COPY --from=builder /app/api .

EXPOSE 8080
CMD ["./api"]