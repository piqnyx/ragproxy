// token.go
package main

/*
#cgo LDFLAGS: -L/home/piqnyx/.local/bin/ragproxy/lib -ltokenizers
//#cgo CFLAGS: -I/home/piqnyx/.local/bin/ragproxy/include
#include "/home/piqnyx/.local/bin/ragproxy/include/tokenizers.h"
*/
import "C"

import (
	"fmt"
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
func calculateTokens(text string) int {
	if appCtx.Tokenizer == nil {
		panic("Tokenizer is not initialized")
	}
	ids, _ := appCtx.Tokenizer.Encode(text, true)
	return len(ids)
}

// tokenIDs: slice of int token IDs for given text.
func tokenIDs(text string) ([]uint32, error) {
	if appCtx.Tokenizer == nil {
		return nil, fmt.Errorf("tokenizer not initialized: call InitEncoder")
	}
	ids, _ := appCtx.Tokenizer.Encode(text, true)
	// return as []int
	return ids, nil
}

// getCachedTokenIDs: returns token IDs for payload.Body with caching.
func getCachedTokenIDs(hash, body string) ([]uint32, error) {
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
