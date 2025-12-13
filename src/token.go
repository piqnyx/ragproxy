package main

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
