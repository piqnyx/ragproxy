package main

// import (
// 	"crypto/sha512"
// 	"fmt"
// 	"sort"
// 	"strings"
// 	"time"
// )

// // test helper: pretty-print a slice of ints (truncate if long)
// func fmtIDs(ids []int, max int) string {
// 	if len(ids) == 0 {
// 		return "[]"
// 	}
// 	if len(ids) <= max {
// 		return fmt.Sprintf("%v", ids)
// 	}
// 	return fmt.Sprintf("%v ...(+%d)", ids[:max], len(ids)-max)
// }

// // decodeIDs tries to decode token ids to a human string using tokenizer.
// // Returns decoded string or a short message if tokenizer is nil or decode fails.
// func decodeIDs(ids []int) string {
// 	if appCtx.Tokenizer == nil {
// 		return "<tokenizer nil>"
// 	}

// 	switch fn := interface{}(appCtx.Tokenizer).(type) {
// 	case interface{ Decode([]int) (string, error) }:
// 		if s, err := fn.Decode(ids); err == nil {
// 			return s
// 		} else {
// 			return fmt.Sprintf("<decode error: %v>", err)
// 		}
// 	case interface{ Decode([]int) string }:
// 		return fn.Decode(ids)
// 	default:
// 		return "<decode unsupported>"
// 	}
// }

// // shortHash returns a shortened hex hash for compact display.
// func shortHash(h string) string {
// 	if len(h) <= 16 {
// 		return h
// 	}
// 	return h[:8] + "..." + h[len(h)-8:]
// }

// // intersection returns unique intersection of two int slices and the count.
// func intersection(a, b []int) ([]int, int) {
// 	set := make(map[int]struct{}, len(b))
// 	for _, x := range b {
// 		set[x] = struct{}{}
// 	}
// 	out := make([]int, 0)
// 	seen := make(map[int]struct{})
// 	for _, x := range a {
// 		if _, ok := set[x]; ok {
// 			if _, s := seen[x]; !s {
// 				out = append(out, x)
// 				seen[x] = struct{}{}
// 			}
// 		}
// 	}
// 	return out, len(out)
// }

// // topIDF returns up to n tokens from ids sorted by IDF descending (reads IDFStore under RLock).
// func topIDF(ids []int, n int) []struct {
// 	ID  int
// 	IDF float64
// } {
// 	appCtx.idfMu.RLock()
// 	defer appCtx.idfMu.RUnlock()
// 	type kv struct {
// 		ID  int
// 		IDF float64
// 	}
// 	out := make([]kv, 0, len(ids))
// 	seen := make(map[int]struct{})
// 	for _, id := range ids {
// 		if _, ok := seen[id]; ok {
// 			continue
// 		}
// 		seen[id] = struct{}{}
// 		w, ok := appCtx.IDFStore.IDF[id]
// 		if !ok {
// 			w = 0.0
// 		}
// 		out = append(out, kv{ID: id, IDF: w})
// 	}
// 	sort.Slice(out, func(i, j int) bool { return out[i].IDF > out[j].IDF })
// 	if len(out) > n {
// 		out = out[:n]
// 	}
// 	res := make([]struct {
// 		ID  int
// 		IDF float64
// 	}, len(out))
// 	for i := range out {
// 		res[i].ID = out[i].ID
// 		res[i].IDF = out[i].IDF
// 	}
// 	return res
// }

// // generateTestCandidate is unchanged, kept here for completeness.
// func generateTestCandidate(doc string) Candidate {
// 	payload := Payload{
// 		PacketID:   "test-packet-123",
// 		Timestamp:  float64(time.Now().UnixNano()),
// 		Role:       "user",
// 		Body:       doc,
// 		TokenCount: 0,
// 		Hash:       "",
// 		FileMeta: FileMeta{
// 			ID:   "",
// 			Path: "",
// 		},
// 	}

// 	payload.Hash = fmt.Sprintf("%x", sha512.Sum512([]byte(payload.Body)))
// 	payload.TokenCount = calculateTokensWithReserve(payload.Body)

// 	embedding := make([]float64, 128)
// 	for i := range embedding {
// 		embedding[i] = randFloat64Deterministic(i)
// 	}

// 	return Candidate{
// 		Payload:         payload,
// 		EmbeddingVector: embedding,
// 		Features:        Features{},
// 		Score:           0.0,
// 	}
// }

// // randFloat64Deterministic returns a deterministic pseudo-random float for tests.
// // Keeps output stable across runs for easier debugging.
// func randFloat64Deterministic(i int) float64 {
// 	// simple LCG-ish deterministic generator
// 	seed := int64(6364136223846793005) + int64(i)*1442695040888963407
// 	seed = (seed*1664525 + 1013904223) & 0x7fffffffffffffff
// 	return float64(seed%1000000) / 1000000.0
// }

// type QueryDocPair struct {
// 	Query string
// 	Doc   string
// }

// func checkPairSimilarity(pair QueryDocPair, round int) error {
// 	query := pair.Query
// 	doc := pair.Doc

// 	// prepare candidate
// 	cand := generateTestCandidate(doc)
// 	cand.Payload.TokenCount = calculateTokensWithReserve(cand.Payload.Body)
// 	cand.Features.EmbSim = 0.85
// 	cand.Features.Recency = timeDecay(cand.Payload.Timestamp - 4*3600*1e9)
// 	cand.Features.RoleScore = appCtx.Config.RoleWeights[cand.Payload.Role]
// 	cand.Features.BodyLen = bodyLenNorm(cand.Payload.TokenCount)

// 	// compute hashes
// 	queryHash := fmt.Sprintf("%x", sha512.Sum512([]byte(query)))
// 	docHash := cand.Payload.Hash

// 	// fetch token ids (query and doc)
// 	qFull, qErr := getCachedTokenIDs(queryHash, query)
// 	if qErr != nil {
// 		appCtx.ErrorLogger.Printf("tokenize query error: %v", qErr)
// 		qFull = []int{}
// 	}
// 	// keep original full docIDs for BM25 diagnostics (do not unique them here)
// 	fullDocIDs, dErr := getCachedTokenIDs(docHash, cand.Payload.Body)
// 	if dErr != nil {
// 		appCtx.ErrorLogger.Printf("tokenize doc error: %v", dErr)
// 		fullDocIDs = []int{}
// 	}

// 	// For overlap/ngram we typically use unique ids; keep both variants
// 	qUnique := uniqueInts(qFull)
// 	docUnique := uniqueInts(fullDocIDs)

// 	// Build per-doc term freq for this single candidate (for diagnostics)
// 	docTF := buildTermFreq(fullDocIDs)

// 	// compute features (call updated function)
// 	if err := updateFeaturesForCandidate(qUnique, qFull, fullDocIDs, docUnique, docTF, &cand); err != nil {
// 		appCtx.ErrorLogger.Printf("Error updating features for candidate: %v", err)
// 	}

// 	// compute intersections and diagnostics
// 	commonIDs, commonCount := intersection(qUnique, docUnique)
// 	commonPreview := fmtIDs(commonIDs, 12)

// 	// top IDF tokens in query and doc (for quick inspection)
// 	topQ := topIDF(qUnique, 5)
// 	topD := topIDF(docUnique, 5)

// 	// prepare decoded strings (safe)
// 	qDecoded := decodeIDs(qFull)
// 	qUniqueDecoded := decodeIDs(qUnique)
// 	docDecoded := decodeIDs(fullDocIDs)
// 	docUniqueDecoded := decodeIDs(docUnique)
// 	commonDecoded := decodeIDs(commonIDs)

// 	// Output: structured, clear, and verbose for debugging
// 	fmt.Println(strings.Repeat("=", 80))
// 	fmt.Printf("ROUND %d\n", round)
// 	fmt.Println(strings.Repeat("-", 80))
// 	fmt.Printf("Query: %s\n", query)
// 	fmt.Printf("QueryHash: %s\n", shortHash(queryHash))
// 	fmt.Printf("Query tokens (raw): %d  (unique: %d)\n", len(qFull), len(qUnique))
// 	fmt.Printf("Query IDs: %s\n", fmtIDs(qFull, 40))
// 	fmt.Printf("Query decoded (raw): %s\n", qDecoded)
// 	fmt.Printf("Query decoded (unique): %s\n", qUniqueDecoded)
// 	fmt.Println()

// 	fmt.Printf("Document: %s\n", doc)
// 	fmt.Printf("DocHash: %s\n", shortHash(docHash))
// 	fmt.Printf("Doc tokens (raw): %d  (unique: %d)\n", len(fullDocIDs), len(docUnique))
// 	fmt.Printf("Doc IDs: %s\n", fmtIDs(fullDocIDs, 40))
// 	fmt.Printf("Doc decoded (raw): %s\n", docDecoded)
// 	fmt.Printf("Doc decoded (unique): %s\n", docUniqueDecoded)
// 	fmt.Println()

// 	fmt.Println("FEATURES (computed):")
// 	fmt.Printf("  EmbSim:         %.4f\n", cand.Features.EmbSim)
// 	fmt.Printf("  Recency:        %.4f\n", cand.Features.Recency)
// 	fmt.Printf("  RoleScore:      %.4f\n", cand.Features.RoleScore)
// 	fmt.Printf("  BodyLen:        %.4f\n", cand.Features.BodyLen)
// 	fmt.Printf("  KeywordOverlap: %.4f\n", cand.Features.KeywordOverlap)
// 	fmt.Printf("  WeightedOverlap:%.4f\n", cand.Features.WeightedOverlap)
// 	fmt.Printf("  BM25:           %.4f\n", cand.Features.BM25)
// 	fmt.Printf("  NgramOverlap:   %.4f\n", cand.Features.NgramOverlap)
// 	fmt.Printf("  WeightedNgram:  %.4f\n", cand.Features.WeightedNgram)
// 	fmt.Println()

// 	fmt.Println("INTERSECTION / DIAGNOSTICS:")
// 	fmt.Printf("  Common unique IDs count: %d\n", commonCount)
// 	fmt.Printf("  Common IDs (preview): %s\n", commonPreview)
// 	fmt.Printf("  Common decoded tokens (preview): %s\n", commonDecoded)
// 	fmt.Println()

// 	fmt.Println("TOP IDF TOKENS (query):")
// 	if len(topQ) == 0 {
// 		fmt.Println("  <no tokens>")
// 	} else {
// 		for _, t := range topQ {
// 			fmt.Printf("  id=%d idf=%.4f\n", t.ID, t.IDF)
// 		}
// 	}
// 	fmt.Println("TOP IDF TOKENS (doc):")
// 	if len(topD) == 0 {
// 		fmt.Println("  <no tokens>")
// 	} else {
// 		for _, t := range topD {
// 			fmt.Printf("  id=%d idf=%.4f\n", t.ID, t.IDF)
// 		}
// 	}
// 	fmt.Println(strings.Repeat("=", 80))
// 	fmt.Println()

// 	// Extra: if tokenizer present, show token->id mapping for first few tokens (helpful)
// 	if appCtx.Tokenizer != nil {
// 		// try to show mapping for query unique tokens (up to 8)
// 		preview := qUnique
// 		if len(preview) > 8 {
// 			preview = preview[:8]
// 		}
// 		if len(preview) > 0 {
// 			fmt.Printf("Token->ID preview (query unique, up to 8):\n")
// 			for _, id := range preview {
// 				appCtx.idfMu.RLock()
// 				idf := appCtx.IDFStore.IDF[id]
// 				appCtx.idfMu.RUnlock()
// 				fmt.Printf("  id=%d idf=%.4f\n", id, idf)
// 			}
// 			fmt.Println()
// 		}
// 	}

// 	return nil
// }

// func testFunc() error {
// 	// Reset IDF store for a clean test run
// 	appCtx.idfMu.Lock()
// 	appCtx.IDFStore = IDFStore{
// 		DF:          make(map[int]int),
// 		N:           0,
// 		IDF:         make(map[int]float64),
// 		NgramDF:     make(map[uint64]int),
// 		NgramIDF:    make(map[uint64]float64),
// 		TotalTokens: 0,
// 	}
// 	appCtx.idfMu.Unlock()

// 	// Reinitialize token cache to avoid stale entries
// 	if appCtx.TokenCache != nil {
// 		_ = initTokenCache()
// 	}

// 	pairs := []QueryDocPair{
// 		{"технологии в языковых моделях", "Какие технологии используются в современных языковых моделях?"},
// 		{"машинное обучение", "Основные подходы машинного обучения: supervised, unsupervised, reinforcement"},
// 		{"языковые модели", "Языковые модели используются в NLP"},
// 		{"нейросети искусственный интеллект", "Области применения искусственного интеллекта и нейросетей"},
// 		{"кролит через норку", "Как выращивать кроликов в норах — практическое руководство"},
// 		{"python анализ данных", "Как использовать Python для анализа больших данных?"},
// 		{"как приготовить борщ", "Рецепт борща с говядиной и свеклой"},
// 		{"лучшие маршруты в Австрии", "Путеводитель по живописным маршрутам в Австрии"},
// 		{"симптомы простуды", "Симптомы и профилактика простуды у взрослых"},
// 		{"права потребителей возврат товара", "Как вернуть товар в магазин по закону о защите прав потребителей"},
// 		{"что такое блокчейн", "Объяснение блокчейна простыми словами"},
// 		{"инвестиции в акции", "Основы инвестирования в акции для начинающих"},
// 		{"упражнения для спины", "Комплекс упражнений для укрепления мышц спины"},
// 		{"переводчик английский русский", "Как выбрать лучший онлайн переводчик для технических текстов"},
// 		{"история Римской империи", "Краткая история Римской империи и её падение"},
// 		{"как учить иностранные слова", "Эффективные техники запоминания слов"},
// 		{"обзор iPhone 14", "Технические характеристики и обзор iPhone 14"},
// 		{"что такое GDPR", "Основные положения GDPR и как они влияют на бизнес"},
// 		{"рецепты веганских блюд", "10 простых веганских рецептов на неделю"},
// 		{"как настроить nginx", "Руководство по настройке Nginx для продакшн сервера"},
// 		{"симптомы депрессии", "Признаки депрессии и куда обратиться за помощью"},
// 		{"правила дорожного движения", "Обновления правил дорожного движения 2025 года"},
// 		{"как писать резюме", "Советы по составлению резюме для IT‑специалистов"},
// 		{"что такое квантовый компьютер", "Введение в квантовые вычисления для неспециалистов"},
// 		{"лучшие практики CI CD", "Настройка CI/CD для микросервисов"},
// 		{"обзор фильма Инцепшн", "Краткий обзор и анализ фильма «Inception»"},
// 		{"как сажать розы", "Советы по посадке и уходу за розами"},
// 		{"что такое SEO", "Основы SEO и оптимизация сайта"},
// 		{"упражнения для бега", "План тренировок для начинающих бегунов"},
// 		{"как выбрать ноутбук", "Руководство по выбору ноутбука для работы и игр"},
// 		{"история интернета", "Этапы развития интернета и ключевые технологии"},
// 		{"как экономить деньги", "Практические советы по личным финансам"},
// 		{"перевод юридического текста", "Особенности перевода юридических документов"},
// 		{"что такое микросервисы", "Преимущества и недостатки микросервисной архитектуры"},
// 		{"рецепты быстрых завтраков", "7 идей для быстрого и полезного завтрака"},
// 		{"как настроить VPN", "Настройка VPN на Windows и мобильных устройствах"},
// 		{"признаки аллергии", "Как распознать и лечить аллергическую реакцию"},
// 		{"основы статистики", "Введение в описательную и инференциальную статистику"},
// 		{"как писать unit тесты", "Практические советы по написанию unit тестов в Go"},
// 	}

// 	// Collect unique documents from pairs and add them to IDFStore
// 	docSet := make(map[string]struct{})
// 	for _, p := range pairs {
// 		docSet[p.Doc] = struct{}{}
// 	}

// 	for d := range docSet {
// 		h := fmt.Sprintf("%x", sha512.Sum512([]byte(d)))
// 		tc := calculateTokensWithReserve(d)
// 		if err := addDocumentToIDF(d, tc, h); err != nil {
// 			appCtx.ErrorLogger.Printf("addDocumentToIDF error for doc=%s: %v", shortHash(h), err)
// 		} else {
// 			appCtx.AccessLogger.Printf("Added doc to IDF: %s (tokens=%d)", shortHash(h), tc)
// 		}
// 	}

// 	// Run checks for each pair
// 	for i, pair := range pairs {
// 		if err := checkPairSimilarity(pair, i+1); err != nil {
// 			return err
// 		}
// 	}

// 	return nil
// }
