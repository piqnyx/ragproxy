package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gammazero/deque"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func NewResponseCollector(w http.ResponseWriter) *ResponseCollector {
	rc := &ResponseCollector{
		ResponseWriter:    w,
		incomingPackets:   make([]ResponsePacket, 0, appCtx.Config.InitialIncomingBufferPreAllocation),
		outgoingPackets:   &deque.Deque[ResponsePacket]{},
		globalTextBuffer:  "",
		currentTextBuffer: "",
		complete:          false,
		collecting:        false,

		templateStreamPacket: ResponsePacket{},
		templateFinishPacket: ResponsePacket{},

		outgoingCh: make(chan ResponsePacket, appCtx.Config.InitialOutgoingGorutineBufferCount),
		notifyCh:   make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	go rc.StartOutgoingLoop()
	return rc
}

func (w *ResponseCollector) WriteHeader(statusCode int) {
	// мы потенциально переписываем body (Direct/Stream), поэтому фиксированную длину убираем
	h := w.ResponseWriter.Header()
	h.Del("Content-Length")

	w.ResponseWriter.WriteHeader(statusCode)
}

func packetWireData(pkt ResponsePacket) string {
	// Non-SSE: как есть
	if !pkt.IsSSE || pkt.Prefix == "" {
		return pkt.RawData
	}

	trimmed := strings.TrimSpace(pkt.RawData)

	// Если уже обёрнут в "data: ..." — просто гарантируем пустую строку в конце
	if strings.HasPrefix(trimmed, pkt.Prefix+":") {
		if strings.HasSuffix(trimmed, "\n\n") {
			return trimmed
		}
		return trimmed + "\n\n"
	}

	// Обычный случай: RawData = JSON или "[DONE]" -> оборачиваем
	return pkt.Prefix + ": " + trimmed + "\n\n"
}

func (w *ResponseCollector) StartOutgoingLoop() {
	defer close(w.doneCh)

	stopping := false
	for {
		// 1) Пытаемся забрать пакет
		w.mu.Lock()
		if w.outgoingPackets.Len() > 0 {
			pkt := w.outgoingPackets.PopFront()
			w.mu.Unlock()

			out := packetWireData(pkt)
			_, _ = w.ResponseWriter.Write([]byte(out))

			// важно для streaming через reverse-proxy/буферы
			if f, ok := w.ResponseWriter.(http.Flusher); ok {
				f.Flush()
			}
			continue
		}

		// 2) Если стоп уже запрошен и очередь пуста — выходим
		if stopping {
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()

		// 3) Ждём сигнал без mutex (иначе дедлок)
		select {
		case <-w.notifyCh:
		case <-w.stopCh:
			stopping = true
		}
	}
}

func (w *ResponseCollector) StopOutgoingLoop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.doneCh
}

func (w *ResponseCollector) EnqueuePacket(pkt ResponsePacket) {
	w.mu.Lock()
	// Предотвращаем подряд идущие дубли по идентичному RawData (простая защита)
	if w.outgoingPackets.Len() > 0 {
		last := w.outgoingPackets.At(w.outgoingPackets.Len() - 1)
		if last.RawData == pkt.RawData {
			w.mu.Unlock()
			return
		}
	}
	w.outgoingPackets.PushBack(pkt)
	// Сигнализируем, если канал пуст (чтобы не блокировать)
	select {
	case w.notifyCh <- struct{}{}:
	default:
	}
	w.mu.Unlock()
}

func (w *ResponseCollector) Write(data []byte) (int, error) {
	rawStr := string(data)

	if appCtx.Config.DumpPackets {
		appCtx.DumpLogger.Printf("----> INCOMING PACKET: \n%s", rawStr)
	}

	incomingPacket, err := parseIncomingBuffer(rawStr)
	if appCtx.Config.DumpPackets {
		appCtx.DumpLogger.Printf("ResponseCollector received packetType=%d isSSE=%v prefix=%q messagePath=%q err=%v\n",
			incomingPacket.PacketType, incomingPacket.IsSSE, incomingPacket.Prefix, incomingPacket.MessagePath, err)
	}
	if err != nil {
		appCtx.ErrorLogger.Printf("Error parsing incoming buffer: %v\n", err)
	}

	// ------- OtherPacket --------

	if incomingPacket.PacketType == OtherPacket {
		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("<---- OUTGOING PACKET: \n%s", string(data))
		}
		return w.ResponseWriter.Write(data)
	}

	// ------- DirectPacket --------

	if incomingPacket.PacketType == DirectPacket {
		jsonStr, replacedStr, rerr := applyResponseReplaceToPacket(incomingPacket)
		if rerr != nil {
			// fallback: отдаём как пришло
			if appCtx.Config.DumpPackets {
				appCtx.DumpLogger.Printf("applyResponseReplaceToString error (fallback): %v\n<---- OUTGOING PACKET:\n%s", rerr, rawStr)
			}
			return w.ResponseWriter.Write(data)
		}

		// сохраняем то, что реально ушло пользователю
		w.mu.Lock()
		w.wasMessages = true
		w.globalTextBuffer = replacedStr
		w.complete = true
		w.mu.Unlock()

		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("<---- OUTGOING PACKET: \n%s", jsonStr)
		}

		// IMPORTANT: вернуть len(data), иначе reverseproxy может считать short write
		if _, werr := w.ResponseWriter.Write([]byte(jsonStr)); werr != nil {
			return 0, werr
		}
		return len(data), nil
	}

	// ------- StreamPacket / FinishStreamPacket --------

	if incomingPacket.PacketType == FinishStreamPacket {
		w.mu.Lock()
		w.complete = true
		// Перезаписываем шаблон финального пакета каждый раз
		w.templateFinishPacket = ResponsePacket{
			RawData:     incomingPacket.RawData,
			Prefix:      incomingPacket.Prefix,
			IsSSE:       incomingPacket.IsSSE,
			MessagePath: incomingPacket.MessagePath,
			PacketType:  incomingPacket.PacketType,
		}

		if w.collecting {
			// collecting включён — не отправляем финальный пакет сразу!
			w.mu.Unlock()
			return len(data), nil
		}

		// collecting НЕ включён:
		// 1) нужно отдать все накопленные чанки, даже если не достигли maxTriggerLen
		packetsToFlush := append([]ResponsePacket(nil), w.incomingPackets...)
		w.globalTextBuffer += w.currentTextBuffer
		w.currentTextBuffer = ""
		w.incomingPackets = w.incomingPackets[:0]
		w.mu.Unlock()
		for _, pkt := range packetsToFlush {
			w.EnqueuePacket(pkt)
		}
		// 2) и затем финальный — тоже через очередь, чтобы порядок не ломался
		w.EnqueuePacket(incomingPacket)

		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("<---- OUTGOING PACKET: \n%s", string(data))
		}
		return len(data), nil
	}

	// Write to globalTextBuffer
	var messageContent string
	if messageContent, err, _ = extractMessage(incomingPacket.RawData, incomingPacket.MessagePath); err != nil {
		appCtx.ErrorLogger.Printf("extractMessage error: %v\n", err)
		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("<---- OUTGOING PACKET: \n%s", string(data))
		}
		return w.ResponseWriter.Write(data)
	}
	// Append to buffers
	w.mu.Lock()
	w.wasMessages = true
	w.currentTextBuffer += messageContent
	w.incomingPackets = append(w.incomingPackets, incomingPacket)

	var needFlush bool

	if !w.collecting && utf8.RuneCountInString(w.currentTextBuffer) >= appCtx.responseReplaceMaxTriggerLen {
		if containsTrigger(w.currentTextBuffer) {
			w.collecting = true
		} else {
			needFlush = true
		}
	}
	collecting := w.collecting
	packetsToFlush := []ResponsePacket(nil)
	if needFlush {
		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("ResponseCollector flushing packets, currentTextBuffer len=%d, content:\n%s", utf8.RuneCountInString(w.currentTextBuffer), w.currentTextBuffer)
		}
		packetsToFlush = append(packetsToFlush, w.incomingPackets...)
		w.globalTextBuffer += w.currentTextBuffer
		w.currentTextBuffer = ""
		w.incomingPackets = w.incomingPackets[:0]
	}
	w.mu.Unlock()

	if collecting {
		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("ResponseCollector collecting chunk, currentTextBuffer len=%d, content:\n%s", utf8.RuneCountInString(w.currentTextBuffer), w.currentTextBuffer)
		}
		return len(data), nil
	}

	if needFlush {
		for _, pkt := range packetsToFlush {
			w.EnqueuePacket(pkt)
		}
		return len(data), nil // <--- ВАЖНО: не пишем data напрямую!
	}

	// Если длины недостаточно — просто продолжаем копить, ничего не отдаём пользователю
	return len(data), nil
}

func (w *ResponseCollector) WriteTemplatePacket() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.incomingPackets) == 0 {
		return fmt.Errorf("no incoming packets to write template from")
	}
	first := w.incomingPackets[0]
	// Глубокая копия всех полей
	w.templateStreamPacket = ResponsePacket{
		RawData:     first.RawData,
		Prefix:      first.Prefix,
		IsSSE:       first.IsSSE,
		MessagePath: first.MessagePath,
		PacketType:  first.PacketType,
	}
	return nil
}

func (w *ResponseCollector) CloseAndProcess() (cleanAssistantContent string, wasMessages bool, err error) {

	// Only if the final chunk was received
	w.mu.Lock()
	wasMessages = w.wasMessages
	if !w.complete {
		w.mu.Unlock()
		return "", wasMessages, nil
	}
	w.mu.Unlock()
	if appCtx.Config.DumpPackets {
		appCtx.DumpLogger.Printf("ResponseCollector CloseAndProcess called, complete=%v, collecting=%v, currentTextBuffer len=%d, content:\n%s",
			w.complete, w.collecting, utf8.RuneCountInString(w.currentTextBuffer), w.currentTextBuffer)
	}

	if w.collecting && len(w.currentTextBuffer) > 0 {
		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("ResponseCollector finalizing collecting, currentTextBuffer len=%d, content:\n%s", utf8.RuneCountInString(w.currentTextBuffer), w.currentTextBuffer)
		}

		w.mu.Lock()
		collectedTextBuffer := w.currentTextBuffer
		w.mu.Unlock()

		replaced, changed := applyReplaceRulesToString(collectedTextBuffer)
		if appCtx.Config.DumpPackets {
			appCtx.DumpLogger.Printf("ResponseCollector after applyReplaceRulesToString, changed=%v, replaced len=%d, content:\n%s", changed, utf8.RuneCountInString(replaced), replaced)
		}

		w.mu.Lock()
		w.globalTextBuffer += replaced
		w.mu.Unlock()

		if changed {
			// Rewrite template packet
			if err := w.WriteTemplatePacket(); err != nil {
				appCtx.ErrorLogger.Printf("ResponseCollector WriteTemplatePacket error: %v", err)
				return "", wasMessages, err
			}

			// Get tokens for replaced text
			ids, _ := appCtx.Tokenizer.Encode(replaced, false) // false = без спец. токенов

			// Resize incomingPackets
			w.mu.Lock()
			w.incomingPackets = make([]ResponsePacket, 0, len(ids)+1) // +1 finish packet
			baseT := time.Now().UTC()
			for i, id := range ids {
				tokenStr := appCtx.Tokenizer.Decode([]uint32{id}, true)

				pkt := ResponsePacket{
					RawData:     w.templateStreamPacket.RawData,
					Prefix:      w.templateStreamPacket.Prefix,
					IsSSE:       w.templateStreamPacket.IsSSE,
					MessagePath: w.templateStreamPacket.MessagePath,
					PacketType:  w.templateStreamPacket.PacketType,
				}

				// Обновляем created_at (чтобы не было одинакового времени на всех чанках)
				pkt.RawData = setCreatedAtIfPresent(pkt.RawData, baseT.Add(time.Duration(i)*25*time.Millisecond))

				// Вставляем response/content/text
				if pkt.MessagePath != "" {
					if newRaw, err := sjson.Set(pkt.RawData, pkt.MessagePath, tokenStr); err == nil {
						pkt.RawData = newRaw
					}
				}

				if pkt.IsSSE && pkt.Prefix != "" {
					pkt.RawData = pkt.Prefix + ": " + pkt.RawData + "\n\n"
				}

				w.incomingPackets = append(w.incomingPackets, pkt)
			}

			finalPkt := ResponsePacket{
				RawData:     w.templateFinishPacket.RawData,
				Prefix:      w.templateFinishPacket.Prefix,
				IsSSE:       w.templateFinishPacket.IsSSE,
				MessagePath: w.templateFinishPacket.MessagePath,
				PacketType:  w.templateFinishPacket.PacketType,
			}
			finalPkt.RawData = setCreatedAtIfPresent(finalPkt.RawData, time.Now().UTC())
			if finalPkt.IsSSE && finalPkt.Prefix != "" {
				finalPkt.RawData = finalPkt.Prefix + ": " + finalPkt.RawData + "\n\n"
			}
			w.incomingPackets = append(w.incomingPackets, finalPkt)

			w.mu.Unlock()
		} else {
			// Подмены нет, но collecting был включён => финальный пакет был удержан в Write()
			// Значит его нужно ДОБАВИТЬ здесь, иначе стрим не завершится.
			w.mu.Lock()
			needFinal := true
			if len(w.incomingPackets) > 0 && w.incomingPackets[len(w.incomingPackets)-1].PacketType == FinishStreamPacket {
				needFinal = false
			}
			if needFinal && w.templateFinishPacket.PacketType == FinishStreamPacket {
				w.incomingPackets = append(w.incomingPackets, w.templateFinishPacket)
			}
			w.mu.Unlock()
		}
	} else {
		w.mu.Lock()
		w.globalTextBuffer += w.currentTextBuffer
		w.currentTextBuffer = ""
		w.mu.Unlock()
	}

	// Finally, enqueue all packets
	w.mu.Lock()
	cleanAssistantContent = w.globalTextBuffer
	pktsToEnqueue := append([]ResponsePacket(nil), w.incomingPackets...)
	w.incomingPackets = w.incomingPackets[:0] // чтобы не энкьюить повторно при странных вызовах
	w.mu.Unlock()

	for _, pkt := range pktsToEnqueue {
		w.EnqueuePacket(pkt)
	}

	if appCtx.Config.DumpPackets {
		appCtx.DumpLogger.Printf("ResponseCollector final cleanAssistantContent wasMessages=%v len=%d, content:\n%s", wasMessages, utf8.RuneCountInString(cleanAssistantContent), cleanAssistantContent)
	}
	return cleanAssistantContent, wasMessages, nil
}

func setCreatedAtIfPresent(raw string, t time.Time) string {
	// /api/generate
	if gjson.Get(raw, "created_at").Exists() {
		if out, err := sjson.Set(raw, "created_at", t.UTC().Format(time.RFC3339Nano)); err == nil {
			return out
		}
	}
	// на будущее: /v1/completions (обычно int seconds)
	if gjson.Get(raw, "created").Exists() {
		if out, err := sjson.Set(raw, "created", t.UTC().Unix()); err == nil {
			return out
		}
	}
	return raw
}

func extractMessage(jsonStr string, path string) (foundPath string, err error, extracted bool) {
	res := gjson.Get(jsonStr, path)
	if !res.Exists() {
		return "", fmt.Errorf("message path %q not found in JSON", path), false
	}
	if res.Type != gjson.String {
		return "", fmt.Errorf("message path %q is not a string", path), false
	}
	return res.String(), nil, true
}

func parseIncomingBuffer(buf string) (incomingPacket ResponsePacket, err error) {

	parseJSONfnc := func(s string) (foundPath string, err error) {
		for _, path := range appCtx.Config.MessageBodyPaths {
			// Exists and it is String
			extracted := false
			if foundPath, _, extracted = extractMessage(s, path); extracted {
				return path, nil
			}
		}
		if foundPath == "" {
			return "", fmt.Errorf("no valid message path found. Check MessageBodyPaths configuration: %v", appCtx.Config.MessageBodyPaths)
		}
		return foundPath, nil
	}

	// Инициализация полей
	incomingPacket.Prefix = ""
	incomingPacket.IsSSE = false
	incomingPacket.MessagePath = ""
	incomingPacket.PacketType = OtherPacket

	// Определяем SSE
	rest := buf
	parts := strings.SplitN(buf, ":", 2)
	if len(parts) == 2 && appCtx.ssePrefixReg.MatchString(strings.TrimSpace(parts[0])) {
		incomingPacket.Prefix = strings.TrimSpace(parts[0])
		incomingPacket.IsSSE = true
		rest = strings.TrimSpace(parts[1])
	}
	incomingPacket.RawData = rest

	if appCtx.streamingPacketStopReg.MatchString(rest) {
		incomingPacket.PacketType = FinishStreamPacket
		return incomingPacket, nil
	}

	if appCtx.directPacketFlagReg.MatchString(rest) {
		if mp, perr := parseJSONfnc(rest); perr == nil && mp != "" {
			incomingPacket.PacketType = DirectPacket
			incomingPacket.MessagePath = mp
			return incomingPacket, nil
		} else if perr != nil {
			return incomingPacket, perr
		}
	}

	if appCtx.streamingPacketFlagReg.MatchString(rest) {
		if mp, perr := parseJSONfnc(rest); perr == nil && mp != "" {
			incomingPacket.PacketType = StreamPacket
			incomingPacket.MessagePath = mp
			return incomingPacket, nil
		} else if perr != nil {
			return incomingPacket, perr
		}
	}

	return incomingPacket, nil
}

func patchUsageForCompletionTokens(jsonStr string, repl string) (string, error) {
	usage := gjson.Get(jsonStr, "usage")
	if !usage.Exists() {
		return jsonStr, nil
	}
	newCompletion := calculateTokens(repl)
	out := jsonStr

	oldCompRes := gjson.Get(out, "usage.completion_tokens")
	oldTotalRes := gjson.Get(out, "usage.total_tokens")
	promptRes := gjson.Get(out, "usage.prompt_tokens")

	oldComp := oldCompRes.Int()
	oldTotal := oldTotalRes.Int()
	prompt := promptRes.Int()

	// completion_tokens
	if oldCompRes.Exists() && oldComp != int64(newCompletion) {
		var err error
		out, err = sjson.Set(out, "usage.completion_tokens", newCompletion)
		if err != nil {
			return jsonStr, fmt.Errorf("sjson.Set usage.completion_tokens: %w", err)
		}
	}

	// total_tokens
	// Обновляем total_tokens если он есть (или если есть prompt_tokens — можно выставить total).
	if oldTotalRes.Exists() || promptRes.Exists() {
		var newTotal int64
		if promptRes.Exists() {
			newTotal = prompt + int64(newCompletion)
		} else if oldTotalRes.Exists() && oldCompRes.Exists() {
			newTotal = oldTotal - oldComp + int64(newCompletion)
		} else {
			// нет достаточных данных, чтобы корректно пересчитать
			return out, nil
		}

		if oldTotalRes.Exists() {
			if oldTotal != newTotal {
				var err error
				out, err = sjson.Set(out, "usage.total_tokens", newTotal)
				if err != nil {
					return jsonStr, fmt.Errorf("sjson.Set usage.total_tokens: %w", err)
				}
			}
		} else {
			// total_tokens отсутствует, но prompt_tokens есть — можно добавить
			var err error
			out, err = sjson.Set(out, "usage.total_tokens", newTotal)
			if err != nil {
				return jsonStr, fmt.Errorf("sjson.Set usage.total_tokens(add): %w", err)
			}
		}
	}

	return out, nil
}

func applyReplaceRulesToString(src string) (string, bool) {
	changed := false
	out := src
	for _, rec := range appCtx.responseReplaceRules {
		for _, rule := range rec.Rules {
			if rule.Find == nil {
				continue
			}
			if rule.Find.FindStringIndex(out) == nil {
				continue
			}
			var newOut string
			if rule.Replace == "" {
				newOut = rule.Find.ReplaceAllString(out, "")
			} else {
				newOut = rule.Find.ReplaceAllString(out, rule.Replace)
			}
			if newOut != out {
				out = newOut
				changed = true
			}
		}
	}
	return out, changed
}

func applyResponseReplaceToPacket(pkt ResponsePacket) (jsonStr string, replacedStr string, err error) {
	// Ничего не меняем
	if pkt.MessagePath == "" || len(appCtx.responseReplaceRules) == 0 {
		if pkt.IsSSE && pkt.Prefix != "" {
			return pkt.Prefix + ": " + pkt.RawData + "\n\n", "", nil
		}
		return pkt.RawData, "", nil
	}

	src := gjson.Get(pkt.RawData, pkt.MessagePath).String()
	repl, changed := applyReplaceRulesToString(src)
	if !changed {
		if pkt.IsSSE && pkt.Prefix != "" {
			return pkt.Prefix + ": " + pkt.RawData + "\n\n", "", nil
		}
		return pkt.RawData, "", nil
	}

	// 1) Пишем заменённый текст обратно в JSON
	newJSON, err := sjson.Set(pkt.RawData, pkt.MessagePath, repl)
	if err != nil {
		return "", "", fmt.Errorf("sjson.Set message: %w", err)
	}

	// 2) Если есть usage — пересчитываем и правим
	newJSONWithUsage, uerr := patchUsageForCompletionTokens(newJSON, repl)
	if uerr != nil {
		return "", "", uerr
	}

	if pkt.IsSSE && pkt.Prefix != "" {
		return pkt.Prefix + ": " + newJSONWithUsage + "\n\n", repl, nil
	}
	return newJSONWithUsage, repl, nil
}

// containsTrigger проверяет, встречается ли один из триггеров в буфере.
func containsTrigger(inStr string) bool {
	if len(appCtx.responseReplaceRules) == 0 {
		return false
	}
	for _, rule := range appCtx.responseReplaceRules {
		if rule.Trigger == "" {
			continue
		}
		if strings.Contains(inStr, rule.Trigger) {
			return true
		}
	}
	return false
}
