package main

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// adaptiveMaxTokensNormalization: adaptive normalization based on token count
func adaptiveMaxTokensNormalization(tokenCount int) float64 {
	norm := int(float64(tokenCount) * 0.75)
	if norm < appCtx.Config.MinTokensNormalization {
		norm = appCtx.Config.MinTokensNormalization
	}
	if norm > appCtx.Config.MaxTokensNormalization {
		norm = appCtx.Config.MaxTokensNormalization
	}
	return float64(norm)
}

// bodyLenNorm: normalize body length using log scale
func bodyLenNorm(tokenCount int64) float64 {

	v := math.Log(1 + float64(tokenCount))
	return v / math.Log(1+adaptiveMaxTokensNormalization(int(tokenCount)))
}

// payloadQuality простая эвристика качества: есть ли body, минимальная длина, корректность парсинга
func payloadQuality(p Payload) float64 {
	if strings.TrimSpace(p.Body) == "" {
		return 0.0
	}
	cnt := p.TokenCount
	if cnt == 0 {
		cnt = calculateTokensWithReserve(p.Body)
	}
	q := bodyLenNorm(cnt)
	return q
}

// timeDecay: recency = exp(-ageDays / tau)
func timeDecay(timestamp float64, tau float64) float64 {
	// timestamp is stored as UnixNano (float64)
	ts := time.Unix(0, int64(timestamp)) // reinterpreting as nanoseconds from epoch
	age := time.Since(ts).Hours() / 24.0 // age in days
	if age < 0 {
		age = 0 // protect against future dates
	}
	return math.Exp(-age / tau) // exponential decay
}

// keywordOverlapIDs computes the keyword overlap ratio between query and document using token IDs.
// Optimizations:
// - query tokens can be cached (optional)
// - early returns for empty cases
// - simplified limit handling
func keywordOverlapIDs(query string, docHash, docBody string) (float64, error) {
	qIDs, err := getCachedQueryTokenIDs(query)
	if err != nil {
		return 0, err
	}
	if len(qIDs) == 0 {
		return 0, nil
	}

	docIDs, err := getCachedBodyTokenIDs(docHash, docBody)
	if err != nil {
		return 0, err
	}
	if len(docIDs) == 0 {
		return 0, nil
	}

	set := make(map[int]struct{}, len(docIDs))
	for _, id := range docIDs {
		set[id] = struct{}{}
	}

	hits := 0
	for _, id := range qIDs {
		if _, ok := set[id]; ok {
			hits++
		}
	}

	return float64(hits) / float64(len(qIDs)), nil
}

// weightedKeywordOverlapIDs computes the weighted keyword overlap ratio between query and document using token IDs and IDF weights.
func weightedKeywordOverlapIDs(query string, docHash, docBody string, fallbackWeight float64) (float64, error) {
	qIDs, err := getCachedQueryTokenIDs(query)
	if err != nil {
		return 0, err
	}
	if len(qIDs) == 0 {
		return 0, nil
	}
	docIDs, err := getCachedBodyTokenIDs(docHash, docBody)
	if err != nil {
		return 0, err
	}
	docSet := make(map[int]struct{}, len(docIDs))
	for _, id := range docIDs {
		docSet[id] = struct{}{}
	}
	var sumFound, sumTotal float64
	for _, id := range qIDs {
		w, ok := appCtx.IDFStore.IDF[id]
		if !ok {
			w = fallbackWeight
		}
		sumTotal += w
		if _, ok := docSet[id]; ok {
			sumFound += w
		}
	}
	if sumTotal == 0 {
		return 0, nil
	}
	return sumFound / sumTotal, nil
}

// ngramsIDs generates n-grams from a slice of token IDs.
// Each n-gram is represented as a string with IDs joined by the given separator.
// Example: ids=[10,20,30], n=2, sep="_" -> ["10_20","20_30"]
func ngramsIDs(ids []int, n int, sep string) []string {
	if n <= 1 {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = strconv.Itoa(id)
		}
		return out
	}
	if len(ids) < n {
		return []string{}
	}

	out := make([]string, 0, len(ids)-n+1)
	for i := 0; i <= len(ids)-n; i++ {
		var sb strings.Builder
		sb.WriteString(strconv.Itoa(ids[i]))
		for j := 1; j < n; j++ {
			sb.WriteString(sep)
			sb.WriteString(strconv.Itoa(ids[i+j]))
		}
		out = append(out, sb.String())
	}
	return out
}

// NgramOverlap: computes the n-gram overlap ratio between query and document using token IDs.
func ngramOverlap(queryIDs, docIDs []int, n int) float64 {
	qNgrams := ngramsIDs(queryIDs, n, "_")
	if len(qNgrams) == 0 {
		return 0
	}
	dNgrams := ngramsIDs(docIDs, n, "_")
	dSet := make(map[string]struct{}, len(dNgrams))
	for _, ng := range dNgrams {
		dSet[ng] = struct{}{}
	}
	hits := 0
	for _, ng := range qNgrams {
		if _, ok := dSet[ng]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(qNgrams))
}

// WeightedNgramOverlap: взвешенный overlap по IDF n-грамм
func weightedNgramOverlap(queryIDs, docIDs []int, n int, fallback float64) float64 {
	qNgrams := ngramsIDs(queryIDs, n, "_")
	if len(qNgrams) == 0 {
		return 0
	}
	dNgrams := ngramsIDs(docIDs, n, "_")
	dSet := make(map[string]struct{}, len(dNgrams))
	for _, ng := range dNgrams {
		dSet[ng] = struct{}{}
	}

	var sumFound, sumTotal float64
	for _, ng := range qNgrams {
		w, ok := appCtx.IDFStore.NgramIDF[ng]
		if !ok {
			w = fallback
		}
		sumTotal += w
		if _, ok := dSet[ng]; ok {
			sumFound += w
		}
	}
	if sumTotal == 0 {
		return 0
	}
	return sumFound / sumTotal
}

// bm25Score computes BM25 score for a query and document.
// qIDs: token IDs of query
// docIDs: token IDs of document
// docLen: length of document (token count)
// store: IDFStore with DF and N
func bm25Score(qIDs, docIDs []int, docLen int64, store IDFStore) float64 {
	if len(qIDs) == 0 || len(docIDs) == 0 {
		appCtx.DebugLogger.Printf("BM25: empty qIDs or docIDs")
		return 0
	}

	// reading config for BM25 parameters
	k1 := appCtx.Config.BM25K1 //1.5
	b := appCtx.Config.BM25B   //0.75

	// average document length
	avgdl := float64(store.N)
	if avgdl == 0 {
		avgdl = 1
	}

	// term frequencies in document
	freq := make(map[int]int)
	for _, id := range docIDs {
		freq[id]++
	}

	score := 0.0
	for _, q := range qIDs {
		f := float64(freq[q])
		if f == 0 {
			continue
		}

		df := float64(store.DF[q])
		N := float64(store.N)
		// IDF by classic BM25 formula
		idf := math.Log((N - df + 0.5) / (df + 0.5))
		if idf < 0 {
			idf = 0 // protect against negative values
		}

		denom := f + k1*(1-b+b*(float64(docLen)/avgdl))
		score += idf * (f * (k1 + 1)) / denom
	}

	return score
}

// normalizeBM25: normalizes BM25 score to [0,1] range using a simple function.
func normalizeBM25(score float64) float64 {
	// simple normalization: 1 - exp(-score)
	// gives values in [0,1), saturates quickly
	return 1.0 - math.Exp(-score)
}

// updateFeaturesForCandidate computes expensive features for reranking.
// It fills KeywordOverlap, WeightedOverlap, BM25, NgramOverlap, WeightedNgram.
func updateFeaturesForCandidate(query string, cand *Candidate) error {

	// get token IDs for query and document
	qIDs, err := getCachedQueryTokenIDs(query)
	if err != nil {
		return err
	}
	if len(qIDs) == 0 {
		return nil
	}

	// document token IDs
	docIDs, err := getCachedBodyTokenIDs(cand.Payload.Hash, cand.Payload.Body)
	if err != nil {
		return err
	}
	if len(docIDs) == 0 {
		return nil
	}

	// Adaptive limit for query tokens
	queryLimit := len(qIDs)
	if queryLimit > appCtx.Config.MaxQueryTokens {
		queryLimit = appCtx.Config.MaxQueryTokens
		if len(qIDs) > 2*queryLimit {
			queryLimit = len(qIDs) / 2
		}
		qIDs = qIDs[:queryLimit]
	}

	// Keyword overlap
	ko, err := keywordOverlapIDs(query, cand.Payload.Hash,
		cand.Payload.Body)
	if err != nil {
		return err
	}
	cand.Features.KeywordOverlap = ko

	// Weighted keyword overlap (с IDF весами)
	wko, err := weightedKeywordOverlapIDs(query, cand.Payload.Hash,
		cand.Payload.Body, 1.0)
	if err != nil {
		return err
	}
	cand.Features.WeightedOverlap = wko

	// BM25
	bm25 := bm25Score(qIDs, docIDs, cand.Payload.TokenCount, appCtx.IDFStore)
	cand.Features.BM25 = normalizeBM25(bm25)

	// N-gram overlap (например биграммы)
	cand.Features.NgramOverlap = ngramOverlap(qIDs, docIDs, 2)

	// Weighted n-gram overlap (с IDF весами для биграмм)
	cand.Features.WeightedNgram = weightedNgramOverlap(qIDs, docIDs, 2, 1.0)

	return nil
}
