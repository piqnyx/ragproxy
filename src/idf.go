// idf.go
package main

import (
	"encoding/json"
	"math"
	"os"
	"time"
)

// SaveIDF writes the IDFStore to a file in JSON format.
func saveIDF(withLock bool) error {
	if withLock {
		appCtx.idfMu.RLock()
	}
	store := appCtx.IDFStore
	if withLock {
		appCtx.idfMu.RUnlock()
	}

	data, err := json.Marshal(store)
	if err != nil {
		return err
	}

	last := appCtx.Config.IDFFile + ".last"
	if err := os.WriteFile(last, data, 0644); err != nil {
		// if write to tmp failed, try to remove tmp (best-effort) and return error
		_ = os.Remove(last)
		return err
	}
	// atomic replace
	return os.Rename(last, appCtx.Config.IDFFile)
}

// LoadIDF reads the IDFStore from a file.
// If the file does not exist or cannot be parsed, it initializes an empty store.
func loadIDF() error {
	data, err := os.ReadFile(appCtx.Config.IDFFile)
	if err != nil {
		if os.IsNotExist(err) {
			appCtx.AccessLogger.Printf("IDF file %s not found — initializing empty store", appCtx.Config.IDFFile)
			initEmptyIDFStore()
			return nil
		}
		appCtx.ErrorLogger.Printf("Error reading IDF file: %v — initializing empty store", err)
		initEmptyIDFStore()
		return nil
	}

	var store IDFStore
	if err := json.Unmarshal(data, &store); err != nil {
		appCtx.ErrorLogger.Printf("IDF file parse error: %v — initializing empty store", err)
		initEmptyIDFStore()
		return nil
	}

	appCtx.idfMu.Lock()
	appCtx.IDFStore = store
	appCtx.idfMu.Unlock()
	appCtx.AccessLogger.Printf("Loaded IDF store with N=%d TotalTokens=%d", store.N, store.TotalTokens)
	return nil
}

// initEmptyIDFStore initializes an empty IDFStore.
func initEmptyIDFStore() {
	appCtx.idfMu.Lock()
	appCtx.IDFStore = IDFStore{
		DF:          make(map[int]int),
		N:           0,
		IDF:         make(map[int]float64),
		NgramDF:     make(map[uint64]int),
		NgramIDF:    make(map[uint64]float64),
		TotalTokens: 0,
	}
	appCtx.idfMu.Unlock()
}

// startIDFAutoSave starts a goroutine that periodically saves the IDFStore to disk.
func startIDFAutoSave(interval time.Duration) {
	appCtx.idfAutoSaveWG.Add(1)
	go func() {
		defer appCtx.idfAutoSaveWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-appCtx.idfAutoSaveStopChan:
				return
			case <-ticker.C:
				appCtx.idfMu.Lock()
				if appCtx.IDFChanged {
					if err := saveIDF(false); err == nil {
						appCtx.IDFChanged = false
						appCtx.JournaldLogger.Printf("IDF autosaved")
					} else {
						appCtx.ErrorLogger.Printf("IDF autosave failed: %v", err)
					}
				}
				appCtx.idfMu.Unlock()
			}
		}
	}()
}

// updateDocumentInIDF updates DF/IDF for tokens and n-grams of a document.
// mode = +1 for adding a document, -1 for removing a document.
func updateDocumentInIDF(body string, tokenCount int, hash string, mode int) error {

	ids, err := getCachedTokenIDs(hash, body)
	if err != nil {
		return err
	}

	seenTokens := make(map[int]struct{})
	seenNgrams := make(map[uint64]struct{})

	appCtx.idfMu.Lock()
	defer appCtx.idfMu.Unlock()

	// Update total document count
	if mode > 0 {
		appCtx.IDFStore.N++
		appCtx.IDFStore.TotalTokens += int64(tokenCount)
	} else if mode < 0 {
		if appCtx.IDFStore.N > 0 {
			appCtx.IDFStore.N--
			// защититься от отрицательного TotalTokens
			if appCtx.IDFStore.TotalTokens >= int64(tokenCount) {
				appCtx.IDFStore.TotalTokens -= int64(tokenCount)
			} else {
				appCtx.IDFStore.TotalTokens = 0
			}
		} else {
			appCtx.ErrorLogger.Printf("Attempted to remove document from IDF when N is 0")
		}
	}

	N := appCtx.IDFStore.N

	// Process tokens
	for _, id := range ids {
		if _, ok := seenTokens[id]; ok {
			continue
		}
		seenTokens[id] = struct{}{}

		if mode > 0 {
			appCtx.IDFStore.DF[id]++
		} else if mode < 0 {
			if appCtx.IDFStore.DF[id] > 0 {
				appCtx.IDFStore.DF[id]--
			} else {
				appCtx.ErrorLogger.Printf("Attempted to remove non-existent token from IDF")
			}
		}

		df := appCtx.IDFStore.DF[id]
		if df == 0 {
			delete(appCtx.IDFStore.DF, id)
			delete(appCtx.IDFStore.IDF, id)
			continue
		}

		if N > 0 {
			// Recalculate IDF for this token
			if appCtx.Config.UseBM25IDF {
				// BM25-style idf: log1p((N - df + 0.5) / (df + 0.5))
				appCtx.IDFStore.IDF[id] = math.Log1p((float64(N) - float64(df) + 0.5) / (float64(df) + 0.5))
			} else {
				// legacy/alternative idf
				appCtx.IDFStore.IDF[id] = math.Log1p(float64(N) / (1.0 + float64(df)))
			}
		} else {
			appCtx.IDFStore.IDF[id] = 0
		}
	}

	// Process bigrams and trigrams
	for _, n := range []int{2, 3} {
		ngHashes := ngramHashes(ids, n)
		for _, h := range ngHashes {
			if _, ok := seenNgrams[h]; ok {
				continue
			}
			seenNgrams[h] = struct{}{}

			if mode > 0 {
				appCtx.IDFStore.NgramDF[h]++
			} else if mode < 0 {
				if appCtx.IDFStore.NgramDF[h] > 0 {
					appCtx.IDFStore.NgramDF[h]--
				} else {
					appCtx.ErrorLogger.Printf("Attempted to remove non-existent ngram from IDF")
				}
			}
			df := appCtx.IDFStore.NgramDF[h]
			if df == 0 {
				delete(appCtx.IDFStore.NgramDF, h)
				delete(appCtx.IDFStore.NgramIDF, h)
				continue
			}
			if N > 0 {
				if appCtx.Config.UseBM25IDF {
					appCtx.IDFStore.NgramIDF[h] = math.Log1p((float64(N) - float64(df) + 0.5) / (float64(df) + 0.5))
				} else {
					appCtx.IDFStore.NgramIDF[h] = math.Log1p(float64(N) / (1.0 + float64(df)))
				}
			} else {
				appCtx.IDFStore.NgramIDF[h] = 0
			}
		}
	}

	appCtx.IDFChanged = true

	return nil
}

// Wrapper for adding a document
func addDocumentToIDF(body string, tokenCount int, hash string) error {
	return updateDocumentInIDF(body, tokenCount, hash, +1)
}

// Wrapper for removing a document
func removeDocumentFromIDF(body string, tokenCount int, hash string) error {
	err := updateDocumentInIDF(body, tokenCount, hash, -1)
	if err != nil {
		return err
	}
	removeFromTokenCache(hash)

	return nil
}
