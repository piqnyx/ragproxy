// config.go
package main

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
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
func checkEmbeddingNormalization() error {
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

// validateSystemMessagePatch checks the SystemMessagePatchConfig for correctness
func validateSystemMessagePatch(cfg *SystemMessagePatchConfig) error {
	if cfg.Replace == nil {
		return fmt.Errorf("SystemMessagePatch.Replace field must be defined")
	}
	if cfg.AddToBegin == nil {
		return fmt.Errorf("SystemMessagePatch.AddToBegin field must be defined")
	}
	if cfg.AddToEnd == nil {
		return fmt.Errorf("SystemMessagePatch.AddToEnd field must be defined")
	}
	if cfg.AddAfter == nil {
		return fmt.Errorf("SystemMessagePatch.AddAfter field must be defined")
	}
	if cfg.Remove == nil {
		return fmt.Errorf("SystemMessagePatch.Remove field must be defined")
	}

	for key, value := range cfg.Replace {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("SystemMessagePatch.Replace: empty key is not allowed")
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("SystemMessagePatch.Replace: empty value for key '%s' is not allowed", key)
		}
	}

	for _, rule := range cfg.AddAfter {
		if strings.TrimSpace(rule.Find) == "" {
			return fmt.Errorf("SystemMessagePatch.AddAfter: empty search key is not allowed")
		}
		// rule.Insert can be empty, meaning no insertion
	}

	return nil
}

// validateReplaceGroups проверяет, что в replaceTpl ссылки на группы ($1 или ${1})
// точно соответствуют группам, определённым в find (findGroups).
// Ошибка если:
//   - replace содержит ссылки, а findGroups == 0
//   - replace не содержит ссылок, а findGroups > 0
//   - есть ссылка на группу > findGroups
//   - количество уникальных ссылок != findGroups (т.е. не все группы упомянуты)
func validateReplaceGroups(findGroups int, replaceTpl string) error {
	// ищем $1 или ${1}
	re, err := regexp.Compile(`\$(\d+)|\$\{(\d+)\}`)
	if err != nil {
		// эта регулярка константная и валидна, но на всякий случай
		return fmt.Errorf("internal validation regex compile failed: %v", err)
	}
	matches := re.FindAllStringSubmatch(replaceTpl, -1)

	// если ссылок нет
	if len(matches) == 0 {
		if findGroups == 0 {
			return nil
		}
		return fmt.Errorf("replace references no groups but find defines %d", findGroups)
	}

	// есть ссылки, но в find нет групп
	if findGroups == 0 {
		return fmt.Errorf("replace references groups but find has none")
	}

	seen := make(map[int]struct{}, len(matches))
	maxRef := 0
	for _, m := range matches {
		// m[1] или m[2] содержит номер группы в зависимости от формы
		var s string
		if m[1] != "" {
			s = m[1]
		} else {
			s = m[2]
		}
		idx, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid group reference '%s' in replace template", s)
		}
		if idx < 1 {
			return fmt.Errorf("invalid group reference %d: groups are 1..%d", idx, findGroups)
		}
		if idx > maxRef {
			maxRef = idx
		}
		seen[idx] = struct{}{}
	}

	if maxRef > findGroups {
		return fmt.Errorf("replace references group %d but find has only %d groups", maxRef, findGroups)
	}

	// требование: replace должен ссылаться на все группы, определённые в find
	if len(seen) != findGroups {
		return fmt.Errorf("replace references %d groups but find defines %d", len(seen), findGroups)
	}

	return nil
}

// initResponseReplaceRules инициализирует правила замены из конфига.
// - Триггер (ключ) тримится; find и replace НЕ тримятся (пробелы могут быть значимы).
// - Для find используется regexp.Compile (ошибка возвращается при некорректной regex).
// - Пустой repl означает удаление совпадений.
func initResponseReplaceRules() error {
	// reset storage
	appCtx.responseReplaceRules = nil
	appCtx.responseReplaceMaxTriggerLen = 0

	if len(appCtx.Config.ResponseReplacer) == 0 {
		return nil
	}

	records := make([]ResponseReplaceRecord, 0, len(appCtx.Config.ResponseReplacer))

	for rawTrig, m := range appCtx.Config.ResponseReplacer {
		trig := strings.TrimSpace(rawTrig)
		if trig == "" {
			return fmt.Errorf("ResponseReplacer contains empty trigger key")
		}
		if len(m) == 0 {
			// нет правил для триггера — пропускаем (можно логировать)
			appCtx.DebugLogger.Printf("ResponseReplacer: trigger %q has no rules, skipping", trig)
			continue
		}

		rules := make([]ResponseMsgReplaceRule, 0, len(m))
		for find, repl := range m {
			// НЕ тримим find и repl — пробелы в regex/replace могут быть значимы
			if strings.TrimSpace(find) == "" {
				return fmt.Errorf("ResponseReplacer[%s] contains empty find regex", trig)
			}

			findReg, err := regexp.Compile(find)
			if err != nil {
				return fmt.Errorf("ResponseReplacer[%s] invalid find regex '%s': %v", trig, find, err)
			}

			// repl может быть пустой — это означает удаление
			if repl != "" {
				if err := validateReplaceGroups(findReg.NumSubexp(), repl); err != nil {
					return fmt.Errorf("ResponseReplacer[%s] invalid replace '%s': %v", trig, repl, err)
				}
			}

			rules = append(rules, ResponseMsgReplaceRule{
				Find:    findReg,
				Replace: repl,
			})
		}

		if len(rules) == 0 {
			continue
		}

		records = append(records, ResponseReplaceRecord{
			Trigger: trig,
			Rules:   rules,
		})

		// считаем длину триггера в рунах (не в байтах)
		if l := utf8.RuneCountInString(trig); l > appCtx.responseReplaceMaxTriggerLen {
			appCtx.responseReplaceMaxTriggerLen = l
		}
	}
	appCtx.responseReplaceMaxTriggerLen *= appCtx.Config.MaxTriggerLengthMultiplier
	appCtx.responseReplaceMaxTriggerLen += appCtx.Config.MaxTriggerLengthAdditional
	appCtx.responseReplaceRules = records
	return nil
}

// validateConfig checks the configuration for correctness
func validateConfig(config Config) error {
	// Listen: IP:port or :port

	if re, err := regexp.Compile(`^(\d{1,3}\.){3}\d{1,3}:\d+$|^:\d+$`); err == nil {
		if !re.MatchString(config.Listen) {
			return fmt.Errorf("`Listen` address is invalid: %s", config.Listen)
		}
	} else {
		return fmt.Errorf("`Listen` address regex compilation failed: %v", err)
	}

	// IDFFile: path to IDF DB file (non-empty)
	if strings.TrimSpace(config.IDFFile) == "" {
		return fmt.Errorf("`IDFFile` path is invalid: %s", config.IDFFile)
	}
	if _, err := os.Stat(config.IDFFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("`IDFFile` path is invalid or inaccessible: %v", err)
	}

	// TokenizerHFModelName: only letters, digits, _, -, :, /
	if re, err := regexp.Compile(`^[a-zA-Z0-9_\-:/]+$`); err == nil {
		if !re.MatchString(config.TokenizerHFModelName) {
			return fmt.Errorf("`TokenizerHFModelName` is invalid: %s", config.TokenizerHFModelName)
		}
	} else {
		return fmt.Errorf("`TokenizerHFModelName` regex compilation failed: %v", err)
	}

	// TokenizerPretrainedCacheDir: path to cache directory (non-empty)
	if strings.TrimSpace(config.TokenizerPretrainedCacheDir) == "" {
		return fmt.Errorf("`TokenizerPretrainedCacheDir` path is invalid: %s", config.TokenizerPretrainedCacheDir)
	}
	if fi, err := os.Stat(config.TokenizerPretrainedCacheDir); err != nil {
		return fmt.Errorf("`TokenizerPretrainedCacheDir` does not exist: %v", err)
	} else if !fi.IsDir() {
		return fmt.Errorf("`TokenizerPretrainedCacheDir` is not a directory: %s", config.TokenizerPretrainedCacheDir)
	}

	// TokenizerHFAPI Key: can be empty, no further validation needed

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
	if re, err := regexp.Compile(`^https?://[\w\.\-]+(:\d+)?$`); err == nil {
		if !re.MatchString(config.OllamaBase) {
			return fmt.Errorf("`OllamaBase` is invalid: %s", config.OllamaBase)
		}
	} else {
		return fmt.Errorf("`OllamaBase` regex compilation failed: %v", err)
	}

	// OllamaKeepAlive: duration in format like 30s, 5m, 2h, 1d
	if re, err := regexp.Compile(`^\d+[smhd]$`); err == nil {
		if !re.MatchString(config.OllamaKeepAlive) {
			return fmt.Errorf("`OllamaKeepAlive` is invalid: %s", config.OllamaKeepAlive)
		}
	} else {
		return fmt.Errorf("`OllamaKeepAlive` regex compilation failed: %v", err)
	}

	// OllamaUnloadOnLoVRAM: boolean, no further validation needed

	// EmbeddingModel: only letters, digits, _, -, :, /
	if re, err := regexp.Compile(`^[a-zA-Z0-9:\.\-_]+$`); err == nil {
		if !re.MatchString(config.EmbeddingModel) {
			return fmt.Errorf("`EmbeddingModel` is invalid: %s", config.EmbeddingModel)
		}
	} else {
		return fmt.Errorf("`EmbeddingModel` regex compilation failed: %v", err)
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
	if re, err := regexp.Compile(`^[a-zA-Z0-9:._-]+$`); err == nil {
		if !re.MatchString(config.MainModel) {
			return fmt.Errorf("`MainModel` is invalid: %s", config.MainModel)
		}
	} else {
		return fmt.Errorf("`MainModel` regex compilation failed: %v", err)
	}

	// MainModelWindowSize: positive integer
	if config.MainModelWindowSize <= 0 {
		return fmt.Errorf("`MainModelWindowSize` is invalid: %d", config.MainModelWindowSize)
	}

	// QdrantHost: localhost or IP or hostname
	if re, err := regexp.Compile(`^(localhost|(\d{1,3}\.){3}\d{1,3}|[a-zA-Z0-9\-\.]+)$`); err == nil {
		if !re.MatchString(config.QdrantHost) {
			return fmt.Errorf("`QdrantHost` is invalid: %s", config.QdrantHost)
		}
	} else {
		return fmt.Errorf("`QdrantHost` regex compilation failed: %v", err)
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
	if re, err := regexp.Compile(`^[a-zA-Z0-9_]+$`); err == nil {
		if !re.MatchString(config.QdrantCollection) {
			return fmt.Errorf("`QdrantCollection` is invalid: %s", config.QdrantCollection)
		}
	} else {
		return fmt.Errorf("`QdrantCollection` regex compilation failed: %v", err)
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
		return fmt.Errorf("`MaxTokensNormalization` is invalid: %d", config.MaxTokensNormalization)
	}

	// MinTokensNormalization: positive integer
	if config.MinTokensNormalization <= 0 {
		return fmt.Errorf("`MinTokensNormalization` is invalid: %d", config.MinTokensNormalization)
	}

	// DefaultWeights: non-empty list of 9 non-negative floats
	if len(config.DefaultWeights) != 9 {
		return fmt.Errorf("`DefaultWeights` must have exactly 9 elements, got %d", len(config.DefaultWeights))
	}
	for i, w := range config.DefaultWeights {
		if w < 0.0 {
			return fmt.Errorf("`DefaultWeights[%d]` is invalid: %f", i, w)
		}
	}

	// ReturnVectors: boolean (no validation needed)

	// BM25K1: 1.2–1.8
	// if config.BM25K1 < 1.2 || config.BM25K1 > 1.8 {
	// 	return fmt.Errorf("`BM25K1` is invalid: %f", config.BM25K1)
	// }

	// BM25B: 0.5–0.75
	// if config.BM25B < 0.5 || config.BM25B > 0.75 {
	// 	return fmt.Errorf("`BM25B` is invalid: %f", config.BM25B)
	// }

	// BM25NormMidpoint: 0.0 - 1.0
	// if config.BM25NormMidpoint < 0.0 {
	// 	return fmt.Errorf("`BM25NormMidpoint` is invalid: %f", config.BM25NormMidpoint)
	// }

	// BM25NormSlope: 0.0 - 1.0
	// if config.BM25NormSlope < 0.0 || config.BM25NormSlope > 1.0 {
	// 	return fmt.Errorf("`BM25NormSlope` is invalid: %f", config.BM25NormSlope)
	// }

	// BM25UseLogNorm: boolean (no validation needed)

	// BM25LogNormScale: 0.0 - 1.0 only if BM25UseLogNorm is true
	// if config.BM25UseLogNorm {
	// 	if config.BM25LogNormScale < 0.0 || config.BM25LogNormScale > 1.0 {
	// 		return fmt.Errorf("`BM25LogNormScale` is invalid: %f", config.BM25LogNormScale)
	// 	}
	// }

	// UseBM25IDF: boolean (no validation needed)

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

	// InitialIncomingBufferPreAllocation: non-negative integer
	if config.InitialIncomingBufferPreAllocation < 0 {
		return fmt.Errorf("`InitialIncomingBufferPreAllocation` is invalid: %d", config.InitialIncomingBufferPreAllocation)
	}

	// InitialOutgoingGorutineBufferCount: non-negative integer
	if config.InitialOutgoingGorutineBufferCount < 0 {
		return fmt.Errorf("`InitialOutgoingGorutineBufferCount` is invalid: %d", config.InitialOutgoingGorutineBufferCount)
	}

	// MessageBodyPaths: non-empty array of non-empty strings
	if len(config.MessageBodyPaths) == 0 {
		return fmt.Errorf("`MessageBodyPaths` is empty")
	}
	for i, path := range config.MessageBodyPaths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("`MessageBodyPaths[%d]` is empty", i)
		}
	}

	// SSEPrefixReg: non-empty valid regexp
	if strings.TrimSpace(config.SSEPrefixReg) == "" {
		return fmt.Errorf("`SSEPrefixReg` is empty")
	}
	appCtx.ssePrefixReg, err = regexp.Compile(config.SSEPrefixReg)
	if err != nil {
		return fmt.Errorf("`SSEPrefixReg` is invalid: %v", err)
	}

	// StreamingPacketFlagReg: non-empty valid regexp
	if strings.TrimSpace(config.StreamingPacketFlagReg) == "" {
		return fmt.Errorf("`StreamingPacketFlagReg` is empty")
	}
	appCtx.streamingPacketFlagReg, err = regexp.Compile(config.StreamingPacketFlagReg)
	if err != nil {
		return fmt.Errorf("`StreamingPacketFlagReg` is invalid: %v", err)
	}

	// StreamingPacketStopReg: non-empty valid regexp
	if strings.TrimSpace(config.StreamingPacketStopReg) == "" {
		return fmt.Errorf("`StreamingPacketStopReg` is empty")
	}
	appCtx.streamingPacketStopReg, err = regexp.Compile(config.StreamingPacketStopReg)
	if err != nil {
		return fmt.Errorf("`StreamingPacketStopReg` is invalid: %v", err)
	}

	// DirectPacketFlagReg: non-empty valid regexp
	if strings.TrimSpace(config.DirectPacketFlagReg) == "" {
		return fmt.Errorf("`DirectPacketFlagReg` is empty")
	}
	appCtx.directPacketFlagReg, err = regexp.Compile(config.DirectPacketFlagReg)
	if err != nil {
		return fmt.Errorf("`DirectPacketFlagReg` is invalid: %v", err)
	}

	// MaxTriggerLengthMultiplier: positive integer
	if config.MaxTriggerLengthMultiplier < 1 {
		return fmt.Errorf("`MaxTriggerLengthMultiplier` is invalid: %d", config.MaxTriggerLengthMultiplier)
	}

	// MaxTriggerLengthAdditional: non-negative integer
	if config.MaxTriggerLengthAdditional < 0 {
		return fmt.Errorf("`MaxTriggerLengthAdditional` is invalid: %d", config.MaxTriggerLengthAdditional)
	}

	// ResponseReplacer: map[string]map[string]string
	if err := initResponseReplaceRules(); err != nil {
		return err
	}

	// SystemMessageFile: path to IDF DB file (non-empty)
	if strings.TrimSpace(config.SystemMessageFile) == "" {
		return fmt.Errorf("`SystemMessageFile` path is invalid: %s", config.SystemMessageFile)
	}
	// RegEx check for correct path and touch file after this validation
	if _, err := os.Stat(config.SystemMessageFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("`SystemMessageFile` path is invalid or inaccessible: %v", err)
	}

	// SystemMessagePath struct
	if err := validateSystemMessagePatch(&config.SystemMessagePatch); err != nil {
		return err
	}

	return nil
}
