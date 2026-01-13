package main

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ============================================================================
// WEB SERVER & WEBSOCKET
// ============================================================================

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Permitir todas las conexiones (ajustar en producción)
		},
	}

	// Clientes WebSocket conectados
	wsClients     = make(map[*websocket.Conn]bool)
	wsClientsMutex sync.RWMutex

	// Canal para broadcast
	wsBroadcast = make(chan DashboardData, 100)

	// Estado del bot
	botRunning      bool
	botRunningMutex sync.RWMutex
	streamsStarted  bool
	streamsMutex    sync.RWMutex
)

// DashboardData contiene todos los datos del dashboard
type DashboardData struct {
	Timestamp        time.Time         `json:"timestamp"`
	Header           HeaderData        `json:"header"`
	Stats            StatsData         `json:"stats"`
	Config           ConfigData        `json:"config"`
	Positions        []PositionData    `json:"positions"`
	Prices           []PriceData       `json:"prices"`
	Logs             []string          `json:"logs"`
	CloseSummary     *CloseSummaryData `json:"closeSummary,omitempty"`
}

type HeaderData struct {
	Title       string `json:"title"`
	Environment string `json:"environment"`
	Connected   bool   `json:"connected"`
	StopLoss    bool   `json:"stopLoss"`
}

type StatsData struct {
	Balance              float64 `json:"balance"`
	InitialBalance       float64 `json:"initialBalance"`
	SessionProfit        float64 `json:"sessionProfit"`
	SessionProfitPercent float64 `json:"sessionProfitPercent"`
	ActivePositions      int     `json:"activePositions"`
	MaxPositions         int     `json:"maxPositions"`
	ActivePairs          int     `json:"activePairs"`
	VolatilePairs        int     `json:"volatilePairs"`
	Workers              int32   `json:"workers"`
	MaxWorkers           int     `json:"maxWorkers"`
	TotalTrades          int     `json:"totalTrades"`
	WinningTrades        int     `json:"winningTrades"`
	LosingTrades         int     `json:"losingTrades"`
	WinRate              float64 `json:"winRate"`
	TotalGains           float64 `json:"totalGains"`
	TotalLosses          float64 `json:"totalLosses"`
	TotalCommissions     float64 `json:"totalCommissions"`
	NetProfit            float64 `json:"netProfit"`
	AvgLatency           int64   `json:"avgLatency"`
	QueueLength          int     `json:"queueLength"`
	QueueCapacity        int     `json:"queueCapacity"`
	JobsProcessed        int64   `json:"jobsProcessed"`
	DirectAnalysisActive int32   `json:"directAnalysisActive"`
	Uptime               string  `json:"uptime"`
	MinVolatility        float64 `json:"minVolatility"`
}

type ConfigData struct {
	BotRunning         bool    `json:"botRunning"`
	UseTestnet         bool    `json:"useTestnet"`         // true = TESTNET, false = PRODUCCIÓN
	UserAPIKey         string  `json:"userAPIKey"`         // API Key del usuario
	UserSecretKey      string  `json:"userSecretKey"`      // Secret Key del usuario
	TotalInvestUSDT    float64 `json:"totalInvestUSDT"`
	InvestPerPosition  float64 `json:"investPerPosition"`
	MaxPositions       int     `json:"maxPositions"`
	UseStopLoss        bool    `json:"useStopLoss"`
	OnlySellOnProfit   bool    `json:"onlySellOnProfit"`
	QuickProfitTarget  float64 `json:"quickProfitTarget"`  // en porcentaje (ej: 0.4 para 0.4%)
	NormalProfitTarget float64 `json:"normalProfitTarget"` // en porcentaje (ej: 0.7 para 0.7%)
	StopLossPercent    float64 `json:"stopLossPercent"`    // en porcentaje (ej: 0.3 para 0.3%)
	TrailingStop       float64 `json:"trailingStop"`       // en porcentaje (ej: 0.1 para 0.1%)
}

type PositionData struct {
	Symbol       string  `json:"symbol"`
	BuyPrice     float64 `json:"buyPrice"`
	CurrentPrice float64 `json:"currentPrice"`
	TargetPrice  float64 `json:"targetPrice"`
	StopLoss     float64 `json:"stopLoss"`
	ProfitPct    float64 `json:"profitPct"`
	Strategy     string  `json:"strategy"`
}

type PriceData struct {
	Symbol      string  `json:"symbol"`
	Price       float64 `json:"price"`
	Volatility  float64 `json:"volatility"`
	IsVolatile  bool    `json:"isVolatile"`
	RSI         float64 `json:"rsi"`
	Signal      string  `json:"signal"`
	Confidence  float64 `json:"confidence"`
}

type CloseSummaryData struct {
	Timestamp    time.Time         `json:"timestamp"`
	FinalBalance float64           `json:"finalBalance"`
	TotalProfit  float64           `json:"totalProfit"`
	SuccessCount int               `json:"successCount"`
	FailedCount  int               `json:"failedCount"`
	SkippedCount int               `json:"skippedCount"`
	SuccessList  []string          `json:"successList"`
	FailedList   []FailedTradeItem `json:"failedList"`
	SkippedList  []FailedTradeItem `json:"skippedList"`
}

type FailedTradeItem struct {
	Symbol string `json:"symbol"`
	Reason string `json:"reason"`
}

// WebSocket handler
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	wsClientsMutex.Lock()
	wsClients[conn] = true
	wsClientsMutex.Unlock()

	log.Printf("New WebSocket client connected from %s", conn.RemoteAddr())

	// Enviar datos iniciales
	data := collectDashboardData()
	if err := conn.WriteJSON(data); err != nil {
		log.Printf("Error sending initial data: %v", err)
	}

	// Leer mensajes del cliente (comandos)
	go func() {
		defer func() {
			wsClientsMutex.Lock()
			delete(wsClients, conn)
			wsClientsMutex.Unlock()
			conn.Close()
			log.Printf("WebSocket client disconnected from %s", conn.RemoteAddr())
		}()

		for {
			var cmd map[string]interface{}
			err := conn.ReadJSON(&cmd)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				break
			}

			// Procesar comandos
			handleCommand(cmd)
		}
	}()
}

// Manejar comandos del cliente
func handleCommand(cmd map[string]interface{}) {
	action, ok := cmd["action"].(string)
	if !ok {
		return
	}

	switch action {
	case "closeAll":
		logMsg("🔄 Cerrando TODAS las posiciones desde web...")
		go closeAllTrades(false)
	case "closeProfit":
		logMsg("🔄 Cerrando posiciones con GANANCIA desde web...")
		go closeAllTrades(true)
	case "configure":
		handleConfigure(cmd)
	case "start":
		handleStart()
	case "stop":
		handleStop()
	case "dismissSummary":
		handleDismissSummary()
	case "reset":
		handleReset()
	}
}

// Manejar configuración
func handleConfigure(cmd map[string]interface{}) {
	config, ok := cmd["config"].(map[string]interface{})
	if !ok {
		logMsg("❌ Error: configuración inválida")
		return
	}

	// Actualizar configuración de conexión
	envChanged := false
	if val, ok := config["useTestnet"].(bool); ok {
		if useTestnet != val {
			useTestnet = val
			envChanged = true
		}
	}
	if val, ok := config["userAPIKey"].(string); ok {
		userAPIKey = val
	}
	if val, ok := config["userSecretKey"].(string); ok {
		userSecretKey = val
	}

	// Si cambió el entorno o las credenciales, actualizar configuración
	if envChanged || userAPIKey != "" || userSecretKey != "" {
		updateEnvironmentConfig()

		// Mostrar advertencia si hay posiciones abiertas
		positionsMutex.RLock()
		hasPositions := len(positions) > 0
		positionsMutex.RUnlock()

		if hasPositions {
			logMsg("⚠️  ADVERTENCIA: Cambio de entorno con posiciones abiertas")
			logMsg("   Se recomienda cerrar todas las posiciones antes de cambiar de entorno")
		}

		if envChanged {
			if useTestnet {
				logMsg("🧪 Modo cambiado a: TESTNET (ficticio)")
			} else {
				logMsg("🔴 Modo cambiado a: PRODUCCIÓN (dinero real)")
			}
		}
		if userAPIKey != "" || userSecretKey != "" {
			logMsg("🔑 Credenciales de API actualizadas")
		}
	}

	// Actualizar configuración global
	if val, ok := config["totalInvestUSDT"].(float64); ok && val > 0 {
		totalInvestUSDT = val
	}
	if val, ok := config["investPerPosition"].(float64); ok && val > 0 {
		investPerPosition = val
	}
	if val, ok := config["maxPositions"].(float64); ok && val > 0 {
		maxPositions = int(val)
	}
	if val, ok := config["useStopLoss"].(bool); ok {
		useStopLoss = val
		if val {
			onlySellOnProfit = false
		} else {
			onlySellOnProfit = true
		}
	}

	// Actualizar targets y stop loss (convertir de % a decimal)
	if val, ok := config["quickProfitTarget"].(float64); ok && val > 0 {
		quickProfitTarget = val / 100.0 // Convertir de porcentaje a decimal
	}
	if val, ok := config["normalProfitTarget"].(float64); ok && val > 0 {
		normalProfitTarget = val / 100.0
	}
	if val, ok := config["stopLossPercent"].(float64); ok && val > 0 {
		stopLossPercent = val / 100.0
	}
	if val, ok := config["trailingStop"].(float64); ok && val > 0 {
		trailingStop = val / 100.0
	}

	logMsg("✅ Configuración actualizada desde web")
	logMsg("   • Inversión total: " + formatFloat(totalInvestUSDT) + " USDT")
	logMsg("   • Por posición: " + formatFloat(investPerPosition) + " USDT")
	logMsg("   • Max posiciones: " + formatInt(maxPositions))
	logMsg("   • Stop Loss: " + formatBool(useStopLoss))
	logMsg("   • Target rápido: " + formatFloat(quickProfitTarget*100) + "%")
	logMsg("   • Target normal: " + formatFloat(normalProfitTarget*100) + "%")
	logMsg("   • Stop Loss %: " + formatFloat(stopLossPercent*100) + "%")
	logMsg("   • Trailing Stop: " + formatFloat(trailingStop*100) + "%")
}

// Manejar inicio del bot
func handleStart() {
	botRunningMutex.Lock()
	if botRunning {
		botRunningMutex.Unlock()
		logMsg("⚠️  El bot ya está en ejecución")
		return
	}
	botRunning = true
	botRunningMutex.Unlock()

	logMsg("🚀 Iniciando bot desde web...")

	// Iniciar streams si no están iniciados
	streamsMutex.Lock()
	if !streamsStarted {
		streamsStarted = true
		streamsMutex.Unlock()

		go func() {
			// Obtener pares USDT dinámicamente
			pairs, err := getExchangeInfo()
			if err != nil {
				logMsg("❌ Error obteniendo pares: " + err.Error())
				return
			}

			// Iniciar stream para cada par
			logMsg("📡 Iniciando streams para " + formatInt(len(pairs)) + " pares...")
			for _, pair := range pairs {
				go startPriceStream(pair)
				time.Sleep(100 * time.Millisecond)
			}

			logMsg("✅ Bot iniciado correctamente")
		}()
	} else {
		streamsMutex.Unlock()
		logMsg("✅ Bot reanudado")
	}
}

// Manejar detención del bot
func handleStop() {
	botRunningMutex.Lock()
	if !botRunning {
		botRunningMutex.Unlock()
		logMsg("⚠️  El bot ya está detenido")
		return
	}
	botRunning = false
	botRunningMutex.Unlock()

	logMsg("⏸️  Bot detenido desde web")
}

// Manejar descarte del resumen de cierre
func handleDismissSummary() {
	summaryModalMutex.Lock()
	showSummaryModal = false
	summaryModalMutex.Unlock()

	logMsg("✅ Resumen de cierre descartado")
}

// Manejar reset completo del bot
func handleReset() {
	logMsg("🔄 Iniciando reset completo del bot...")

	// Detener el bot si está corriendo
	botRunningMutex.Lock()
	wasRunning := botRunning
	botRunning = false
	botRunningMutex.Unlock()

	if wasRunning {
		logMsg("⏸️  Bot detenido para reset")
		time.Sleep(500 * time.Millisecond) // Dar tiempo a que terminen operaciones
	}

	// Cerrar todas las posiciones abiertas
	positionsMutex.RLock()
	hasPositions := len(positions) > 0
	positionsMutex.RUnlock()

	if hasPositions {
		logMsg("📤 Cerrando posiciones abiertas...")
		closeAllTrades(false) // Cerrar todas sin filtro
		time.Sleep(1 * time.Second) // Esperar a que se cierren
	}

	// Resetear estadísticas de trading
	profitMutex.Lock()
	totalProfit = 0
	totalTrades = 0
	winningTrades = 0
	losingTrades = 0
	totalGains = 0
	totalLosses = 0
	totalCommissions = 0
	profitMutex.Unlock()

	// Resetear métricas de performance
	latencyMutex.Lock()
	totalLatency = 0
	latencyCount = 0
	latencyMutex.Unlock()

	atomic.StoreInt64(&jobsProcessed, 0)
	atomic.StoreInt32(&directAnalysisActive, 0)

	// Limpiar resumen de cierre
	closeSummaryMutex.Lock()
	lastCloseSummary = nil
	closeSummaryMutex.Unlock()

	summaryModalMutex.Lock()
	showSummaryModal = false
	summaryModalMutex.Unlock()

	// Limpiar señales y estados de pares
	statusesMutex.Lock()
	for symbol := range pairStatuses {
		if status := pairStatuses[symbol]; status != nil {
			status.Signal = &TradeSignal{Action: "HOLD"}
			if status.PriceHistory != nil {
				status.PriceHistory.mutex.Lock()
				status.PriceHistory.Prices = nil
				status.PriceHistory.Times = nil
				status.PriceHistory.Volumes = nil
				status.PriceHistory.mutex.Unlock()
			}
		}
	}
	statusesMutex.Unlock()

	// Limpiar cache de indicadores
	indicatorCacheMux.Lock()
	indicatorCache = make(map[string]*TechnicalIndicators)
	indicatorCacheTime = make(map[string]time.Time)
	indicatorCacheMux.Unlock()

	// Actualizar balance desde Binance
	logMsg("🔄 Actualizando balance desde Binance...")
	err := getAccountBalance()
	if err != nil {
		logMsg(fmt.Sprintf("⚠️  Error obteniendo balance: %v", err))
	}

	// Resetear balance inicial al actual
	balanceMutex.Lock()
	initialBalance = usdtBalance
	balanceMutex.Unlock()

	// Resetear tiempo de inicio
	botStartTime = time.Now()

	// Limpiar logs antiguos
	logMutex.Lock()
	logMessages = []string{}
	logMutex.Unlock()

	logMsg("✅ Reset completado exitosamente")
	logMsg(fmt.Sprintf("💰 Balance inicial reseteado a: %.2f USDT", initialBalance))
	logMsg("📊 Todas las estadísticas han sido reiniciadas")
	logMsg("🎯 El bot está listo para operar desde cero")
}

// Funciones helper para formateo
func formatFloat(val float64) string {
	return fmt.Sprintf("%.2f", val)
}

func formatInt(val int) string {
	return fmt.Sprintf("%d", val)
}

func formatBool(val bool) string {
	if val {
		return "Activado"
	}
	return "Desactivado"
}

// Recopilar datos del dashboard
func collectDashboardData() DashboardData {
	// Header
	env := "TESTNET"
	if !useTestnet {
		env = "PRODUCCIÓN"
	}

	header := HeaderData{
		Title:       "⚡ HFT PRO BOT - MICRO-SCALPING ULTRA RÁPIDO",
		Environment: env,
		Connected:   true,
		StopLoss:    useStopLoss,
	}

	// Stats
	balanceMutex.RLock()
	balance := usdtBalance
	initial := initialBalance
	balanceMutex.RUnlock()

	sessionProfit := balance - initial
	sessionProfitPercent := 0.0
	if initial > 0 {
		sessionProfitPercent = (sessionProfit / initial) * 100
	}

	profitMutex.Lock()
	trades := totalTrades
	winning := winningTrades
	losing := losingTrades
	gains := totalGains
	losses := totalLosses
	commissions := totalCommissions
	profitMutex.Unlock()

	winRate := 0.0
	if trades > 0 {
		winRate = (float64(winning) / float64(trades)) * 100
	}

	positionsMutex.RLock()
	activePos := len(positions)
	positionsMutex.RUnlock()

	statusesMutex.RLock()
	activePairs := len(pairStatuses)
	statusesMutex.RUnlock()

	volatilePairsMutex.RLock()
	volPairs := volatilePairsCount
	volatilePairsMutex.RUnlock()

	latencyMutex.Lock()
	avgLatency := int64(0)
	if latencyCount > 0 {
		avgLatency = totalLatency / latencyCount
	}
	latencyMutex.Unlock()

	workers := atomic.LoadInt32(&currentWorkers)
	queueLen := len(tradeJobsChan)
	queueCap := cap(tradeJobsChan)
	processed := atomic.LoadInt64(&jobsProcessed)

	uptime := time.Since(botStartTime).Round(time.Second).String()

	stats := StatsData{
		Balance:              balance,
		InitialBalance:       initial,
		SessionProfit:        sessionProfit,
		SessionProfitPercent: sessionProfitPercent,
		ActivePositions:      activePos,
		MaxPositions:         maxPositions,
		ActivePairs:          activePairs,
		VolatilePairs:        volPairs,
		Workers:              workers,
		MaxWorkers:           maxWorkers,
		TotalTrades:          trades,
		WinningTrades:        winning,
		LosingTrades:         losing,
		WinRate:              winRate,
		TotalGains:           gains,
		TotalLosses:          losses,
		TotalCommissions:     commissions,
		NetProfit:            gains - losses,
		AvgLatency:           avgLatency,
		QueueLength:          queueLen,
		QueueCapacity:        queueCap,
		JobsProcessed:        processed,
		DirectAnalysisActive: atomic.LoadInt32(&directAnalysisActive),
		Uptime:               uptime,
		MinVolatility:        minVolatility * 100,
	}

	// Posiciones (inicializar como array vacío para evitar null en JSON)
	positionsData := make([]PositionData, 0)
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
		if currentPrice > 0 {
			profitPct = ((currentPrice - pos.BuyPrice) / pos.BuyPrice) * 100
		}

		positionsData = append(positionsData, PositionData{
			Symbol:       pos.Symbol,
			BuyPrice:     pos.BuyPrice,
			CurrentPrice: currentPrice,
			TargetPrice:  pos.TargetPrice,
			StopLoss:     pos.StopLoss,
			ProfitPct:    profitPct,
			Strategy:     pos.Strategy,
		})
	}
	positionsMutex.RUnlock()

	// Precios (inicializar como array vacío para evitar null en JSON)
	pricesData := make([]PriceData, 0)
	statusesMutex.RLock()
	var statusList []*PairStatus
	for _, s := range pairStatuses {
		statusList = append(statusList, s)
	}
	statusesMutex.RUnlock()

	// Ordenar por volatilidad
	sort.Slice(statusList, func(i, j int) bool {
		return statusList[i].Volatility > statusList[j].Volatility
	})

	// Top 12
	for i, status := range statusList {
		if i >= 12 {
			break
		}

		rsi := 0.0
		if status.Indicators != nil {
			rsi = status.Indicators.RSI
		}

		signal := "WAIT"
		confidence := 0.0
		if status.Signal != nil {
			signal = status.Signal.Action
			confidence = status.Signal.Confidence
		}

		pricesData = append(pricesData, PriceData{
			Symbol:     status.Symbol,
			Price:      status.CurrentPrice,
			Volatility: status.Volatility,
			IsVolatile: status.IsVolatile,
			RSI:        rsi,
			Signal:     signal,
			Confidence: confidence,
		})
	}

	// Logs (inicializar como array vacío para evitar null en JSON)
	logMutex.Lock()
	logs := make([]string, 0)
	if len(logMessages) > 0 {
		if len(logMessages) > maxLogLines {
			logs = append(logs, logMessages[len(logMessages)-maxLogLines:]...)
		} else {
			logs = append(logs, logMessages...)
		}
	}
	logMutex.Unlock()

	// Close Summary - Solo enviar si showSummaryModal es true
	var closeSummary *CloseSummaryData
	summaryModalMutex.Lock()
	shouldShowModal := showSummaryModal
	summaryModalMutex.Unlock()

	closeSummaryMutex.RLock()
	if lastCloseSummary != nil && shouldShowModal {
		closeSummary = &CloseSummaryData{
			Timestamp:    lastCloseSummary.Timestamp,
			FinalBalance: lastCloseSummary.FinalBalance,
			TotalProfit:  lastCloseSummary.TotalProfit,
			SuccessCount: lastCloseSummary.SuccessCount,
			FailedCount:  lastCloseSummary.FailedCount,
			SkippedCount: lastCloseSummary.SkippedCount,
			SuccessList:  lastCloseSummary.SuccessList,
			FailedList:   make([]FailedTradeItem, 0),
			SkippedList:  make([]FailedTradeItem, 0),
		}

		for _, f := range lastCloseSummary.FailedList {
			closeSummary.FailedList = append(closeSummary.FailedList, FailedTradeItem{
				Symbol: f.Symbol,
				Reason: f.Reason,
			})
		}

		for _, s := range lastCloseSummary.SkippedList {
			closeSummary.SkippedList = append(closeSummary.SkippedList, FailedTradeItem{
				Symbol: s.Symbol,
				Reason: s.Reason,
			})
		}
	}
	closeSummaryMutex.RUnlock()

	// Config
	botRunningMutex.RLock()
	running := botRunning
	botRunningMutex.RUnlock()

	// Enmascarar las claves secretas para mostrar en el frontend
	maskedAPIKey := ""
	if userAPIKey != "" {
		if len(userAPIKey) > 8 {
			maskedAPIKey = userAPIKey[:4] + "..." + userAPIKey[len(userAPIKey)-4:]
		} else {
			maskedAPIKey = "***"
		}
	}

	maskedSecretKey := ""
	if userSecretKey != "" {
		maskedSecretKey = "***************"
	}

	config := ConfigData{
		BotRunning:         running,
		UseTestnet:         useTestnet,
		UserAPIKey:         maskedAPIKey,    // Enviar clave enmascarada para mostrar
		UserSecretKey:      maskedSecretKey, // Enviar clave enmascarada para mostrar
		TotalInvestUSDT:    totalInvestUSDT,
		InvestPerPosition:  investPerPosition,
		MaxPositions:       maxPositions,
		UseStopLoss:        useStopLoss,
		OnlySellOnProfit:   onlySellOnProfit,
		QuickProfitTarget:  quickProfitTarget * 100,  // Convertir a % para mostrar
		NormalProfitTarget: normalProfitTarget * 100, // Convertir a % para mostrar
		StopLossPercent:    stopLossPercent * 100,    // Convertir a % para mostrar
		TrailingStop:       trailingStop * 100,       // Convertir a % para mostrar
	}

	return DashboardData{
		Timestamp:    time.Now(),
		Header:       header,
		Stats:        stats,
		Config:       config,
		Positions:    positionsData,
		Prices:       pricesData,
		Logs:         logs,
		CloseSummary: closeSummary,
	}
}

// Broadcast a todos los clientes
func broadcastDashboardData() {
	wsClientsMutex.RLock()
	defer wsClientsMutex.RUnlock()

	if len(wsClients) == 0 {
		return
	}

	data := collectDashboardData()

	for client := range wsClients {
		err := client.WriteJSON(data)
		if err != nil {
			log.Printf("Error broadcasting to client: %v", err)
			client.Close()
			delete(wsClients, client)
		}
	}
}

// Worker para enviar actualizaciones
func startWebSocketBroadcaster() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		<-ticker.C
		broadcastDashboardData()
	}
}

// Iniciar servidor web
func startWebServer() {
	// Servir archivos estáticos desde el directorio static
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	// Endpoint WebSocket
	http.HandleFunc("/ws", handleWebSocket)

	port := ":8080"
	log.Printf("🌐 Servidor web iniciado en http://localhost%s", port)

	go startWebSocketBroadcaster()

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Error starting web server: %v", err)
	}
}
