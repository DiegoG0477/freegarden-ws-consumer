package consumer // Asegúrate que el nombre del paquete sea 'consumer'

import (
	"api-ws/src/ws"
	"bytes"
	"encoding/json"
	"fmt" // Necesario para formatear mensajes de alerta
	"io"
	"log"
	"net/http"
	"time"

	"github.com/streadway/amqp"
)

// --- Estructuras de Datos (Sin cambios) ---
type IncomingData struct {
	KitID                     int     `json:"kit_id"`
	HumedadSuelo              int     `json:"humedad_suelo"`
	NivelAguaCm               float64 `json:"nivel_agua_cm"`
	TemperaturaCelsius        float64 `json:"temperatura_celsius"`
	HumedadAmbientePorcentaje float64 `json:"humedad_ambiente_porcentaje"`
	ValorPh                   float64 `json:"valor_ph"`
	MarcaTiempo               int64   `json:"marca_tiempo"`
}

type GardenData struct {
	EnvironmentHumidity float64 `json:"environment_humidity"`
	GroundHumidity      int     `json:"ground_humidity"`
	KitID               int     `json:"kit_id"`
	PhLevel             float64 `json:"ph_level"`
	Temperature         float64 `json:"temperature"`
	Time                int64   `json:"time"`
}

// --- Estructura para Alertas ---
type AlertData struct {
	AlertType string `json:"alert_type"` // "under_min" or "higher_max"
	KitID     int    `json:"kit_id"`
	Message   string `json:"message"`
}

// --- Constantes API (Actualizadas) ---
const apiBaseURL = "http://52.21.24.207:8080" // Tu URL base de API
const gardenDataEndpoint = "/v1/garden/data/"
const alertsEndpoint = "/v1/alerts/" // Nuevo endpoint para alertas

// --- Constantes de Umbrales para Alertas (Ajusta estos valores según necesidad) ---
const (
	minSueloHumidity      = 1000 // Ejemplo: Umbral mínimo humedad suelo
	maxSueloHumidity      = 3000 // Ejemplo: Umbral máximo humedad suelo
	minNivelAguaCm        = 5.0  // Ejemplo: Umbral mínimo nivel agua
	maxNivelAguaCm        = 25.0 // Ejemplo: Umbral máximo nivel agua
	minTemperaturaCelsius = 15.0 // Ejemplo: Umbral mínimo temperatura
	maxTemperaturaCelsius = 35.0 // Ejemplo: Umbral máximo temperatura
	minAmbienteHumidity   = 40.0 // Ejemplo: Umbral mínimo humedad ambiente
	maxAmbienteHumidity   = 85.0 // Ejemplo: Umbral máximo humedad ambiente
	minValorPh            = 5.5  // Ejemplo: Umbral mínimo pH
	maxValorPh            = 7.5  // Ejemplo: Umbral máximo pH
)

// --- Cliente HTTP (Sin cambios) ---
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// --- StartAMQPConsumer (Ajusta URL y queueName si es necesario) ---
func StartAMQPConsumer() {
	conn, err := amqp.Dial("amqp://admin:admin@184.73.194.217:5672/") // Tu URL de RabbitMQ
	if err != nil {
		log.Fatalf("Error fatal al conectar con RabbitMQ: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Error fatal al abrir canal: %v", err)
	}
	defer ch.Close()

	queueName := "iot_queue" // Tu nombre de cola

	msgs, err := ch.Consume(
		queueName,
		"",
		true, // auto-ack: true (simple, pero menos robusto)
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Fatalf("Error fatal al registrar consumidor en la cola '%s': %v", queueName, err)
	}

	log.Printf("Consumidor AMQP iniciado. Esperando mensajes en la cola '%s'...", queueName)

	for msg := range msgs {
		log.Printf("Mensaje AMQP recibido (raw): %s", msg.Body)

		// a) Reenviar mensaje raw a clientes WebSocket (como antes)
		ws.SendMessageToClients(string(msg.Body))

		// b) Parsear, Mapear, Enviar a API de Datos y Chequear/Enviar Alertas
		// Ejecutar en goroutine para no bloquear el consumo de AMQP
		go processMessageAndAlerts(msg.Body)
	}

	log.Println("El canal de consumo AMQP se cerró. Terminando consumidor.")
}

// --- Función Principal de Procesamiento ---
func processMessageAndAlerts(msgBody []byte) {
	// 1. Parsear JSON entrante
	var incomingData IncomingData
	if err := json.Unmarshal(msgBody, &incomingData); err != nil {
		log.Printf("Error al parsear JSON entrante: %v. Mensaje: %s", err, msgBody)
		return // No continuar si no se puede parsear
	}

	// 2. Enviar datos del jardín a la API correspondiente
	sendGardenDataToAPI(incomingData)

	// 3. Verificar y enviar alertas si es necesario
	checkAndSendAlerts(incomingData)
}

// --- Función para Enviar Datos del Jardín a la API ---
func sendGardenDataToAPI(data IncomingData) {
	// Mapear a la estructura GardenData
	gardenData := GardenData{
		EnvironmentHumidity: data.HumedadAmbientePorcentaje,
		GroundHumidity:      data.HumedadSuelo,
		KitID:               data.KitID,
		PhLevel:             data.ValorPh,
		Temperature:         data.TemperaturaCelsius,
		Time:                data.MarcaTiempo,
	}

	// Convertir GardenData a JSON
	jsonData, err := json.Marshal(gardenData)
	if err != nil {
		log.Printf("Error al convertir GardenData a JSON: %v. Data: %+v", err, gardenData)
		return
	}

	// Enviar a la API /v1/garden/data/
	apiURL := apiBaseURL + gardenDataEndpoint
	err = sendPostRequest(apiURL, jsonData)
	if err != nil {
		log.Printf("Error enviando GardenData a la API (%s): %v", apiURL, err)
	} else {
		log.Printf("GardenData enviado exitosamente a la API (%s)", apiURL)
	}
}

// --- Función para Verificar y Enviar Alertas ---
func checkAndSendAlerts(data IncomingData) {
	// Verificar cada métrica contra sus umbrales
	// Humedad Suelo
	if data.HumedadSuelo < minSueloHumidity {
		generateAndSendAlert(data.KitID, "under_min", fmt.Sprintf("Humedad del suelo (%d) por debajo del mínimo (%d)", data.HumedadSuelo, minSueloHumidity))
	} else if data.HumedadSuelo > maxSueloHumidity {
		generateAndSendAlert(data.KitID, "higher_max", fmt.Sprintf("Humedad del suelo (%d) por encima del máximo (%d)", data.HumedadSuelo, maxSueloHumidity))
	}

	// Nivel Agua
	if data.NivelAguaCm < minNivelAguaCm {
		generateAndSendAlert(data.KitID, "under_min", fmt.Sprintf("Nivel de agua (%.2f cm) por debajo del mínimo (%.2f cm)", data.NivelAguaCm, minNivelAguaCm))
	} else if data.NivelAguaCm > maxNivelAguaCm {
		generateAndSendAlert(data.KitID, "higher_max", fmt.Sprintf("Nivel de agua (%.2f cm) por encima del máximo (%.2f cm)", data.NivelAguaCm, maxNivelAguaCm))
	}

	// Temperatura
	if data.TemperaturaCelsius < minTemperaturaCelsius {
		generateAndSendAlert(data.KitID, "under_min", fmt.Sprintf("Temperatura (%.1f°C) por debajo del mínimo (%.1f°C)", data.TemperaturaCelsius, minTemperaturaCelsius))
	} else if data.TemperaturaCelsius > maxTemperaturaCelsius {
		generateAndSendAlert(data.KitID, "higher_max", fmt.Sprintf("Temperatura (%.1f°C) por encima del máximo (%.1f°C)", data.TemperaturaCelsius, maxTemperaturaCelsius))
	}

	// Humedad Ambiente
	if data.HumedadAmbientePorcentaje < minAmbienteHumidity {
		generateAndSendAlert(data.KitID, "under_min", fmt.Sprintf("Humedad ambiente (%.1f%%) por debajo del mínimo (%.1f%%)", data.HumedadAmbientePorcentaje, minAmbienteHumidity))
	} else if data.HumedadAmbientePorcentaje > maxAmbienteHumidity {
		generateAndSendAlert(data.KitID, "higher_max", fmt.Sprintf("Humedad ambiente (%.1f%%) por encima del máximo (%.1f%%)", data.HumedadAmbientePorcentaje, maxAmbienteHumidity))
	}

	// Valor pH
	if data.ValorPh < minValorPh {
		generateAndSendAlert(data.KitID, "under_min", fmt.Sprintf("Valor pH (%.1f) por debajo del mínimo (%.1f)", data.ValorPh, minValorPh))
	} else if data.ValorPh > maxValorPh {
		generateAndSendAlert(data.KitID, "higher_max", fmt.Sprintf("Valor pH (%.1f) por encima del máximo (%.1f)", data.ValorPh, maxValorPh))
	}
}

// --- Función Auxiliar para Generar y Enviar una Alerta Específica ---
func generateAndSendAlert(kitID int, alertType string, message string) {
	alert := AlertData{
		KitID:     kitID,
		AlertType: alertType,
		Message:   message,
	}

	// Convertir Alerta a JSON
	alertJson, err := json.Marshal(alert)
	if err != nil {
		log.Printf("Error al convertir AlertData a JSON: %v. Alerta: %+v", err, alert)
		return
	}

	log.Printf("ALERTA GENERADA: %s", alertJson) // Log para ver la alerta

	// 1. Enviar Alerta a Clientes WebSocket
	// El mensaje enviado será el JSON de la alerta.
	ws.SendMessageToClients(string(alertJson))

	// 2. Enviar Alerta a la API /v1/alerts/ (en una goroutine para no bloquear)
	go func() {
		apiURL := apiBaseURL + alertsEndpoint
		err := sendPostRequest(apiURL, alertJson)
		if err != nil {
			log.Printf("Error enviando Alerta a la API (%s): %v", apiURL, err)
		} else {
			log.Printf("Alerta enviada exitosamente a la API (%s)", apiURL)
		}
	}()
}

// --- Función Genérica para Enviar Peticiones POST ---
func sendPostRequest(url string, jsonData []byte) error {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		// Retornamos el error para que quien llame decida cómo manejarlo
		return fmt.Errorf("error al crear petición HTTP POST para %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error al enviar petición HTTP POST a %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		// Retornamos un error con detalles
		return fmt.Errorf("respuesta no exitosa de API (%s). Status: %s, Respuesta: %s", url, resp.Status, string(bodyBytes))
	}

	// Éxito, no retornamos error (nil)
	return nil
}
