// WebSocket connection
let ws = null;
let reconnectInterval = null;
let lastData = null;
let isEditingConfig = false; // Flag para evitar sobrescribir config mientras el usuario edita

// Connect to WebSocket
function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.host;
    ws = new WebSocket(`${protocol}//${host}/ws`);

    ws.onopen = () => {
        console.log('WebSocket connected');
        clearInterval(reconnectInterval);
    };

    ws.onmessage = (event) => {
        const data = JSON.parse(event.data);
        lastData = data;
        updateDashboard(data);
    };

    ws.onclose = () => {
        console.log('WebSocket disconnected, reconnecting...');
        reconnectInterval = setInterval(() => {
            connectWebSocket();
        }, 3000);
    };

    ws.onerror = (error) => {
        console.error('WebSocket error:', error);
    };
}

// Update dashboard with received data
function updateDashboard(data) {
    updateHeader(data.header);
    updateStats(data.stats);
    updateConfig(data.config);
    updatePositions(data.positions);
    updatePrices(data.prices);
    updateLogs(data.logs);

    if (data.closeSummary) {
        showSummaryModal(data.closeSummary);
    }
}

// Update header
function updateHeader(header) {
    document.getElementById('headerTitle').textContent = header.title;

    const envBadge = document.getElementById('envBadge');
    envBadge.textContent = header.environment;
    if (header.environment === 'TESTNET') {
        envBadge.className = 'px-3 py-1 rounded-full text-sm font-semibold bg-yellow-600 text-white';
    } else {
        envBadge.className = 'px-3 py-1 rounded-full text-sm font-semibold bg-red-600 text-white';
    }

    const stopLossBadge = document.getElementById('stopLossBadge');
    if (header.stopLoss) {
        stopLossBadge.textContent = '🛡️ Stop Loss: ON';
        stopLossBadge.className = 'px-3 py-1 rounded-full text-sm font-semibold bg-green-600 text-white';
    } else {
        stopLossBadge.textContent = '⚠️ Stop Loss: OFF';
        stopLossBadge.className = 'px-3 py-1 rounded-full text-sm font-semibold bg-red-600 text-white';
    }
}

// Update stats
function updateStats(stats) {
    document.getElementById('balance').textContent = stats.balance.toFixed(2);
    document.getElementById('initialBalance').textContent = stats.initialBalance.toFixed(2);

    const sessionProfitEl = document.getElementById('sessionProfit');
    const sessionProfitPctEl = document.getElementById('sessionProfitPct');
    const profitColor = stats.sessionProfit >= 0 ? 'text-green-400' : 'text-red-400';
    sessionProfitEl.textContent = (stats.sessionProfit >= 0 ? '+' : '') + stats.sessionProfit.toFixed(2);
    sessionProfitEl.className = profitColor;
    sessionProfitPctEl.textContent = '(' + (stats.sessionProfitPercent >= 0 ? '+' : '') + stats.sessionProfitPercent.toFixed(2) + '%)';
    sessionProfitPctEl.className = profitColor;

    document.getElementById('activePositions').textContent = stats.activePositions;
    document.getElementById('maxPositions').textContent = stats.maxPositions;
    document.getElementById('activePairs').textContent = stats.activePairs;
    document.getElementById('volatilePairs').textContent = stats.volatilePairs;

    document.getElementById('totalTrades').textContent = stats.totalTrades;
    document.getElementById('winningTrades').textContent = stats.winningTrades;
    document.getElementById('losingTrades').textContent = stats.losingTrades;
    document.getElementById('winRate').textContent = stats.winRate.toFixed(1) + '%';

    const netProfitEl = document.getElementById('netProfit');
    netProfitEl.textContent = (stats.netProfit >= 0 ? '+' : '') + stats.netProfit.toFixed(4);
    netProfitEl.className = 'text-3xl font-bold ' + (stats.netProfit >= 0 ? 'text-green-400' : 'text-red-400');

    document.getElementById('totalGains').textContent = '+' + stats.totalGains.toFixed(4);
    document.getElementById('totalLosses').textContent = '-' + stats.totalLosses.toFixed(4);
    document.getElementById('totalCommissions').textContent = stats.totalCommissions.toFixed(4);
    document.getElementById('avgLatency').textContent = stats.avgLatency + 'ms';

    document.getElementById('workers').textContent = stats.workers + '/' + stats.maxWorkers;
    document.getElementById('directAnalysis').textContent = stats.directAnalysisActive + '/50';
    document.getElementById('queue').textContent = stats.queueLength + '/' + stats.queueCapacity;
    document.getElementById('jobsProcessed').textContent = stats.jobsProcessed;
    document.getElementById('uptime').textContent = stats.uptime;
}

// Update positions table
function updatePositions(positions) {
    const tbody = document.getElementById('positionsTable');

    if (!positions || positions.length === 0) {
        tbody.innerHTML = '<tr><td colspan="7" class="text-center py-4 text-gray-500">No hay posiciones activas</td></tr>';
        return;
    }

    tbody.innerHTML = positions.map(pos => {
        const profitColor = pos.profitPct > 0 ? 'text-green-400' : (pos.profitPct < -0.3 ? 'text-red-400' : 'text-white');
        return `
            <tr class="border-b border-gray-800 hover:bg-gray-900">
                <td class="py-2 font-semibold">${pos.symbol}</td>
                <td class="py-2">${pos.buyPrice.toFixed(4)}</td>
                <td class="py-2">${pos.currentPrice.toFixed(4)}</td>
                <td class="py-2">${pos.targetPrice.toFixed(4)}</td>
                <td class="py-2">${pos.stopLoss.toFixed(4)}</td>
                <td class="py-2 font-bold ${profitColor}">${pos.profitPct.toFixed(2)}%</td>
                <td class="py-2 text-xs">${pos.strategy}</td>
            </tr>
        `;
    }).join('');
}

// Update prices table
function updatePrices(prices) {
    const tbody = document.getElementById('pricesTable');

    if (!prices || prices.length === 0) {
        tbody.innerHTML = '<tr><td colspan="6" class="text-center py-4 text-gray-500">No hay datos de precios</td></tr>';
        return;
    }

    tbody.innerHTML = prices.map(price => {
        // Volatility color and icon
        let volColor = 'text-red-400';
        let volIcon = '❄️';
        if (price.volatility >= 0.8) {
            volColor = 'text-green-400 font-bold';
            volIcon = '🔥';
        } else if (price.volatility >= 0.3) {
            volColor = 'text-green-400';
            volIcon = '';
        }

        // Signal color
        let signalColor = 'text-white';
        let signalText = price.signal;
        if (price.signal === 'BUY') {
            if (price.confidence >= 80) {
                signalColor = 'text-green-400 font-bold';
                signalText = '⚡BUY';
            } else if (price.confidence >= 70) {
                signalColor = 'text-green-400';
            }
        } else if (price.signal === 'SELL') {
            signalColor = 'text-red-400';
        }

        return `
            <tr class="border-b border-gray-800 hover:bg-gray-900">
                <td class="py-2 font-semibold">${price.symbol}</td>
                <td class="py-2">${price.price.toFixed(4)}</td>
                <td class="py-2 ${volColor}">${volIcon}${price.volatility.toFixed(2)}</td>
                <td class="py-2">${price.rsi > 0 ? price.rsi.toFixed(0) : '-'}</td>
                <td class="py-2 ${signalColor}">${signalText}</td>
                <td class="py-2">${price.confidence > 0 ? price.confidence.toFixed(0) : '-'}</td>
            </tr>
        `;
    }).join('');
}

// Update logs
function updateLogs(logs) {
    const logBox = document.getElementById('logBox');

    if (!logs || logs.length === 0) {
        logBox.innerHTML = '<div class="text-gray-500">No hay logs disponibles</div>';
        return;
    }

    logBox.innerHTML = logs.map(log => {
        // Color based on log content
        let color = 'text-gray-300';
        if (log.includes('✅') || log.includes('COMPRA') || log.includes('PROFIT')) {
            color = 'text-green-400';
        } else if (log.includes('❌') || log.includes('ERROR') || log.includes('STOP LOSS')) {
            color = 'text-red-400';
        } else if (log.includes('⚠️') || log.includes('WARNING')) {
            color = 'text-yellow-400';
        } else if (log.includes('🔄') || log.includes('VENTA')) {
            color = 'text-cyan-400';
        }

        return `<div class="${color}">${escapeHtml(log)}</div>`;
    }).join('');

    // Auto-scroll to bottom
    logBox.scrollTop = logBox.scrollHeight;
}

// Show summary modal
function showSummaryModal(summary) {
    const modal = document.getElementById('summaryModal');
    const content = document.getElementById('summaryContent');

    let html = `
        <div class="space-y-3">
            <div class="text-lg">
                <span class="text-gray-400">⏰ Hora:</span>
                <span class="text-white ml-2">${new Date(summary.timestamp).toLocaleTimeString()}</span>
            </div>

            <div class="text-lg">
                <span class="text-gray-400">💰 Balance Final:</span>
                <span class="text-green-400 ml-2 font-bold">${summary.finalBalance.toFixed(2)} USDT</span>
            </div>

            <div class="text-lg">
                <span class="text-gray-400">📊 Ganancia Neta:</span>
                <span class="${summary.totalProfit >= 0 ? 'text-green-400' : 'text-red-400'} ml-2 font-bold">
                    ${(summary.totalProfit >= 0 ? '+' : '')}${summary.totalProfit.toFixed(4)} USDT
                </span>
            </div>

            <div class="flex gap-6 text-base">
                <div>✅ Vendidas: <span class="text-green-400 font-semibold">${summary.successCount}</span></div>
                <div>❌ Fallidas: <span class="text-red-400 font-semibold">${summary.failedCount}</span></div>
                <div>⏸️ Omitidas: <span class="text-yellow-400 font-semibold">${summary.skippedCount}</span></div>
            </div>
    `;

    if (summary.successList && summary.successList.length > 0) {
        html += `
            <div class="mt-4">
                <div class="text-green-400 font-semibold mb-2">✅ VENTAS EXITOSAS:</div>
                <div class="space-y-1 pl-4">
                    ${summary.successList.map(item => `<div class="text-gray-300">• ${escapeHtml(item)}</div>`).join('')}
                </div>
            </div>
        `;
    }

    if (summary.failedList && summary.failedList.length > 0) {
        html += `
            <div class="mt-4">
                <div class="text-red-400 font-semibold mb-2">❌ VENTAS FALLIDAS:</div>
                <div class="space-y-1 pl-4">
                    ${summary.failedList.map(item => `<div class="text-gray-300">• ${escapeHtml(item.symbol)} - ${escapeHtml(item.reason)}</div>`).join('')}
                </div>
            </div>
        `;
    }

    if (summary.skippedList && summary.skippedList.length > 0) {
        html += `
            <div class="mt-4">
                <div class="text-yellow-400 font-semibold mb-2">⏸️ POSICIONES OMITIDAS:</div>
                <div class="space-y-1 pl-4">
                    ${summary.skippedList.map(item => `<div class="text-gray-300">• ${escapeHtml(item.symbol)} - ${escapeHtml(item.reason)}</div>`).join('')}
                </div>
            </div>
        `;
    }

    html += '</div>';

    content.innerHTML = html;
    modal.classList.remove('hidden');
    modal.classList.add('flex');
}

// Close summary modal
function closeSummaryModal() {
    const modal = document.getElementById('summaryModal');
    modal.classList.add('hidden');
    modal.classList.remove('flex');

    // Notificar al servidor que el modal fue cerrado
    sendCommand('dismissSummary');
}

// Send command to server
function sendCommand(action) {
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ action }));
    }
}

// Close all trades
function closeAllTrades() {
    if (confirm('¿Estás seguro de que quieres cerrar TODAS las posiciones?')) {
        sendCommand('closeAll');
    }
}

// Close only profitable trades
function closeProfitTrades() {
    if (confirm('¿Cerrar solo las posiciones con ganancia?')) {
        sendCommand('closeProfit');
    }
}

// Reset bot completely
function resetBot() {
    if (confirm('⚠️ ADVERTENCIA: Esto resetará TODAS las estadísticas, cerrará posiciones y reiniciará el bot desde cero.\n\n¿Estás seguro de continuar?')) {
        if (confirm('✋ ÚLTIMA CONFIRMACIÓN: Esta acción NO se puede deshacer.\n\n¿Proceder con el reset completo?')) {
            sendCommand('reset');
        }
    }
}

// Update configuration display
function updateConfig(config) {
    if (!config) return;

    // No actualizar inputs si el usuario está editando (evita sobrescribir cambios)
    if (!isEditingConfig) {
        // Update form inputs - API Configuration
        document.getElementById('useTestnet').checked = config.useTestnet;
        // No mostrar las claves enmascaradas en los inputs para evitar confusión
        // Solo mostrar si están vacías (primera carga)
        if (!document.getElementById('userAPIKey').value) {
            document.getElementById('userAPIKey').value = '';
        }
        if (!document.getElementById('userSecretKey').value) {
            document.getElementById('userSecretKey').value = '';
        }

        // Update form inputs - Capital Management
        document.getElementById('totalInvestUSDT').value = config.totalInvestUSDT;
        document.getElementById('investPerPosition').value = config.investPerPosition;
        document.getElementById('maxPositionsInput').value = config.maxPositions;
        document.getElementById('useStopLoss').checked = config.useStopLoss;

        // Update form inputs - Targets & Stop Loss (ya vienen en %)
        document.getElementById('quickProfitTarget').value = config.quickProfitTarget.toFixed(2);
        document.getElementById('normalProfitTarget').value = config.normalProfitTarget.toFixed(2);
        document.getElementById('stopLossPercent').value = config.stopLossPercent.toFixed(2);
        document.getElementById('trailingStop').value = config.trailingStop.toFixed(2);

        // Update form inputs - Advanced Configuration (ya vienen en %)
        document.getElementById('microScalpTarget').value = config.microScalpTarget.toFixed(2);
        document.getElementById('maxProfitTarget').value = config.maxProfitTarget.toFixed(2);
        document.getElementById('microStopLoss').value = config.microStopLoss.toFixed(2);
    }

    // Update bot status (siempre actualizar, no depende de inputs)
    updateBotStatus(config.botRunning);
}

// Update bot status UI
function updateBotStatus(isRunning) {
    const statusCard = document.getElementById('botStatusCard');
    const statusTitle = document.getElementById('botStatusTitle');
    const statusIndicator = document.getElementById('botStatusIndicator');
    const statusText = document.getElementById('botStatusText');
    const startBtn = document.getElementById('startBtn');
    const stopBtn = document.getElementById('stopBtn');

    if (isRunning) {
        // Bot running
        statusCard.className = 'bg-dark-card border-2 border-green-500 rounded-lg p-4';
        statusTitle.className = 'text-lg font-bold text-green-400 mb-3';
        statusTitle.textContent = '🤖 Bot en Ejecución';
        statusIndicator.className = 'w-4 h-4 rounded-full bg-green-500 pulse-glow';
        statusText.textContent = 'Operando...';
        statusText.className = 'font-semibold text-green-400';
        startBtn.disabled = true;
        stopBtn.disabled = false;
    } else {
        // Bot stopped
        statusCard.className = 'bg-dark-card border-2 border-red-500 rounded-lg p-4';
        statusTitle.className = 'text-lg font-bold text-red-400 mb-3';
        statusTitle.textContent = '🤖 Bot Detenido';
        statusIndicator.className = 'w-4 h-4 rounded-full bg-red-500';
        statusText.textContent = 'Detenido';
        statusText.className = 'font-semibold text-red-400';
        startBtn.disabled = false;
        stopBtn.disabled = true;
    }
}

// Save configuration
function saveConfig() {
    const config = {
        useTestnet: document.getElementById('useTestnet').checked,
        userAPIKey: document.getElementById('userAPIKey').value.trim(),
        userSecretKey: document.getElementById('userSecretKey').value.trim(),
        totalInvestUSDT: parseFloat(document.getElementById('totalInvestUSDT').value),
        investPerPosition: parseFloat(document.getElementById('investPerPosition').value),
        maxPositions: parseInt(document.getElementById('maxPositionsInput').value),
        useStopLoss: document.getElementById('useStopLoss').checked,
        quickProfitTarget: parseFloat(document.getElementById('quickProfitTarget').value),
        normalProfitTarget: parseFloat(document.getElementById('normalProfitTarget').value),
        stopLossPercent: parseFloat(document.getElementById('stopLossPercent').value),
        trailingStop: parseFloat(document.getElementById('trailingStop').value),
        // Advanced configuration
        microScalpTarget: parseFloat(document.getElementById('microScalpTarget').value),
        maxProfitTarget: parseFloat(document.getElementById('maxProfitTarget').value),
        microStopLoss: parseFloat(document.getElementById('microStopLoss').value)
    };

    console.log('💾 Guardando configuración:', { ...config, userSecretKey: '***' }); // No loggear la secret key

    // Validate - Capital Management
    if (config.totalInvestUSDT < 10 || config.investPerPosition < 10 || config.maxPositions < 1) {
        alert('Por favor, introduce valores válidos para gestión de capital');
        isEditingConfig = false;
        return;
    }

    if (config.investPerPosition > config.totalInvestUSDT) {
        alert('La inversión por posición no puede ser mayor que la inversión total');
        isEditingConfig = false;
        return;
    }

    // Validate - Targets & Stop Loss
    if (config.quickProfitTarget < 0.1 || config.normalProfitTarget < 0.1 ||
        config.stopLossPercent < 0.1 || config.trailingStop < 0.1) {
        alert('Los porcentajes deben ser mayores a 0.1%');
        isEditingConfig = false;
        return;
    }

    if (config.quickProfitTarget >= config.normalProfitTarget) {
        alert('El target normal debe ser mayor que el target rápido');
        isEditingConfig = false;
        return;
    }

    // Validate - Advanced Configuration
    if (config.microScalpTarget < 0.1 || config.maxProfitTarget < 0.5 || config.microStopLoss < 0.05) {
        alert('Los valores avanzados deben estar dentro de los rangos permitidos');
        isEditingConfig = false;
        return;
    }

    if (config.microScalpTarget < 0.25) {
        if (!confirm('⚠️ ADVERTENCIA: Un Micro Scalp Target menor a 0.25% puede no cubrir las comisiones de Binance (0.20%).\n\n¿Estás seguro de continuar?')) {
            isEditingConfig = false;
            return;
        }
    }

    // Send to server
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({
            action: 'configure',
            config: config
        }));

        // Show confirmation
        const btn = event.target;
        const originalText = btn.textContent;
        btn.textContent = '✅ Guardado!';
        btn.className = 'bg-green-600 text-white px-6 py-2 rounded-lg font-semibold transition-colors text-sm';

        // Permitir actualización de config después de guardar
        isEditingConfig = false;

        setTimeout(() => {
            btn.textContent = originalText;
            btn.className = 'bg-cyan-600 hover:bg-cyan-700 text-white px-6 py-2 rounded-lg font-semibold transition-colors text-sm';
        }, 2000);
    }
}

// Start bot
function startBot() {
    if (confirm('¿Iniciar el bot con la configuración actual?')) {
        sendCommand('start');
    }
}

// Stop bot
function stopBot() {
    if (confirm('¿Detener el bot? Las posiciones abiertas permanecerán activas.')) {
        sendCommand('stop');
    }
}

// Reconnect to Binance
function reconnectBinance() {
    if (confirm('¿Reconectar a Binance? Esto reiniciará la conexión WebSocket con las API keys configuradas.')) {
        sendCommand('reconnect');
    }
}

// Escape HTML to prevent XSS
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Toggle advanced configuration panel
function toggleAdvancedConfig() {
    const panel = document.getElementById('advancedConfigPanel');
    const icon = document.getElementById('advancedToggleIcon');

    if (panel.classList.contains('hidden')) {
        panel.classList.remove('hidden');
        icon.textContent = '▼';
    } else {
        panel.classList.add('hidden');
        icon.textContent = '▶';
    }
}

// Reset advanced configuration to defaults
function resetAdvancedToDefaults() {
    if (confirm('¿Restaurar los valores avanzados a sus valores por defecto optimizados?')) {
        document.getElementById('microScalpTarget').value = '0.30';
        document.getElementById('maxProfitTarget').value = '1.30';
        document.getElementById('microStopLoss').value = '0.10';

        isEditingConfig = true;

        alert('✅ Valores restaurados. Haz clic en "Guardar Configuración" para aplicar los cambios.');
    }
}

// Detect when user starts editing config inputs
function setupConfigEditDetection() {
    const configInputs = [
        'useTestnet',
        'userAPIKey',
        'userSecretKey',
        'totalInvestUSDT',
        'investPerPosition',
        'maxPositionsInput',
        'useStopLoss',
        'quickProfitTarget',
        'normalProfitTarget',
        'stopLossPercent',
        'trailingStop',
        'microScalpTarget',
        'maxProfitTarget',
        'microStopLoss'
    ];

    configInputs.forEach(inputId => {
        const input = document.getElementById(inputId);
        if (input) {
            // Detectar cuando el usuario hace focus en el input
            input.addEventListener('focus', () => {
                isEditingConfig = true;
            });

            // Detectar cuando el usuario cambia el valor
            input.addEventListener('change', () => {
                isEditingConfig = true;
            });

            input.addEventListener('input', () => {
                isEditingConfig = true;
            });

            // Para el checkbox, detectar clicks
            if (input.type === 'checkbox') {
                input.addEventListener('click', () => {
                    isEditingConfig = true;
                });
            }
        }
    });
}

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    setupConfigEditDetection();
});

connectWebSocket();
