package main

import (
	"fmt"
	"regexp"
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

// validateConfig checks the configuration for correctness
func validateConfig(config Config) error {
	// Listen: IP:port or :port
	listenRe := regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}:\d+$|^:\d+$`)
	if !listenRe.MatchString(config.Listen) {
		return fmt.Errorf("`Listen` address is invalid: %s", config.Listen)
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

	// UserMessageAttachmentTags: comma-separated list of tags (only letters)
	err = validateEnumList(config.UserMessageAttachmentTags, appConsts.AvailableMessageAttachmentTags)
	if err != nil {
		return fmt.Errorf("`UserMessageAttachmentTags` is invalid: %v", err)
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

	// OllamaUnloadBeforeEmbedding: boolean, no further validation needed

	// EmbeddingModel: only letters, digits, _, -, :
	embeddingModelRe := regexp.MustCompile(`^[a-zA-Z0-9_\-:]+$`)
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

	// MainModel: only letters, digits, _, -, :
	mainModelRe := regexp.MustCompile(`^[a-zA-Z0-9_\-:]+$`)
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

	// FeedAugmentationPercent: 1-100
	if config.FeedAugmentationPercent < 1 || config.FeedAugmentationPercent > 100 {
		return fmt.Errorf("`FeedAugmentationPercent` is invalid: %d", config.FeedAugmentationPercent)
	}

	// VerboseDiskLogs: boolean (no validation needed)

	return nil
}
