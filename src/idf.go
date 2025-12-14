package main

import (
	"encoding/json"
	"math"
	"os"
	"time"
)

// SaveIDF writes the IDFStore to a file in JSON format.
func saveIDF() error {
	appCtx.idfMu.RLock()
	defer appCtx.idfMu.RUnlock()
	data, err := json.Marshal(appCtx.IDFStore)
	if err != nil {
		return err
	}
	return os.WriteFile(appCtx.Config.IDFFile, data, 0644)
}

// LoadIDF reads the IDFStore from a file.
// If the file does not exist, it initializes an empty store instead of failing.
func loadIDF() error {
	data, err := os.ReadFile(appCtx.Config.IDFFile)
	if err != nil {
		if os.IsNotExist(err) {
			appCtx.JournaldLogger.Printf("IDF file %s does not exist, initializing empty IDF store", appCtx.Config.IDFFile)
			appCtx.AccessLogger.Printf("IDF file %s does not exist, initializing empty IDF store", appCtx.Config.IDFFile)
			// initialize empty store
			appCtx.idfMu.Lock()
			appCtx.IDFStore = IDFStore{
				DF:       make(map[int]int),
				N:        0,
				IDF:      make(map[int]float64),
				NgramDF:  make(map[string]int),
				NgramIDF: make(map[string]float64),
			}
			appCtx.idfMu.Unlock()
			return nil
		}
		return err
	}

	var store IDFStore
	if err := json.Unmarshal(data, &store); err != nil {
		return err
	}

	appCtx.idfMu.Lock()
	appCtx.IDFStore = store
	appCtx.idfMu.Unlock()
	return nil
}

// updateDocumentInIDF updates DF/IDF for tokens and n-grams of a document.
// mode = +1 for adding a document, -1 for removing a document.
func updateDocumentInIDF(body string, hash string, mode int) error {
	ids, err := getCachedBodyTokenIDs(hash, body)
	if err != nil {
		return err
	}

	seenTokens := make(map[int]struct{})
	seenNgrams := make(map[string]struct{})

	appCtx.idfMu.Lock()
	defer appCtx.idfMu.Unlock()

	// Update total document count
	if mode > 0 {
		appCtx.IDFStore.N++
	} else if mode < 0 {
		if appCtx.IDFStore.N > 0 {
			appCtx.IDFStore.N--
		} else {
			// Log warning but don't fail
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
				// Log warning but don't fail
				appCtx.ErrorLogger.Printf("Attempted to remove non-existent token from IDF")
			}
		}

		df := appCtx.IDFStore.DF[id]
		if N > 0 {
			// Recalculate IDF for this token
			appCtx.IDFStore.IDF[id] = math.Log1p(float64(N) / (1.0 + float64(df)))
		} else {
			appCtx.IDFStore.IDF[id] = 0
		}
	}

	// Process bigrams and trigrams
	for _, n := range []int{2, 3} {
		ngrams := ngramsIDs(ids, n, "_")
		for _, ng := range ngrams {
			if _, ok := seenNgrams[ng]; ok {
				continue
			}
			seenNgrams[ng] = struct{}{}

			if mode > 0 {
				appCtx.IDFStore.NgramDF[ng]++
			} else if mode < 0 {
				if appCtx.IDFStore.NgramDF[ng] > 0 {
					appCtx.IDFStore.NgramDF[ng]--
				} else {
					// Log warning but don't fail
					appCtx.ErrorLogger.Printf("Attempted to remove non-existent ngram from IDF")
				}
			}
			df := appCtx.IDFStore.NgramDF[ng]
			if N > 0 {
				// Recalculate IDF for this n-gram
				appCtx.IDFStore.NgramIDF[ng] = math.Log1p(float64(N) / (1.0 + float64(df)))
			} else {
				appCtx.IDFStore.NgramIDF[ng] = 0
			}
		}
	}

	return nil
}

// Wrapper for adding a document
func addDocumentToIDF(body string, hash string) error {
	return updateDocumentInIDF(body, hash, +1)
}

// Wrapper for removing a document
func removeDocumentFromIDF(body string, hash string) error {
	err := updateDocumentInIDF(body, hash, -1)
	if err != nil {
		return err
	}
	removeFromTokenCache(hash)

	return nil
}

// periodicSaveIDF periodically saves the IDF store to file every 5 minutes
func periodicSaveIDF() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		if err := saveIDF(); err != nil {
			appCtx.ErrorLogger.Printf("Failed to save IDF: %v", err)
		} else {
			appCtx.AccessLogger.Printf("IDF successfully saved to file")
		}
	}
}
