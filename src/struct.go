// struct.go
package main

/*
#cgo LDFLAGS: -L/home/piqnyx/.local/bin/ragproxy/lib -ltokenizers
//#cgo CFLAGS: -I/home/piqnyx/.local/bin/ragproxy/include
#include "/home/piqnyx/.local/bin/ragproxy/include/tokenizers.h"
*/
import "C"

import (
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/gammazero/deque"
	lru "github.com/hashicorp/golang-lru"

	// "github.com/pkoukk/tiktoken-go"
	"github.com/daulet/tokenizers"
	"github.com/qdrant/go-client/qdrant"
)

// Config struct for TOML configuration
type Config struct {
	Listen                             string                       `toml:"Listen"`
	IDFFile                            string                       `toml:"IDFFile"`
	AutoSaveIDFInterval                Duration                     `toml:"AutoSaveIDFInterval"`
	TokenizerPretrainedCacheDir        string                       `toml:"TokenizerPretrainedCacheDir"`
	TokenizerHFModelName               string                       `toml:"TokenizerHFModelName"`
	TokenizerHFAPI                     string                       `toml:"TokenizerHFAPI"`
	UserMessageTags                    []string                     `toml:"UserMessageTags"`
	UserMessageAskAttachmentTags       []string                     `toml:"UserMessageAskAttachmentTags"`
	UserMessageAgentAttachmentTags     []string                     `toml:"UserMessageAgentAttachmentTags"`
	Temperature                        float64                      `toml:"Temperature"`
	OllamaBase                         string                       `toml:"OllamaBase"`
	OllamaKeepAlive                    string                       `toml:"OllamaKeepAlive"`
	OllamaUnloadOnLoVRAM               bool                         `toml:"OllamaUnloadOnLoVRAM"`
	EmbeddingModel                     string                       `toml:"EmbeddingModel"`
	EmbeddingsEndpoint                 string                       `toml:"EmbeddingsEndpoint"`
	EmbeddingsModeWindowSize           int64                        `toml:"EmbeddingsModeWindowSize"`
	MainModel                          string                       `toml:"MainModel"`
	MainModelWindowSize                int                          `toml:"MainModelWindowSize"`
	QdrantHost                         string                       `toml:"QdrantHost"`
	QdrantPort                         int                          `toml:"QdrantPort"`
	QdrantKeepAlive                    int                          `toml:"QdrantKeepAlive"`
	QdrantCollection                   string                       `toml:"QdrantCollection"`
	QdrantMetric                       string                       `toml:"QdrantMetric"`
	QdrantVectorSize                   int                          `toml:"QdrantVectorSize"`
	MaxFileSize                        int                          `toml:"MaxFileSize"`
	FilePatterns                       []string                     `toml:"FilePatterns"`
	FilePatternsReg                    []*regexp.Regexp             `toml:"-"`
	SearchSource                       []string                     `toml:"SearchSource"`
	SearchMaxAgeDays                   int64                        `toml:"SearchMaxAgeDays"`
	SearchTopK                         int64                        `toml:"SearchTopK"`
	CosineMinScore                     float32                      `toml:"CosineMinScore"`
	EuclidMaxDistance                  float32                      `toml:"EuclidMaxDistance"`
	RerankTopN                         int                          `toml:"RerankTopN"`
	MinRankScore                       float64                      `toml:"MinRankScore"`
	MaxQueryTokens                     int                          `toml:"MaxQueryTokens"`
	TokensCacheTTL                     Duration                     `toml:"TokensCacheTTL"`
	TokensCacheSize                    int                          `toml:"TokensCacheSize"`
	TauDays                            float64                      `toml:"TauDays"`
	MaxTokensNormalization             int                          `toml:"MaxTokensNormalization"`
	MinTokensNormalization             int                          `toml:"MinTokensNormalization"`
	DefaultWeights                     []float64                    `toml:"DefaultWeights"`
	ReturnVectors                      bool                         `toml:"ReturnVectors"`
	BM25K1                             float64                      `toml:"BM25K1"`
	BM25B                              float64                      `toml:"BM25B"`
	BM25NormMidpoint                   float64                      `toml:"BM25NormMidpoint"`
	BM25NormSlope                      float64                      `toml:"BM25NormSlope"`
	BM25UseLogNorm                     bool                         `toml:"BM25UseLogNorm"`
	BM25LogNormScale                   float64                      `toml:"BM25LogNormScale"`
	UseBM25IDF                         bool                         `toml:"UseBM25IDF"`
	RoleWeights                        map[string]float64           `toml:"RoleWeights"`
	FeedAugmentationPercent            int                          `toml:"FeedAugmentationPercent"`
	VerboseDiskLogs                    bool                         `toml:"VerboseDiskLogs"`
	DumpPackets                        bool                         `toml:"DumpPackets"`
	InitialIncomingBufferPreAllocation int                          `toml:"InitialIncomingBufferPreAllocation"`
	InitialOutgoingGorutineBufferCount int                          `toml:"InitialOutgoingGorutineBufferCount"`
	MessageBodyPaths                   []string                     `toml:"MessageBodyPaths"`
	SSEPrefixReg                       string                       `toml:"SSEPrefixReg"`
	StreamingPacketFlagReg             string                       `toml:"StreamingPacketFlagReg"`
	StreamingPacketStopReg             string                       `toml:"StreamingPacketStopReg"`
	DirectPacketFlagReg                string                       `toml:"DirectPacketFlagReg"`
	MaxTriggerLengthMultiplier         int                          `toml:"MaxTriggerLengthMultiplier"`
	MaxTriggerLengthAdditional         int                          `toml:"MaxTriggerLengthAdditional"`
	ResponseReplacer                   map[string]map[string]string `toml:"ResponseReplacer"`
	SystemMessageFile                  string                       `toml:"SystemMessageFile"`
	SystemMessagePatch                 SystemMessagePatchConfig     `toml:"SystemMessagePatch"`
}

type ResponsePacket struct {
	PacketType  int
	IsSSE       bool
	Prefix      string
	MessagePath string
	RawData     string
}

type ResponseCollector struct {
	http.ResponseWriter
	mu                sync.Mutex
	incomingPackets   []ResponsePacket
	outgoingPackets   *deque.Deque[ResponsePacket]
	globalTextBuffer  string
	currentTextBuffer string
	complete          bool
	collecting        bool
	wasMessages       bool

	templateStreamPacket ResponsePacket
	templateFinishPacket ResponsePacket

	outgoingCh chan ResponsePacket
	notifyCh   chan struct{}
	stopCh     chan struct{}
	doneCh     chan struct{}

	stopOnce sync.Once
}

type PatchRule struct {
	Find   string `toml:"find"`
	Insert string `toml:"insert"`
}

type ResponseMsgReplaceRule struct {
	Find    *regexp.Regexp
	Replace string
}

type ResponseReplaceRecord struct {
	Trigger string
	Rules   []ResponseMsgReplaceRule
}

type SystemMessagePatchConfig struct {
	Replace    map[string]string `toml:"Replace"`
	AddToBegin []string          `toml:"AddToBegin"`
	AddToEnd   []string          `toml:"AddToEnd"`
	AddAfter   []PatchRule       `toml:"AddAfter"`
	Remove     []string          `toml:"Remove"`
}

// Duration is a wrapper around time.Duration to support custom unmarshaling
type Duration struct {
	time.Duration
}

// UnmarshalText parses a duration string into a Duration type
func (d *Duration) UnmarshalText(text []byte) error {
	dur, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

// AppContext holds global application state
type AppContext struct {
	Config                       Config
	DB                           *qdrant.Client
	Tokenizer                    *tokenizers.Tokenizer // *tiktoken.Tiktoken
	JournaldLogger               *log.Logger
	AccessLogger                 *log.Logger
	ErrorLogger                  *log.Logger
	DebugLogger                  *log.Logger
	DumpLogger                   *log.Logger
	TokenCache                   *TokenCacheWrapper
	IDFStore                     IDFStore
	idfMu                        sync.RWMutex
	IDFChanged                   bool
	idfAutoSaveStopChan          chan struct{}
	idfAutoSaveWG                sync.WaitGroup
	responseReplaceRules         []ResponseReplaceRecord
	responseReplaceMaxTriggerLen int
	ssePrefixReg                 *regexp.Regexp
	streamingPacketFlagReg       *regexp.Regexp
	streamingPacketStopReg       *regexp.Regexp
	directPacketFlagReg          *regexp.Regexp
}

// IDFStore structure for IDF data
type IDFStore struct {
	DF          map[uint32]int     // document frequency counters
	N           uint64             // total number of documents
	IDF         map[uint32]float64 // cached weights
	NgramDF     map[uint64]int
	NgramIDF    map[uint64]float64
	TotalTokens int64
}

// Qdrant FileMeta structure
type FileMeta struct {
	ID   string `json:"ID"`
	Path string `json:"Path"`
}

type TokenCacheWrapper struct {
	mu sync.RWMutex
	c  *lru.Cache
}

// cachedEntry structure for token caching
type cachedEntry struct {
	IDs     []uint32
	Strs    []string
	created time.Time
}

// Qdrant Payload structure
type Payload struct {
	PacketID        string   `json:"PacketID"`
	Timestamp       float64  `json:"Timestamp"`
	Role            string   `json:"Role"`
	Body            string   `json:"Body"`
	TokenCount      int      `json:"TokenCount"`
	CleanTokenCount int      `json:"CleanTokenCount"`
	Hash            string   `json:"Hash"`
	FileMeta        FileMeta `json:"FileMeta"`
}

// Features structure for candidate scoring
type Features struct {
	// Light features (fill in first step)
	EmbSim    float64 // [0,1]
	Recency   float64 // [0,1]
	RoleScore float64 // [0,1]
	BodyLen   float64 // [0,1]
	// Heavy features (fill in second step)
	KeywordOverlap  float64 // [0,1]
	WeightedOverlap float64 // [0,1]
	BM25            float64 // [0,1]
	NgramOverlap    float64 // [0,1]
	WeightedNgram   float64 // [0,1]
}

// First Step Candidate structure
type Candidate struct {
	Payload         Payload
	EmbeddingVector []float64
	Features        Features
	Score           float64
}

// Attachment represents a file attachment
type Attachment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type AttachmentReplacement struct {
	Attachment         Attachment
	OldPointID         string
	OldHash            string
	OldTokenCount      int
	OldCleanTokenCount int
}
