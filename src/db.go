package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

// initDB initializes the Qdrant database: creates collection if not exists
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
			appCtx.JournaldLogger.Printf("collection '%s' config mismatch: expected size=%d, distance=%s; got size=%d, distance=%v. Run: ragproxy --flush-db --qhost %s --qport %d --qcollection %s to !!!FLASH ALL DATA IN CURRENT COLLECTION!!! after that restart service to initialize new DB with correct metrics and vector size defined in current config, or change metric and size in config to recongnize current collection", collectionName, appCtx.Config.QdrantVectorSize, appCtx.Config.QdrantMetric, params.Size, params.Distance, appCtx.Config.QdrantHost, appCtx.Config.QdrantPort, appCtx.Config.QdrantCollection)
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

	// Create index on "hash" field for faster lookups
	yeah_wait := true
	var indexRes *qdrant.UpdateResult
	indexRes, err = appCtx.DB.CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
		CollectionName: appCtx.Config.QdrantCollection,
		Wait:           &yeah_wait,
		FieldName:      "hash",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	})
	if err != nil {
		appCtx.ErrorLogger.Printf("Error creating index on 'hash' field: %v", err)
		return fmt.Errorf("error creating index: %w", err)
	}

	if indexRes.GetStatus() == qdrant.UpdateStatus_Completed {
		appCtx.JournaldLogger.Printf("Index on 'hash' field created successfully")
	} else {
		appCtx.JournaldLogger.Printf("Index creation on 'hash' field returned status: %s", indexRes.GetStatus())
		return fmt.Errorf("index creation failed, status: %s", indexRes.GetStatus())
	}

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

// SearchRelevantContent ищет релевантные записи по вектору и фильтрам из конфига
func SearchRelevantContent(queryVector []float32) ([]Payload, error) {
	var results []Payload

	err := withDB(func() error {
		// Retrieve filter parameters from config
		roles := appCtx.Config.SearchSource
		maxAgeDays := appCtx.Config.SearchMaxAgeDays
		topKCfg := appCtx.Config.SearchTopK

		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Searching relevant content with roles: %v, maxAgeDays: %d, topK: %d, queryVector: %v", roles, maxAgeDays, topKCfg, queryVector)
		} else {
			appCtx.AccessLogger.Printf("Searching relevant content with roles: %v, maxAgeDays: %d, topK: %d", roles, maxAgeDays, topKCfg)
		}

		// Create filter conditions
		var conditions []*qdrant.Condition

		// Filter by roles (role must be one of the specified roles)
		conditions = append(conditions, &qdrant.Condition{
			ConditionOneOf: &qdrant.Condition_Field{
				Field: &qdrant.FieldCondition{
					Key: "role",
					Match: &qdrant.Match{
						MatchValue: &qdrant.Match_Keywords{
							Keywords: &qdrant.RepeatedStrings{Strings: roles},
						},
					},
				},
			},
		})

		// Filter by time (if maxAgeDays > 0)
		if maxAgeDays > 0 {
			minTs := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour).UnixNano()
			minTsFloat := float64(minTs)
			conditions = append(conditions, &qdrant.Condition{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "timestamp",
						Range: &qdrant.Range{
							Gte: &minTsFloat,
						},
					},
				},
			})
		}
		// Build the final filter
		filter := &qdrant.Filter{
			Must: conditions,
		}

		var topK *uint64
		if topKCfg > 0 {
			v := uint64(topKCfg)
			topK = &v
		}

		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf(
				"Searching relevant content | roles=%v maxAgeDays=%d topK=%v len(queryVector)=%d",
				roles, maxAgeDays, func() any {
					if topK == nil {
						return "nil"
					}
					return *topK
				}(), len(queryVector),
			)
		} else {
			appCtx.AccessLogger.Printf("Searching relevant content | roles=%v maxAgeDays=%d topK=%v",
				roles, maxAgeDays, func() any {
					if topK == nil {
						return "nil"
					}
					return *topK
				}())
		}

		// Search in Qdrant
		searchResult, err := appCtx.DB.Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: appCtx.Config.QdrantCollection,
			Query:          qdrant.NewQuery(queryVector...),
			Filter:         filter,
			Limit:          topK,
			WithPayload:    qdrant.NewWithPayload(true),
		})
		if err != nil {
			appCtx.ErrorLogger.Printf("Error during Qdrant search: %v", err)
			return fmt.Errorf("error during Qdrant search: %w", err)
		}

		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Qdrant search returned %d results", len(searchResult))
		} else {
			appCtx.AccessLogger.Printf("Qdrant search returned %d results", len(searchResult))
		}

		results = make([]Payload, 0, len(searchResult))
		for _, point := range searchResult {
			var payload Payload

			if v, ok := point.Payload["packet_id"]; ok {
				payload.PacketID = v.GetStringValue()
			}
			if v, ok := point.Payload["timestamp"]; ok {
				payload.Timestamp = v.GetDoubleValue()
			}
			if v, ok := point.Payload["role"]; ok {
				payload.Role = v.GetStringValue()
			}
			if v, ok := point.Payload["body"]; ok {
				payload.Body = v.GetStringValue()
			}
			if v, ok := point.Payload["token_count"]; ok {
				payload.TokenCount = v.GetIntegerValue()
			}
			if v, ok := point.Payload["hash"]; ok {
				payload.Hash = v.GetStringValue()
			}
			if v, ok := point.Payload["file_meta"]; ok {
				if fm := v.GetStructValue(); fm != nil {
					if id, ok := fm.Fields["id"]; ok {
						payload.FileMeta.ID = id.GetStringValue()
					}
					if path, ok := fm.Fields["path"]; ok {
						payload.FileMeta.Path = path.GetStringValue()
					}
				}
			}

			results = append(results, payload)
		}

		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Parsed %d payloads", len(results))
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return results, nil
}

func planAttachmentSync(attachments []Attachment) (toInsert []AttachmentReplacement, toReplace []AttachmentReplacement, err error) {
	err = withDB(func() error {
		ctx := context.Background()

		seen := make(map[string]struct{})
		order := make([]string, 0, len(attachments))
		latest := make(map[string]Attachment, len(attachments))
		for _, att := range attachments {
			if att.ID == "" {
				continue
			}
			if _, ok := seen[att.ID]; !ok {
				order = append(order, att.ID)
				seen[att.ID] = struct{}{}
			}
			latest[att.ID] = att
		}
		if len(order) == 0 {
			return nil
		}

		existing := make(map[string]struct {
			pointID string
			hash    string
		}, len(order))

		for _, chunk := range chunkStrings(order, 256) {
			limit := uint32(len(chunk))
			filter := &qdrant.Filter{
				Must: []*qdrant.Condition{{
					ConditionOneOf: &qdrant.Condition_Field{
						Field: &qdrant.FieldCondition{
							Key: "file_meta.id",
							Match: &qdrant.Match{
								MatchValue: &qdrant.Match_Keywords{
									Keywords: &qdrant.RepeatedStrings{Strings: chunk},
								},
							},
						},
					},
				}},
			}

			resp, err := appCtx.DB.Scroll(ctx, &qdrant.ScrollPoints{
				CollectionName: appCtx.Config.QdrantCollection,
				Filter:         filter,
				Limit:          &limit,
				WithPayload:    qdrant.NewWithPayload(true),
				WithVectors:    qdrant.NewWithVectors(false),
			})
			if err != nil {
				return fmt.Errorf("scroll attachments: %w", err)
			}

			for _, point := range resp {
				meta := point.Payload["file_meta"]
				if meta == nil {
					continue
				}
				fields := meta.GetStructValue().GetFields()
				id := fields["id"].GetStringValue()
				if id == "" {
					continue
				}

				var hashVal string
				if h := point.Payload["hash"]; h != nil {
					hashVal = h.GetStringValue()
				}

				pointID := ""
				switch pid := point.GetId().GetPointIdOptions().(type) {
				case *qdrant.PointId_Uuid:
					pointID = pid.Uuid
				case *qdrant.PointId_Num:
					pointID = strconv.FormatUint(pid.Num, 10)
				}

				existing[id] = struct {
					pointID string
					hash    string
				}{
					pointID: pointID,
					hash:    hashVal,
				}
			}
		}

		processed := make(map[string]struct{}, len(order))
		for _, att := range attachments {
			if att.ID == "" {
				continue
			}
			if _, ok := processed[att.ID]; ok {
				continue
			}
			processed[att.ID] = struct{}{}

			if info, ok := existing[att.ID]; !ok {
				toInsert = append(toInsert, AttachmentReplacement{
					Attachment: att,
					OldPointID: "-",
				})
			} else if info.hash != att.Hash {
				toReplace = append(toReplace, AttachmentReplacement{
					Attachment: att,
					OldPointID: info.pointID,
				})
			}
		}

		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error planning attachment sync: %w", err)
	}
	return toInsert, toReplace, nil
}

func chunkStrings(values []string, chunkSize int) [][]string {
	if chunkSize <= 0 {
		chunkSize = 256
	}
	chunks := make([][]string, 0, (len(values)+chunkSize-1)/chunkSize)
	for start := 0; start < len(values); start += chunkSize {
		end := start + chunkSize
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}

// upsertPoint adds a new point to the Qdrant database with the given parameters
func upsertPoint(body string, vector []float32, role string, tokenCount int64, hash string, packetID string, fileMeta *FileMeta, pointID string) error {

	timestamp := float64(time.Now().UnixNano())

	if fileMeta == nil {
		fileMeta = &FileMeta{ID: "", Path: ""}
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Upserting point with ID: %s, PacketID: %s, Role: %s, TokenCount: %d, Body: %s, Hash: %s, FileMeta: %+v, Vector Length: %d", pointID, packetID, role, tokenCount, body, hash, *fileMeta, len(vector))
	} else {
		appCtx.AccessLogger.Printf("Upserting point with ID: %s, PacketID: %s, Role: %s, TokenCount: %d, Hash: %s, File: %t, Vector Length: %d", pointID, packetID, role, tokenCount, hash, fileMeta != nil, len(vector))
	}

	valPacketID := qdrant.NewValueString(packetID)
	valTimestamp := qdrant.NewValueDouble(timestamp)
	valRole := qdrant.NewValueString(role)
	valBody := qdrant.NewValueString(body)
	valTokenCount := qdrant.NewValueInt(tokenCount)
	valHash := qdrant.NewValueString(hash)
	valFileMeta, _ := qdrant.NewValue(map[string]interface{}{
		"id":   fileMeta.ID,
		"path": fileMeta.Path,
	})

	return withDB(func() error {
		_, err := appCtx.DB.Upsert(context.Background(), &qdrant.UpsertPoints{
			CollectionName: appCtx.Config.QdrantCollection,
			Points: []*qdrant.PointStruct{
				{
					Id:      &qdrant.PointId{PointIdOptions: &qdrant.PointId_Uuid{Uuid: pointID}},
					Vectors: qdrant.NewVectors(vector...),
					Payload: map[string]*qdrant.Value{
						"packet_id":   valPacketID,
						"timestamp":   valTimestamp,
						"role":        valRole,
						"body":        valBody,
						"token_count": valTokenCount,
						"hash":        valHash,
						"file_meta":   valFileMeta,
					},
				},
			},
		})
		if err != nil {
			appCtx.ErrorLogger.Printf("Error inserting model response: %v", err)
			return err
		}
		appCtx.AccessLogger.Printf("Inserted model response with packet_id: %s", packetID)
		return nil
	})
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
