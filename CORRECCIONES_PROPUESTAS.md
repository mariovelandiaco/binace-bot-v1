# 🔧 CORRECCIONES PROPUESTAS - CÓDIGO DE EJEMPLO

## 📦 1. ESTRUCTURA PARA PARSEAR RESPUESTAS DE BINANCE

```go
// Agregar estas estructuras después de las existentes (línea ~145)

type OrderResponse struct {
    Symbol              string  `json:"symbol"`
    OrderID             int64   `json:"orderId"`
    ClientOrderID       string  `json:"clientOrderId"`
    TransactTime        int64   `json:"transactTime"`
    Price               string  `json:"price"`
    OrigQty             string  `json:"origQty"`
    ExecutedQty         string  `json:"executedQty"`           // ← CANTIDAD REAL EJECUTADA
    CummulativeQuoteQty string  `json:"cummulativeQuoteQty"`   // ← USDT GASTADO REAL
    Status              string  `json:"status"`
    Type                string  `json:"type"`
    Side                string  `json:"side"`
    Fills               []Fill  `json:"fills"`                 // ← COMISIONES
}

type Fill struct {
    Price           string `json:"price"`
    Qty             string `json:"qty"`
    Commission      string `json:"commission"`       // ← COMISIÓN PAGADA
    CommissionAsset string `json:"commissionAsset"`  // ← MONEDA DE LA COMISIÓN
}
```

---

## 🔧 2. MODIFICAR placeBuyOrder PARA RETORNAR DATOS REALES

### ANTES (Incorrecto)
```go
func placeBuyOrder(symbol string, usdtAmount float64) error {
    // ... código actual ...
    _, err := sendAPIRequest("order.place", params, true)
    return err  // ❌ No retorna datos de la orden
}
```

### DESPUÉS (Correcto)
```go
// Nueva firma que retorna OrderResponse
func placeBuyOrder(symbol string, usdtAmount float64) (*OrderResponse, error) {
    startTime := time.Now()

    logMsg(fmt.Sprintf("🟢 COMPRA %s | %.2f USDT", symbol, usdtAmount))

    params := map[string]interface{}{
        "symbol":        symbol,
        "side":          "BUY",
        "type":          "MARKET",
        "quoteOrderQty": fmt.Sprintf("%.2f", usdtAmount),
    }

    // CAMBIO: Usar sendAPIRequestAndWait para obtener respuesta
    response, err := sendAPIRequestAndWait("order.place", params, true, 10*time.Second)
    if err != nil {
        return nil, fmt.Errorf("error ejecutando orden: %v", err)
    }

    // Parsear respuesta de Binance
    var apiResponse struct {
        Result OrderResponse `json:"result"`
    }

    if err := json.Unmarshal(response, &apiResponse); err != nil {
        return nil, fmt.Errorf("error parseando respuesta: %v", err)
    }

    orderResp := &apiResponse.Result

    // Registrar latencia
    latency := time.Since(startTime).Milliseconds()
    latencyMutex.Lock()
    totalLatency += latency
    latencyCount++
    latencyMutex.Unlock()

    // Log con datos reales
    executedQty, _ := strconv.ParseFloat(orderResp.ExecutedQty, 64)
    quoteQty, _ := strconv.ParseFloat(orderResp.CummulativeQuoteQty, 64)

    logMsg(fmt.Sprintf("✅ Orden ejecutada en %dms | Qty: %.8f | Gastado: %.2f USDT",
        latency, executedQty, quoteQty))

    return orderResp, nil
}
```

---

## 🔧 3. MODIFICAR placeSellOrder PARA RETORNAR DATOS REALES

```go
func placeSellOrder(symbol string, quantity float64) (*OrderResponse, error) {
    logMsg(fmt.Sprintf("🔴 Vendiendo %.8f %s...", quantity, symbol))

    params := map[string]interface{}{
        "symbol":   symbol,
        "side":     "SELL",
        "type":     "MARKET",
        "quantity": fmt.Sprintf("%.8f", quantity),
    }

    response, err := sendAPIRequestAndWait("order.place", params, true, 10*time.Second)
    if err != nil {
        return nil, err
    }

    var apiResponse struct {
        Result OrderResponse `json:"result"`
    }

    if err := json.Unmarshal(response, &apiResponse); err != nil {
        return nil, err
    }

    return &apiResponse.Result, nil
}
```

---

## 🔧 4. ACTUALIZAR LÓGICA DE COMPRA (analyzeTrendAndTrade)

### ANTES (Líneas 1815-1850)
```go
go func() {
    investAmount := investPerPosition

    if err := placeBuyOrder(symbol, investAmount); err == nil {
        quantity := investAmount / currentPrice  // ❌ ESTIMADO

        pos := &Position{
            Symbol:       symbol,
            BuyPrice:     currentPrice,  // ❌ ESTIMADO
            Quantity:     quantity,       // ❌ ESTIMADO
            // ...
        }

        positionsMutex.Lock()
        positions[symbol] = pos
        positionsMutex.Unlock()

        balanceMutex.Lock()
        usdtBalance -= investAmount  // ❌ DOBLE RESTA
        balanceMutex.Unlock()
    }
}()
```

### DESPUÉS (Correcto)
```go
go func() {
    investAmount := investPerPosition
    if indicators.Volatility > 2.0 {
        investAmount = investPerPosition * 0.7
    }

    // CAMBIO: Recibir respuesta de la orden
    orderResp, err := placeBuyOrder(symbol, investAmount)
    if err != nil {
        logMsg(fmt.Sprintf("❌ Error comprando %s: %v", symbol, err))
        return
    }

    // CAMBIO: Usar datos REALES de Binance
    executedQty, _ := strconv.ParseFloat(orderResp.ExecutedQty, 64)
    avgPrice := calculateAvgPrice(orderResp.Fills)
    totalCommission := calculateTotalCommission(orderResp.Fills)

    // Definir stop loss y target según estrategia
    stopLoss := avgPrice * (1 - stopLossPercent)
    targetPrice := avgPrice * (1 + normalProfitTarget)
    if strategy == "HFT" {
        targetPrice = avgPrice * (1 + quickProfitTarget)
    }

    pos := &Position{
        Symbol:       symbol,
        BuyPrice:     avgPrice,      // ✓ PRECIO REAL
        Quantity:     executedQty,   // ✓ CANTIDAD REAL
        TargetPrice:  targetPrice,
        StopLoss:     stopLoss,
        HighestPrice: avgPrice,
        BuyTime:      time.Now(),
        Strategy:     strategy,
    }

    positionsMutex.Lock()
    positions[symbol] = pos
    positionsMutex.Unlock()

    // CAMBIO: NO actualizar balance manualmente
    // Binance ya lo hizo automáticamente

    // Actualizar estadísticas de comisión
    profitMutex.Lock()
    totalCommissions += totalCommission
    profitMutex.Unlock()

    logMsg(fmt.Sprintf("📝 Posición creada: %.8f @ %.4f | Comisión: %.4f",
        executedQty, avgPrice, totalCommission))
}()
```

---

## 🔧 5. FUNCIONES AUXILIARES PARA CALCULAR PROMEDIOS

```go
// Calcular precio promedio de ejecución
func calculateAvgPrice(fills []Fill) float64 {
    if len(fills) == 0 {
        return 0
    }

    totalValue := 0.0
    totalQty := 0.0

    for _, fill := range fills {
        price, _ := strconv.ParseFloat(fill.Price, 64)
        qty, _ := strconv.ParseFloat(fill.Qty, 64)

        totalValue += price * qty
        totalQty += qty
    }

    if totalQty == 0 {
        return 0
    }

    return totalValue / totalQty
}

// Calcular comisión total
func calculateTotalCommission(fills []Fill) float64 {
    totalCommission := 0.0

    for _, fill := range fills {
        commission, _ := strconv.ParseFloat(fill.Commission, 64)
        // Si la comisión es en BNB u otra moneda, convertir a USDT
        // Por ahora asumimos que es en USDT o equivalente
        totalCommission += commission
    }

    return totalCommission
}
```

---

## 🔧 6. ACTUALIZAR executeImmediateSell

### ANTES (Líneas 743-813)
```go
func executeImmediateSell(pos *Position, currentPrice, profitPct float64, reason string) {
    err := placeSellOrder(pos.Symbol, pos.Quantity)
    if err != nil {
        return
    }

    soldValue := pos.Quantity * currentPrice  // ❌ ESTIMADO
    buyValue := pos.Quantity * pos.BuyPrice
    profitBruto := soldValue - buyValue

    commissionBuy := buyValue * commissionPerTrade     // ❌ ESTIMADO
    commissionSell := soldValue * commissionPerTrade   // ❌ ESTIMADO
    commissionTotal := commissionBuy + commissionSell

    profitNeto := profitBruto - commissionTotal

    balanceMutex.Lock()
    usdtBalance += soldValue  // ❌ NO HACER
    balanceMutex.Unlock()

    // ... actualizar estadísticas ...
}
```

### DESPUÉS (Correcto)
```go
func executeImmediateSell(pos *Position, currentPrice, profitPct float64, reason string) {
    startTime := time.Now()

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

    // CAMBIO: Recibir respuesta de la orden
    orderResp, err := placeSellOrder(pos.Symbol, pos.Quantity)
    if err != nil {
        logMsg(fmt.Sprintf("❌ Error venta inmediata %s: %v", pos.Symbol, err))
        return
    }

    // CAMBIO: Usar datos REALES de Binance
    executedQty, _ := strconv.ParseFloat(orderResp.ExecutedQty, 64)
    quoteQty, _ := strconv.ParseFloat(orderResp.CummulativeQuoteQty, 64)  // USDT recibido
    avgSellPrice := calculateAvgPrice(orderResp.Fills)
    sellCommission := calculateTotalCommission(orderResp.Fills)

    // Calcular profit real
    buyValue := pos.BuyPrice * pos.Quantity
    soldValue := quoteQty  // ✓ USDT REAL recibido
    profitBruto := soldValue - buyValue

    // Comisiones (la de compra debe estar guardada en la posición)
    // NOTA: Necesitas guardar la comisión de compra en Position
    commissionBuy := buyValue * commissionPerTrade  // O usar el valor guardado
    commissionTotal := commissionBuy + sellCommission

    profitNeto := profitBruto - commissionTotal

    // CAMBIO: NO actualizar balance (Binance ya lo hizo)

    // Actualizar estadísticas
    profitMutex.Lock()
    totalProfit += profitNeto
    totalCommissions += commissionTotal
    totalTrades++
    if profitNeto > 0 {
        winningTrades++
        totalGains += profitNeto
    } else {
        losingTrades++
        totalLosses += math.Abs(profitNeto)
    }
    profitMutex.Unlock()

    // Registrar latencia
    latency := time.Since(startTime).Milliseconds()
    latencyMutex.Lock()
    totalLatency += latency
    latencyCount++
    latencyMutex.Unlock()

    // Eliminar posición
    positionsMutex.Lock()
    delete(positions, pos.Symbol)
    positionsMutex.Unlock()

    logMsg(fmt.Sprintf("✅ Venta ejecutada en %dms | Precio: %.4f | Recibido: %.4f | Com: %.4f | Neto: %+.4f USDT",
        latency, avgSellPrice, quoteQty, commissionTotal, profitNeto))
}
```

---

## 🔧 7. AGREGAR SINCRONIZACIÓN PERIÓDICA DEL BALANCE

```go
// Agregar esta función al inicio del main() después de initHFTSystem()

func startBalanceSync() {
    go func() {
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()

        for range ticker.C {
            oldBalance := usdtBalance

            err := getAccountBalance()
            if err != nil {
                logMsg(fmt.Sprintf("⚠️  Error sincronizando balance: %v", err))
                continue
            }

            balanceMutex.RLock()
            newBalance := usdtBalance
            balanceMutex.RUnlock()

            // Solo loggear si hay diferencia significativa
            diff := math.Abs(newBalance - oldBalance)
            if diff > 0.01 {
                logMsg(fmt.Sprintf("🔄 Balance sincronizado: %.4f → %.4f USDT (diff: %.4f)",
                    oldBalance, newBalance, newBalance-oldBalance))
            }
        }
    }()
}

// En main(), después de initHFTSystem():
initHFTSystem()
startBalanceSync()  // ← AGREGAR ESTA LÍNEA
```

---

## 🔧 8. ACTUALIZAR ESTRUCTURA Position PARA GUARDAR COMISIÓN

```go
type Position struct {
    Symbol        string
    BuyPrice      float64
    Quantity      float64
    TargetPrice   float64
    StopLoss      float64
    HighestPrice  float64
    BuyTime       time.Time
    Strategy      string
    BuyCommission float64  // ← AGREGAR: Comisión pagada en la compra
}
```

Y al crear la posición:
```go
pos := &Position{
    Symbol:        symbol,
    BuyPrice:      avgPrice,
    Quantity:      executedQty,
    TargetPrice:   targetPrice,
    StopLoss:      stopLoss,
    HighestPrice:  avgPrice,
    BuyTime:       time.Now(),
    Strategy:      strategy,
    BuyCommission: totalCommission,  // ← GUARDAR comisión de compra
}
```

---

## 📊 9. MEJORAR LOGS PARA DEBUGGING

```go
// En placeBuyOrder, después de parsear respuesta:
logMsg(fmt.Sprintf("📋 COMPRA DETALLE:"))
logMsg(fmt.Sprintf("   Symbol: %s", orderResp.Symbol))
logMsg(fmt.Sprintf("   Ejecutado: %.8f", executedQty))
logMsg(fmt.Sprintf("   Gastado: %.4f USDT", quoteQty))
logMsg(fmt.Sprintf("   Precio promedio: %.4f", avgPrice))
logMsg(fmt.Sprintf("   Comisión: %.4f", totalCommission))

// En placeSellOrder, después de parsear respuesta:
logMsg(fmt.Sprintf("📋 VENTA DETALLE:"))
logMsg(fmt.Sprintf("   Symbol: %s", orderResp.Symbol))
logMsg(fmt.Sprintf("   Ejecutado: %.8f", executedQty))
logMsg(fmt.Sprintf("   Recibido: %.4f USDT", quoteQty))
logMsg(fmt.Sprintf("   Precio promedio: %.4f", avgPrice))
logMsg(fmt.Sprintf("   Comisión: %.4f", sellCommission))
```

---

## ✅ CHECKLIST DE IMPLEMENTACIÓN

- [ ] Agregar estructuras OrderResponse y Fill
- [ ] Modificar placeBuyOrder para retornar OrderResponse
- [ ] Modificar placeSellOrder para retornar OrderResponse
- [ ] Agregar funciones calculateAvgPrice y calculateTotalCommission
- [ ] Actualizar campo BuyCommission en Position
- [ ] Modificar lógica de compra en analyzeTrendAndTrade (línea ~1815)
- [ ] Modificar lógica de venta en analyzeTrendAndTrade (línea ~1674)
- [ ] Modificar executeImmediateSell
- [ ] ELIMINAR `usdtBalance -= investAmount` en línea 1848
- [ ] ELIMINAR `usdtBalance += saleAmount` en línea 1693
- [ ] ELIMINAR `usdtBalance += soldValue` en línea 783
- [ ] Agregar startBalanceSync() en main
- [ ] Modificar closeAllTrades para usar OrderResponse
- [ ] Actualizar executeUrgentTrade para usar OrderResponse
- [ ] Testing exhaustivo en testnet
- [ ] Comparar balance local vs Binance

---

## 🧪 SCRIPT DE TESTING

```go
// Agregar esta función para testing
func testTradingAccuracy() {
    logMsg("🧪 Iniciando test de precisión de trading...")

    // 1. Guardar balance inicial de Binance
    getAccountBalance()
    balanceMutex.RLock()
    binanceInitial := usdtBalance
    balanceMutex.RUnlock()

    // 2. Hacer un trade de prueba
    symbol := "BTCUSDT"
    amount := 15.0

    orderResp, err := placeBuyOrder(symbol, amount)
    if err != nil {
        logMsg(fmt.Sprintf("❌ Test failed: %v", err))
        return
    }

    time.Sleep(1 * time.Second)

    // 3. Vender inmediatamente
    executedQty, _ := strconv.ParseFloat(orderResp.ExecutedQty, 64)
    sellResp, err := placeSellOrder(symbol, executedQty)
    if err != nil {
        logMsg(fmt.Sprintf("❌ Test failed: %v", err))
        return
    }

    time.Sleep(1 * time.Second)

    // 4. Comparar balances
    getAccountBalance()
    balanceMutex.RLock()
    binanceFinal := usdtBalance
    balanceMutex.RUnlock()

    diff := binanceFinal - binanceInitial

    logMsg(fmt.Sprintf("📊 TEST RESULTS:"))
    logMsg(fmt.Sprintf("   Balance inicial: %.4f", binanceInitial))
    logMsg(fmt.Sprintf("   Balance final: %.4f", binanceFinal))
    logMsg(fmt.Sprintf("   Diferencia: %.4f (debería ser ~-0.06 por comisiones)", diff))

    buyComm := calculateTotalCommission(orderResp.Fills)
    sellComm := calculateTotalCommission(sellResp.Fills)
    totalComm := buyComm + sellComm

    logMsg(fmt.Sprintf("   Comisiones totales: %.4f", totalComm))

    if math.Abs(diff + totalComm) < 0.01 {
        logMsg("✅ Test PASSED - Cálculos correctos!")
    } else {
        logMsg("❌ Test FAILED - Revisar cálculos")
    }
}
```

---

**Fecha:** 2026-01-12
**Nota:** Implementar cambios en orden, testear cada paso en TESTNET antes de producción.
