package main

import (
	"encoding/json"
	"fmt"
)

// calcMetaSize calculates metadata token size and remaining window size
func calcMetaSize(req map[string]any) (metaSize int64, err error) {
	meta := make(map[string]any)
	for k, v := range req {
		if k != "messages" {
			meta[k] = v
		}
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return 0, err
	}
	metaStr := string(metaBytes)
	metaSize = calculateTokensWithReserve(metaStr)

	return metaSize, nil
}

// calcSystemMsgSize calculates system message size and returns the message
func calcSystemMsgSize(req map[string]any) (systemMsgSize int64, systemMsg map[string]any, found bool, err error) {
	messages := req["messages"].([]any)
	systemMsg = messages[0].(map[string]any)
	if role, ok := systemMsg["role"].(string); !ok || role != "system" {
		return 0, nil, false, nil
	}

	// Marshal system message to JSON
	msgBytes, err := json.Marshal(systemMsg)
	if err != nil {
		return 0, nil, false, err
	}
	systemMsgStr := string(msgBytes)

	// Add comma size if there are more messages
	if len(messages) > 1 {
		systemMsgStr += ","
	}

	systemMsgSize = calculateTokensWithReserve(systemMsgStr)
	return systemMsgSize, systemMsg, true, nil
}

// calcUserPromptSize calculates user prompt message size and returns the message
func calcUserPromptSize(req map[string]any) (userPromptSize int64, userPromptMsg map[string]any, err error) {
	messages := req["messages"].([]any)
	userPromptMsg = messages[len(messages)-1].(map[string]any)
	if role, ok := userPromptMsg["role"].(string); !ok || role != "user" {
		return 0, nil, fmt.Errorf("last message is not user role")
	}

	// Marshal user prompt message to JSON
	msgBytes, err := json.Marshal(userPromptMsg)
	if err != nil {
		return 0, nil, err
	}

	userPromptSize = calculateTokensWithReserve(string(msgBytes))
	return userPromptSize, userPromptMsg, nil
}

// calcSizes calculates feed and history sizes based on the request
func calcSizes(req map[string]any) (feedSize int64, historySize int64, systemMsg map[string]any, userPromptMsg map[string]any, err error) {
	windowSize := appCtx.Config.MainModelWindowSize
	metaSize, err := calcMetaSize(req)
	if err != nil {
		return 0, 0, nil, nil, err
	}

	windowSize -= metaSize
	systemMsgSize, systemMsg, found, err := calcSystemMsgSize(req)
	if err != nil {
		return 0, 0, nil, nil, err
	}
	if found {
		windowSize -= systemMsgSize
	}

	userPromptSize, userPromptMsg, err := calcUserPromptSize(req)
	if err != nil {
		return 0, 0, nil, nil, err
	}
	windowSize -= userPromptSize

	windowSize -= appConsts.MessagesWrapperSize
	if windowSize < 0 {
		windowSize = 0
		return 0, 0, systemMsg, userPromptMsg, fmt.Errorf("not enough window size after accounting for meta, system, and user prompt sizes")
	}

	feedPercent := appCtx.Config.FeedAugmentationPercent

	feedSize = windowSize * feedPercent / 100
	historySize = windowSize - feedSize

	if appCtx.Config.VerboseDiskLogs {
		appCtx.AccessLogger.Printf("Calculated sizes - Meta: %d, System: %d, Feed: %d, History: %d, Window: %d", metaSize, systemMsgSize, feedSize, historySize, appCtx.Config.MainModelWindowSize)
	}
	return feedSize, historySize, systemMsg, userPromptMsg, nil
}
