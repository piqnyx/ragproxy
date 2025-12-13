package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

// Attachment represents a user message attachment
func storeAttachments(attachments []Attachment) error {
	for _, att := range attachments {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.DebugLogger.Printf("Storing attachment ID: %s\nBody length: %d\nBody: %s", att.ID, len(att.Body), att.Body)
		}

	}
	return nil
}

// SearchRelevantContent ищет релевантные записи по вектору и фильтрам из конфига
func SearchRelevantContent(queryVector []float32, now time.Time) ([]Payload, error) {
	var results []Payload

	err := withDB(func() error {
		// Retrieve filter parameters from config
		roles := appCtx.Config.SearchSource
		maxAgeDays := appCtx.Config.SearchMaxAgeDays
		topK := uint64(appCtx.Config.SearchTopK)

		if appCtx.Config.VerboseDiskLogs {
			appCtx.DebugLogger.Printf("Searching relevant content with roles: %v, maxAgeDays: %d, topK: %d, queryVector: %v", roles, maxAgeDays, topK, queryVector)
		} else {
			appCtx.DebugLogger.Printf("Searching relevant content with roles: %v, maxAgeDays: %d, topK: %d", roles, maxAgeDays, topK)
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
							Keywords: &qdrant.RepeatedStrings{
								Strings: roles,
							},
						},
					},
				},
			},
		})

		// Filter by time (if maxAgeDays > 0)
		if maxAgeDays > 0 {
			minTimestamp := now.Add(-time.Duration(maxAgeDays) * 24 * time.Hour).Unix()
			minTimestampFloat := float64(minTimestamp)
			conditions = append(conditions, &qdrant.Condition{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "timestamp",
						Range: &qdrant.Range{
							Gte: &minTimestampFloat,
						},
					},
				},
			})
		}

		// Build the final filter
		filter := &qdrant.Filter{
			Must: conditions,
		}

		// Search in Qdrant
		searchResult, err := appCtx.DB.Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: appCtx.Config.QdrantCollection,
			Query:          qdrant.NewQuery(queryVector...),
			Filter:         filter,
			Limit:          &topK,
			WithPayload:    qdrant.NewWithPayload(true), // Return the entire payload
		})
		if err != nil {
			appCtx.ErrorLogger.Printf("Error during Qdrant search: %v", err)
			return fmt.Errorf("error during Qdrant search: %w", err)
		}

		if appCtx.Config.VerboseDiskLogs {
			appCtx.DebugLogger.Printf("Qdrant search returned %d results: %v", len(searchResult), searchResult)
		} else {
			appCtx.DebugLogger.Printf("Qdrant search returned %d results", len(searchResult))
		}

		// Parse results
		for _, point := range searchResult {
			var payload Payload

			// Extract fields from payload
			if v, ok := point.Payload["packet_id"]; ok && v.GetStringValue() != "" {
				payload.PacketID = v.GetStringValue()
			}
			if v, ok := point.Payload["timestamp"]; ok {
				payload.Timestamp = int64(v.GetIntegerValue())
			}
			if v, ok := point.Payload["role"]; ok && v.GetStringValue() != "" {
				payload.Role = v.GetStringValue()
			}
			if v, ok := point.Payload["body"]; ok && v.GetStringValue() != "" {
				payload.Body = v.GetStringValue()
			}
			if v, ok := point.Payload["token_count"]; ok {
				payload.TokenCount = int(v.GetIntegerValue())
			}
			if v, ok := point.Payload["hash"]; ok && v.GetStringValue() != "" {
				payload.Hash = v.GetStringValue()
			}

			// Extract file_meta
			if v, ok := point.Payload["file_meta"]; ok {
				fileMeta := v.GetStructValue()
				if fileMeta != nil {
					if id, ok := fileMeta.Fields["id"]; ok && id.GetStringValue() != "" {
						payload.FileMeta.ID = id.GetStringValue()
					}
					if path, ok := fileMeta.Fields["path"]; ok && path.GetStringValue() != "" {
						payload.FileMeta.Path = path.GetStringValue()
					}
				}
			}

			results = append(results, payload)
		}

		if appCtx.Config.VerboseDiskLogs {
			appCtx.DebugLogger.Printf("Parsed %d relevant content payloads: %v", len(results), results)
		} else {
			appCtx.DebugLogger.Printf("Parsed %d relevant content payloads", len(results))
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return results, nil
}

func decodeTag(b64 string) string {
	b, _ := base64.StdEncoding.DecodeString(b64)
	return string(b)
}

// feedPrompt processes the parsed request elements (placeholder for RAG logic)
func feedPrompt(cleanUserContent string, req map[string]any) (err error, changed bool) {

	feedSize, historySize, systemMsg, userPromptMsg, err := calcSizes(req)
	if err != nil {
		return err, false
	}
	appCtx.AccessLogger.Printf("System message: %t, User prompt message: %t", systemMsg != nil, userPromptMsg != nil)
	appCtx.DebugLogger.Printf("System message: %t, User prompt message: %t", systemMsg != nil, userPromptMsg != nil)

	promptVector, err := embedText(cleanUserContent)
	if err != nil {
		return err, false
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Prompt vector generated. Length: %d, Content: %v", len(promptVector), promptVector)
		appCtx.DebugLogger.Printf("Prompt vector generated. Length: %d, Content: %v", len(promptVector), promptVector)
	} else {
		appCtx.AccessLogger.Printf("Prompt vector generated. Length: %d", len(promptVector))
	}

	relevantContent, err := SearchRelevantContent(promptVector, time.Now())
	if err != nil {
		return err, false
	}

	var feeds []map[string]any

	// Create slice of relevant content within feed size
	openFilesTag := "<" + decodeTag(appConsts.Base64FilesTag) + ">"
	closeFilesTag := "</" + decodeTag(appConsts.Base64FilesTag) + ">"
	openFileTag := "<" + decodeTag(appConsts.Base64FileTag) + ` id="%s">`
	closeFileTag := "</" + decodeTag(appConsts.Base64FileTag) + ">"
	for _, payload := range relevantContent {

		if feedSize < payload.TokenCount {
			continue // Trying to fit with another payload
		}

		var content string
		var role string

		if payload.Role == "file" {
			role = "user"
			content = fmt.Sprintf(
				`%s

%s
// filepath: %s
%s
%s

</%s>`,
				openFilesTag,
				fmt.Sprintf(openFileTag, payload.FileMeta.ID),
				payload.FileMeta.Path,
				payload.Body,
				closeFileTag,
				closeFilesTag,
			)
		} else {
			role = payload.Role
			content = payload.Body
		}

		feeds = append(feeds, map[string]any{
			"content": content,
			"role":    role,
		})

		feedSize -= payload.TokenCount
	}

	historySize += feedSize // Use remaining for history

	appCtx.AccessLogger.Printf("Prepared %d feed messages. Remaining feed size: %d", len(feeds), feedSize)
	appCtx.DebugLogger.Printf("Prepared %d feed messages. Remaining feed size: %d", len(feeds), feedSize)

	// Create slice for history messages within updated history size
	var history []map[string]any

	// Guarantee that we have at least one message in history
	messages := req["messages"].([]any)
	startIdx := len(messages) - 2
	endIdx := 1
	if systemMsg == nil {
		endIdx = 0
	}
	if startIdx < endIdx-1 {
		startIdx = endIdx - 1 // No honney no money
	}

	for i := startIdx; i >= endIdx; i-- {
		msgMap, ok := messages[i].(map[string]any)
		if !ok {
			return fmt.Errorf("invalid message format in request"), false
		}

		msgBytes, err := json.Marshal(msgMap)
		if err != nil {
			return err, false
		}
		msgStr := string(msgBytes)
		msgSize := calculateTokensWithReserve(msgStr)

		if historySize < msgSize {
			break
		}

		history = append(history, msgMap)
		historySize -= msgSize
	}

	appCtx.AccessLogger.Printf("Prepared %d history messages. Remaining history size: %d", len(history), historySize)
	appCtx.DebugLogger.Printf("Prepared %d history messages. Remaining history size: %d", len(history), historySize)

	var resultMessages []map[string]any

	// 1. systemMsg
	if systemMsg != nil {
		resultMessages = append(resultMessages, systemMsg)
	}

	// 2. feeds: from low relevance to high relevance (reverse order)
	for i := len(feeds) - 1; i >= 0; i-- {
		resultMessages = append(resultMessages, feeds[i])
	}

	// 3. history: from oldest to second last (as is)
	resultMessages = append(resultMessages, history...)

	// 4. userPromptMsg
	if userPromptMsg != nil {
		resultMessages = append(resultMessages, userPromptMsg)
	}

	// Transform to []any for req["messages"]
	msgs := make([]any, len(resultMessages))
	for i, msg := range resultMessages {
		msgs[i] = msg
	}
	req["messages"] = msgs

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Final messages count: %d, request: %v", len(msgs), msgs)
		appCtx.DebugLogger.Printf("Final messages count: %d, request: %v", len(msgs), msgs)
	} else {
		appCtx.AccessLogger.Printf("Final messages count in request: %d", len(msgs))
	}

	return nil, true
}

// processInbound processes the inbound request data (placeholder)
func processInbound(data string) string {

	req := make(map[string]any)
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Skipping processing. Reason: data is not valid JSON.")
		}
		return data
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Inbound data: %s", truncateJSONStrings(data))
	}

	cleanUserContent, attachments, err := processMessages(req)
	if err != nil {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Skipping processing. Reason: %v", err)
		}
		return data
	}

	if err := storeAttachments(attachments); err != nil {
		appCtx.ErrorLogger.Printf("Error storing attachments: %v", err)
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Clean user content: %s", cleanUserContent)
		appCtx.AccessLogger.Printf("Attachments: %v", attachments)
		appCtx.AccessLogger.Printf("Attachments count: %d", len(attachments))
		appCtx.AccessLogger.Printf("Full request object: %v", req)
	}

	err, changed := feedPrompt(cleanUserContent, req)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error in feedPrompt: %v", err)
		return data
	}

	if !changed {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("No changes made to the request.")
		}
		return data
	}

	// Marhall and return modified request (currently unchanged)
	modifiedData, err := json.Marshal(req)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error marshaling modified req: %v", err)
		return data
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Modified request object: %v", req)
	} else {
		appCtx.AccessLogger.Printf("Modified request object prepared. Original: %d bytes, Modified: %d bytes", len(data), len(modifiedData))
	}
	return string(modifiedData)
}

// processOutbound processes the outbound response data (placeholder)
func processOutbound(data string) string {
	// appCtx.DebugLogger.Printf("Outbound data: %s", data)
	return data
}
