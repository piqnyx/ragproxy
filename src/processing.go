package main

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

func decodeTag(b64 string) string {
	b, _ := base64.StdEncoding.DecodeString(b64)
	return string(b)
}

// feedPrompt processes the parsed request elements (placeholder for RAG logic)
func feedPrompt(cleanUserContent string, req map[string]any) (changed bool, promptVector []float32, queryHash string, err error) {

	feedSize, historySize, systemMsg, userPromptMsg, err := calcSizes(req)
	if err != nil {
		return false, nil, "", err
	}
	appCtx.AccessLogger.Printf("System message: %t, User prompt message: %t", systemMsg != nil, userPromptMsg != nil)

	promptVector, err = embedText(cleanUserContent)
	if err != nil {
		return false, nil, "", err
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Prompt vector generated. Length: %d, Content: %v", len(promptVector), promptVector)
	} else {
		appCtx.AccessLogger.Printf("Prompt vector generated. Length: %d", len(promptVector))
	}
	queryHash = sha512sum(cleanUserContent)
	relevantContent, err := SearchRelevantContentWithRerank(promptVector, cleanUserContent, queryHash)
	if err != nil {
		return false, nil, queryHash, err
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

	appCtx.DebugLogger.Printf("Feeds prepared: %d, Remaining history size: %d", len(feeds), historySize)
	formatFeed := func(feed map[string]any) string {
		content, ok := feed["content"].(string)
		if !ok {
			return ""
		}
		if len(content) > 64 {
			content = content[:64] + "..."
		}

		return content
	}
	for _, feed := range feeds {
		appCtx.DebugLogger.Printf("----------------")
		appCtx.DebugLogger.Printf("%s", formatFeed(feed))
	}
	appCtx.DebugLogger.Printf("----------------------------------------------------------")

	appCtx.AccessLogger.Printf("Prepared %d feed messages. Remaining feed size: %d", len(feeds), feedSize)

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
			return false, nil, queryHash, fmt.Errorf("invalid message format in request")
		}

		msgBytes, err := json.Marshal(msgMap)
		if err != nil {
			return false, nil, queryHash, err
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
	} else {
		appCtx.AccessLogger.Printf("Final messages count in request: %d", len(msgs))
	}

	return true, promptVector, queryHash, nil
}

// processInbound processes the inbound request data (placeholder)
func processInbound(data string) (
	responseBody string,
	cleanUserContent string,
	attachments []Attachment,
	promptVector []float32,
	queryHash string) {

	req := make(map[string]any)
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Skipping processing. Reason: data is not valid JSON: %s", data)
		}
		return data, "", nil, nil, ""
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Inbound data: %s", truncateJSONStrings(data))
	}

	var err error
	cleanUserContent, attachments, err = processMessages(req)
	if err != nil {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("Skipping processing. Reason: %v", err)
		}
		return data, "", nil, nil, ""
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Clean user content: %s", cleanUserContent)
		appCtx.AccessLogger.Printf("Attachments: %v", attachments)
		appCtx.AccessLogger.Printf("Attachments count: %d", len(attachments))
	}

	changed, promptVector, queryHash, err := feedPrompt(cleanUserContent, req)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error in feedPrompt: %v", err)
		return data, "", nil, nil, queryHash
	}

	if !changed {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.AccessLogger.Printf("No changes made to the request.")
		}
		return data, "", nil, nil, queryHash
	}

	// Change temperature
	req["temperature"] = appCtx.Config.Temperature

	// Marhall and return modified request (currently unchanged)
	modifiedData, err := json.Marshal(req)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error marshaling modified req: %v", err)
		return data, "", nil, nil, queryHash
	}

	if appCtx.Config.VerboseDiskLogs {
		reqBytes, _ := json.Marshal(req)
		appCtx.AccessLogger.Printf("Modified request object: %v", req)
		appCtx.AccessLogger.Printf("Modified request object JSON: %s", string(reqBytes))
	} else {
		appCtx.AccessLogger.Printf("Modified request object prepared. Original: %d bytes, Modified: %d bytes", len(data), len(modifiedData))
	}
	return string(modifiedData), cleanUserContent, attachments, promptVector, queryHash
}

// sha512sum computes the SHA-512 hash of the given text and returns it as a hexadecimal string
func sha512sum(text string) string {
	hash := sha512.Sum512([]byte(text))
	return hex.EncodeToString(hash[:])
}

func calcFileSize(att Attachment) (tokenCount int64, err error) {
	// Formatting content with tags to compute tokens
	openFilesTag := "<" + decodeTag(appConsts.Base64FilesTag) + ">"
	closeFilesTag := "</" + decodeTag(appConsts.Base64FilesTag) + ">"
	openFileTag := "<" + decodeTag(appConsts.Base64FileTag) + ` id="%s">`
	closeFileTag := "</" + decodeTag(appConsts.Base64FileTag) + ">"

	content := fmt.Sprintf(
		`%s

%s
// filepath: %s
%s
%s

</%s>`,
		openFilesTag,
		fmt.Sprintf(openFileTag, att.ID),
		att.Path,
		att.Body,
		closeFileTag,
		closeFilesTag,
	)

	msg := map[string]any{
		"content": content,
		"role":    "user",
	}

	// Marshal message to JSON string
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return 0, fmt.Errorf("error marshaling attachment message: %w", err)
	}
	msgStr := string(msgBytes)

	// Calculate token count with reserve
	return calculateTokensWithReserve(msgStr), nil
}

// Attachment represents a user message attachment
func storeAttachments(attachments []Attachment, packetID string) error {

	toInsert, toReplace, err := planAttachmentSync(attachments)
	if err != nil {
		return fmt.Errorf("error planning attachment sync: %w", err)
	}

	proc := func(listAttachments []AttachmentReplacement) error {
		replace := false
		var pointID string
		for _, att := range listAttachments {

			replace = len(att.OldPointID) > 1

			attachmentVector, err := embedText(att.Attachment.Body)
			if err != nil {
				return fmt.Errorf("error embedding attachment ID %s: %w", att.Attachment.ID, err)
			}

			tokenCount, err := calcFileSize(att.Attachment)
			if err != nil {
				return fmt.Errorf("error calculating token size for attachment ID %s: %w", att.Attachment.ID, err)
			}

			if appCtx.Config.VerboseDiskLogs {
				if replace {
					appCtx.DebugLogger.Printf("Replacing attachment ID %s token count: %d, path: %s, old point ID: %s", att.Attachment.ID, tokenCount, att.Attachment.Path, att.OldPointID)
				} else {
					appCtx.DebugLogger.Printf("Inserting attachment ID %s token count: %d, path: %s", att.Attachment.ID, tokenCount, att.Attachment.Path)
				}
			}

			if replace {
				pointID = att.OldPointID
				oldBody, err := getPointBodyByID(pointID)
				if err != nil {
					return fmt.Errorf("error fetching old attachment body for ID %s: %w", att.Attachment.ID, err)
				}
				// Remove old from IDF
				if err := removeDocumentFromIDF(oldBody, att.OldTokenCount, att.OldHash); err != nil {
					return fmt.Errorf("error removing old attachment from IDF for ID %s: %w", att.Attachment.ID, err)
				}
			} else {
				pointID = uuid.NewString()
			}
			// Upsert attachment
			err = upsertPoint(att.Attachment.Body, attachmentVector, "file", tokenCount, att.Attachment.Hash, packetID, &FileMeta{
				ID:   att.Attachment.ID,
				Path: att.Attachment.Path,
			}, pointID)
			if err != nil {
				return fmt.Errorf("error upserting attachment point: %w", err)
			}
		}
		return nil
	}

	if len(toReplace) > 0 {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.DebugLogger.Printf("Processing %d attachments for replacement", len(toReplace))
		}
		if err := proc(toReplace); err != nil {
			return fmt.Errorf("error processing attachments for replacement: %w", err)
		}
	}

	if len(toInsert) > 0 {
		if appCtx.Config.VerboseDiskLogs {
			appCtx.DebugLogger.Printf("Processing %d attachments for insertion", len(toInsert))
		}
		if err := proc(toInsert); err != nil {
			return fmt.Errorf("error processing attachments for insertion: %w", err)
		}
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.DebugLogger.Printf("All attachments processed successfully.---------------------------------")
	}

	return nil
}

// processOutbound processes the outbound response data (placeholder)
func processOutbound(cleanAssistantContent string, cleanUserContent string, attachments []Attachment, promptVector []float32, queryHash string) {

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Request parsed data: Vector length: %d, Clean user content: %s, Attachments count: %d, Attachments: %v, Prompt vector: %v", len(promptVector), cleanUserContent, len(attachments), attachments, promptVector)
	}

	packetID := uuid.NewString()
	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Generated packet ID: %s", packetID)
	}

	responseVector, err := embedText(cleanAssistantContent)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error embedding assistant content: %v", err)
		return
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Response vector generated. Length: %d, Content: %v", len(responseVector), responseVector)
	} else {
		appCtx.AccessLogger.Printf("Response vector generated. Length: %d", len(responseVector))
	}

	promptSize := calculateTokensWithReserve(appConsts.UserMessageLeftWrapper + cleanUserContent + appConsts.UserMessageRightWrapper)
	assistantSize := calculateTokensWithReserve(appConsts.AssistantMessageLeftWrapper + cleanAssistantContent + appConsts.AssistantMessageRightWrapper)

	appCtx.AccessLogger.Printf("Calculated token sizes - Prompt: %d, Assistant: %d", promptSize, assistantSize)

	assistantHash := sha512sum(cleanAssistantContent)

	appCtx.AccessLogger.Printf("Calculated content hashes - Prompt: %s, Assistant: %s", queryHash, assistantHash)

	// Store user message
	err = upsertPoint(cleanUserContent, promptVector, "user", promptSize, queryHash, packetID, nil, uuid.NewString())
	if err != nil {
		appCtx.ErrorLogger.Printf("Error storing user message: %v", err)
		return
	}

	// Store assistant message
	err = upsertPoint(cleanAssistantContent, responseVector, "assistant", assistantSize, assistantHash, packetID, nil, uuid.NewString())
	if err != nil {
		appCtx.ErrorLogger.Printf("Error storing assistant message: %v", err)
		return
	}

	err = storeAttachments(attachments, packetID)
	if err != nil {
		appCtx.ErrorLogger.Printf("Error storing attachments: %v", err)
		return
	}

}
