// token.go
package main

import (
	"fmt"
	"math"
	"time"

	lru "github.com/hashicorp/golang-lru"
)

func NewTokenCacheWrapper(size int) (*TokenCacheWrapper, error) {
	c, err := lru.New(size)
	if err != nil {
		return nil, err
	}
	return &TokenCacheWrapper{c: c}, nil
}

func (w *TokenCacheWrapper) Get(k string) (interface{}, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.c.Get(k)
}

func (w *TokenCacheWrapper) Add(k string, v interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.Add(k, v)
}

func (w *TokenCacheWrapper) Remove(k string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.Remove(k)
}

// initTokenCache: initializes the token cache
func initTokenCache() error {
	var err error
	wrapper, err := NewTokenCacheWrapper(appCtx.Config.TokensCacheSize)
	if err != nil {
		return err
	}
	appCtx.TokenCache = wrapper
	return nil
}

// Calculates token count with reserve percentage
func calculateTokensWithReserve(text string) int {
	if appCtx.Tokenizer == nil {
		panic("Tokenizer is not initialized")
	}
	tokens := appCtx.Tokenizer.Encode(text, nil, nil)
	baseCount := len(tokens)
	reservePercent := float64(appCtx.Config.TokenBufferReserve) / 100.0
	adjusted := int(math.Ceil(float64(baseCount) * (1 + reservePercent)))
	if adjusted < 0 {
		adjusted = 0
	}
	return adjusted
}

func calculateTokensWithReserveTL(tokenList []int) int {
	raw := len(tokenList)
	reservePercent := float64(appCtx.Config.TokenBufferReserve) / 100.0
	adjusted := int(math.Ceil(float64(raw) * (1.0 + reservePercent)))
	if adjusted < 0 {
		adjusted = 0
	}
	return adjusted
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
