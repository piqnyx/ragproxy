package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
)

// validateEnumList validates each value in a list against allowed options
func validateEnumList(values []string, allowed []string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, v := range allowed {
		allowedSet[v] = struct{}{}
	}
	for _, t := range values {
		if _, ok := allowedSet[t]; !ok {
			return fmt.Errorf("invalid value: '%s'", t)
		}
	}
	if len(values) == 0 {
		return fmt.Errorf("list is empty")
	}
	return nil
}

// compileFilePatterns compiles the FilePatterns strings into regexps
func compileFilePatterns(cfg *Config) error {
	if len(cfg.FilePatterns) == 0 {
		cfg.FilePatternsReg = nil
		return nil
	}

	regs := make([]*regexp.Regexp, 0, len(cfg.FilePatterns))
	for i, p := range cfg.FilePatterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		r, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("invalid FilePatterns[%d]: %w", i, err)
		}
		regs = append(regs, r)
	}
	cfg.FilePatternsReg = regs
	return nil
}

// CheckEmbeddingNormalization tests embedding normalization by embedding a test string
// and calculating the L2 norm of the resulting vector.
func CheckEmbeddingNormalization() error {
	const testStr = "embedding normalization test"
	vec, err := embedText(testStr)
	if err != nil {
		return fmt.Errorf("embedding error: %w", err)
	}
	var sum float64
	for _, v := range vec {
		sum += float64(v * v)
	}
	norm := math.Sqrt(sum)
	appCtx.AccessLogger.Printf("Embedding vector L2 norm for test string: %.6f", norm)
	if math.Abs(norm-1.0) > 0.01 {
		appCtx.ErrorLogger.Printf("WARNING: Embedding vector is NOT normalized (norm=%.6f). Consider normalizing output of embedText().", norm)
	} else {
		appCtx.JournaldLogger.Printf("Embedding vector is normalized (norm=%.6f).", norm)
	}
	return nil
}

// validateConfig checks the configuration for correctness
func validateConfig(config Config) error {
	// Listen: IP:port or :port
	listenRe := regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}:\d+$|^:\d+$`)
	if !listenRe.MatchString(config.Listen) {
		return fmt.Errorf("`Listen` address is invalid: %s", config.Listen)
	}

	// IDFFile: path to IDF DB file (non-empty)
	if strings.TrimSpace(config.IDFFile) == "" {
		return fmt.Errorf("`IDFFile` path is invalid: %s", config.IDFFile)
	}
	// RegEx check for correct path and touch file after this validation
	if _, err := os.Stat(config.IDFFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("`IDFFile` path is invalid or inaccessible: %v", err)
	}

	// TokenBufferReserve: non-negative integer
	if config.TokenBufferReserve < 0 {
		return fmt.Errorf("`TokenBufferReserve` is invalid: %d", config.TokenBufferReserve)
	}

	var err error
	// UserMessageTags and UserMessageAttachmentTags: comma-separated list of tags (only letters)
	err = validateEnumList(config.UserMessageTags, appConsts.AvailableMessageTags)
	if err != nil {
		return fmt.Errorf("`UserMessageTags` is invalid: %v", err)
	}

	// UserMessageAskAttachmentTags: comma-separated list of tags (only letters)
	err = validateEnumList(config.UserMessageAskAttachmentTags, appConsts.AvailableMessageAskAttachmentTags)
	if err != nil {
		return fmt.Errorf("`UserMessageAskAttachmentTags` is invalid: %v", err)
	}

	// UserMessageAgentAttachmentTags: comma-separated list of tags (only letters)
	err = validateEnumList(config.UserMessageAgentAttachmentTags, appConsts.AvailableMessageAgentAttachmentTags)
	if err != nil {
		return fmt.Errorf("`UserMessageAgentAttachmentTags` is invalid: %v", err)
	}

	// Temperature: 0.0 - 1.0
	if config.Temperature < 0.0 || config.Temperature > 1.0 {
		return fmt.Errorf("`Temperature` is invalid: %f", config.Temperature)
	}

	// OllamaBase: http(s)://host:port
	ollamaBaseRe := regexp.MustCompile(`^https?://[\w\.\-]+(:\d+)?$`)
	if !ollamaBaseRe.MatchString(config.OllamaBase) {
		return fmt.Errorf("`OllamaBase` is invalid: %s", config.OllamaBase)
	}

	ollamaKeepAliveRe := regexp.MustCompile(`^\d+[smhd]$`)
	if !ollamaKeepAliveRe.MatchString(config.OllamaKeepAlive) {
		return fmt.Errorf("`OllamaKeepAlive` is invalid: %s", config.OllamaKeepAlive)
	}

	// OllamaUnloadOnLoVRAM: boolean, no further validation needed

	// EmbeddingModel: only letters, digits, _, -, :, /
	embeddingModelRe := regexp.MustCompile(`^[a-zA-Z0-9_\-:/]+$`)
	if !embeddingModelRe.MatchString(config.EmbeddingModel) {
		return fmt.Errorf("`EmbeddingModel` is invalid: %s", config.EmbeddingModel)
	}

	// EmbeddingsEndpoint: starts with /
	if !strings.HasPrefix(config.EmbeddingsEndpoint, "/") {
		return fmt.Errorf("`EmbeddingsEndpoint` must start with '/': %s", config.EmbeddingsEndpoint)
	}

	// EmbeddingsModeWindowSize: positive integer
	if config.EmbeddingsModeWindowSize <= 0 {
		return fmt.Errorf("`EmbeddingsModeWindowSize` is invalid: %d", config.EmbeddingsModeWindowSize)
	}

	// MainModel: only letters, digits, _, -, :, /
	mainModelRe := regexp.MustCompile(`^[a-zA-Z0-9_\-:/]+$`)
	if !mainModelRe.MatchString(config.MainModel) {
		return fmt.Errorf("`MainModel` is invalid: %s", config.MainModel)
	}

	// MainModelWindowSize: positive integer
	if config.MainModelWindowSize <= 0 {
		return fmt.Errorf("`MainModelWindowSize` is invalid: %d", config.MainModelWindowSize)
	}

	// QdrantHost: localhost or IP or hostname
	hostRe := regexp.MustCompile(`^(localhost|(\d{1,3}\.){3}\d{1,3}|[a-zA-Z0-9\-\.]+)$`)
	if !hostRe.MatchString(config.QdrantHost) {
		return fmt.Errorf("`QdrantHost` is invalid: %s", config.QdrantHost)
	}

	// QdrantPort: 1-65535
	if config.QdrantPort < 1 || config.QdrantPort > 65535 {
		return fmt.Errorf("`QdrantPort` is invalid: %d", config.QdrantPort)
	}

	// QdrantKeepAlive: non-negative integer
	if config.QdrantKeepAlive < 0 {
		return fmt.Errorf("`QdrantKeepAlive` is invalid: %d", config.QdrantKeepAlive)
	}

	// QdrantCollection: only letters, digits, _
	collRe := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !collRe.MatchString(config.QdrantCollection) {
		return fmt.Errorf("`QdrantCollection` is invalid: %s", config.QdrantCollection)
	}

	// QdrantMetric: Cosine, Euclid, Dot
	if config.QdrantMetric != "Cosine" && config.QdrantMetric != "Euclid" && config.QdrantMetric != "Dot" {
		return fmt.Errorf("`QdrantMetric` is invalid: %s", config.QdrantMetric)
	}

	// QdrantVectorSize: 1-32768
	if config.QdrantVectorSize <= 0 || config.QdrantVectorSize > 32768 {
		return fmt.Errorf("`QdrantVectorSize` must be between 1 and 32768: %d", config.QdrantVectorSize)
	}

	// MaxFileSize: -1 or greater than zero
	if config.MaxFileSize < -1 || config.MaxFileSize == 0 {
		return fmt.Errorf("`MaxFileSize` is invalid: %d", config.MaxFileSize)
	}

	// FilePatterns compiled into FilePatterns
	if err := compileFilePatterns(&appCtx.Config); err != nil {
		return fmt.Errorf("`FilePatterns` Invalid file pattern: %v", err)
	}

	// SearchSource: comma-separated list of tags (only letters)
	err = validateEnumList(config.SearchSource, appConsts.AvailableSearchSources)
	if err != nil {
		return fmt.Errorf("`SearchSource` is invalid: %v", err)
	}

	// SearchMaxAgeDays: -1 or greater than zero
	if config.SearchMaxAgeDays < -1 || config.SearchMaxAgeDays == 0 {
		return fmt.Errorf("`SearchMaxAgeDays` is invalid: %d", config.SearchMaxAgeDays)
	}

	// SearchTopK: -1 or greater than zero
	if config.SearchTopK < -1 || config.SearchTopK == 0 {
		return fmt.Errorf("`SearchTopK` is invalid: %d", config.SearchTopK)
	}

	// CosineMinScore: 0.0 - 1.0
	if config.CosineMinScore < 0.0 || config.CosineMinScore > 1.0 {
		return fmt.Errorf("`CosineMinScore` is invalid: %f", config.CosineMinScore)
	}

	// EuclidMaxDistance: non-negative
	if config.EuclidMaxDistance < 0.0 {
		return fmt.Errorf("`EuclidMaxDistance` is invalid: %f", config.EuclidMaxDistance)
	}

	// RerankTopN: -1 or greater than zero, not greater than SearchTopK (if SearchTopK != -1)
	if config.RerankTopN < -1 || config.RerankTopN == 0 {
		return fmt.Errorf("`RerankTopN` is invalid: %d", config.RerankTopN)
	}
	if config.SearchTopK != -1 && config.RerankTopN != -1 && int64(config.RerankTopN) > config.SearchTopK {
		return fmt.Errorf("`RerankTopN` (%d) cannot be greater than `SearchTopK` (%d)", config.RerankTopN, config.SearchTopK)
	}

	// MinRankScore: 0.0 - 1.0
	if config.MinRankScore < 0.0 || config.MinRankScore > 1.0 {
		return fmt.Errorf("`MinRankScore` is invalid: %f", config.MinRankScore)
	}

	// MaxQueryTokens: positive integer
	if config.MaxQueryTokens <= 0 {
		return fmt.Errorf("`MaxQueryTokens` is invalid: %d", config.MaxQueryTokens)
	}

	// TokensCacheTTL: not empty duration
	if config.TokensCacheTTL.Duration <= 0 {
		return fmt.Errorf("`TokensCacheTTL` must be positive: %v", config.TokensCacheTTL)
	}

	// TokensCacheSize: positive integer
	if config.TokensCacheSize <= 0 {
		return fmt.Errorf("`TokensCacheSize` is invalid: %d", config.TokensCacheSize)
	}

	// TauDays: positive float
	if config.TauDays <= 0.0 {
		return fmt.Errorf("`TauDays` is invalid: %f", config.TauDays)
	}

	// MaxTokensNormalization: positive integer
	if config.MaxTokensNormalization <= 0 {
		return fmt.Errorf("`MaxTokensNormalization` is invalid: %f", config.MaxTokensNormalization)
	}

	// MinTokensNormalization: positive integer
	if config.MinTokensNormalization <= 0 {
		return fmt.Errorf("`MinTokensNormalization` is invalid: %d", config.MinTokensNormalization)
	}

	// DefaultWeights: non-empty list of 7 non-negative floats
	if len(config.DefaultWeights) != 10 {
		return fmt.Errorf("`DefaultWeights` must have exactly 7 elements, got %d", len(config.DefaultWeights))
	}
	for i, w := range config.DefaultWeights {
		if w < 0.0 {
			return fmt.Errorf("`DefaultWeights[%d]` is invalid: %f", i, w)
		}
	}

	// ReturnVectors: boolean (no validation needed)

	// BM25K1: positive float
	if config.BM25K1 <= 0.0 {
		return fmt.Errorf("`BM25K1` is invalid: %f", config.BM25K1)
	}

	// BM25B: 0.0 - 1.0
	if config.BM25B < 0.0 || config.BM25B > 1.0 {
		return fmt.Errorf("`BM25B` is invalid: %f", config.BM25B)
	}

	// RoleWeights: map of string to non-negative float
	for role, weight := range config.RoleWeights {
		if strings.TrimSpace(role) == "" {
			return fmt.Errorf("`RoleWeights` contains empty role name")
		}
		if weight < 0.0 {
			return fmt.Errorf("`RoleWeights[%s]` is invalid: %f", role, weight)
		}
		if !slices.Contains(appConsts.AvailableSearchSources, role) {
			return fmt.Errorf("`RoleWeights[%s]` is not in AvailableSearchSources", role)
		}
	}
	for _, allowed := range appConsts.AvailableSearchSources {
		if _, ok := config.RoleWeights[allowed]; !ok {
			return fmt.Errorf("`RoleWeights` missing required role: %s", allowed)
		}
	}

	// FeedAugmentationPercent: 1-100
	if config.FeedAugmentationPercent < 1 || config.FeedAugmentationPercent > 100 {
		return fmt.Errorf("`FeedAugmentationPercent` is invalid: %d", config.FeedAugmentationPercent)
	}

	// VerboseDiskLogs: boolean (no validation needed)

	return nil
}
