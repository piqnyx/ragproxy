package main

import (
	"context"
	"fmt"
	"os"
	"sort"
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

// scoreCandidate computes a final score from Features using provided weights.
// weights must have length == 10, corresponding to the Features fields in order.
func scoreCandidate(f Features, weights []float64) float64 {
	if len(weights) != 10 {
		panic("scoreCandidate: weights length must be 10")
	}

	vals := []float64{
		f.EmbSim,          // 0
		f.Recency,         // 1
		f.RoleScore,       // 2
		f.BodyLen,         // 3
		f.PayloadQuality,  // 4
		f.KeywordOverlap,  // 5
		f.WeightedOverlap, // 6
		f.BM25,            // 7
		f.NgramOverlap,    // 8
		f.WeightedNgram,   // 9
	}

	score := 0.0
	for i := range vals {
		score += vals[i] * weights[i]
	}
	return score
}

// SearchRelevantContentWithRerank searches relevant records using initial vector search and then reranks them
func SearchRelevantContentWithRerank(queryVector []float32, queryText string, queryHash string) ([]Payload, error) {
	candidates, err := SearchRelevantContent(queryVector)
	if err != nil {
		return nil, err
	}
	appCtx.DebugLogger.Printf("Search returned %d candidates before reranking", len(candidates))
	for i := range candidates {
		err := updateFeaturesForCandidate(queryText, queryHash, &candidates[i])
		if err != nil {
			appCtx.ErrorLogger.Printf("Error updating features for candidate: %v", err)
		}
	}
	appCtx.DebugLogger.Printf("Updated features for %d candidates", len(candidates))
	for i := range candidates {
		appCtx.DebugLogger.Printf("\tCandidate %d features: %+v", i, candidates[i].Features)
		appCtx.DebugLogger.Printf("\tCandidate %d body (first 100 chars): %.100s", i, candidates[i].Payload.Body)
	}

	for i := range candidates {
		candidates[i].Score = scoreCandidate(candidates[i].Features, appCtx.Config.DefaultWeights)
	}
	appCtx.DebugLogger.Printf("Reranked %d candidates", len(candidates))
	for i := range candidates {
		appCtx.DebugLogger.Printf("\tCandidate %d final score: %.4f", i, candidates[i].Score)
	}

	filtered := make([]Candidate, 0, len(candidates))
	for _, cand := range candidates {
		if cand.Score >= appCtx.Config.MinRankScore {
			appCtx.DebugLogger.Printf("Candidate passed MinRankScore %.4f: score=%.4f", appCtx.Config.MinRankScore, cand.Score)
			filtered = append(filtered, cand)
		}
	}
	appCtx.DebugLogger.Printf("%d candidates passed MinRankScore %.4f", len(filtered), appCtx.Config.MinRankScore)

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Score > filtered[j].Score
	})

	topN := appCtx.Config.RerankTopN
	if topN > 0 && len(filtered) > topN {
		filtered = filtered[:topN]
	}
	appCtx.DebugLogger.Printf("Returning top %d candidates after reranking", len(filtered))
	for i := range filtered {
		appCtx.DebugLogger.Printf("\tFinal Candidate %d score: %.4f", i, filtered[i].Score)
		appCtx.DebugLogger.Printf("\tFinal Candidate %d body (first 100 chars): %.100s", i, filtered[i].Payload.Body)
	}

	// collect payloads of top candidates
	payloads := make([]Payload, len(filtered))
	for i, cand := range filtered {
		payloads[i] = cand.Payload
	}

	return payloads, nil
}

// SearchRelevantContent searches Qdrant and returns a slice of Candidate with fast features filled.
// - cheap features (EmbSim, Recency, RoleScore, BodyLen, PayloadQuality) are computed here.
// - expensive features (IDF overlap, BM25, ngrams, cross-encoder) should be computed later in rerank step for top-K.
func SearchRelevantContent(queryVector []float32) ([]Candidate, error) {
	var results []Candidate

	err := withDB(func() error {
		// Retrieve filter parameters from config
		roles := appCtx.Config.SearchSource
		maxAgeDays := appCtx.Config.SearchMaxAgeDays
		topKCfg := appCtx.Config.SearchTopK

		appCtx.AccessLogger.Printf("Searching relevant content with roles: %v, maxAgeDays: %d, topK: %d, queryVector length: %d",
			roles, maxAgeDays, topKCfg, len(queryVector))
		appCtx.DebugLogger.Printf("Searching relevant content with roles: %v, maxAgeDays: %d, topK: %d, queryVector length: %d",
			roles, maxAgeDays, topKCfg, len(queryVector))

		// Build filter conditions
		var conditions []*qdrant.Condition

		// Filter by roles
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

		// Filter by time (if configured)
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

		filter := &qdrant.Filter{Must: conditions}

		var topK uint64 = 100000
		if topKCfg > 0 {
			topK = uint64(topKCfg)
		}

		// Query Qdrant. WithVectors controlled by config (may be expensive).
		resp, err := appCtx.DB.Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: appCtx.Config.QdrantCollection,
			Query:          qdrant.NewQuery(queryVector...),
			Filter:         filter,
			Limit:          &topK,
			WithPayload:    qdrant.NewWithPayload(true),
			WithVectors:    qdrant.NewWithVectors(appCtx.Config.ReturnVectors),
		})
		if err != nil {
			appCtx.ErrorLogger.Printf("Error during Qdrant search: %v", err)
			return fmt.Errorf("error during Qdrant search: %w", err)
		}

		appCtx.AccessLogger.Printf("Qdrant search returned %d results", len(resp))
		appCtx.DebugLogger.Printf("Qdrant search returned %d results", len(resp))

		// cutoff by score/distance depending on metric
		pass := func(score float32) bool {
			switch appCtx.Config.QdrantMetric {
			case "Cosine", "Dot":
				return score >= appCtx.Config.CosineMinScore
			case "Euclid":
				return score <= appCtx.Config.EuclidMaxDistance
			default:
				return true
			}
		}

		results = make([]Candidate, 0, len(resp))
		for _, point := range resp {
			if !pass(point.Score) {
				appCtx.DebugLogger.Printf("Skipping point %s with score %.4f due to cutoff", point.Id, point.Score)
				continue
			}

			// populate payload from point.Payload
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

			// Verbose logging
			if appCtx.Config.VerboseDiskLogs {
				if payload.FileMeta.ID != "" {
					appCtx.AccessLogger.Printf("hit score=%.4f role=%s file id=%s path=%s", point.Score, payload.Role, payload.FileMeta.ID, payload.FileMeta.Path)
					appCtx.DebugLogger.Printf("hit score=%.4f role=%s file id=%s path=%s", point.Score, payload.Role, payload.FileMeta.ID, payload.FileMeta.Path)
				} else {
					appCtx.AccessLogger.Printf("hit score=%.4f role=%s", point.Score, payload.Role)
					appCtx.DebugLogger.Printf("hit score=%.4f role=%s", point.Score, payload.Role)
				}
			}

			// build candidate and fill cheap features
			cand := Candidate{Payload: payload}

			// use raw score but clamp to [0,1] to be safe
			raw := float64(point.Score)
			if raw < 0 {
				raw = 0
			}
			if raw > 1 {
				raw = 1
			}
			cand.Features.EmbSim = raw

			// optional: if metric is Euclid, convert distance -> similarity
			if appCtx.Config.QdrantMetric == "Euclid" {
				d := float64(point.Score)
				if d < 0 {
					d = 0
				}
				cand.Features.EmbSim = 1.0 / (1.0 + d)
			}

			// If vectors were returned and config requests them, keep vector for optional local cosine
			if appCtx.Config.ReturnVectors && point.Vectors.GetVector() != nil {
				cand.EmbeddingVector = convertPointVectorToFloat64(point.Vectors.GetVector())
			}

			// Recency
			cand.Features.Recency = timeDecay(cand.Payload.Timestamp)

			// Role score
			cand.Features.RoleScore = appCtx.Config.RoleWeights[cand.Payload.Role]

			// Body length normalized
			var tokenCnt int64 = cand.Payload.TokenCount
			if tokenCnt == 0 {
				tokenCnt = calculateTokensWithReserve(cand.Payload.Body)
			}
			cand.Features.BodyLen = bodyLenNorm(tokenCnt)

			// Payload quality heuristic
			cand.Features.PayloadQuality = payloadQuality(cand.Payload)

			/*
				Ramain for second step (rerank):

				KeywordOverlap  float64 // [0,1]
				WeightedOverlap float64 // [0,1]
				BM25            float64 // [0,1]
				PayloadQuality  float64 // [0,1]
				NgramOverlap    float64 // [0,1]
				WeightedNgram   float64 // [0,1]
			*/

			results = append(results, cand)
		}

		appCtx.AccessLogger.Printf("Filtered to %d results after applying score/distance cutoff", len(results))
		appCtx.DebugLogger.Printf("Filtered to %d results after applying score/distance cutoff", len(results))
		return nil
	})

	if err != nil {
		return nil, err
	}
	return results, nil
}

// convertPointVectorToFloat64 converts Qdrant point.Vector to []float64.
// It handles common underlying types returned by the client (e.g., []float32, []float64).
func convertPointVectorToFloat64(vec interface{}) []float64 {
	switch v := vec.(type) {
	case []float32:
		out := make([]float64, len(v))
		for i, x := range v {
			out[i] = float64(x)
		}
		return out
	case []float64:
		out := make([]float64, len(v))
		copy(out, v)
		return out
	case []interface{}:
		out := make([]float64, len(v))
		for i, xi := range v {
			switch xv := xi.(type) {
			case float32:
				out[i] = float64(xv)
			case float64:
				out[i] = xv
			case int:
				out[i] = float64(xv)
			default:
				out[i] = 0.0
			}
		}
		return out
	default:
		return nil
	}
}

// getPointBodyByID fetches the "body" payload field for a given pointID.
func getPointBodyByID(pointID string) (string, error) {
	var body string
	err := withDB(func() error {
		ctx := context.Background()

		resp, err := appCtx.DB.Get(ctx, &qdrant.GetPoints{
			CollectionName: appCtx.Config.QdrantCollection,
			Ids: []*qdrant.PointId{
				{PointIdOptions: &qdrant.PointId_Uuid{Uuid: pointID}},
			},
			WithPayload: qdrant.NewWithPayload(true),
			WithVectors: qdrant.NewWithVectors(false),
		})
		if err != nil {
			return fmt.Errorf("get point body: %w", err)
		}

		if len(resp) == 0 {
			return fmt.Errorf("point not found: %s", pointID)
		}

		if b := resp[0].Payload["body"]; b != nil {
			body = b.GetStringValue()
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return body, nil
}

// planAttachmentSync plans which attachments to insert or replace in the DB.
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
			pointID    string
			hash       string
			tokenCount int64
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
				var tokenCountVal int64
				if tc := point.Payload["token_count"]; tc != nil {
					tokenCountVal = tc.GetIntegerValue()
				}

				// extract point ID

				pointID := ""
				switch pid := point.GetId().GetPointIdOptions().(type) {
				case *qdrant.PointId_Uuid:
					pointID = pid.Uuid
				case *qdrant.PointId_Num:
					pointID = strconv.FormatUint(pid.Num, 10)
				}

				existing[id] = struct {
					pointID    string
					hash       string
					tokenCount int64
				}{
					pointID:    pointID,
					hash:       hashVal,
					tokenCount: tokenCountVal,
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
					Attachment:    att,
					OldPointID:    "-",
					OldHash:       "-",
					OldTokenCount: 0,
				})
			} else if info.hash != att.Hash {
				toReplace = append(toReplace, AttachmentReplacement{
					Attachment:    att,
					OldPointID:    info.pointID,
					OldHash:       info.hash,
					OldTokenCount: info.tokenCount,
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

	// add to IDF

	if err := addDocumentToIDF(body, tokenCount, hash); err != nil {
		return fmt.Errorf("error adding document to IDF: %w", err)
	}

	// add to Qdrant

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
