# ============================================
# STAGE 1: Build
# ============================================
FROM golang:1.25.5-alpine AS builder

# Instalar dependencias del sistema necesarias
RUN apk add --no-cache git ca-certificates tzdata

# Establecer el directorio de trabajo
WORKDIR /app

# Copiar archivos de módulos Go
COPY go.mod go.sum ./

# Descargar dependencias
RUN go mod download

# Copiar el código fuente
COPY *.go ./

# Compilar el binario
# CGO_ENABLED=0: Para crear un binario estático sin dependencias de C
# -ldflags="-w -s": Para reducir el tamaño del binario (eliminar símbolos de debug)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bot .

# ============================================
# STAGE 2: Runtime
# ============================================
FROM alpine:latest

# Instalar certificados CA para conexiones HTTPS/WSS
RUN apk --no-cache add ca-certificates tzdata

# Crear usuario no-root para seguridad
RUN addgroup -g 1000 botuser && \
    adduser -D -u 1000 -G botuser botuser

# Establecer el directorio de trabajo
WORKDIR /home/botuser/app

# Copiar el binario compilado desde el builder
COPY --from=builder /app/bot .

# Copiar los archivos estáticos (interfaz web)
COPY --chown=botuser:botuser static/ ./static/

# Cambiar al usuario no-root
USER botuser

# Exponer el puerto del servidor web
EXPOSE 8080

# Variables de entorno opcionales (pueden ser sobrescritas)
ENV TZ=UTC

# Healthcheck para verificar que el servidor web está respondiendo
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

# Comando para ejecutar el bot
CMD ["./bot"]
