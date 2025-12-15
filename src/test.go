package main

import (
	"crypto/sha512"
	"fmt"
	"math/rand"
	"time"
)

func generateTestCandidate(doc string) Candidate {
	payload := Payload{
		PacketID:   "test-packet-123",
		Timestamp:  float64(time.Now().UnixNano()),
		Role:       "user",
		Body:       doc,
		TokenCount: 0,
		Hash:       "",
		FileMeta: FileMeta{
			ID:   "",
			Path: "",
		},
	}

	payload.Hash = fmt.Sprintf("%x", sha512.Sum512([]byte(payload.Body)))
	payload.TokenCount = calculateTokensWithReserve(payload.Body)

	embedding := make([]float64, 128)
	for i := range embedding {
		embedding[i] = rand.Float64()
	}

	return Candidate{
		Payload:         payload,
		EmbeddingVector: embedding,
		Features:        Features{},
		Score:           0.0,
	}
}

type QueryDocPair struct {
	Query string
	Doc   string
}

func checkPairSimilarity(pair QueryDocPair, round int) error {
	query := pair.Query
	doc := pair.Doc
	cand := generateTestCandidate(doc)
	cand.Payload.TokenCount = calculateTokensWithReserve(cand.Payload.Body)
	cand.Features.EmbSim = 0.85
	cand.Features.Recency = timeDecay(cand.Payload.Timestamp - 4*3600*1e9)
	cand.Features.RoleScore = appCtx.Config.RoleWeights[cand.Payload.Role]
	cand.Features.BodyLen = bodyLenNorm(cand.Payload.TokenCount)
	cand.Features.PayloadQuality = payloadQuality(cand.Payload)

	queryHash := fmt.Sprintf("%x", sha512.Sum512([]byte(query)))

	updateFeaturesForCandidate(query, queryHash, &cand)

	// Получаем токены запроса и документа
	qIDs, _ := getCachedTokenIDs(queryHash, query)
	docIDs, _ := getCachedTokenIDs(cand.Payload.Hash, cand.Payload.Body)

	// Срезаем хэш для вывода
	hash := cand.Payload.Hash
	hashShort := hash[:8] + "..." + hash[len(hash)-8:]

	fmt.Printf("=== Раунд %d ===\n", round)
	fmt.Printf("Query:\t%s\n", query)
	fmt.Printf("Doc:\t%s\n", doc)
	fmt.Printf("Query tokens:\t%d\n", len(qIDs))
	fmt.Printf("Doc tokens:\t%d\n", len(docIDs))
	fmt.Printf("Doc hash:\t%s\n", hashShort)
	fmt.Printf("EmbSim:\t\t%.4f\n", cand.Features.EmbSim)
	fmt.Printf("Recency:\t%.4f\n", cand.Features.Recency)
	fmt.Printf("RoleScore:\t%.4f\n", cand.Features.RoleScore)
	fmt.Printf("BodyLen:\t%.4f\n", cand.Features.BodyLen)
	fmt.Printf("PayloadQuality:\t%.4f\n", cand.Features.PayloadQuality)
	fmt.Printf("KeywordOverlap:\t%.4f\n", cand.Features.KeywordOverlap)
	fmt.Printf("WeightedOverlap:\t%.4f\n", cand.Features.WeightedOverlap)
	fmt.Printf("BM25:\t\t%.4f\n", cand.Features.BM25)
	fmt.Printf("NgramOverlap:\t%.4f\n", cand.Features.NgramOverlap)
	fmt.Printf("WeightedNgram:\t%.4f\n", cand.Features.WeightedNgram)
	fmt.Println()

	return nil
}

func testFunc() error {
	pairs := []QueryDocPair{
		{"технологии в языковых моделях", "Какие технологии используются в современных языковых моделях?"},
		{"машинное обучение", "Какие технологии используются в современных языковых моделях?"},
		{"языковые модели", "Языковые модели используются в NLP"},
		{"нейросети и искусственный интеллект", "Области применения искусственного интеллекта и нейросетей"},
		{"кролит через норку", "Какие технологии используются в современных языковых моделях?"},
		{"python для анализа данных", "Как использовать Python для анализа больших данных?"},
	}

	for i, pair := range pairs {
		if err := checkPairSimilarity(pair, i+1); err != nil {
			return err
		}
	}
	return nil
}
