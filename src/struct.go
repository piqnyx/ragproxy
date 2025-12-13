package main

import (
	"log"

	"github.com/pkoukk/tiktoken-go"
	"github.com/qdrant/go-client/qdrant"
)

// Config struct for TOML configuration
type Config struct {
	Listen                      string   `toml:"Listen"`
	TokenBufferReserve          int      `toml:"TokenBufferReserve"`
	UserMessageTags             []string `toml:"UserMessageTags"`
	UserMessageAttachmentTags   []string `toml:"UserMessageAttachmentTags"`
	OllamaBase                  string   `toml:"OllamaBase"`
	OllamaKeepAlive             string   `toml:"OllamaKeepAlive"`
	OllamaUnloadBeforeEmbedding bool     `toml:"OllamaUnloadBeforeEmbedding"`
	EmbeddingModel              string   `toml:"EmbeddingModel"`
	EmbeddingsEndpoint          string   `toml:"EmbeddingsEndpoint"`
	EmbeddingsModeWindowSize    int      `toml:"EmbeddingsModeWindowSize"`
	MainModel                   string   `toml:"MainModel"`
	MainModelWindowSize         int      `toml:"MainModelWindowSize"`
	QdrantHost                  string   `toml:"QdrantHost"`
	QdrantPort                  int      `toml:"QdrantPort"`
	QdrantKeepAlive             int      `toml:"QdrantKeepAlive"`
	QdrantCollection            string   `toml:"QdrantCollection"`
	QdrantMetric                string   `toml:"QdrantMetric"`
	QdrantVectorSize            int      `toml:"QdrantVectorSize"`
	SearchSource                []string `toml:"SearchSource"`
	SearchMaxAgeDays            int      `toml:"SearchMaxAgeDays"`
	SearchTopK                  int      `toml:"SearchTopK"`
	FeedAugmentationPercent     int      `toml:"FeedAugmentationPercent"`
	VerboseDiskLogs             bool     `toml:"VerboseDiskLogs"`
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
}

// Qdrant FileMeta structure
type FileMeta struct {
	ID   string `json:"ID"`
	Path string `json:"Path"`
}

// Qdrant Payload structure
type Payload struct {
	PacketID   string   `json:"PacketID"`
	Timestamp  int64    `json:"Timestamp"`
	Role       string   `json:"Role"`
	Body       string   `json:"Body"`
	TokenCount int      `json:"TokenCount"`
	Hash       string   `json:"Hash"`
	FileMeta   FileMeta `json:"FileMeta"`
}
