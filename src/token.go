package main

import (
	"fmt"
	"time"

	lru "github.com/hashicorp/golang-lru"
)

// initTokenCache: initializes the token cache
func initTokenCache() error {
	var err error
	appCtx.TokenCache, err = lru.New(appCtx.Config.TokensCacheSize)
	if err != nil {
		return err
	}
	return nil
}

// Calculates token count with reserve percentage
func calculateTokensWithReserve(text string) int64 {
	if appCtx.Tokenizer == nil {
		panic("Tokenizer is not initialized")
	}
	tokens := appCtx.Tokenizer.Encode(text, nil, nil)
	baseCount := len(tokens)
	reservePercent := float64(appCtx.Config.TokenBufferReserve) / 100.0
	adjustedCount := float64(baseCount) * (1 + reservePercent)
	if adjustedCount < 0 {
		adjustedCount = 0
	}
	return int64(adjustedCount)
}

// tokenIDs: slice of int token IDs for given text.
func tokenIDs(text string) ([]int, error) {
	if appCtx.Tokenizer == nil {
		return nil, fmt.Errorf("tokenizer not initialized: call InitEncoder")
	}
	ids := appCtx.Tokenizer.Encode(text, nil, nil)
	// return as []int
	return ids, nil
}

// getCachedTokenIDs: returns token IDs for payload.Body with caching.
func getCachedTokenIDs(hash, body string) ([]int, error) {
	if hash != "" {
		if v, ok := appCtx.TokenCache.Get(hash); ok {
			if e, ok := v.(*cachedEntry); ok {
				ttl := appCtx.Config.TokensCacheTTL.Duration // time.Duration
				if ttl == 0 || time.Since(e.created) < ttl {
					return e.IDs, nil
				}
				// expired -> remove
				appCtx.TokenCache.Remove(hash)
			}
		}
	}

	ids, err := tokenIDs(body)
	if err != nil {
		return nil, err
	}
	if hash != "" {
		entry := &cachedEntry{IDs: ids, created: time.Now()}
		appCtx.TokenCache.Add(hash, entry)
	}
	return ids, nil
}

// removeFromTokenCache removes the token cache for given payload. (called after payload update)
func removeFromTokenCache(hash string) {
	if hash != "" {
		appCtx.TokenCache.Remove(hash)
	}
}
