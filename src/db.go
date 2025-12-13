package main

import (
	"context"
	"fmt"
	"os"

	"github.com/qdrant/go-client/qdrant"
)

// initDB initializes the Qdrant database: creates collection "entries" if not exists
func initDB() error {
	collectionName := appCtx.Config.QdrantCollection

	// Map metric string to qdrant.Distance
	var distance qdrant.Distance
	switch appCtx.Config.QdrantMetric {
	case "Cosine":
		distance = qdrant.Distance_Cosine
	case "Euclid":
		distance = qdrant.Distance_Euclid
	case "Dot":
		distance = qdrant.Distance_Dot
	default:
		return fmt.Errorf("unsupported metric '%s'; supported: Cosine, Euclid, Dot", appCtx.Config.QdrantMetric)
	}

	// Check if collection exists
	exists, err := appCtx.DB.CollectionExists(context.Background(), collectionName)
	if err != nil {
		return fmt.Errorf("error checking collection existence: %w", err)
	}

	if exists {
		// Check collection structure
		info, err := appCtx.DB.GetCollectionInfo(context.Background(), collectionName)
		if err != nil {
			return fmt.Errorf("error getting collection info: %w", err)
		}

		// Check VectorsConfig
		vectorsConfig := info.GetConfig().GetParams().GetVectorsConfig()
		if vectorsConfig == nil {
			return fmt.Errorf("collection '%s' has no vectors config", collectionName)
		}

		// Get params directly
		params := vectorsConfig.GetParams()
		if params == nil {
			return fmt.Errorf("collection '%s' has no vector params", collectionName)
		}

		if params.Size != uint64(appCtx.Config.QdrantVectorSize) || params.Distance != distance {
			appCtx.ErrorLogger.Printf("collection '%s' config mismatch: expected size=%d, distance=%s; got size=%d, distance=%v. Run: ragproxy --flush-db --qhost %s --qport %d --qcollection %s to !!!FLASH ALL DATA IN CURRENT COLLECTION!!! after that restart service to initialize new DB with correct metrics and vector size defined in current config, or change metric and size in config to recongnize current collection", collectionName, appCtx.Config.QdrantVectorSize, appCtx.Config.QdrantMetric, params.Size, params.Distance, appCtx.Config.QdrantHost, appCtx.Config.QdrantPort, appCtx.Config.QdrantCollection)
			os.Exit(1)
		}

		appCtx.JournaldLogger.Printf("Using existing collection '%s' with %d-dim vectors, %s distance", collectionName, appCtx.Config.QdrantVectorSize, appCtx.Config.QdrantMetric)
		return nil
	}

	// Create collection
	err = appCtx.DB.CreateCollection(context.Background(), &qdrant.CreateCollection{
		CollectionName: collectionName,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(appCtx.Config.QdrantVectorSize),
			Distance: distance,
		}),
	})
	if err != nil {
		return fmt.Errorf("error creating collection '%s': %w", collectionName, err)
	}

	appCtx.JournaldLogger.Printf("Created collection '%s' with %d-dim vectors, %s distance", collectionName, appCtx.Config.QdrantVectorSize, appCtx.Config.QdrantMetric)
	return nil
}

// flushDatabase connects to Qdrant and deletes the collection
func flushDatabase(host string, port int, collection string) error {
	db, err := qdrant.NewClient(&qdrant.Config{
		Host: host,
		Port: port,
	})
	if err != nil {
		return fmt.Errorf("error connecting to Qdrant: %w", err)
	}

	err = db.DeleteCollection(context.Background(), collection)
	if err != nil {
		return fmt.Errorf("error deleting collection '%s': %w", collection, err)
	}

	return nil
}

// withDB creates a fresh Qdrant client, sets it in appCtx.DB, calls fn, then closes the client
func withDB(fn func() error) error {
	db, err := qdrant.NewClient(&qdrant.Config{
		Host:          appCtx.Config.QdrantHost,
		Port:          appCtx.Config.QdrantPort,
		KeepAliveTime: appCtx.Config.QdrantKeepAlive,
	})
	if err != nil {
		return fmt.Errorf("error connecting to Qdrant: %w", err)
	}
	appCtx.DB = db
	defer func() {
		appCtx.DB.Close()
		appCtx.DB = nil
	}()
	return fn()
}
