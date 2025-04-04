package main

import (
	// Importa el paquete renombrado
	"api-ws/src/consumer" // Cambiado de "api-ws/src/consumer" o "api-ws/src/mqtt"
	"api-ws/src/ws"
	"log"

	"github.com/gin-gonic/gin"
)

func main() {
	// Inicia el consumidor AMQP en una goroutine
	// Llama a la función renombrada del paquete renombrado
	go consumer.StartAMQPConsumer() // Cambiado de mqtt.StartMQTTConsumer

	r := gin.Default()

	r.GET("/ws", ws.HandleWebSocket)

	// Asegúrate que el puerto aquí coincida con el puerto en r.Run()
	log.Println("Servidor WebSocket corriendo en http://localhost:8080/ws") // Corregido puerto a 8080
	r.Run(":8080")                                                          // Gin corre en el puerto 8080
}
