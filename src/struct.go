package main

import (
	"log"
	"regexp"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/pkoukk/tiktoken-go"
	"github.com/qdrant/go-client/qdrant"
)

// Config struct for TOML configuration
type Config struct {
	Listen                         string             `toml:"Listen"`
	IDFFile                        string             `toml:"IDFFile"`
	TokenBufferReserve             int64              `toml:"TokenBufferReserve"`
	UserMessageTags                []string           `toml:"UserMessageTags"`
	UserMessageAskAttachmentTags   []string           `toml:"UserMessageAskAttachmentTags"`
	UserMessageAgentAttachmentTags []string           `toml:"UserMessageAgentAttachmentTags"`
	Temperature                    float64            `toml:"Temperature"`
	OllamaBase                     string             `toml:"OllamaBase"`
	OllamaKeepAlive                string             `toml:"OllamaKeepAlive"`
	OllamaUnloadOnLoVRAM           bool               `toml:"OllamaUnloadOnLoVRAM"`
	EmbeddingModel                 string             `toml:"EmbeddingModel"`
	EmbeddingsEndpoint             string             `toml:"EmbeddingsEndpoint"`
	EmbeddingsModeWindowSize       int64              `toml:"EmbeddingsModeWindowSize"`
	MainModel                      string             `toml:"MainModel"`
	MainModelWindowSize            int64              `toml:"MainModelWindowSize"`
	QdrantHost                     string             `toml:"QdrantHost"`
	QdrantPort                     int                `toml:"QdrantPort"`
	QdrantKeepAlive                int                `toml:"QdrantKeepAlive"`
	QdrantCollection               string             `toml:"QdrantCollection"`
	QdrantMetric                   string             `toml:"QdrantMetric"`
	QdrantVectorSize               int                `toml:"QdrantVectorSize"`
	MaxFileSize                    int                `toml:"MaxFileSize"`
	FilePatterns                   []string           `toml:"FilePatterns"`
	FilePatternsReg                []*regexp.Regexp   `toml:"-"`
	SearchSource                   []string           `toml:"SearchSource"`
	SearchMaxAgeDays               int64              `toml:"SearchMaxAgeDays"`
	SearchTopK                     int64              `toml:"SearchTopK"`
	CosineMinScore                 float32            `toml:"CosineMinScore"`
	EuclidMaxDistance              float32            `toml:"EuclidMaxDistance"`
	RerankTopN                     int                `toml:"RerankTopN"`
	MinRankScore                   float64            `toml:"MinRankScore"`
	MaxQueryTokens                 int                `toml:"MaxQueryTokens"`
	TokensCacheTTL                 Duration           `toml:"TokensCacheTTL"`
	TokensCacheSize                int                `toml:"TokensCacheSize"`
	TauDays                        float64            `toml:"TauDays"`
	MaxTokensNormalization         int                `toml:"MaxTokensNormalization"`
	MinTokensNormalization         int                `toml:"MinTokensNormalization"`
	DefaultWeights                 []float64          `toml:"DefaultWeights"`
	ReturnVectors                  bool               `toml:"ReturnVectors"`
	BM25K1                         float64            `toml:"BM25K1"`
	BM25B                          float64            `toml:"BM25B"`
	RoleWeights                    map[string]float64 `toml:"RoleWeights"`
	FeedAugmentationPercent        int64              `toml:"FeedAugmentationPercent"`
	VerboseDiskLogs                bool               `toml:"VerboseDiskLogs"`
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
	Config         Config
	DB             *qdrant.Client
	Tokenizer      *tiktoken.Tiktoken
	JournaldLogger *log.Logger
	AccessLogger   *log.Logger
	ErrorLogger    *log.Logger
	DebugLogger    *log.Logger
	TokenCache     *lru.Cache
	IDFStore       IDFStore
	idfMu          sync.RWMutex
}

// IDFStore structure for IDF data
type IDFStore struct {
	DF          map[int]int     // document frequency counters
	N           int             // total number of documents
	IDF         map[int]float64 // cached weights
	NgramDF     map[string]int
	NgramIDF    map[string]float64
	TotalTokens int64
}

// Qdrant FileMeta structure
type FileMeta struct {
	ID   string `json:"ID"`
	Path string `json:"Path"`
}

// cachedEntry structure for token caching
type cachedEntry struct {
	IDs     []int
	Strs    []string
	created time.Time
}

// Qdrant Payload structure
type Payload struct {
	PacketID   string   `json:"PacketID"`
	Timestamp  float64  `json:"Timestamp"`
	Role       string   `json:"Role"`
	Body       string   `json:"Body"`
	TokenCount int64    `json:"TokenCount"`
	Hash       string   `json:"Hash"`
	FileMeta   FileMeta `json:"FileMeta"`
}

// Features structure for candidate scoring
type Features struct {
	// Light features (fill in first step)
	EmbSim         float64 // [0,1]
	Recency        float64 // [0,1]
	RoleScore      float64 // [0,1]
	BodyLen        float64 // [0,1]
	PayloadQuality float64 // [0,1]
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
	Attachment    Attachment
	OldPointID    string
	OldHash       string
	OldTokenCount int64
}
