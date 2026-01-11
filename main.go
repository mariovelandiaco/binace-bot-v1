package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/gorilla/websocket"
)

// ============================================================================
// CONFIGURACIÓN
// ============================================================================
const (
	useTestnet = true // true = TESTNET (ficticio), false = PRODUCCIÓN (real)
	
	// TESTNET
	testnetWSAPI  = "wss://ws-api.testnet.binance.vision/ws-api/v3"
	testnetStream = "wss://stream.testnet.binance.vision/ws"
	testnetAPIKey = "cIxbsNc9CX08ppMM6BioVtdg5dVMquof1vTRcviZogL6i9oRdwoVQyKwT0gDEZEN"
	testnetSecret = "0GxTGs6BSlUWkjY9I3CoNzgh6Z6tZKLxgUWJ5TzsqUbtXzKSD39zYAT4pduWHzER"
	
	// PRODUCCIÓN
	prodWSAPI  = "wss://ws-api.binance.com/ws-api/v3"
	prodStream = "wss://stream.binance.com:9443/ws"
	prodAPIKey = "TU_API_KEY_PRODUCCION"
	prodSecret = "TU_SECRET_KEY_PRODUCCION"
	
	// ==================== ESTRATEGIA HFT PRO - AJUSTADA A COMISIONES ====================
	
	// COMISIONES BINANCE (0.10% por operación = 0.20% ida y vuelta SIN BNB)
	commissionPerTrade = 0.001  // 0.10% por operación (compra o venta)
	commissionRoundTrip = 0.002 // 0.20% ciclo completo (compra + venta)
	
	// TARGETS AJUSTADOS - YA INCLUYEN MARGEN PARA COMISIONES
	// Ganancia BRUTA = target, Ganancia NETA = target - 0.20%
	microScalpTarget   = 0.003  // 0.30% bruto → 0.10% neto real
	quickProfitTarget  = 0.004  // 0.40% bruto → 0.20% neto real
	normalProfitTarget = 0.007  // 0.70% bruto → 0.50% neto real
	maxProfitTarget    = 0.013  // 1.30% bruto → 1.10% neto real
	
	// STOP LOSS - Ajustados para PÉRDIDA REAL después de comisiones (0.20%)
	// Fórmula: stopLoss = pérdidaRealDeseada - comisiónRoundTrip
	microStopLoss   = 0.001  // 0.10% bruto → 0.30% pérdida REAL (0.10% + 0.20% com)
	stopLossPercent = 0.003  // 0.30% bruto → 0.50% pérdida REAL (0.30% + 0.20% com)
	trailingStop    = 0.001  // 0.10% bruto → 0.30% pérdida REAL (0.10% + 0.20% com)
	
	// GESTIÓN DE CAPITAL PRO (valores por defecto, se configuran al inicio)
	defaultMinInvestUSDT   = 11.0  // Inversión mínima por defecto
	defaultMaxInvestUSDT   = 100.0 // Inversión máxima por trade por defecto
	defaultMaxPositions    = 10    // Posiciones simultáneas por defecto
	maxRiskPerTrade        = 0.04  // 4% del balance (agresivo)
	
	// INDICADORES - Optimizados para velocidad
	rsiOverbought    = 73.0 // RSI sobrecomprado
	rsiOversold      = 27.0 // RSI sobrevendido
	rsiIdeal         = 40.0 // Zona óptima
	rsiMicroBuy      = 35.0 // RSI para micro-scalp
	
	volumeMultiplier = 1.2  // Confirmación volumen
	volumeSpike      = 3.0  // Spike = oportunidad urgente
	
	// HFT CRÍTICO
	minPriceUpdates  = 3    // Mínimo 3 updates
	analysisWindow   = 45   // Ventana 45 segundos
	microWindow      = 10   // Micro-análisis 10 segundos
	
	// MOMENTUM DETECTION
	momentumStrong   = 0.6  // Momentum fuerte
	momentumMicro    = 0.2  // Micro momentum
	
	// VOLATILIDAD - Solo operar en mercados con suficiente movimiento
	minVolatility     = 0.3   // Volatilidad mínima para considerar operar (0.3%)
	idealVolatility   = 0.8   // Volatilidad ideal para HFT (0.8%)
	maxVolatility     = 3.0   // Volatilidad máxima (muy riesgoso > 3%)
	volatilityBoost   = 1.5   // Multiplicador de confianza en alta volatilidad
	
	// PATTERN RECOGNITION
	ticksForPattern   = 8    // Ticks para patrón
	priceAcceleration = 0.03 // Aceleración de precio
	
	// WORKER POOL (Auto-scaling)
	minWorkers    = 5    // Workers mínimos
	maxWorkers    = 50   // Workers máximos
	signalBuffer  = 500  // Buffer de señales ampliado
	
	// AUTO-SCALING THRESHOLDS
	scaleUpThreshold   = 0.7  // 70% de cola llena = agregar workers
	scaleDownThreshold = 0.2  // 20% de cola = reducir workers
	scaleCheckInterval = 500 * time.Millisecond
)

var (
	wsAPIURL  string
	streamURL string
	apiKey    string
	secretKey string
	
	// Configuración del usuario (se pregunta al inicio)
	totalInvestUSDT   float64 // Dinero total a invertir en este trade
	investPerPosition float64 // Dinero por posición
	maxPositions      int     // Número máximo de posiciones simultáneas
	useStopLoss       bool    // ¿Usar estrategias de stop loss?
	onlySellOnProfit  bool    // ¿Solo vender si hay ganancia?
)

// ============================================================================
// ESTRUCTURAS DE DATOS
// ============================================================================

type AccountBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

type AccountResponse struct {
	Balances []AccountBalance `json:"balances"`
}

type SymbolInfo struct {
	Symbol string `json:"symbol"`
	Status string `json:"status"`
}

type ExchangeInfoResponse struct {
	Symbols []SymbolInfo `json:"symbols"`
}

type TickerData struct {
	EventType string `json:"e"` // Event type (24hrMiniTicker)
	EventTime int64  `json:"E"` // Event time
	Symbol    string `json:"s"` // Symbol
	Close     string `json:"c"` // Close price
	Open      string `json:"o"` // Open price
	High      string `json:"h"` // High price
	Low       string `json:"l"` // Low price
	Volume    string `json:"v"` // Total traded base asset volume
	QuoteVol  string `json:"q"` // Total traded quote asset volume
}

type PriceHistory struct {
	Prices      []float64
	Times       []int64
	Volumes     []float64
	High24h     float64   // High de 24h del ticker
	Low24h      float64   // Low de 24h del ticker
	Vol24h      float64   // Volatilidad 24h calculada
	mutex       sync.RWMutex
}

type TechnicalIndicators struct {
	RSI           float64
	MACD          float64
	MACDSignal    float64
	EMA12         float64
	EMA26         float64
	BollingerUp   float64
	BollingerDown float64
	AvgVolume     float64
	Momentum      float64
	Volatility    float64
}

type Position struct {
	Symbol        string
	BuyPrice      float64
	Quantity      float64
	TargetPrice   float64
	StopLoss      float64
	HighestPrice  float64 // Para trailing stop
	BuyTime       time.Time
	Strategy      string // "HFT", "SCALP", "SWING"
}

type TradeSignal struct {
	Action     string  // "BUY", "SELL", "HOLD"
	Confidence float64 // 0-100
	Reason     string
}

type PairStatus struct {
	Symbol        string
	CurrentPrice  float64
	Trend         string // "STRONG_UP", "UP", "NEUTRAL", "DOWN", "STRONG_DOWN"
	Change1m      float64
	Change5m      float64
	HasPosition   bool
	Position      *Position
	LastUpdate    time.Time
	PriceHistory  *PriceHistory
	Indicators    *TechnicalIndicators
	UpdateCount   int
	Signal        *TradeSignal
	Volatility    float64 // Volatilidad actual del par
	IsVolatile    bool    // ¿Supera el umbral mínimo?
}

// ============================================================================
// VARIABLES GLOBALES
// ============================================================================

// ============================================================================
// ESTRUCTURAS HFT AVANZADAS
// ============================================================================

type TradeJob struct {
	Symbol       string
	Price        float64
	Volume       float64
	Timestamp    int64
	Indicators   *TechnicalIndicators
	Signal       *TradeSignal
}

type MicroPattern struct {
	Type          string  // "BREAKOUT", "REVERSAL", "MOMENTUM", "SPIKE"
	Strength      float64
	Direction     string  // "LONG", "SHORT"
	Confidence    float64
	PriceVelocity float64
}

type CloseSummary struct {
	SuccessCount   int
	FailedCount    int
	SkippedCount   int
	TotalProfit    float64
	SuccessList    []string
	FailedList     []struct {
		Symbol string
		Reason string
	}
	SkippedList    []struct {
		Symbol string
		Reason string
	}
	FinalBalance   float64
	Timestamp      time.Time
}

var (
	apiConn     *websocket.Conn
	apiMutex    sync.Mutex
	requestID   int64
	
	usdtBalance     float64
	initialBalance  float64
	balanceMutex    sync.RWMutex
	
	pairStatuses  = make(map[string]*PairStatus)
	statusesMutex sync.RWMutex
	
	positions      = make(map[string]*Position)
	positionsMutex sync.RWMutex
	
	totalProfit      float64
	totalTrades      int
	winningTrades    int
	losingTrades     int
	totalGains       float64  // Solo suma de ganancias (operaciones positivas)
	totalLosses      float64  // Solo suma de pérdidas (operaciones negativas)
	totalCommissions float64  // Comisiones totales pagadas
	profitMutex      sync.Mutex
	
	logMessages  []string
	logMutex     sync.Mutex
	maxLogLines  = 25
	
	botStartTime time.Time
	
	// HFT Channels para procesamiento paralelo
	tradeJobsChan     chan TradeJob
	urgentSignalsChan chan TradeJob
	
	// Auto-scaling de workers
	currentWorkers    int32          // Número actual de workers
	workerWaitGroup   sync.WaitGroup // Para controlar workers
	workerContext     context.Context
	workerCancel      context.CancelFunc
	scalingMutex      sync.Mutex
	workerStopChans   []chan struct{} // Canales para detener workers individuales
	
	// Métricas de rendimiento
	totalLatency    int64
	latencyCount    int64
	latencyMutex    sync.Mutex
	jobsProcessed   int64  // Jobs procesados para métricas
	jobsQueued      int64  // Jobs en cola
	
	// Cache de indicadores para velocidad
	indicatorCache     = make(map[string]*TechnicalIndicators)
	indicatorCacheMux  sync.RWMutex
	indicatorCacheTime = make(map[string]time.Time)
	
	// Tracking de volatilidad
	volatileParirs      []string     // Pares ordenados por volatilidad (más volátil primero)
	volatilePairsMutex  sync.RWMutex
	volatilePairsCount  int          // Cuántos pares cumplen el umbral mínimo

	// Resumen de cierre de posiciones
	lastCloseSummary   *CloseSummary
	closeSummaryMutex  sync.RWMutex
	showSummaryModal   bool
	summaryModalMutex  sync.Mutex
)

// ============================================================================
// WEBSOCKET API - FUNCIONES BÁSICAS
// ============================================================================

func connectWebSocketAPI() error {
	logMsg("Conectando a WebSocket API...")
	
	conn, _, err := websocket.DefaultDialer.Dial(wsAPIURL, nil)
	if err != nil {
		return fmt.Errorf("error conectando WebSocket API: %v", err)
	}
	
	apiConn = conn
	logMsg("✅ WebSocket API conectado")
	
	// Iniciar goroutine para leer respuestas
	go readWebSocketAPIResponses()
	
	return nil
}

func readWebSocketAPIResponses() {
	for {
		_, message, err := apiConn.ReadMessage()
		if err != nil {
			logMsg(fmt.Sprintf("❌ Error leyendo WebSocket API: %v", err))
			time.Sleep(5 * time.Second)
			connectWebSocketAPI()
			return
		}
		
		// Procesar respuesta
		go handleAPIResponse(message)
	}
}

func sendAPIRequest(method string, params map[string]interface{}, needsSignature bool) (string, error) {
	apiMutex.Lock()
	requestID++
	reqID := requestID
	apiMutex.Unlock()
	
	if needsSignature {
		timestamp := time.Now().UnixMilli()
		params["timestamp"] = timestamp
		params["apiKey"] = apiKey
		
		// Crear firma
		query := buildQueryString(params)
		signature := createSignature(query)
		params["signature"] = signature
	}
	
	id := fmt.Sprintf("req_%d", reqID)
	request := map[string]interface{}{
		"id":     id,
		"method": method,
		"params": params,
	}
	
	apiMutex.Lock()
	err := apiConn.WriteJSON(request)
	apiMutex.Unlock()
	
	if err != nil {
		return "", fmt.Errorf("error enviando request: %v", err)
	}
	
	return id, nil
}

func sendAPIRequestAndWait(method string, params map[string]interface{}, needsSignature bool, timeout time.Duration) ([]byte, error) {
	id, err := sendAPIRequest(method, params, needsSignature)
	if err != nil {
		return nil, err
	}
	
	// Crear channel para recibir respuesta
	responseChan := make(chan []byte, 1)
	
	handlerMutex.Lock()
	responseHandlers[id] = responseChan
	handlerMutex.Unlock()
	
	// Esperar respuesta con timeout
	select {
	case response := <-responseChan:
		return response, nil
	case <-time.After(timeout):
		handlerMutex.Lock()
		delete(responseHandlers, id)
		handlerMutex.Unlock()
		return nil, fmt.Errorf("timeout esperando respuesta")
	}
}

func buildQueryString(params map[string]interface{}) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "signature" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	
	var parts []string
	for _, k := range keys {
		v := params[k]
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, "&")
}

func createSignature(queryString string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(queryString))
	return hex.EncodeToString(mac.Sum(nil))
}

var responseHandlers = make(map[string]chan []byte)
var handlerMutex sync.Mutex

func handleAPIResponse(message []byte) {
	var response map[string]interface{}
	if err := json.Unmarshal(message, &response); err != nil {
		return
	}
	
	// Obtener ID de la respuesta
	id := ""
	if idVal, ok := response["id"].(string); ok {
		id = idVal
	}
	
	// Si hay un handler esperando esta respuesta, enviársela
	if id != "" {
		handlerMutex.Lock()
		if ch, exists := responseHandlers[id]; exists {
			ch <- message
			delete(responseHandlers, id)
			handlerMutex.Unlock()
			return
		}
		handlerMutex.Unlock()
	}
	
	// Log de respuesta para debug
	if status, ok := response["status"].(float64); ok && status != 200 {
		logMsg(fmt.Sprintf("⚠️  API Response: %s", string(message)))
	}
	
}

// ============================================================================
// FUNCIONES DE TRADING
// ============================================================================

func getAccountBalance() error {
	params := map[string]interface{}{}
	response, err := sendAPIRequestAndWait("account.status", params, true, 10*time.Second)
	if err != nil {
		return err
	}
	
	// Parsear respuesta
	var apiResponse struct {
		Result struct {
			Balances []struct {
				Asset  string `json:"asset"`
				Free   string `json:"free"`
				Locked string `json:"locked"`
			} `json:"balances"`
		} `json:"result"`
	}
	
	if err := json.Unmarshal(response, &apiResponse); err != nil {
		return err
	}
	
	// Buscar balance USDT
	for _, balance := range apiResponse.Result.Balances {
		if balance.Asset == "USDT" {
			free, _ := strconv.ParseFloat(balance.Free, 64)
			
			balanceMutex.Lock()
			usdtBalance = free
			// Guardar balance inicial si es la primera vez
			if initialBalance == 0 {
				initialBalance = free
				botStartTime = time.Now()
			}
			balanceMutex.Unlock()
			
			logMsg(fmt.Sprintf("💰 Balance USDT: %.2f", free))
			break
		}
	}
	
	return nil
}

func getExchangeInfo() ([]string, error) {
	logMsg("Obteniendo pares USDT disponibles...")
	
	params := map[string]interface{}{}
	response, err := sendAPIRequestAndWait("exchangeInfo", params, false, 10*time.Second)
	if err != nil {
		logMsg(fmt.Sprintf("❌ Error obteniendo exchangeInfo: %v", err))
		return nil, err
	}
	
	// Parsear respuesta
	var apiResponse struct {
		Result struct {
			Symbols []struct {
				Symbol string `json:"symbol"`
				Status string `json:"status"`
			} `json:"symbols"`
		} `json:"result"`
	}
	
	if err := json.Unmarshal(response, &apiResponse); err != nil {
		logMsg(fmt.Sprintf("❌ Error parseando exchangeInfo: %v", err))
		return nil, err
	}
	
	// Filtrar pares USDT activos y tomar los primeros 10
	var usdtPairs []string
	for _, symbol := range apiResponse.Result.Symbols {
		if symbol.Status == "TRADING" && len(symbol.Symbol) > 4 && symbol.Symbol[len(symbol.Symbol)-4:] == "USDT" {
			usdtPairs = append(usdtPairs, symbol.Symbol)
			if len(usdtPairs) >= 100 {
				break
			}
		}
	}
	
	if len(usdtPairs) == 0 {
		return nil, fmt.Errorf("no se encontraron pares USDT activos")
	}
	
	logMsg(fmt.Sprintf("✅ %d pares USDT encontrados: %v", len(usdtPairs), usdtPairs))
	return usdtPairs, nil
}

func executeUrgentTrade(job TradeJob) {
	positionsMutex.RLock()
	posCount := len(positions)
	_, hasPos := positions[job.Symbol]
	positionsMutex.RUnlock()
	
	if hasPos || posCount >= maxPositions {
		return
	}
	
	balanceMutex.RLock()
	balance := usdtBalance
	balanceMutex.RUnlock()
	
	if balance < investPerPosition {
		return
	}
	
	// Calcular tamaño óptimo basado en confianza
	investAmount := investPerPosition
	if job.Signal != nil && job.Signal.Confidence > 85 {
		investAmount = math.Min(totalInvestUSDT, investPerPosition*1.5)
	}
	
	logMsg(fmt.Sprintf("⚡ SEÑAL URGENTE %s | Conf:%.0f%% | Ejecutando...", 
		job.Symbol, job.Signal.Confidence))
	
	err := placeBuyOrder(job.Symbol, investAmount)
	if err == nil {
		quantity := investAmount / job.Price
		
		pos := &Position{
			Symbol:       job.Symbol,
			BuyPrice:     job.Price,
			Quantity:     quantity,
			TargetPrice:  job.Price * (1 + microScalpTarget),
			StopLoss:     job.Price * (1 - microStopLoss),
			HighestPrice: job.Price,
			BuyTime:      time.Now(),
			Strategy:     "MICRO",
		}
		
		positionsMutex.Lock()
		positions[job.Symbol] = pos
		positionsMutex.Unlock()
		
		balanceMutex.Lock()
		usdtBalance -= investAmount
		balanceMutex.Unlock()
	}
}

func processTradeJob(job TradeJob) {
	analyzeTrendAndTrade(job.Symbol)
}

func placeBuyOrder(symbol string, usdtAmount float64) error {
	startTime := time.Now()
	
	balanceMutex.RLock()
	balanceBefore := usdtBalance
	balanceMutex.RUnlock()
	
	logMsg(fmt.Sprintf("🟢 COMPRA %s | %.2f USDT | Bal: %.2f", symbol, usdtAmount, balanceBefore))
	
	params := map[string]interface{}{
		"symbol":        symbol,
		"side":          "BUY",
		"type":          "MARKET",
		"quoteOrderQty": fmt.Sprintf("%.2f", usdtAmount),
	}
	
	_, err := sendAPIRequest("order.place", params, true)
	
	// Registrar latencia
	latency := time.Since(startTime).Milliseconds()
	latencyMutex.Lock()
	totalLatency += latency
	latencyCount++
	latencyMutex.Unlock()
	
	if err == nil {
		logMsg(fmt.Sprintf("✅ Orden ejecutada en %dms", latency))
	}
	
	return err
}

func placeSellOrder(symbol string, quantity float64) error {
	balanceMutex.RLock()
	balanceBefore := usdtBalance
	balanceMutex.RUnlock()
	
	logMsg(fmt.Sprintf("🔴 Vendiendo %.8f %s... (Balance antes: %.2f)", quantity, symbol, balanceBefore))
	
	params := map[string]interface{}{
		"symbol":   symbol,
		"side":     "SELL",
		"type":     "MARKET",
		"quantity": fmt.Sprintf("%.8f", quantity),
	}
	
	_, err := sendAPIRequest("order.place", params, true)
	
	// Nota: El balance se actualiza en la función analyzeTrendAndTrade después de confirmar la venta
	
	return err
}

// ============================================================================
// EJECUCIÓN INMEDIATA - PRIORIDAD SOBRE ANÁLISIS
// ============================================================================

// checkAndExecutePosition verifica si una posición debe cerrarse INMEDIATAMENTE
// basado en el precio actual. Retorna true si se ejecutó una acción.
// ESTA FUNCIÓN TIENE PRIORIDAD SOBRE CUALQUIER ANÁLISIS.
func checkAndExecutePosition(symbol string, currentPrice float64) bool {
	positionsMutex.RLock()
	pos, exists := positions[symbol]
	positionsMutex.RUnlock()
	
	if !exists {
		return false
	}
	
	// Calcular P/L actual (BRUTO y NETO)
	profitPctBruto := (currentPrice - pos.BuyPrice) / pos.BuyPrice
	profitPctNeto := profitPctBruto - commissionRoundTrip  // Restar 0.20% de comisiones
	
	// 🟢 TARGET ALCANZADO → VENDER INMEDIATAMENTE (solo si profit NETO > 0)
	targetPct := (pos.TargetPrice - pos.BuyPrice) / pos.BuyPrice
	if profitPctBruto >= targetPct && profitPctNeto > 0 {
		executeImmediateSell(pos, currentPrice, profitPctNeto, "TARGET_HIT")
		return true
	}
	
	// 🔴 STOP LOSS ALCANZADO → VENDER INMEDIATAMENTE (solo si useStopLoss Y !onlySellOnProfit)
	if useStopLoss && !onlySellOnProfit {
		stopPct := (pos.StopLoss - pos.BuyPrice) / pos.BuyPrice
		if profitPctBruto <= stopPct {
			executeImmediateSell(pos, currentPrice, profitPctNeto, "STOP_LOSS")
			return true
		}
	}
	
	// 📈 TRAILING STOP - Actualizar si el precio subió
	if currentPrice > pos.HighestPrice {
		positionsMutex.Lock()
		if p, ok := positions[symbol]; ok {
			p.HighestPrice = currentPrice
			// Subir el stop loss dinámicamente
			newStopLoss := currentPrice * (1 - trailingStop)
			if newStopLoss > p.StopLoss {
				p.StopLoss = newStopLoss
			}
		}
		positionsMutex.Unlock()
	}
	
	// 🔴 TRAILING STOP ALCANZADO (precio cayó desde máximo) - solo si profit NETO > 0
	if pos.HighestPrice > pos.BuyPrice {
		trailingPct := (currentPrice - pos.HighestPrice) / pos.HighestPrice
		if trailingPct <= -trailingStop && profitPctNeto > 0 {
			// Solo si estamos en profit NETO, cerrar por trailing
			executeImmediateSell(pos, currentPrice, profitPctNeto, "TRAILING_STOP")
			return true
		}
	}
	
	return false
}

// executeImmediateSell ejecuta una venta SIN ESPERAR análisis
func executeImmediateSell(pos *Position, currentPrice, profitPct float64, reason string) {
	startTime := time.Now()
	
	// Emoji según resultado
	emoji := "💰"
	if profitPct < 0 {
		emoji = "🔻"
	} else if reason == "TARGET_HIT" {
		emoji = "🎯"
	} else if reason == "TRAILING_STOP" {
		emoji = "📈"
	}
	
	logMsg(fmt.Sprintf("%s VENTA INMEDIATA %s: %.8f @ %.8f | %s | P/L: %+.3f%%",
		emoji, pos.Symbol, pos.Quantity, currentPrice, reason, profitPct*100))
	
	// Ejecutar venta
	err := placeSellOrder(pos.Symbol, pos.Quantity)
	
	latency := time.Since(startTime).Milliseconds()
	
	if err != nil {
		logMsg(fmt.Sprintf("❌ Error venta inmediata %s: %v", pos.Symbol, err))
		return
	}
	
	// Actualizar balance
	soldValue := pos.Quantity * currentPrice
	buyValue := pos.Quantity * pos.BuyPrice
	profitBruto := soldValue - buyValue
	
	// Calcular comisiones (0.10% compra + 0.10% venta)
	commissionBuy := buyValue * commissionPerTrade
	commissionSell := soldValue * commissionPerTrade
	commissionTotal := commissionBuy + commissionSell
	
	// Profit NETO = bruto - comisiones
	profitNeto := profitBruto - commissionTotal
	
	balanceMutex.Lock()
	usdtBalance += soldValue
	balanceMutex.Unlock()
	
	// Actualizar estadísticas
	profitMutex.Lock()
	totalProfit += profitNeto  // Ahora guarda el profit NETO
	totalCommissions += commissionTotal
	totalTrades++
	if profitNeto > 0 {
		winningTrades++
		totalGains += profitNeto  // Acumular solo ganancias NETAS
	} else {
		losingTrades++
		totalLosses += math.Abs(profitNeto)  // Acumular solo pérdidas NETAS
	}
	profitMutex.Unlock()
	
	// Registrar latencia
	latencyMutex.Lock()
	totalLatency += latency
	latencyCount++
	latencyMutex.Unlock()
	
	// Eliminar posición
	positionsMutex.Lock()
	delete(positions, pos.Symbol)
	positionsMutex.Unlock()
	
	logMsg(fmt.Sprintf("✅ Venta ejecutada en %dms | Bruto: %.4f | Com: %.4f | Neto: %+.4f USDT", 
		latency, profitBruto, commissionTotal, profitNeto))
}

// ============================================================================
// WEBSOCKET STREAMS - MONITOREO DE PRECIOS
// ============================================================================

func startPriceStream(symbol string) {
	streamEndpoint := fmt.Sprintf("%s/%s@miniTicker", streamURL, strings.ToLower(symbol))
	
	// Log del endpoint para debug
	if symbol == "BTCUSDC" {
		logMsg(fmt.Sprintf("🔗 Endpoint ejemplo: %s", streamEndpoint))
	}
	
	messageCount := 0
	
	for {
		conn, _, err := websocket.DefaultDialer.Dial(streamEndpoint, nil)
		if err != nil {
			logMsg(fmt.Sprintf("❌ Error conectando stream %s: %v", symbol, err))
			time.Sleep(5 * time.Second)
			continue
		}
		
		logMsg(fmt.Sprintf("📡 Stream conectado: %s", symbol))
		
		// Timeout para detectar si no llegan mensajes
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if messageCount == 0 {
					logMsg(fmt.Sprintf("⚠️  Stream %s: Sin datos en 30s (posible símbolo inexistente en Testnet)", symbol))
				} else {
					logMsg(fmt.Sprintf("❌ Stream %s desconectado después de %d mensajes", symbol, messageCount))
				}
				conn.Close()
				break
			}
			
			// Resetear timeout
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			messageCount++
			
			// Log del primer mensaje para debug
			if messageCount == 1 {
				logMsg(fmt.Sprintf("✅ %s: Primer mensaje recibido", symbol))
				// Log del mensaje raw para ver formato
				if len(message) < 500 {
					logMsg(fmt.Sprintf("📦 %s mensaje: %s", symbol, string(message)))
				}
			}
			
			var ticker TickerData
			if err := json.Unmarshal(message, &ticker); err != nil {
				if messageCount <= 2 {
					logMsg(fmt.Sprintf("⚠️  Error parseando %s: %v", symbol, err))
				}
				continue
			}
			
			price, err := strconv.ParseFloat(ticker.Close, 64)
			if err != nil || price == 0 {
				continue
			}
			
			volume, _ := strconv.ParseFloat(ticker.Volume, 64)
			high24h, _ := strconv.ParseFloat(ticker.High, 64)
			low24h, _ := strconv.ParseFloat(ticker.Low, 64)
			
			// Actualizar historial - usar timestamp actual del sistema
			currentTimestamp := time.Now().UnixMilli()
			updatePriceHistory(symbol, price, volume, currentTimestamp, high24h, low24h)
			
			// ⚡ PRIORIDAD 1: Verificar posiciones ANTES de cualquier análisis
			// Si el precio alcanzó target o stop loss → ejecutar INMEDIATAMENTE
			if checkAndExecutePosition(symbol, price) {
				continue // Posición cerrada, no analizar más
			}
			
			// HFT: Análisis inmediato de micro-patrones
			statusesMutex.RLock()
			status := pairStatuses[symbol]
			statusesMutex.RUnlock()
			
			if status != nil && status.PriceHistory != nil {
				status.PriceHistory.mutex.RLock()
				prices := status.PriceHistory.Prices
				volumes := status.PriceHistory.Volumes
				status.PriceHistory.mutex.RUnlock()
				
				// Detección de micro-patrones (ultra rápido)
				if len(prices) >= ticksForPattern {
					pattern := detectMicroPattern(prices, volumes)
					if pattern != nil && pattern.Confidence > 75 && pattern.Direction == "LONG" {
						// Verificar si no tenemos posición
						positionsMutex.RLock()
						_, hasPos := positions[symbol]
						positionsMutex.RUnlock()
						
						if !hasPos {
							// Enviar señal urgente
							job := TradeJob{
								Symbol:    symbol,
								Price:     price,
								Volume:    volume,
								Timestamp: ticker.EventTime,
								Signal: &TradeSignal{
									Action:     "BUY",
									Confidence: pattern.Confidence,
									Reason:     fmt.Sprintf("Pattern:%s Str:%.2f", pattern.Type, pattern.Strength),
								},
							}
							
							select {
							case urgentSignalsChan <- job:
								// Enviado
							default:
								// Canal lleno, procesar normal
							}
						}
					}
				}
			}
			
			// Análisis estándar (en paralelo)
			select {
			case tradeJobsChan <- TradeJob{Symbol: symbol, Price: price}:
			default:
				// Buffer lleno, procesar directo
				go analyzeTrendAndTrade(symbol)
			}
		}
		
		time.Sleep(2 * time.Second)
	}
}

func updatePriceHistory(symbol string, price float64, volume float64, timestamp int64, high24h float64, low24h float64) {
	statusesMutex.Lock()
	defer statusesMutex.Unlock()
	
	status, exists := pairStatuses[symbol]
	if !exists {
		status = &PairStatus{
			Symbol:       symbol,
			PriceHistory: &PriceHistory{},
			Indicators:   &TechnicalIndicators{},
			Signal:       &TradeSignal{Action: "HOLD"},
		}
		pairStatuses[symbol] = status
	}
	
	status.CurrentPrice = price
	status.LastUpdate = time.Now()
	status.UpdateCount++
	
	// Agregar precio y volumen al historial
	history := status.PriceHistory
	history.mutex.Lock()
	history.Prices = append(history.Prices, price)
	history.Times = append(history.Times, timestamp)
	history.Volumes = append(history.Volumes, volume)
	
	// Guardar High/Low de 24h y calcular volatilidad 24h
	history.High24h = high24h
	history.Low24h = low24h
	if low24h > 0 {
		history.Vol24h = ((high24h - low24h) / low24h) * 100  // Volatilidad 24h en %
	}
	
	// Mantener solo últimos 2 minutos de datos (120 segundos)
	cutoffTime := timestamp - int64(analysisWindow*1000)
	idx := 0
	for i, t := range history.Times {
		if t >= cutoffTime {
			idx = i
			break
		}
	}
	if idx > 0 {
		history.Prices = history.Prices[idx:]
		history.Times = history.Times[idx:]
		history.Volumes = history.Volumes[idx:]
	}
	
	// Log de diagnóstico cada 50 actualizaciones para el primer par
	priceCount := len(history.Prices)
	if status.UpdateCount%100 == 0 && priceCount > 0 {
		high := history.Prices[0]
		low := history.Prices[0]
		for _, p := range history.Prices {
			if p > high { high = p }
			if p < low { low = p }
		}
		spread := 0.0
		if low > 0 {
			spread = ((high - low) / low) * 100
		}
		logMsg(fmt.Sprintf("📊 %s: %d precios | High:%.4f Low:%.4f | Spread:%.4f%%", 
			symbol, priceCount, high, low, spread))
	}
	
	history.mutex.Unlock()
}

// ============================================================================
// INICIALIZACIÓN HFT CON AUTO-SCALING
// ============================================================================

func initHFTSystem() {
	// Crear contexto para control de workers
	workerContext, workerCancel = context.WithCancel(context.Background())
	
	// Crear canales de alta velocidad
	tradeJobsChan = make(chan TradeJob, signalBuffer)
	urgentSignalsChan = make(chan TradeJob, 100)
	workerStopChans = make([]chan struct{}, 0, maxWorkers)
	
	// Iniciar workers mínimos
	for i := 0; i < minWorkers; i++ {
		spawnWorker()
	}
	
	// Worker para señales urgentes (prioridad alta)
	go urgentSignalWorker()
	
	// Iniciar auto-scaler
	go autoScaler()
	
	logMsg(fmt.Sprintf("⚡ Sistema HFT con Auto-Scaling iniciado: %d-%d workers", minWorkers, maxWorkers))
}

func spawnWorker() {
	scalingMutex.Lock()
	defer scalingMutex.Unlock()
	
	if int(atomic.LoadInt32(&currentWorkers)) >= maxWorkers {
		return
	}
	
	stopChan := make(chan struct{})
	workerStopChans = append(workerStopChans, stopChan)
	workerID := len(workerStopChans) - 1
	
	atomic.AddInt32(&currentWorkers, 1)
	workerWaitGroup.Add(1)
	
	go tradeWorker(workerID, stopChan)
}

func removeWorker() {
	scalingMutex.Lock()
	defer scalingMutex.Unlock()
	
	if int(atomic.LoadInt32(&currentWorkers)) <= minWorkers {
		return
	}
	
	if len(workerStopChans) > 0 {
		// Detener el último worker
		lastIdx := len(workerStopChans) - 1
		close(workerStopChans[lastIdx])
		workerStopChans = workerStopChans[:lastIdx]
		atomic.AddInt32(&currentWorkers, -1)
	}
}

func autoScaler() {
	ticker := time.NewTicker(scaleCheckInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-workerContext.Done():
			return
		case <-ticker.C:
			queueLen := len(tradeJobsChan)
			queueCap := cap(tradeJobsChan)
			fillRatio := float64(queueLen) / float64(queueCap)
			
			workers := atomic.LoadInt32(&currentWorkers)
			
			if fillRatio > scaleUpThreshold && int(workers) < maxWorkers {
				// Escalar hacia arriba
				workersToAdd := int(math.Ceil(float64(maxWorkers-int(workers)) * 0.3))
				if workersToAdd < 1 {
					workersToAdd = 1
				}
				for i := 0; i < workersToAdd; i++ {
					spawnWorker()
				}
				logMsg(fmt.Sprintf("📈 Auto-scale UP: %d → %d workers (cola: %.0f%%)", 
					workers, atomic.LoadInt32(&currentWorkers), fillRatio*100))
				
			} else if fillRatio < scaleDownThreshold && int(workers) > minWorkers {
				// Escalar hacia abajo (más gradual)
				workersToRemove := 1
				for i := 0; i < workersToRemove; i++ {
					removeWorker()
				}
				logMsg(fmt.Sprintf("📉 Auto-scale DOWN: %d → %d workers (cola: %.0f%%)", 
					workers, atomic.LoadInt32(&currentWorkers), fillRatio*100))
			}
		}
	}
}

func tradeWorker(id int, stopChan chan struct{}) {
	defer workerWaitGroup.Done()
	
	for {
		select {
		case <-stopChan:
			return
		case <-workerContext.Done():
			return
		case job, ok := <-tradeJobsChan:
			if !ok {
				return
			}
			atomic.AddInt64(&jobsProcessed, 1)
			processTradeJob(job)
		}
	}
}

func urgentSignalWorker() {
	for job := range urgentSignalsChan {
		// Procesar señales urgentes inmediatamente
		processUrgentSignal(job)
	}
}

func processUrgentSignal(job TradeJob) {
	// Verificar si la señal sigue siendo válida
	statusesMutex.RLock()
	status, exists := pairStatuses[job.Symbol]
	if !exists {
		statusesMutex.RUnlock()
		return
	}
	currentPrice := status.CurrentPrice
	statusesMutex.RUnlock()
	
	// Si el precio cambió más de 0.1%, la señal ya no es válida
	priceDiff := math.Abs((currentPrice - job.Price) / job.Price * 100)
	if priceDiff > 0.1 {
		return
	}
	
	// Ejecutar trade urgente
	executeUrgentTrade(job)
}

// ============================================================================
// DETECCIÓN DE MICRO-PATRONES HFT
// ============================================================================

func detectMicroPattern(prices []float64, volumes []float64) *MicroPattern {
	if len(prices) < ticksForPattern {
		return nil
	}
	
	n := len(prices)
	current := prices[n-1]
	
	// Calcular velocidad de precio (derivada)
	velocities := make([]float64, n-1)
	for i := 1; i < n; i++ {
		velocities[i-1] = (prices[i] - prices[i-1]) / prices[i-1] * 100
	}
	
	avgVelocity := 0.0
	for _, v := range velocities {
		avgVelocity += v
	}
	avgVelocity /= float64(len(velocities))
	
	recentVelocity := velocities[len(velocities)-1]
	
	// Calcular aceleración
	acceleration := recentVelocity - avgVelocity
	
	// Detectar spike de volumen
	avgVol := 0.0
	for _, v := range volumes[:n-1] {
		avgVol += v
	}
	avgVol /= float64(n - 1)
	volRatio := volumes[n-1] / avgVol
	
	pattern := &MicroPattern{
		Type:          "NONE",
		Strength:      0,
		Direction:     "NEUTRAL",
		PriceVelocity: recentVelocity,
	}
	
	// BREAKOUT: Aceleración positiva + volumen alto
	if acceleration > priceAcceleration && volRatio > volumeSpike {
		pattern.Type = "BREAKOUT"
		pattern.Strength = acceleration * volRatio
		pattern.Direction = "LONG"
		pattern.Confidence = math.Min(95, 60+acceleration*100+volRatio*10)
	}
	
	// REVERSAL: Precio bajo + momentum positivo
	if recentVelocity > 0 && avgVelocity < -0.05 {
		pattern.Type = "REVERSAL"
		pattern.Strength = recentVelocity - avgVelocity
		pattern.Direction = "LONG"
		pattern.Confidence = math.Min(90, 50+pattern.Strength*50)
	}
	
	// MOMENTUM: Velocidad consistente
	if recentVelocity > momentumMicro && avgVelocity > 0 {
		pattern.Type = "MOMENTUM"
		pattern.Strength = recentVelocity
		pattern.Direction = "LONG"
		pattern.Confidence = math.Min(85, 55+recentVelocity*30)
	}
	
	// SPIKE: Cambio brusco con volumen
	priceChange := (current - prices[n-3]) / prices[n-3] * 100
	if priceChange > 0.15 && volRatio > 2.0 {
		pattern.Type = "SPIKE"
		pattern.Strength = priceChange * volRatio
		pattern.Direction = "LONG"
		pattern.Confidence = math.Min(88, 65+priceChange*20)
	}
	
	return pattern
}

// ============================================================================
// INDICADORES TÉCNICOS PROFESIONALES
// ============================================================================

// Calcular RSI (Relative Strength Index)
func calculateRSI(prices []float64, period int) float64 {
	if len(prices) < period+1 {
		return 50.0 // Neutral
	}
	
	gains := 0.0
	losses := 0.0
	
	for i := len(prices) - period; i < len(prices); i++ {
		change := prices[i] - prices[i-1]
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}
	
	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)
	
	if avgLoss == 0 {
		return 100.0
	}
	
	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))
	
	return rsi
}

// Calcular EMA (Exponential Moving Average)
func calculateEMA(prices []float64, period int) float64 {
	if len(prices) < period {
		return prices[len(prices)-1]
	}
	
	multiplier := 2.0 / float64(period+1)
	ema := prices[len(prices)-period]
	
	for i := len(prices) - period + 1; i < len(prices); i++ {
		ema = (prices[i]-ema)*multiplier + ema
	}
	
	return ema
}

// Calcular MACD
func calculateMACD(prices []float64) (float64, float64, float64) {
	ema12 := calculateEMA(prices, 12)
	ema26 := calculateEMA(prices, 26)
	macd := ema12 - ema26
	
	// Señal (EMA de 9 del MACD) - simplificado
	signal := macd * 0.9
	histogram := macd - signal
	
	return macd, signal, histogram
}

// Calcular Bandas de Bollinger
func calculateBollinger(prices []float64, period int) (float64, float64, float64) {
	if len(prices) < period {
		p := prices[len(prices)-1]
		return p, p, p
	}
	
	// Media
	sum := 0.0
	for i := len(prices) - period; i < len(prices); i++ {
		sum += prices[i]
	}
	sma := sum / float64(period)
	
	// Desviación estándar
	variance := 0.0
	for i := len(prices) - period; i < len(prices); i++ {
		diff := prices[i] - sma
		variance += diff * diff
	}
	stdDev := math.Sqrt(variance / float64(period))
	
	upper := sma + (stdDev * 2)
	lower := sma - (stdDev * 2)
	
	return upper, sma, lower
}

// Calcular todos los indicadores
func calculateIndicators(history *PriceHistory) *TechnicalIndicators {
	history.mutex.RLock()
	defer history.mutex.RUnlock()
	
	priceCount := len(history.Prices)
	if priceCount < 5 {
		return &TechnicalIndicators{}
	}
	
	prices := history.Prices
	volumes := history.Volumes
	
	// RSI
	rsi := calculateRSI(prices, 14)
	
	// MACD
	macd, signal, _ := calculateMACD(prices)
	
	// EMAs
	ema12 := calculateEMA(prices, 12)
	ema26 := calculateEMA(prices, 26)
	
	// Bollinger
	bollUp, _, bollDown := calculateBollinger(prices, 20)
	
	// Volumen promedio
	avgVol := 0.0
	if len(volumes) > 0 {
		for _, v := range volumes {
			avgVol += v
		}
		avgVol /= float64(len(volumes))
	}
	
	// Momentum (cambio en últimos 10 períodos)
	momentum := 0.0
	if len(prices) >= 10 {
		momentum = ((prices[len(prices)-1] - prices[len(prices)-10]) / prices[len(prices)-10]) * 100
	}
	
	// Volatilidad - Usar Vol24h del ticker (más confiable) + cálculo local
	volatility := history.Vol24h  // Volatilidad 24h de Binance
	
	// Si no hay Vol24h, calcular con datos locales
	if volatility == 0 && len(prices) >= 5 {
		window := len(prices)
		if window > 30 {
			window = 30
		}
		
		startIdx := len(prices) - window
		high := prices[startIdx]
		low := prices[startIdx]
		sum := 0.0
		
		for i := startIdx; i < len(prices); i++ {
			if prices[i] > high {
				high = prices[i]
			}
			if prices[i] < low {
				low = prices[i]
			}
			sum += prices[i]
		}
		mean := sum / float64(window)
		
		if mean > 0 && low > 0 {
			volatility = ((high - low) / low) * 100
		}
	}
	
	return &TechnicalIndicators{
		RSI:           rsi,
		MACD:          macd,
		MACDSignal:    signal,
		EMA12:         ema12,
		EMA26:         ema26,
		BollingerUp:   bollUp,
		BollingerDown: bollDown,
		AvgVolume:     avgVol,
		Momentum:      momentum,
		Volatility:    volatility,
	}
}

var lastLogTime = make(map[string]time.Time)
var logTimeMutex sync.Mutex

// ============================================================================
// ESTRATEGIA AVANZADA DE TRADING
// ============================================================================

func generateTradeSignal(status *PairStatus, indicators *TechnicalIndicators) *TradeSignal {
	signal := &TradeSignal{Action: "HOLD", Confidence: 0}
	
	history := status.PriceHistory
	history.mutex.RLock()
	priceCount := len(history.Prices)
	if priceCount < minPriceUpdates {
		history.mutex.RUnlock()
		return signal
	}
	
	currentPrice := history.Prices[priceCount-1]
	avgVolume := indicators.AvgVolume
	currentVolume := history.Volumes[priceCount-1]
	history.mutex.RUnlock()
	
	// Sistema de puntuación multifactor (0-100)
	buyScore := 50.0  // Neutral
	sellScore := 50.0
	
	// 1. RSI (peso: 25 puntos)
	if indicators.RSI < rsiOversold {
		buyScore += 25 // Fuertemente sobrevendido
	} else if indicators.RSI < rsiIdeal {
		buyScore += 15 // Zona de compra
	} else if indicators.RSI > rsiOverbought {
		buyScore -= 20 // Sobrecomprado
		sellScore += 15
	}
	
	// 2. MACD (peso: 20 puntos)
	if indicators.MACD > indicators.MACDSignal && indicators.MACD > 0 {
		buyScore += 20 // Cruce alcista
	} else if indicators.MACD < indicators.MACDSignal && indicators.MACD < 0 {
		sellScore += 15 // Cruce bajista
	}
	
	// 3. EMAs (peso: 15 puntos)
	if indicators.EMA12 > indicators.EMA26 {
		buyScore += 15 // Tendencia alcista
	} else {
		sellScore += 10 // Tendencia bajista
	}
	
	// 4. Bollinger Bands (peso: 15 puntos)
	bollRange := indicators.BollingerUp - indicators.BollingerDown
	if bollRange > 0 {
		position := (currentPrice - indicators.BollingerDown) / bollRange
		if position < 0.2 {
			buyScore += 15 // Cerca del límite inferior
		} else if position > 0.8 {
			sellScore += 15 // Cerca del límite superior
		}
	}
	
	// 5. Momentum (peso: 15 puntos)
	if indicators.Momentum > 0.5 {
		buyScore += 15
	} else if indicators.Momentum < -0.5 {
		sellScore += 15
	} else if indicators.Momentum > 0.2 {
		buyScore += 8
	}
	
	// 6. Volumen (peso: 10 puntos) - Alta frecuencia
	if currentVolume > avgVolume*volumeMultiplier {
		if buyScore > 60 {
			buyScore += 10 // Confirma compra con volumen
		} else if sellScore > 60 {
			sellScore += 10 // Confirma venta con volumen
		}
	}
	
	// Decisión final
	if buyScore >= 70 && buyScore > sellScore+10 {
		signal.Action = "BUY"
		signal.Confidence = math.Min(buyScore, 100)
		signal.Reason = fmt.Sprintf("RSI:%.0f MACD:%.4f Mom:%.2f%% Vol:%.0f%%", 
			indicators.RSI, indicators.MACD, indicators.Momentum, (currentVolume/avgVolume)*100)
	} else if sellScore >= 70 && sellScore > buyScore+10 {
		signal.Action = "SELL"
		signal.Confidence = math.Min(sellScore, 100)
		signal.Reason = "Indicadores bajistas"
	}
	
	return signal
}

func analyzeTrendAndTrade(symbol string) {
	statusesMutex.RLock()
	status, exists := pairStatuses[symbol]
	if !exists {
		statusesMutex.RUnlock()
		return
	}
	
	history := status.PriceHistory
	history.mutex.RLock()
	priceCount := len(history.Prices)
	history.mutex.RUnlock()
	statusesMutex.RUnlock()
	
	// Requiere mínimo de actualizaciones
	if priceCount < minPriceUpdates {
		return
	}
	
	// Calcular indicadores técnicos
	indicators := calculateIndicators(history)
	
	// Generar señal de trading
	signal := generateTradeSignal(status, indicators)
	
	// Calcular cambios porcentuales
	history.mutex.RLock()
	currentPrice := history.Prices[priceCount-1]
	oldPrice1m := history.Prices[0]
	change1m := ((currentPrice - oldPrice1m) / oldPrice1m) * 100
	
	// Cambio 5 minutos si tenemos suficientes datos
	change5m := 0.0
	for i := len(history.Times) - 1; i >= 0; i-- {
		if history.Times[len(history.Times)-1]-history.Times[i] >= 300000 { // 5 min
			change5m = ((currentPrice - history.Prices[i]) / history.Prices[i]) * 100
			break
		}
	}
	history.mutex.RUnlock()
	
	// Determinar tendencia con indicadores
	trend := "NEUTRAL"
	if indicators.RSI < 30 && indicators.Momentum > 0 {
		trend = "STRONG_UP"
	} else if indicators.RSI > 70 && indicators.Momentum < 0 {
		trend = "STRONG_DOWN"
	} else if change1m > 0.1 {
		trend = "UP"
	} else if change1m < -0.1 {
		trend = "DOWN"
	}
	
	// Log periódico con indicadores
	logTimeMutex.Lock()
	lastLog := lastLogTime[symbol]
	shouldLog := time.Since(lastLog) > 60*time.Second
	if shouldLog {
		lastLogTime[symbol] = time.Now()
	}
	logTimeMutex.Unlock()
	
	if shouldLog {
		logMsg(fmt.Sprintf("📈 %s: RSI:%.0f MACD:%.4f Mom:%.2f%% | Señal:%s(%.0f%%) | %s", 
			symbol, indicators.RSI, indicators.MACD, indicators.Momentum, 
			signal.Action, signal.Confidence, signal.Reason))
	}
	
	// Actualizar status
	// Actualizar volatilidad del par
	pairVolatility := 0.0
	isVolatile := false
	if indicators != nil {
		pairVolatility = indicators.Volatility
		isVolatile = pairVolatility >= minVolatility
	}
	
	statusesMutex.Lock()
	status.Trend = trend
	status.Change1m = change1m
	status.Change5m = change5m
	status.Indicators = indicators
	status.Signal = signal
	status.Volatility = pairVolatility
	status.IsVolatile = isVolatile
	statusesMutex.Unlock()
	
	// ====== GESTIÓN DE POSICIONES ======
	positionsMutex.RLock()
	position, hasPosition := positions[symbol]
	positionsMutex.RUnlock()
	
	if hasPosition {
		// ====== GESTIÓN DE POSICIÓN ACTIVA ======
		profitPercentBruto := ((currentPrice - position.BuyPrice) / position.BuyPrice) * 100
		// Profit NETO = bruto - comisión ida y vuelta (0.20%)
		profitPercentNeto := profitPercentBruto - (commissionRoundTrip * 100)
		
		// Actualizar trailing stop
		if currentPrice > position.HighestPrice {
			positionsMutex.Lock()
			position.HighestPrice = currentPrice
			// Trailing stop: si ha subido, ajustar stop loss (usar bruto para trailing)
			if profitPercentBruto > quickProfitTarget*100 {
				newStopLoss := position.HighestPrice * (1 - trailingStop)
				if newStopLoss > position.StopLoss {
					position.StopLoss = newStopLoss
				}
			}
			positionsMutex.Unlock()
		}
		
		shouldSell := false
		sellReason := ""
		
		// 1. Stop Loss - cortar pérdidas (solo si useStopLoss y !onlySellOnProfit)
		if useStopLoss && !onlySellOnProfit && currentPrice <= position.StopLoss {
			shouldSell = true
			sellReason = fmt.Sprintf("Stop Loss (Neto: %.2f%%)", profitPercentNeto)
		}
		
		// 2. Take Profit rápido (HFT) - verificar profit NETO > 0
		if profitPercentBruto >= quickProfitTarget*100 && position.Strategy == "HFT" && profitPercentNeto > 0 {
			shouldSell = true
			sellReason = fmt.Sprintf("Take Profit Rápido (Neto: %.2f%%)", profitPercentNeto)
		}
		
		// 3. Take Profit normal - verificar profit NETO > 0
		if profitPercentBruto >= normalProfitTarget*100 && profitPercentNeto > 0 {
			shouldSell = true
			sellReason = fmt.Sprintf("Take Profit (Neto: %.2f%%)", profitPercentNeto)
		}
		
		// 4. Señal técnica de venta - SOLO si profit NETO > 0 cuando onlySellOnProfit está activo
		if signal.Action == "SELL" && signal.Confidence > 75 {
			if profitPercentNeto > 0 || !onlySellOnProfit {
				shouldSell = true
				sellReason = fmt.Sprintf("Señal Técnica (Neto: %.2f%%)", profitPercentNeto)
			}
		}
		
		// 5. RSI extremadamente sobrecomprado - SOLO si profit NETO > 0
		if indicators.RSI > 80 && profitPercentNeto > 0 {
			shouldSell = true
			sellReason = fmt.Sprintf("RSI Sobrecomprado (Neto: %.2f%%)", profitPercentNeto)
		}
		
		if shouldSell {
			go func() {
				placeSellOrder(symbol, position.Quantity)
				
				// Calcular ganancia/pérdida de la operación
				buyValue := position.BuyPrice * position.Quantity
				saleAmount := currentPrice * position.Quantity
				profitBruto := saleAmount - buyValue
				
				// Calcular comisiones (0.10% compra + 0.10% venta)
				commBuy := buyValue * commissionPerTrade
				commSell := saleAmount * commissionPerTrade
				commTotal := commBuy + commSell
				
				// Profit NETO
				profitNeto := profitBruto - commTotal
				
				// Actualizar balance (agregar lo vendido)
				balanceMutex.Lock()
				usdtBalance += saleAmount
				balanceMutex.Unlock()
				
				// Actualizar estadísticas
				profitMutex.Lock()
				totalProfit += profitNeto
				totalCommissions += commTotal
				totalTrades++
				if profitNeto > 0 {
					winningTrades++
					totalGains += profitNeto  // Acumular solo ganancias NETAS
				} else {
					losingTrades++
					totalLosses += math.Abs(profitNeto)  // Acumular solo pérdidas NETAS
				}
				profitMutex.Unlock()
				
				positionsMutex.Lock()
				delete(positions, symbol)
				positionsMutex.Unlock()
				
				emoji := "✅"
				if profitNeto < 0 {
					emoji = "❌"
				}
				
				logMsg(fmt.Sprintf("%s %s | %s | Bruto: %.4f | Com: %.4f | Neto: %.4f USDT", 
					emoji, symbol, sellReason, profitBruto, commTotal, profitNeto))
			}()
		}
		
	} else {
		// ====== BUSCAR OPORTUNIDADES DE COMPRA ======
		
		// Verificar número máximo de posiciones
		positionsMutex.RLock()
		posCount := len(positions)
		positionsMutex.RUnlock()
		
		if posCount >= maxPositions {
			return
		}
		
		// Verificar balance disponible
		balanceMutex.RLock()
		balance := usdtBalance
		balanceMutex.RUnlock()
		
		if balance < investPerPosition {
			return
		}
		
		// ⚡ FILTRO DE VOLATILIDAD - Solo operar en mercados con movimiento suficiente
		if !isVolatile {
			// Par con volatilidad muy baja, no vale la pena operar (comisiones se comen las ganancias)
			return
		}
		
		// Determinar si comprar basado en señal
		shouldBuy := false
		buyReason := ""
		strategy := "NORMAL"
		
		// Boost de confianza por volatilidad (mercados volátiles = más oportunidades)
		volatilityBoostFactor := 1.0
		if pairVolatility >= idealVolatility {
			volatilityBoostFactor = volatilityBoost
		}
		
		// Ajustar umbrales si la volatilidad es alta
		confThresholdHigh := 80.0
		confThresholdMod := 70.0
		if pairVolatility >= idealVolatility {
			confThresholdHigh = 70.0  // Más permisivo en alta volatilidad
			confThresholdMod = 60.0
		}
		
		// 1. Señal técnica fuerte de compra
		if signal.Action == "BUY" && signal.Confidence >= confThresholdHigh {
			shouldBuy = true
			buyReason = fmt.Sprintf("Señal Fuerte (%.0f%%) Vol:%.2f%% - %s", signal.Confidence, pairVolatility, signal.Reason)
			strategy = "HFT"
		}
		
		// 2. Señal técnica moderada + tendencia
		if signal.Action == "BUY" && signal.Confidence >= confThresholdMod && (trend == "UP" || trend == "STRONG_UP") {
			shouldBuy = true
			buyReason = fmt.Sprintf("Señal+Tendencia (%.0f%%) Vol:%.2f%%", signal.Confidence, pairVolatility)
			strategy = "SCALP"
		}
		
		// 3. RSI sobrevendido + momentum positivo (solo en alta volatilidad)
		if indicators.RSI < 35 && indicators.Momentum > 0.3 && pairVolatility >= minVolatility {
			shouldBuy = true
			buyReason = fmt.Sprintf("RSI Bajo (%.0f) + Mom Vol:%.2f%%", indicators.RSI, pairVolatility)
			strategy = "SWING"
		}
		
		// 4. Cruce MACD alcista fuerte
		if indicators.MACD > indicators.MACDSignal && 
		   indicators.MACD > 0 && 
		   indicators.EMA12 > indicators.EMA26 &&
		   pairVolatility >= minVolatility {
			shouldBuy = true
			buyReason = fmt.Sprintf("MACD Alcista Vol:%.2f%%", pairVolatility)
			strategy = "NORMAL"
		}
		
		// Boost final de confianza si el mercado es muy volátil
		if shouldBuy && volatilityBoostFactor > 1.0 {
			signal.Confidence = math.Min(100, signal.Confidence*volatilityBoostFactor)
		}
		
		if shouldBuy {
			logMsg(fmt.Sprintf("💡 %s: %s | RSI:%.0f MACD:%.4f", 
				symbol, buyReason, indicators.RSI, indicators.MACD))
			
			go func() {
				// Calcular tamaño de posición basado en volatilidad
				investAmount := investPerPosition
				if indicators.Volatility > 2.0 {
					investAmount = investPerPosition * 0.7 // Reducir en alta volatilidad
				}
				
				if err := placeBuyOrder(symbol, investAmount); err == nil {
					quantity := investAmount / currentPrice
					
					// Definir stop loss y target según estrategia
					stopLoss := currentPrice * (1 - stopLossPercent)
					targetPrice := currentPrice * (1 + normalProfitTarget)
					if strategy == "HFT" {
						targetPrice = currentPrice * (1 + quickProfitTarget)
					}
					
					pos := &Position{
						Symbol:       symbol,
						BuyPrice:     currentPrice,
						Quantity:     quantity,
						TargetPrice:  targetPrice,
						StopLoss:     stopLoss,
						HighestPrice: currentPrice,
						BuyTime:      time.Now(),
						Strategy:     strategy,
					}
					
					positionsMutex.Lock()
					positions[symbol] = pos
					positionsMutex.Unlock()
					
					balanceMutex.Lock()
					usdtBalance -= investAmount
					balanceMutex.Unlock()
				}
			}()
		}
	}
}

// ============================================================================
// UI - PANEL TIPO HTOP
// ============================================================================

func startUI() {
	if err := ui.Init(); err != nil {
		log.Fatalf("failed to initialize termui: %v", err)
	}
	defer ui.Close()
	
	// Crear widgets
	headerBox := widgets.NewParagraph()
	stopLossStatus := "🛡️ Stop Loss: ON"
	if !useStopLoss {
		stopLossStatus = "⚠️ Stop Loss: OFF"
	}
	headerBox.Title = fmt.Sprintf("⚡ HFT PRO BOT - MICRO-SCALPING ULTRA RÁPIDO (RSI+MACD+Patterns+Workers) | %s", stopLossStatus)
	headerBox.SetRect(0, 0, 120, 3)
	headerBox.BorderStyle.Fg = ui.ColorCyan
	
	statsBox := widgets.NewParagraph()
	statsBox.Title = "📊 Estadísticas"
	statsBox.SetRect(0, 3, 60, 11)  // Aumentado para 6 líneas de stats
	statsBox.BorderStyle.Fg = ui.ColorGreen
	
	positionsTable := widgets.NewTable()
	positionsTable.Title = "💼 Posiciones Activas"
	positionsTable.SetRect(60, 3, 120, 20)
	positionsTable.TextStyle = ui.NewStyle(ui.ColorWhite)
	positionsTable.BorderStyle.Fg = ui.ColorYellow
	
	pricesTable := widgets.NewTable()
	pricesTable.Title = "💹 Precios en Tiempo Real"
	pricesTable.SetRect(0, 11, 60, 35)  // Ajustado para no superponerse
	pricesTable.TextStyle = ui.NewStyle(ui.ColorWhite)
	pricesTable.RowSeparator = false
	
	logBox := widgets.NewList()
	logBox.Title = "📝 Log de Actividad"
	logBox.SetRect(60, 20, 120, 35)
	logBox.BorderStyle.Fg = ui.ColorMagenta
	
	// Actualizar UI cada 500ms
	uiEvents := ui.PollEvents()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	
	render := func() {
		// Header
		env := "TESTNET"
		if !useTestnet {
			env = "PRODUCCIÓN"
		}
		headerBox.Text = fmt.Sprintf(" Entorno: [%s](fg:yellow)  |  WebSocket API: ✅ Conectado", env)
		
		// Stats
		balanceMutex.RLock()
		balance := usdtBalance
		balanceMutex.RUnlock()
		
		profitMutex.Lock()
		trades := totalTrades
		profitMutex.Unlock()
		
		positionsMutex.RLock()
		activePos := len(positions)
		positionsMutex.RUnlock()
		
		statusesMutex.RLock()
		activePairs := len(pairStatuses)
		statusesMutex.RUnlock()
		
		volatilePairsMutex.RLock()
		volPairs := volatilePairsCount
		volatilePairsMutex.RUnlock()
		
		// Calcular ganancia de sesión
		balanceMutex.RLock()
		initial := initialBalance
		current := balance
		balanceMutex.RUnlock()
		
		sessionProfit := current - initial
		sessionProfitPercent := 0.0
		if initial > 0 {
			sessionProfitPercent = (sessionProfit / initial) * 100
		}
		
		// Calcular win rate real
		profitMutex.Lock()
		winning := winningTrades
		losing := losingTrades
		profitMutex.Unlock()
		
		winRate := 0.0
		if trades > 0 {
			winRate = (float64(winning) / float64(trades)) * 100
		}
		
		// Tiempo de ejecución
		uptime := time.Since(botStartTime).Round(time.Second)
		
		profitColor := "green"
		if sessionProfit < 0 {
			profitColor = "red"
		}
		
		// Calcular latencia promedio
		latencyMutex.Lock()
		avgLatency := int64(0)
		if latencyCount > 0 {
			avgLatency = totalLatency / latencyCount
		}
		latencyMutex.Unlock()
		
		// Obtener ganancias, pérdidas y comisiones acumuladas
		profitMutex.Lock()
		gains := totalGains
		losses := totalLosses
		commissions := totalCommissions
		profitMutex.Unlock()
		
		netProfit := gains - losses
		netColor := "green"
		if netProfit < 0 {
			netColor = "red"
		}
		
		workers := atomic.LoadInt32(&currentWorkers)
		queueLen := len(tradeJobsChan)
		queueCap := cap(tradeJobsChan)
		processed := atomic.LoadInt64(&jobsProcessed)
		
		statsBox.Text = fmt.Sprintf(
			"💰 Balance: [%.2f](fg:green) | Inicial: [%.2f](fg:white) | Sesión: [%+.2f USDT](fg:%s) [(%+.2f%%)](fg:%s)\n"+
				"📊 Posiciones: [%d/%d](fg:yellow) | Pares: [%d](fg:cyan) 🔥Volátiles: [%d](fg:green) | Workers: [%d/%d](fg:magenta)\n"+
				"📈 Trades: [%d](fg:white) ✅[%d](fg:green) ❌[%d](fg:red) | Win Rate: [%.1f%%](fg:cyan)\n"+
				"💵 Ganancias(NETO): [+%.4f](fg:green) | Pérdidas: [-%.4f](fg:red) | 💸Comisiones: [%.4f](fg:yellow)\n"+
				"📊 P/L Real: [%+.4f USDT](fg:%s) | ⚡Lat: [%dms](fg:yellow) | Cola: [%d/%d](fg:cyan) | Jobs: [%d](fg:cyan)\n"+
				"⏱️  Uptime: [%s](fg:white) | Modo: [⚡ HFT - Min Vol: %.1f%%](fg:magenta)",
			current, initial, sessionProfit, profitColor, sessionProfitPercent, profitColor,
			activePos, maxPositions, activePairs, volPairs, workers, maxWorkers,
			trades, winning, losing, winRate,
			gains, losses, commissions,
			netProfit, netColor, avgLatency, queueLen, queueCap, processed,
			uptime, minVolatility*100,
		)
		
		// Tabla de posiciones con stop loss y estrategia
		posRows := [][]string{{"Símbolo", "Compra", "Actual", "Target", "Stop", "P/L%", "Estrategia"}}
		positionsMutex.RLock()
		for _, pos := range positions {
			statusesMutex.RLock()
			status := pairStatuses[pos.Symbol]
			currentPrice := 0.0
			if status != nil {
				currentPrice = status.CurrentPrice
			}
			statusesMutex.RUnlock()
			
			profitPct := 0.0
			plColor := "white"
			if currentPrice > 0 {
				profitPct = ((currentPrice - pos.BuyPrice) / pos.BuyPrice) * 100
				if profitPct > 0 {
					plColor = "green"
				} else if profitPct < -0.3 {
					plColor = "red"
				}
			}
			
			posRows = append(posRows, []string{
				pos.Symbol,
				fmt.Sprintf("%.4f", pos.BuyPrice),
				fmt.Sprintf("%.4f", currentPrice),
				fmt.Sprintf("%.4f", pos.TargetPrice),
				fmt.Sprintf("%.4f", pos.StopLoss),
				fmt.Sprintf("[%.2f%%](fg:%s)", profitPct, plColor),
				pos.Strategy,
			})
		}
		positionsMutex.RUnlock()
		positionsTable.Rows = posRows
		
		// Tabla de precios con indicadores HFT - Ordenado por VOLATILIDAD
		priceRows := [][]string{{"Símbolo", "Precio", "Vol%", "RSI", "Señal", "Conf%"}}
		statusesMutex.RLock()
		
		// Convertir a slice para ordenar
		var statusList []*PairStatus
		volatileCount := 0
		for _, s := range pairStatuses {
			statusList = append(statusList, s)
			if s.IsVolatile {
				volatileCount++
			}
		}
		statusesMutex.RUnlock()
		
		// Actualizar contador global
		volatilePairsMutex.Lock()
		volatilePairsCount = volatileCount
		volatilePairsMutex.Unlock()
		
		// Ordenar por VOLATILIDAD (más volátil primero = más oportunidades)
		sort.Slice(statusList, func(i, j int) bool {
			return statusList[i].Volatility > statusList[j].Volatility
		})
		
		// Mostrar top 12
		for i, status := range statusList {
			if i >= 12 {
				break
			}
			
			rsiStr := "-"
			signalStr := "WAIT"
			confStr := "-"
			volStr := "-"
			volColor := "white"
			signalColor := "white"
			
			if status.Indicators != nil {
				rsiStr = fmt.Sprintf("%.0f", status.Indicators.RSI)
			}
			
			// Mostrar volatilidad con colores
			volStr = fmt.Sprintf("%.2f", status.Volatility)
			if status.Volatility >= idealVolatility {
				volColor = "green,bold"  // Alta volatilidad = bueno para HFT
				volStr = "🔥" + volStr
			} else if status.Volatility >= minVolatility {
				volColor = "green"       // Volatilidad aceptable
			} else {
				volColor = "red"         // Muy baja, no operar
				volStr = "❄️" + volStr
			}
			
			if status.Signal != nil {
				signalStr = status.Signal.Action
				confStr = fmt.Sprintf("%.0f", status.Signal.Confidence)
				
				if status.Signal.Action == "BUY" {
					if status.Signal.Confidence >= 80 {
						signalColor = "green,bold"
						signalStr = "⚡BUY"
					} else if status.Signal.Confidence >= 70 {
						signalColor = "green"
					}
				} else if status.Signal.Action == "SELL" {
					signalColor = "red"
				}
			}
			
			priceRows = append(priceRows, []string{
				status.Symbol,
				fmt.Sprintf("%.4f", status.CurrentPrice),
				fmt.Sprintf("[%s](fg:%s)", volStr, volColor),
				rsiStr,
				fmt.Sprintf("[%s](fg:%s)", signalStr, signalColor),
				confStr,
			})
		}
		pricesTable.Rows = priceRows
		
		// Log
		logMutex.Lock()
		logBox.Rows = logMessages
		if len(logMessages) > maxLogLines {
			logBox.Rows = logMessages[len(logMessages)-maxLogLines:]
		}
		logMutex.Unlock()
		
		ui.Render(headerBox, statsBox, positionsTable, pricesTable, logBox)
	}
	
	// Modal de cierre de trades
	closeModal := widgets.NewParagraph()
	closeModal.Title = "🔒 CERRAR TRADES"
	closeModal.SetRect(30, 10, 90, 20)
	closeModal.BorderStyle.Fg = ui.ColorRed
	closeModal.Text = "\n   Selecciona una opción:\n\n" +
		"   [1] Vender TODAS las posiciones al precio actual\n" +
		"   [2] Vender SOLO con GANANCIA (mantener pérdidas)\n\n" +
		"   [ESC] Cancelar y volver"

	showCloseModal := false

	// Modal de resumen de cierre
	summaryModal := widgets.NewParagraph()
	summaryModal.Title = "📋 RESUMEN DE CIERRE"
	summaryModal.SetRect(10, 5, 110, 30)
	summaryModal.BorderStyle.Fg = ui.ColorGreen
	summaryModal.WrapText = true
	
	for {
		select {
		case e := <-uiEvents:
			// Verificar si hay modal de resumen disponible
			summaryModalMutex.Lock()
			summaryActive := showSummaryModal
			summaryModalMutex.Unlock()

			if summaryActive {
				// Modal de resumen activo
				switch e.ID {
				case "<Enter>", "<Escape>", "q":
					summaryModalMutex.Lock()
					showSummaryModal = false
					summaryModalMutex.Unlock()
					logMsg("✅ Resumen cerrado")
					render()
				}
			} else if showCloseModal {
				// Modal de cierre activo
				switch e.ID {
				case "1":
					showCloseModal = false
					logMsg("🔄 Cerrando TODAS las posiciones...")
					go closeAllTrades(false) // false = vender todo
					render()
				case "2":
					showCloseModal = false
					logMsg("🔄 Cerrando posiciones con GANANCIA...")
					go closeAllTrades(true) // true = solo con ganancia
					render()
				case "<Escape>", "q":
					showCloseModal = false
					logMsg("❌ Cierre de trades cancelado")
					render()
				}
			} else {
				// Modo normal
				switch e.ID {
				case "q", "<C-c>":
					return
				case "c":
					showCloseModal = true
					ui.Render(headerBox, statsBox, positionsTable, pricesTable, logBox, closeModal)
				case "<Enter>":
					// Si hay un resumen disponible, mostrarlo
					closeSummaryMutex.RLock()
					hasSummary := lastCloseSummary != nil
					closeSummaryMutex.RUnlock()

					if hasSummary {
						summaryModalMutex.Lock()
						showSummaryModal = true
						summaryModalMutex.Unlock()
					}
				}
			}
		case <-ticker.C:
			// Verificar si hay modal de resumen disponible
			summaryModalMutex.Lock()
			summaryActive := showSummaryModal
			summaryModalMutex.Unlock()

			if summaryActive {
				// Actualizar contenido del modal de resumen
				closeSummaryMutex.RLock()
				if lastCloseSummary != nil {
					summaryText := fmt.Sprintf("\n  ⏰ Hora: %s\n\n", lastCloseSummary.Timestamp.Format("15:04:05"))
					summaryText += fmt.Sprintf("  💰 Balance Final: %.2f USDT\n", lastCloseSummary.FinalBalance)
					summaryText += fmt.Sprintf("  📊 Ganancia Neta: %+.4f USDT\n\n", lastCloseSummary.TotalProfit)

					summaryText += fmt.Sprintf("  ✅ Vendidas: %d | ❌ Fallidas: %d | ⏸️  Omitidas: %d\n\n",
						lastCloseSummary.SuccessCount, lastCloseSummary.FailedCount, lastCloseSummary.SkippedCount)

					if len(lastCloseSummary.SuccessList) > 0 {
						summaryText += "  ✅ VENTAS EXITOSAS:\n"
						for _, item := range lastCloseSummary.SuccessList {
							summaryText += fmt.Sprintf("     • %s\n", item)
						}
						summaryText += "\n"
					}

					if len(lastCloseSummary.FailedList) > 0 {
						summaryText += "  ❌ VENTAS FALLIDAS:\n"
						for _, item := range lastCloseSummary.FailedList {
							summaryText += fmt.Sprintf("     • %s - %s\n", item.Symbol, item.Reason)
						}
						summaryText += "\n"
					}

					if len(lastCloseSummary.SkippedList) > 0 {
						summaryText += "  ⏸️  POSICIONES OMITIDAS:\n"
						for _, item := range lastCloseSummary.SkippedList {
							summaryText += fmt.Sprintf("     • %s - %s\n", item.Symbol, item.Reason)
						}
						summaryText += "\n"
					}

					summaryText += "\n  [ENTER/ESC] Cerrar"
					summaryModal.Text = summaryText

					// Ajustar color del borde según el resultado
					if lastCloseSummary.TotalProfit > 0 {
						summaryModal.BorderStyle.Fg = ui.ColorGreen
					} else if lastCloseSummary.TotalProfit < 0 {
						summaryModal.BorderStyle.Fg = ui.ColorRed
					} else {
						summaryModal.BorderStyle.Fg = ui.ColorYellow
					}
				}
				closeSummaryMutex.RUnlock()

				ui.Render(headerBox, statsBox, positionsTable, pricesTable, logBox, summaryModal)
			} else if !showCloseModal {
				render()
			} else {
				// Mantener el modal de cierre visible
				ui.Render(headerBox, statsBox, positionsTable, pricesTable, logBox, closeModal)
			}
		}
	}
}

func logMsg(msg string) {
	timestamp := time.Now().Format("15:04:05")
	logLine := fmt.Sprintf("[%s] %s", timestamp, msg)
	
	logMutex.Lock()
	logMessages = append(logMessages, logLine)
	if len(logMessages) > 100 {
		logMessages = logMessages[1:]
	}
	logMutex.Unlock()
}

// ============================================================================
// CONFIGURACIÓN INICIAL INTERACTIVA
// ============================================================================

func askUserConfiguration() {
	reader := bufio.NewReader(os.Stdin)
	
	fmt.Println("\n" + strings.Repeat("═", 60))
	fmt.Println("⚙️  CONFIGURACIÓN DEL TRADE")
	fmt.Println(strings.Repeat("═", 60))
	
	// Pregunta 1: ¿Cuánto dinero invertir?
	fmt.Print("\n💰 ¿Cuánto dinero invertir en este trade? (USDT) [default: 100]: ")
	input1, _ := reader.ReadString('\n')
	input1 = strings.TrimSpace(input1)
	if input1 == "" {
		totalInvestUSDT = defaultMaxInvestUSDT
	} else {
		val, err := strconv.ParseFloat(input1, 64)
		if err != nil || val <= 0 {
			fmt.Println("⚠️  Valor inválido, usando default: 100 USDT")
			totalInvestUSDT = defaultMaxInvestUSDT
		} else {
			totalInvestUSDT = val
		}
	}
	fmt.Printf("   ✅ Inversión total: %.2f USDT\n", totalInvestUSDT)
	
	// Pregunta 2: ¿Cuánto dinero por posición?
	maxPerPos := totalInvestUSDT / float64(maxPositions)
	fmt.Printf("\n📊 ¿Cuánto dinero por posición? (USDT) [default: %.2f, max: %.2f]: ", defaultMinInvestUSDT, maxPerPos)
	input2, _ := reader.ReadString('\n')
	input2 = strings.TrimSpace(input2)
	if input2 == "" {
		investPerPosition = defaultMinInvestUSDT
	} else {
		val, err := strconv.ParseFloat(input2, 64)
		if err != nil || val <= 0 {
			fmt.Println("⚠️  Valor inválido, usando default")
			investPerPosition = defaultMinInvestUSDT
		} else if val > maxPerPos {
			fmt.Printf("⚠️  Valor muy alto, ajustando a %.2f USDT\n", maxPerPos)
			investPerPosition = maxPerPos
		} else {
			investPerPosition = val
		}
	}
	fmt.Printf("   ✅ Inversión por posición: %.2f USDT\n", investPerPosition)
	
	// Pregunta 3: ¿Número de posiciones?
	fmt.Printf("\n📊 ¿Cuántas posiciones simultáneas máximo? [default: %d]: ", defaultMaxPositions)
	input3, _ := reader.ReadString('\n')
	input3 = strings.TrimSpace(input3)
	if input3 == "" {
		maxPositions = defaultMaxPositions
	} else {
		val, err := strconv.Atoi(input3)
		if err != nil || val <= 0 {
			fmt.Printf("⚠️  Valor inválido, usando default: %d\n", defaultMaxPositions)
			maxPositions = defaultMaxPositions
		} else if val > 50 {
			fmt.Println("⚠️  Máximo permitido: 50 posiciones")
			maxPositions = 50
		} else {
			maxPositions = val
		}
	}
	fmt.Printf("   ✅ Posiciones máximas: %d\n", maxPositions)
	
	// Pregunta 4: ¿Usar stop loss?
	fmt.Print("\n🛡️  ¿Aplicar estrategias de Stop Loss? (s/n) [default: s]: ")
	input4, _ := reader.ReadString('\n')
	input4 = strings.ToLower(strings.TrimSpace(input4))
	if input4 == "" || input4 == "s" || input4 == "si" || input4 == "yes" || input4 == "y" {
		useStopLoss = true
		onlySellOnProfit = false  // Con stop loss, puede vender con pérdida
		fmt.Println("   ✅ Stop Loss: ACTIVADO (venderá si llega al stop loss)")
	} else {
		useStopLoss = false
		onlySellOnProfit = true   // Sin stop loss, solo vende con ganancia
		fmt.Println("   ✅ Stop Loss: DESACTIVADO (solo venderá con ganancia)")
	}
	
	// Resumen
	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Println("📋 RESUMEN DE CONFIGURACIÓN:")
	fmt.Printf("   • Inversión total: %.2f USDT\n", totalInvestUSDT)
	fmt.Printf("   • Por posición: %.2f USDT\n", investPerPosition)
	fmt.Printf("   • Max posiciones: %d\n", maxPositions)
	fmt.Printf("   • Stop Loss: %v\n", useStopLoss)
	fmt.Println(strings.Repeat("─", 60))
	
	fmt.Print("\n🚀 Presiona ENTER para iniciar el bot...")
	reader.ReadString('\n')
}

// ============================================================================
// CIERRE DE TRADES
// ============================================================================

func closeAllTrades(onlyProfit bool) {
	positionsMutex.RLock()
	positionsToClose := make([]*Position, 0)
	for _, pos := range positions {
		positionsToClose = append(positionsToClose, pos)
	}
	positionsMutex.RUnlock()

	if len(positionsToClose) == 0 {
		logMsg("📭 No hay posiciones activas para cerrar")
		return
	}

	// Crear resumen de cierre
	summary := &CloseSummary{
		SuccessList: make([]string, 0),
		FailedList:  make([]struct{ Symbol string; Reason string }, 0),
		SkippedList: make([]struct{ Symbol string; Reason string }, 0),
		Timestamp:   time.Now(),
	}

	closedCount := 0
	skippedCount := 0
	totalProfitClosed := 0.0
	
	for _, pos := range positionsToClose {
		statusesMutex.RLock()
		status := pairStatuses[pos.Symbol]
		currentPrice := 0.0
		if status != nil {
			currentPrice = status.CurrentPrice
		}
		statusesMutex.RUnlock()
		
		if currentPrice <= 0 {
			reason := "Sin precio actual"
			logMsg(fmt.Sprintf("⚠️  %s: %s, omitido", pos.Symbol, reason))
			summary.SkippedList = append(summary.SkippedList, struct{ Symbol string; Reason string }{
				Symbol: pos.Symbol,
				Reason: reason,
			})
			skippedCount++
			continue
		}
		
		buyValue := pos.BuyPrice * pos.Quantity
		saleValue := currentPrice * pos.Quantity
		profitBruto := saleValue - buyValue
		
		// Calcular comisiones
		commBuy := buyValue * commissionPerTrade
		commSell := saleValue * commissionPerTrade
		commTotal := commBuy + commSell
		profitNeto := profitBruto - commTotal
		
		profitPct := (profitNeto / buyValue) * 100  // Porcentaje NETO
		
		// Si solo con ganancia, verificar profit NETO
		if onlyProfit && profitNeto < 0 {
			reason := fmt.Sprintf("P/L neto %.2f%% (pérdida)", profitPct)
			logMsg(fmt.Sprintf("⏸️  %s: %s, NO vendido", pos.Symbol, reason))
			summary.SkippedList = append(summary.SkippedList, struct{ Symbol string; Reason string }{
				Symbol: pos.Symbol,
				Reason: reason,
			})
			skippedCount++
			continue
		}
		
		// Ejecutar venta
		logMsg(fmt.Sprintf("📤 Vendiendo %s: Compra=%.4f, Actual=%.4f, Bruto=%.4f, Com=%.4f", 
			pos.Symbol, pos.BuyPrice, currentPrice, profitBruto, commTotal))
		
		params := map[string]interface{}{
			"symbol":   pos.Symbol,
			"side":     "SELL",
			"type":     "MARKET",
			"quantity": fmt.Sprintf("%.6f", pos.Quantity),
		}
		
		_, err := sendAPIRequestAndWait("order.place", params, true, 10*time.Second)
		if err != nil {
			reason := fmt.Sprintf("Error API: %v", err)
			logMsg(fmt.Sprintf("❌ Error vendiendo %s: %v", pos.Symbol, err))
			summary.FailedList = append(summary.FailedList, struct{ Symbol string; Reason string }{
				Symbol: pos.Symbol,
				Reason: reason,
			})
			continue
		}
		
		// Actualizar estadísticas con profit NETO
		profitMutex.Lock()
		totalTrades++
		totalCommissions += commTotal
		if profitNeto > 0 {
			winningTrades++
			totalGains += profitNeto
		} else {
			losingTrades++
			totalLosses += math.Abs(profitNeto)
		}
		totalProfit += profitNeto
		profitMutex.Unlock()
		
		totalProfitClosed += profitNeto
		
		// Eliminar posición
		positionsMutex.Lock()
		delete(positions, pos.Symbol)
		positionsMutex.Unlock()

		// Registrar venta exitosa
		summary.SuccessList = append(summary.SuccessList, fmt.Sprintf("%s: %.4f USDT (%.2f%%)", pos.Symbol, profitNeto, profitPct))
		closedCount++
		logMsg(fmt.Sprintf("✅ %s vendido: Neto %.4f USDT (%.2f%%)", pos.Symbol, profitNeto, profitPct))
	}
	
	// Completar resumen con contadores
	summary.SuccessCount = closedCount
	summary.FailedCount = len(summary.FailedList)
	summary.SkippedCount = skippedCount
	summary.TotalProfit = totalProfitClosed

	logMsg(fmt.Sprintf("📊 Cierre completado: %d vendidas, %d fallidas, %d omitidas, P/L total: %.4f USDT",
		closedCount, len(summary.FailedList), skippedCount, totalProfitClosed))

	// Actualizar balance desde Binance (solo si se vendió algo)
	if closedCount > 0 {
		logMsg("🔄 Actualizando balance desde Binance...")
		// Delay para dar tiempo a que Binance procese las órdenes
		time.Sleep(500 * time.Millisecond)

		// Intentar actualizar con reintentos
		maxRetries := 3
		for i := 0; i < maxRetries; i++ {
			err := getAccountBalance()
			if err == nil {
				logMsg("✅ Balance actualizado correctamente")
				break
			}

			if i < maxRetries-1 {
				logMsg(fmt.Sprintf("⚠️  Error actualizando balance (intento %d/%d): %v", i+1, maxRetries, err))
				time.Sleep(1 * time.Second)
			} else {
				logMsg(fmt.Sprintf("❌ No se pudo actualizar el balance después de %d intentos: %v", maxRetries, err))
			}
		}
	}

	// Obtener balance final
	balanceMutex.RLock()
	summary.FinalBalance = usdtBalance
	balanceMutex.RUnlock()

	// Guardar resumen y activar modal
	closeSummaryMutex.Lock()
	lastCloseSummary = summary
	closeSummaryMutex.Unlock()

	summaryModalMutex.Lock()
	showSummaryModal = true
	summaryModalMutex.Unlock()

	logMsg("📋 Resumen de cierre disponible - Presiona [ENTER] para ver detalles")
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	// Configurar entorno
	if useTestnet {
		wsAPIURL = testnetWSAPI
		streamURL = testnetStream
		apiKey = testnetAPIKey
		secretKey = testnetSecret
	} else {
		wsAPIURL = prodWSAPI
		streamURL = prodStream
		apiKey = prodAPIKey
		secretKey = prodSecret
	}
	
	// Log inicial en terminal
	fmt.Println("🚀 Iniciando Bot de Trading Binance - USDT")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	
	if useTestnet {
		fmt.Println("🧪 Entorno: TESTNET (dinero ficticio)")
	} else {
		fmt.Println("🔴 Entorno: PRODUCCIÓN (dinero REAL)")
	}
	
	// Preguntas de configuración
	askUserConfiguration()
	
	fmt.Println()
	
	// Inicializar sistema HFT
	initHFTSystem()
	
	// Conectar WebSocket API
	if err := connectWebSocketAPI(); err != nil {
		log.Fatal(err)
	}
	
	// Esperar un poco a que se establezca la conexión
	time.Sleep(500 * time.Millisecond) // Reducido para más velocidad
	
	// Obtener balance
	logMsg("Consultando balance de cuenta...")
	if err := getAccountBalance(); err != nil {
		logMsg(fmt.Sprintf("⚠️  Error obteniendo balance: %v", err))
		logMsg("Continuando con balance en 0...")
	}
	
	// Obtener pares USDT dinámicamente
	pairs, err := getExchangeInfo()
	if err != nil {
		log.Fatal(fmt.Sprintf("Error obteniendo pares: %v", err))
	}
	
	// Iniciar stream para cada par
	logMsg(fmt.Sprintf("Iniciando streams para %d pares...", len(pairs)))
	for _, pair := range pairs {
		go startPriceStream(pair)
		time.Sleep(100 * time.Millisecond) // Evitar saturar
	}
	
	time.Sleep(2 * time.Second)
	
	logMsg("✅ Sistema iniciado - Presiona 'q' salir | 'c' cerrar trades")
	
	// Iniciar UI
	startUI()
	
	fmt.Println("\n👋 Bot detenido")
}
