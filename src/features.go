// features.go
package main

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/cespare/xxhash/v2"
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
func bodyLenNorm(tokenCount int) float64 {

	v := math.Log(1 + float64(tokenCount))
	return v / math.Log(1+adaptiveMaxTokensNormalization(int(tokenCount)))
}

// timeDecay: recency = exp(-ageDays / tau)
func timeDecay(timestamp float64) float64 {
	// timestamp is stored as UnixNano (float64)
	ts := time.Unix(0, int64(timestamp)) // reinterpreting as nanoseconds from epoch
	age := time.Since(ts).Hours() / 24.0 // age in days
	if age < 0 {
		age = 0 // protect against future dates
	}
	return math.Exp(-age / appCtx.Config.TauDays) // exponential decay
}

// keywordOverlapIDs computes the keyword overlap ratio between query and document using token IDs.
func keywordOverlapIDs(qIDs []int, docIDs []int) float64 {
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
	return float64(hits) / float64(len(qIDs))
}

// weightedKeywordOverlapIDs computes the weighted keyword overlap ratio between query and document using token IDs and IDF weights.
func weightedKeywordOverlapIDs(qIDs []int, docIDs []int, fallbackWeight float64) float64 {
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
		return 0
	}
	return sumFound / sumTotal
}

// ngramHashes computes hashes for n-grams of token IDs using xxhash.
func ngramHashes(ids []int, n int) []uint64 {
	if n <= 1 {
		out := make([]uint64, len(ids))
		for i, id := range ids {
			out[i] = ngramHash([]int{id})
		}
		return out
	}
	if len(ids) < n {
		return nil
	}
	out := make([]uint64, 0, len(ids)-n+1)
	for i := 0; i <= len(ids)-n; i++ {
		out = append(out, ngramHash(ids[i:i+n]))
	}
	return out
}

// ngramHash computes a hash for a slice of token IDs using xxhash.
func ngramHash(ids []int) uint64 {
	h := xxhash.New()
	for _, id := range ids {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(id))
		h.Write(b[:])
	}
	return h.Sum64()
}

func ngramOverlapHashes(qHashes, dHashes []uint64) float64 {
	if len(qHashes) == 0 {
		return 0
	}
	dSet := make(map[uint64]struct{}, len(dHashes))
	for _, h := range dHashes {
		dSet[h] = struct{}{}
	}
	hits := 0
	seen := make(map[uint64]struct{}, len(qHashes))
	for _, h := range qHashes {
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		if _, ok := dSet[h]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(qHashes))
}

func weightedNgramOverlapHashes(qHashes, dHashes []uint64, ngramIDF map[uint64]float64, fallback float64) float64 {
	if len(qHashes) == 0 {
		return 0
	}
	dSet := make(map[uint64]struct{}, len(dHashes))
	for _, h := range dHashes {
		dSet[h] = struct{}{}
	}

	var sumFound, sumTotal float64
	seen := make(map[uint64]struct{}, len(qHashes))
	for _, h := range qHashes {
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		w, ok := ngramIDF[h]
		if !ok {
			w = fallback
		}
		sumTotal += w
		if _, ok := dSet[h]; ok {
			sumFound += w
		}
	}
	if sumTotal == 0 {
		return 0
	}
	return sumFound / sumTotal
}

// uniqueInts: returns a slice of unique integers from the input slice.
func uniqueInts(ids []int) []int {
	set := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if _, ok := set[id]; !ok {
			set[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func buildTermFreq(ids []int) map[int]int {
	tf := make(map[int]int, len(ids))
	for _, id := range ids {
		tf[id]++
	}
	return tf
}

// bm25ScoreFromTF computes BM25 score for a query and document.
func bm25ScoreFromTF(qIDs []int, docTF map[int]int, docLen int, store IDFStore, avgdl float64) float64 {
	if len(qIDs) == 0 || len(docTF) == 0 {
		return 0
	}
	k1 := appCtx.Config.BM25K1
	b := appCtx.Config.BM25B
	if avgdl <= 0 {
		avgdl = 1.0
	}

	score := 0.0
	N := float64(store.N)
	for _, q := range qIDs {
		f := float64(docTF[q])
		if f == 0 {
			continue
		}

		// primary: take stored IDF
		idf := store.IDF[q]

		// fallback: compute using the same formula as updateDocumentInIDF
		if idf == 0 {
			df := float64(store.DF[q])
			if appCtx.Config.UseBM25IDF {
				idf = math.Log1p((N - df + 0.5) / (df + 0.5))
			} else {
				idf = math.Log1p(N / (1.0 + df))
			}
		}

		denom := f + k1*(1-b+b*(float64(docLen)/avgdl))
		score += idf * (f * (k1 + 1)) / denom
	}
	return score
}

func normalizeBM25(score float64) float64 {
	// if log normalization is enabled
	if appCtx.Config.BM25UseLogNorm {
		return math.Log1p(score) / math.Log1p(appCtx.Config.BM25LogNormScale)
	}
	return 1.0 / (1.0 + math.Exp(-appCtx.Config.BM25NormSlope*(score-appCtx.Config.BM25NormMidpoint)))
}

// updateFeaturesForCandidate computes and fills candidate features.
// Contract:
// - qUnique: unique, possibly truncated query token ids (computed before taking locks)
// - docFull: full token id sequence for the document (may contain repeats) — required for BM25
// - docUnique: unique token ids for the document (computed before taking locks)
// - docTF: term frequency map for the document (computed before taking locks)
// - cand: pointer to candidate to fill features for
func updateFeaturesForCandidate(qUnique []int, qFull []int, docFull []int, docUnique []int, docTF map[int]int, cand *Candidate) error {
	if cand == nil {
		return nil
	}
	if len(qUnique) == 0 || len(docFull) == 0 || len(docUnique) == 0 || len(qFull) == 0 || len(docTF) == 0 {
		// nothing to compute
		return nil
	}

	// Keyword overlap (set-based)
	cand.Features.KeywordOverlap = keywordOverlapIDs(qUnique, docUnique)

	// Weighted keyword overlap (uses IDF weights)
	cand.Features.WeightedOverlap = weightedKeywordOverlapIDs(qUnique, docUnique, 1.0)

	// Document length: prefer payload token count, fallback to actual full doc length
	docLen := cand.Payload.CleanTokenCount
	if docLen == 0 {
		docLen = calculateTokensWithReserveTL(docFull)
	}

	// avgdl for BM25
	avgdl := 1.0
	if appCtx.IDFStore.N > 0 {
		avgdl = float64(appCtx.IDFStore.TotalTokens) / float64(appCtx.IDFStore.N)
	}

	// Compute BM25 using qUnique (query terms) and docTF (document frequencies)
	rawBM25 := bm25ScoreFromTF(qUnique, docTF, docLen, appCtx.IDFStore, avgdl)

	// Optional debug: print per-term TFs (controlled by appCtx.Debug)
	// fmt.Printf("BM25 debug: rawBM25=%.6f", rawBM25)

	cand.Features.BM25 = normalizeBM25(rawBM25)

	// log normalized value
	// fmt.Printf("BM25 debug: normalizedBM25=%.6f\n", cand.Features.BM25)

	// n-grams: use qUnique for query ngrams (avoid duplicate ngrams from repeated query tokens)
	// and use full doc sequence to capture document ngram order
	qBigrams := ngramHashes(qFull, 2)
	dBigrams := ngramHashes(docFull, 2)
	cand.Features.NgramOverlap = ngramOverlapHashes(qBigrams, dBigrams)
	cand.Features.WeightedNgram = weightedNgramOverlapHashes(qBigrams, dBigrams, appCtx.IDFStore.NgramIDF, 1.0)

	return nil
}
