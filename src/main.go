// main.go
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/pkoukk/tiktoken-go"
)

var appCtx AppContext

// initApp initializes the application: checks user, sets up logging, reads config, connects to Qdrant
func initApp(configPath string) error {

	var err error
	// Initialize global app context
	appCtx = AppContext{
		Config:              Config{},
		DB:                  nil, // Will be used with withDB per call
		Tokenizer:           nil,
		JournaldLogger:      nil,
		AccessLogger:        nil,
		ErrorLogger:         nil,
		DebugLogger:         nil,
		IDFChanged:          false,
		idfAutoSaveStopChan: make(chan struct{}),
		idfAutoSaveWG:       sync.WaitGroup{},
	}

	// Check if the 'ragproxy' user exists
	err = checkRagproxyUser()
	if err != nil {
		return err
	}

	// Set up logging
	appCtx.JournaldLogger, appCtx.AccessLogger, appCtx.ErrorLogger, appCtx.DebugLogger = setupLogging()

	// Read and parse config file
	var configData []byte
	configData, err = os.ReadFile(configPath)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error reading config file: %v", err)
		appCtx.JournaldLogger.Printf("Error reading config file: %v", err)
		return err
	}
	appCtx.JournaldLogger.Printf("Config file %s loaded successfully", configPath)

	err = toml.Unmarshal(configData, &appCtx.Config)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error parsing config file: %v", err)
		appCtx.JournaldLogger.Printf("Error parsing config file: %v", err)
		return err
	}
	// appCtx.DebugLogger.Printf("Config parsed: %+v", appCtx.Config)

	appCtx.JournaldLogger.Printf("Config file %s parsed successfully", configPath)

	appCtx.Tokenizer, err = tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		appCtx.ErrorLogger.Printf("Error initializing tiktoken encoder: %v", err)
		appCtx.JournaldLogger.Printf("Error initializing tiktoken encoder: %v", err)
		return err
	}
	appCtx.JournaldLogger.Printf("Tokenizer initialized successfully")

	initConsts()
	appCtx.JournaldLogger.Printf("Application constants initialized: %+v", appConsts)

	err = validateConfig(appCtx.Config)
	if err != nil {
		appCtx.ErrorLogger.Printf("Invalid config: %v", err)
		appCtx.JournaldLogger.Printf("Invalid config: %v", err)
		return err
	}
	appCtx.JournaldLogger.Printf("Configuration validated successfully")

	err = initTokenCache()
	if err != nil {
		appCtx.ErrorLogger.Printf("Error initializing token cache: %v", err)
		appCtx.JournaldLogger.Printf("Error initializing token cache: %v", err)
		return err
	}
	appCtx.JournaldLogger.Printf("Token cache initialized successfully. Capacity: %d", appCtx.Config.TokensCacheSize)

	// Application initialization log
	appCtx.JournaldLogger.Printf("Application context initialized")

	// Initialize database with fresh connection
	err = withDB(func() error {
		return initDB()
	})
	if err != nil {
		appCtx.ErrorLogger.Printf("Error initializing database: %v", err)
		appCtx.JournaldLogger.Printf("Error initializing database: %v", err)
		return err
	}

	// Check embedding normalization
	if err := CheckEmbeddingNormalization(); err != nil {
		appCtx.ErrorLogger.Printf("Embedding normalization check failed: %v", err)
		appCtx.JournaldLogger.Printf("Embedding normalization check failed: %v", err)
		return err
	}

	// Load IDF store from file
	err = loadIDF()
	if err != nil {
		appCtx.ErrorLogger.Printf("Error loading IDF store: %v", err)
		appCtx.JournaldLogger.Printf("Error loading IDF store: %v", err)
		return err
	}
	appCtx.JournaldLogger.Printf("IDF store loaded successfully")

	// Start IDF autosave goroutine if interval > 0
	if d := appCtx.Config.AutoSaveIDFInterval.Duration; d > 0 {
		startIDFAutoSave(d)
	}

	// Application fully initialized
	appCtx.JournaldLogger.Printf("Application initialized successfully")
	return nil
}

// runApp runs the main application logic: starts the proxy server
func runApp() error {
	// Log program startup in journald (stdout)
	appCtx.JournaldLogger.Printf("Starting ragproxy on %s, forwarding requests to %s", appCtx.Config.Listen, appCtx.Config.OllamaBase)

	// Parse the Ollama server URL
	ollamaURL, err := url.Parse(appCtx.Config.OllamaBase)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error parsing Ollama URL: %v", err)
		appCtx.JournaldLogger.Printf("Error parsing Ollama URL: %v", err)
		return err
	}

	// Create outbound to Ollama
	outbound := httputil.NewSingleHostReverseProxy(ollamaURL)

	// Handle incoming requests
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var requestBody string
		var cleanUserContent string
		var attachments []Attachment
		var promptVector []float32
		var queryHash string
		// Read and log request body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			if appCtx.Config.VerboseDiskLogs {
				appCtx.ErrorLogger.Printf("Error reading request body: %v", err)
			}
		} else {
			requestBody = string(bodyBytes)
			requestBody, cleanUserContent, attachments, promptVector, queryHash = processInbound(requestBody)
			r.Body = io.NopCloser(bytes.NewReader([]byte(requestBody))) // Restore body
			r.ContentLength = int64(len(requestBody))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Content-Length", fmt.Sprintf("%d", len(requestBody)))
		}

		// Log incoming request
		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Received request: %s %s\nBody: %s", r.Method, r.URL, requestBody)
		} else {
			appCtx.AccessLogger.Printf("Received request: %s %s", r.Method, r.URL)
		}

		// Using StreamCollectorWriter to capture streaming response
		collector := &StreamCollectorWriter{ResponseWriter: w}

		// Log full request if verbose
		if appCtx.Config.VerboseDiskLogs {
			dump, _ := httputil.DumpRequest(r, true)
			appCtx.AccessLogger.Printf("Full HTTP request to Ollama:\n%s", dump)
		}

		// Proxy the request to Ollama
		outbound.ServeHTTP(collector, r)

		// After the stream is complete, collect and process the response for the database
		collector.CloseAndProcess(cleanUserContent, attachments, promptVector, queryHash)

	})

	// Create inbound
	inbound := &http.Server{
		Addr: appCtx.Config.Listen,
	}

	// Channel to listen for interrupt signal
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	// Start inbound in a goroutine
	go func() {
		appCtx.JournaldLogger.Printf("Inbound is listening on %s", appCtx.Config.Listen)
		if err := inbound.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			appCtx.ErrorLogger.Printf("Error starting inbound: %v", err)
			appCtx.JournaldLogger.Printf("Error starting inbound: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-done
	appCtx.JournaldLogger.Printf("Shutting down inbound...")

	// Graceful shutdown of inbound
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := inbound.Shutdown(ctx); err != nil {
		appCtx.ErrorLogger.Printf("Inbound forced to shutdown: %v", err)
		appCtx.JournaldLogger.Printf("Inbound forced to shutdown: %v", err)
	}

	appCtx.JournaldLogger.Printf("Inbound exited")
	return nil
}

// shutdownApp handles application shutdown: closes connections, logs
func shutdownApp(dontSaveIDF bool) {
	// Close database connection if open
	if appCtx.DB != nil {
		err := appCtx.DB.Close()
		if err != nil {
			appCtx.ErrorLogger.Printf("Error closing database connection: %v", err)
		} else {
			appCtx.JournaldLogger.Printf("Database connection closed successfully")
		}
	}

	if !dontSaveIDF {
		// Store IDF store to file
		err := saveIDF(true)
		if err != nil {
			appCtx.ErrorLogger.Printf("Error storing IDF store: %v", err)
			appCtx.JournaldLogger.Printf("Error storing IDF store: %v", err)
		} else {
			appCtx.JournaldLogger.Printf("IDF store saved successfully")
		}
		// Stop IDF autosave goroutine
		close(appCtx.idfAutoSaveStopChan)
		appCtx.idfAutoSaveWG.Wait()
	}
	// Log shutdown completion
	appCtx.JournaldLogger.Printf("Ragproxy stopped")
}

func main() {
	// Command-line flags
	configPath := flag.String("config", "", "Path to config file")
	test := flag.Bool("test", false, "Debug: run tests and exit")
	flushDB := flag.Bool("flush-db", false, "Flush the Qdrant database and exit")
	qhost := flag.String("qhost", "", "Qdrant host for flush-db")
	qport := flag.Int("qport", 0, "Qdrant port for flush-db")
	qcollection := flag.String("qcollection", "", "Qdrant collection for flush-db")
	flag.Parse()

	// Handle flush-db flag
	if *flushDB {
		if *qhost == "" || *qport == 0 || *qcollection == "" {
			fmt.Printf("Error: --flush-db requires --qhost, --qport, and --qcollection flags\n")
			os.Exit(1)
		}
		err := flushDatabase(*qhost, *qport, *qcollection)
		if err != nil {
			fmt.Printf("Error flushing database: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Database '%s' flushed successfully.\nYou can now start the service: sudo systemctl restart ragproxy\n", *qcollection)
		os.Exit(0)
	}

	// Check if config path is provided
	if *configPath == "" {
		fmt.Printf("Error: --config flag is required\n")
		os.Exit(1)
	}

	// Initialize application
	err := initApp(*configPath)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	dontSaveIDF := false
	if !*test {
		// Run application
		err = runApp()
	} else {
		dontSaveIDF = true
		// err = testFunc()
		// if err != nil {
		// 	fmt.Printf("Tests failed: %v\n", err)
		// 	os.Exit(1)
		// } else {
		// 	fmt.Printf("All tests passed successfully.\n")
		// }
	}

	// Always shutdown even if runApp returned error
	shutdownApp(dontSaveIDF)
	if err != nil {
		os.Exit(1)
	}
}
