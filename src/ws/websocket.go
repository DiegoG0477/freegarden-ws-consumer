package ws

import (
	"encoding/json" // Necesario para parsear comandos JSON
	"fmt"           // Necesario para formatear routing keys
	"log"
	"net/http"
	"sync"
	"time" // Necesario para el timeout de conexión AMQP

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/streadway/amqp" // Necesario para publicar comandos
)

// --- Estructura para comandos recibidos desde WebSocket ---
type WebSocketCommand struct {
	Action string `json:"action"` // Ej: "relay_on", "relay_off"
	KitID  int    `json:"kit_id"`
}

// --- Constantes para la publicación AMQP de comandos ---
//
//	(Puedes moverlas a un archivo de configuración o variables de entorno)
const amqpURIForPublish = "amqp://admin:admin@184.73.194.217:5672" // Misma URL que el consumidor
const commandExchange = "amq.topic"                                // El exchange a usar para publicar comandos
const commandRoutingKeyPattern = "command.10.data"                 // Patrón para la routing key (usando .)

// --- Gestor WebSocket (sin cambios) ---
type WebSocketManager struct {
	clients map[*websocket.Conn]bool
	mu      sync.Mutex
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	manager = WebSocketManager{
		clients: make(map[*websocket.Conn]bool),
	}
	// Podríamos cachear la conexión/canal AMQP para publicar,
	// pero por simplicidad, lo crearemos por petición por ahora.
	// Una optimización sería gestionar un pool de canales o una conexión persistente.
)

// --- Handler WebSocket Modificado ---
func HandleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Error al actualizar conexión a WebSocket: %v", err)
		return
	}
	defer conn.Close()

	manager.mu.Lock()
	manager.clients[conn] = true
	manager.mu.Unlock()

	log.Printf("Cliente WebSocket conectado: %s", conn.RemoteAddr())

	// Bucle para leer mensajes del cliente
	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			// Manejar desconexión
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Error inesperado leyendo de WebSocket: %v", err)
			} else {
				log.Printf("Cliente WebSocket desconectado: %s, Razón: %v", conn.RemoteAddr(), err)
			}
			manager.mu.Lock()
			delete(manager.clients, conn)
			manager.mu.Unlock()
			break // Salir del bucle for
		}

		// Procesar solo mensajes de texto
		if messageType == websocket.TextMessage {
			log.Printf("Mensaje recibido de %s: %s", conn.RemoteAddr(), string(p))

			// Intentar parsear como comando
			var cmd WebSocketCommand
			if err := json.Unmarshal(p, &cmd); err != nil {
				log.Printf("Error al parsear comando JSON de %s: %v. Mensaje: %s", conn.RemoteAddr(), err, string(p))
				// Podrías enviar un mensaje de error de vuelta al cliente si lo deseas
				// conn.WriteMessage(websocket.TextMessage, []byte(`{"error": "formato de comando inválido"}`))
				continue // Esperar el siguiente mensaje
			}

			// Validar y procesar el comando
			processClientCommand(cmd, conn) // Pasamos conn por si queremos responder algo específico

		} else {
			log.Printf("Tipo de mensaje no soportado (%d) recibido de %s", messageType, conn.RemoteAddr())
		}
	}
}

// --- Función para procesar el comando recibido del cliente ---
func processClientCommand(cmd WebSocketCommand, conn *websocket.Conn) {
	var commandPayload string

	// Determinar el payload basado en la acción
	switch cmd.Action {
	case "relay_on":
		commandPayload = "ON"
	case "relay_off":
		commandPayload = "OFF"
	default:
		log.Printf("Acción de comando desconocida '%s' recibida de %s para kit %d", cmd.Action, conn.RemoteAddr(), cmd.KitID)
		// Opcional: Enviar error de vuelta al cliente
		// conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"error": "acción desconocida: %s"}`, cmd.Action)))
		return // No hacer nada si la acción no es reconocida
	}

	// Construir la routing key específica para este kit
	routingKey := fmt.Sprintf(commandRoutingKeyPattern, cmd.KitID)

	log.Printf("Intentando enviar comando '%s' al kit %d via AMQP (Exchange: %s, RoutingKey: %s)",
		commandPayload, cmd.KitID, commandExchange, routingKey)

	// Publicar el comando en AMQP
	err := publishCommandToAMQP(routingKey, commandPayload)
	if err != nil {
		log.Printf("ERROR al publicar comando AMQP para kit %d: %v", cmd.KitID, err)
		// Opcional: Enviar error de vuelta al cliente
		// conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"error": "no se pudo enviar comando al kit %d"}`, cmd.KitID)))
	} else {
		log.Printf("Comando '%s' publicado exitosamente via AMQP para kit %d (RoutingKey: %s)",
			commandPayload, cmd.KitID, routingKey)
		// Opcional: Enviar confirmación de vuelta al cliente
		// conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"success": "comando %s enviado al kit %d"}`, cmd.Action, cmd.KitID)))
	}
}

// --- Función para publicar un comando en AMQP ---
//
//	Nota: Esta función abre y cierra conexión/canal en cada llamada.
//	Para producción, considera mantener una conexión/canal abierto y reutilizarlo.
func publishCommandToAMQP(routingKey string, payload string) error {
	// 1. Conectar a RabbitMQ
	conn, err := amqp.Dial(amqpURIForPublish)
	if err != nil {
		return fmt.Errorf("fallo al conectar a RabbitMQ (%s): %w", amqpURIForPublish, err)
	}
	defer conn.Close() // Asegura que la conexión se cierre

	// 2. Abrir un canal
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("fallo al abrir un canal AMQP: %w", err)
	}
	defer ch.Close() // Asegura que el canal se cierre

	// 3. Publicar el mensaje
	err = ch.Publish(
		commandExchange, // exchange: usar amq.topic
		routingKey,      // routing key: ej. "garden.command.123"
		false,           // mandatory: si es true, devuelve error si no hay cola bindeada
		false,           // immediate: si es true, devuelve error si no hay consumidor listo
		amqp.Publishing{
			ContentType: "text/plain",    // O "application/json" si envías JSON
			Body:        []byte(payload), // El comando ("ON" o "OFF")
			Timestamp:   time.Now(),      // Marca de tiempo opcional
			// DeliveryMode: amqp.Persistent, // Opcional: si quieres que el mensaje sobreviva reinicios del broker
		})
	if err != nil {
		return fmt.Errorf("fallo al publicar mensaje AMQP: %w", err)
	}

	return nil // Éxito
}

// --- Función para enviar mensajes a TODOS los clientes (sin cambios) ---
func SendMessageToClients(message string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	// Loguear qué se está enviando a los clientes WS
	// log.Printf("Enviando a %d clientes WS: %s", len(manager.clients), message)

	for client := range manager.clients {
		err := client.WriteMessage(websocket.TextMessage, []byte(message))
		if err != nil {
			log.Printf("Error al enviar mensaje a cliente WS %s: %v. Desconectando.", client.RemoteAddr(), err)
			client.Close()
			delete(manager.clients, client)
		}
	}
}
