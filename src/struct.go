package main

import (
	"log"

	"github.com/pkoukk/tiktoken-go"
	"github.com/qdrant/go-client/qdrant"
)

// Config struct for TOML configuration
type Config struct {
	Listen                      string   `toml:"Listen"`
	TokenBufferReserve          int64    `toml:"TokenBufferReserve"`
	UserMessageTags             []string `toml:"UserMessageTags"`
	UserMessageAttachmentTags   []string `toml:"UserMessageAttachmentTags"`
	Temperature                 float64  `toml:"Temperature"`
	OllamaBase                  string   `toml:"OllamaBase"`
	OllamaKeepAlive             string   `toml:"OllamaKeepAlive"`
	OllamaUnloadBeforeEmbedding bool     `toml:"OllamaUnloadBeforeEmbedding"`
	EmbeddingModel              string   `toml:"EmbeddingModel"`
	EmbeddingsEndpoint          string   `toml:"EmbeddingsEndpoint"`
	EmbeddingsModeWindowSize    int64    `toml:"EmbeddingsModeWindowSize"`
	MainModel                   string   `toml:"MainModel"`
	MainModelWindowSize         int64    `toml:"MainModelWindowSize"`
	QdrantHost                  string   `toml:"QdrantHost"`
	QdrantPort                  int      `toml:"QdrantPort"`
	QdrantKeepAlive             int      `toml:"QdrantKeepAlive"`
	QdrantCollection            string   `toml:"QdrantCollection"`
	QdrantMetric                string   `toml:"QdrantMetric"`
	QdrantVectorSize            int      `toml:"QdrantVectorSize"`
	SearchSource                []string `toml:"SearchSource"`
	SearchMaxAgeDays            int64    `toml:"SearchMaxAgeDays"`
	SearchTopK                  int64    `toml:"SearchTopK"`
	FeedAugmentationPercent     int64    `toml:"FeedAugmentationPercent"`
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
	Timestamp  float64  `json:"Timestamp"`
	Role       string   `json:"Role"`
	Body       string   `json:"Body"`
	TokenCount int64    `json:"TokenCount"`
	Hash       string   `json:"Hash"`
	FileMeta   FileMeta `json:"FileMeta"`
}

// Attachment represents a file attachment
type Attachment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type AttachmentReplacement struct {
	Attachment Attachment
	OldPointID string
}
