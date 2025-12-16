// processing.go
package main

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// saveSystemMessage: rewrite existing system message
func saveSystemMessage(content string) error {
	if appCtx.Config.SystemMessageFile == "" {
		return nil
	}
	err := os.WriteFile(appCtx.Config.SystemMessageFile, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("error saving system message to file: %w", err)
	}
	return nil
}

func patchSystemMessage(systemMessage string) string {
	cfg := appCtx.Config.SystemMessagePatch

	msg := systemMessage // Работаем с копией строки

	// 1. Remove phrases specified in the Remove list
	for _, phrase := range cfg.Remove {
		if phrase != "" {
			msg = strings.ReplaceAll(msg, phrase, "")
		}
	}

	// 2. Perform direct text replacements
	for oldStr, newStr := range cfg.Replace {
		if oldStr != "" {
			msg = strings.ReplaceAll(msg, oldStr, newStr)
		}
	}

	// 3. Insert text after specified search strings
	for searchStr, insertStr := range cfg.AddAfter {
		sstr := fmt.Sprintf("%v", searchStr)
		istr := fmt.Sprintf("%v", insertStr)
		if sstr == "" {
			continue
		}
		var result strings.Builder
		parts := strings.Split(msg, sstr)
		for i, part := range parts {
			result.WriteString(part)
			if i < len(parts)-1 {
				result.WriteString(sstr)
				result.WriteString(istr)
			}
		}
		msg = result.String()
	}

	// 4. Append text to the end of the message
	if len(cfg.AddToEnd) > 0 {
		var toAdd strings.Builder
		for _, line := range cfg.AddToEnd {
			toAdd.WriteString("\n")
			toAdd.WriteString(line)
		}
		msg += toAdd.String()
	}

	// 5. Prepend text to the beginning of the message
	if len(cfg.AddToBegin) > 0 {
		var toAdd strings.Builder
		for _, line := range cfg.AddToBegin {
			toAdd.WriteString("\n")
			toAdd.WriteString(line)
		}
		msg = toAdd.String() + msg
	}

	// 6. Remove double newlines
	// msg = strings.ReplaceAll(msg, "\n\n", "\n")

	return msg
}

// normalizeText: normalizes text by converting to lowercase and removing non-alphanumeric and non-punctuation characters
func normalizeText(s string) string {
	s = norm.NFC.String(s) // нормализуем в NFC
	var b strings.Builder
	for _, r := range s {
		r = unicode.ToLower(r)
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case strings.ContainsRune(`!"#$%&'()*+,-./:;<=>?@[\]^_{|}~`, r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// messageExists: checks if a message with the given content already exists in the request
func messageExists(req map[string]any, content string) bool {
	normContent := normalizeText(content)

	// guaranteee that req["messages"] is []any
	messages := req["messages"].([]any)

	for _, m := range messages {
		if mm, ok := m.(map[string]any); ok {
			if c, ok2 := mm["content"].(string); ok2 && normalizeText(c) == normContent {
				return true
			}
		}
		if s, ok := m.(string); ok && normalizeText(s) == normContent {
			return true
		}
	}
	return false
}

func decodeTag(b64 string) string {
	b, _ := base64.StdEncoding.DecodeString(b64)
	return string(b)
}

func prepareFeeds(historySize *int, feedSize *int, relevantContent []Payload, req map[string]any) []map[string]any {

	var feeds []map[string]any
	// Create slice of relevant content within feed size
	// openFilesTag := "<" + decodeTag(appConsts.Base64FilesTag) + ">"
	// closeFilesTag := "</" + decodeTag(appConsts.Base64FilesTag) + ">"
	openFileTag := "<" + decodeTag(appConsts.Base64FileTag) + ` id="%s" isSummarized="true">`
	closeFileTag := "</" + decodeTag(appConsts.Base64FileTag) + ">"
	for _, payload := range relevantContent {
		if *feedSize < payload.TokenCount {
			continue // Trying to fit with another payload
		}

		n := 64
		if len(payload.Body) < n {
			n = len(payload.Body)
		}
		txt := payload.Body[:n]
		if messageExists(req, payload.Body) {
			appCtx.AccessLogger.Printf("Skipping already existing message in request: %s", txt)
			appCtx.DebugLogger.Printf("Skipping already existing message in request: %s", txt)
			continue
		} else {
			appCtx.AccessLogger.Printf("Adding new message to request: %s", txt)
			appCtx.DebugLogger.Printf("Adding new message to request: %s", txt)
		}

		var content string

		if payload.Role == "rag-file" {
			content = fmt.Sprintf(
				`%s
// filepath: %s
%s
%s
`,
				fmt.Sprintf(openFileTag, payload.FileMeta.ID),
				payload.FileMeta.Path,
				payload.Body,
				closeFileTag,
			)
		} else {
			content = payload.Body
		}

		feeds = append(feeds, map[string]any{
			"content": content,
			"role":    payload.Role,
		})

		*feedSize -= payload.TokenCount
	}

	*historySize += *feedSize // Use remaining for history

	appCtx.AccessLogger.Printf("Feeds prepared: %d, Remaining history size: %d", len(feeds), *historySize)
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
	appCtx.DebugLogger.Printf("FEEDS BEGIN ====================")
	for _, feed := range feeds {
		appCtx.DebugLogger.Printf(">> %s\n", formatFeed(feed))
	}
	appCtx.DebugLogger.Printf("FEEDS END ======================")

	appCtx.AccessLogger.Printf("Prepared %d feed messages. Remaining feed size: %d", len(feeds), feedSize)
	return feeds
}

func prepareHistory(historySize *int, systemMsg map[string]any, req map[string]any) ([]map[string]any, error) {
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
			return nil, fmt.Errorf("invalid message format in request")
		}

		msgBytes, err := json.Marshal(msgMap)
		if err != nil {
			return nil, err
		}
		msgStr := string(msgBytes)
		msgSize := calculateTokensWithReserve(msgStr)

		if *historySize < msgSize {
			break
		}

		history = append(history, msgMap)
		*historySize -= msgSize
	}

	appCtx.AccessLogger.Printf("Prepared %d history messages. Remaining history size: %d", len(history), *historySize)
	return history, nil
}

func updateReq(systemMsg, userPromptMsg map[string]any, history, feeds []map[string]any, req map[string]any) {
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
	for i := len(history) - 1; i >= 0; i-- {
		resultMessages = append(resultMessages, history[i])
	}

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
}

// feedPrompt processes the parsed request elements (placeholder for RAG logic)
func feedPrompt(cleanUserContent string, req map[string]any) (changed bool, promptVector []float32, queryHash string, err error) {

	feedSize, historySize, systemMsg, userPromptMsg, err := calcSizes(req)
	if err != nil {
		return false, nil, "", err
	}
	appCtx.AccessLogger.Printf("System message: %t, User prompt message: %t", systemMsg != nil, userPromptMsg != nil)

	// check if systemMsg has content field
	if systemMsg != nil {
		if content, ok := systemMsg["content"].(string); ok {

			systemMsgText := patchSystemMessage(content)
			saveSystemMessage(content + "\n\n=======================================\n\nPatched version:\n\n" + systemMsgText)
			appCtx.AccessLogger.Printf("Patched system message. and saved orifinal to file if configured. Length: %d", len(systemMsgText))
			systemMsg["content"] = systemMsgText
		} else {
			systemMsg = nil // discard invalid system message
		}
	}

	// Get prompt embeddings
	promptVector, err = embedText(cleanUserContent)
	if err != nil {
		return false, nil, "", err
	}

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Prompt vector generated. Length: %d, Content: %v", len(promptVector), promptVector)
	} else {
		appCtx.AccessLogger.Printf("Prompt vector generated. Length: %d", len(promptVector))
	}

	// Hash the clean user content
	queryHash = sha512sum(cleanUserContent)

	// Search for relevant content
	relevantContent, err := SearchRelevantContentWithRerank(promptVector, cleanUserContent, queryHash)
	if err != nil {
		return false, nil, queryHash, err
	}
	// Prepare feeds from relevant content
	feeds := prepareFeeds(&historySize, &feedSize, relevantContent, req)

	// Prepare history messages within history size
	history, err := prepareHistory(&historySize, systemMsg, req)
	if err != nil {
		return false, nil, queryHash, err
	}

	// Reconstruct final messages array
	updateReq(systemMsg, userPromptMsg, history, feeds, req)

	// Log final messages to DebugLogger Truncating long contents
	appCtx.DebugLogger.Printf("FINAL MESSAGES BEGIN ====================")
	//print role and truncated (128 chars max) content
	for _, msg := range req["messages"].([]any) {
		m := msg.(map[string]any)
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if len(content) > 256 {
			content = content[:256] + "..."
		}
		appCtx.DebugLogger.Printf(">>------")
		appCtx.DebugLogger.Printf("Role: %s", role)
		appCtx.DebugLogger.Printf("Content: %s", content)
	}
	appCtx.DebugLogger.Printf("FINAL MESSAGES END ======================")

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Final messages count: %d, request: %v", len(req["messages"].([]any)), req["messages"])
	} else {
		appCtx.AccessLogger.Printf("Final messages count in request: %d", len(req["messages"].([]any)))
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

func calcFileSize(att Attachment) (tokenCount int, err error) {
	// Formatting content with tags to compute tokens
	// openFilesTag := "<" + decodeTag(appConsts.Base64FilesTag) + ">"
	// closeFilesTag := "</" + decodeTag(appConsts.Base64FilesTag) + ">"
	openFileTag := "<" + decodeTag(appConsts.Base64FileTag) + ` id="%s" isSummarized="true">`
	closeFileTag := "</" + decodeTag(appConsts.Base64FileTag) + ">"

	content := fmt.Sprintf(
		`%s
// filepath: %s
%s
%s`,
		// openFilesTag,
		fmt.Sprintf(openFileTag, att.ID),
		att.Path,
		att.Body,
		closeFileTag,
		// closeFilesTag,
	)

	// Calculate token count with reserve
	return calculateTokensWithReserve(appConsts.AttachmentLeftWrapper + content + appConsts.AttachmentRightWrapper), nil
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
			cleanTokenCount := calculateTokensWithReserve(att.Attachment.Body)
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
				if err := removeDocumentFromIDF(oldBody, att.OldCleanTokenCount, att.OldHash); err != nil {
					return fmt.Errorf("error removing old attachment from IDF for ID %s: %w", att.Attachment.ID, err)
				}
				appCtx.AccessLogger.Printf("Replaced attachment ID %s with body size %d at point ID %s", att.Attachment.ID, len(oldBody), pointID)
			} else {
				pointID = uuid.NewString()
				appCtx.AccessLogger.Printf("Inserted attachment ID %s with body size %d at new point ID %s", att.Attachment.ID, len(att.Attachment.Body), pointID)
			}
			// Upsert attachment
			err = upsertPoint(att.Attachment.Body, attachmentVector, "rag-file", tokenCount, cleanTokenCount, att.Attachment.Hash, packetID, &FileMeta{
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
	cleanPromptSize := calculateTokensWithReserve(cleanUserContent)
	assistantSize := calculateTokensWithReserve(appConsts.AssistantMessageLeftWrapper + cleanAssistantContent + appConsts.AssistantMessageRightWrapper)
	cleanAssistantSize := calculateTokensWithReserve(cleanAssistantContent)

	appCtx.AccessLogger.Printf("Calculated token sizes - Prompt: %d, Assistant: %d", promptSize, assistantSize)

	assistantHash := sha512sum(cleanAssistantContent)

	appCtx.AccessLogger.Printf("Calculated content hashes - Prompt: %s, Assistant: %s", queryHash, assistantHash)

	// Store user message
	appCtx.AccessLogger.Printf("Inserted point with packet_id: %s, role: %s", packetID, "rag-user")
	err = upsertPoint(cleanUserContent, promptVector, "rag-user", promptSize, cleanPromptSize, queryHash, packetID, nil, uuid.NewString())
	if err != nil {
		appCtx.ErrorLogger.Printf("Error storing user message: %v", err)
		return
	}

	// Store assistant message
	appCtx.AccessLogger.Printf("Inserted point with packet_id: %s, role: %s", packetID, "rag-assistant")
	err = upsertPoint(cleanAssistantContent, responseVector, "rag-assistant", assistantSize, cleanAssistantSize, assistantHash, packetID, nil, uuid.NewString())
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
