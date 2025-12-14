package main

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

// normalizePath normalizes and sanitizes a candidate file path for comparison.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`)
	p = strings.TrimRight(p, ".;, \t\r\n")
	return p
}

// isDuplicate checks if a given path already exists in the list of attachments.
func isDuplicate(attachments []Attachment, pathToCheck string) bool {
	norm := normalizePath(pathToCheck)
	for _, a := range attachments {
		if normalizePath(a.Path) == norm {
			return true
		}
	}
	return false
}

// isFileAllowed checks if a file path matches the configured allowed patterns.
func isFileAllowed(filePath string) bool {
	// quick allow when no patterns configured
	if len(appCtx.Config.FilePatternsReg) == 0 {
		return true
	}

	for _, r := range appCtx.Config.FilePatternsReg {
		if r == nil {
			continue
		}
		if r.MatchString(filePath) {
			return true
		}
	}

	// ни один паттерн не совпал — запрещаем
	if appCtx.Config.VerboseDiskLogs {
		appCtx.ErrorLogger.Printf("file disallowed by patterns: %q", filePath)
	}
	return false
}

// parseAttachments scans content for tag blocks and extracts attachments.
func parseAttachments(content string, tagList []string) (attachments []Attachment) {
	fpLineRe := regexp.MustCompile(`(?i)^[ \t]*//[ \t]*filepath:[ \t]*(.+)$`)
	userFileRemoveRe := regexp.MustCompile(`(?im)^[ \t]*user(?:'s)?[ \t]+active[ \t]+file(?:[ \t]+for[ \t]+additional[ \t]+context)?:[ \t]*$`)
	attrFilePathRe := regexp.MustCompile(`(?i)\bfilepath\s*=\s*"([^"]+)"`)

	for _, rawTag := range tagList {
		tag := strings.TrimSpace(rawTag)
		if tag == "" {
			continue
		}

		pattern := `(?is)<` + regexp.QuoteMeta(tag) + `\b([^>]*)>(.*?)</` + regexp.QuoteMeta(tag) + `>`
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)

		for _, m := range matches {
			attrStr := ""
			bodyRaw := ""
			if len(m) > 1 {
				attrStr = m[1]
			}
			if len(m) > 2 {
				bodyRaw = m[2]
			}

			if regexp.MustCompile(`(?i)users?\s*'?s?\s*active\s*selection`).MatchString(bodyRaw) {
				continue
			}

			filePath := ""
			matchedLine := ""

			const maxLinesToCheck = 6
			lines := strings.SplitN(bodyRaw, "\n", maxLinesToCheck+1)
			for _, ln := range lines {
				if fpMatch := fpLineRe.FindStringSubmatch(ln); len(fpMatch) > 1 {
					filePath = strings.TrimSpace(fpMatch[1])
					matchedLine = fpMatch[0]
					break
				}
			}

			if filePath == "" {
				if attrMatch := attrFilePathRe.FindStringSubmatch(attrStr); len(attrMatch) > 1 {
					candidate := strings.TrimSpace(attrMatch[1])
					if candidate != "" && !strings.Contains(candidate, "%s") && !strings.Contains(candidate, "regexp.MustCompile") {
						filePath = candidate
					}
				}
			}

			if filePath == "" {
				continue
			}

			filePath = normalizePath(filePath)
			id := path.Base(filePath)
			if id == "" || id == "." || id == "/" {
				continue
			}
			if isDuplicate(attachments, filePath) {
				continue
			}

			bodyAfter := bodyRaw
			if matchedLine != "" {
				bodyAfter = strings.Replace(bodyAfter, matchedLine, "", 1)
			}

			bodyAfter = userFileRemoveRe.ReplaceAllString(bodyAfter, "")
			bodyAfter = strings.Trim(bodyAfter, "\r\n")

			if len(bodyAfter) == 0 {
				continue
			}

			if appCtx.Config.MaxFileSize > 0 && len(bodyAfter) > appCtx.Config.MaxFileSize {
				continue
			}

			if !isFileAllowed(filePath) {
				continue
			}

			attachments = append(attachments, Attachment{
				ID:   id,
				Body: bodyAfter,
				Path: filePath,
				Hash: sha512sum(bodyAfter),
			})

		}
	}

	return attachments
}

// readAttachments scans editor-like blocks
func readAttachments(existing []Attachment, content string, tagList []string) []Attachment {
	for _, rawTag := range tagList {
		tag := strings.TrimSpace(rawTag)
		if tag == "" {
			continue
		}

		pattern := `(?is)<` + regexp.QuoteMeta(tag) + `\b([^>]*)>(.*?)</` + regexp.QuoteMeta(tag) + `>`
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)

		for _, m := range matches {
			bodyRaw := ""
			if len(m) > 2 {
				bodyRaw = m[2]
			}

			editorPathRe := regexp.MustCompile(`(?im)current file is[:\s]+(.+?)(?:\r?\n|<|$)`)
			if epMatch := editorPathRe.FindStringSubmatch(bodyRaw); len(epMatch) > 1 {
				filePath := strings.TrimSpace(epMatch[1])
				filePath = strings.Trim(filePath, `"'`)
				filePath = normalizePath(filePath)
				if filePath == "" {
					continue
				}

				id := path.Base(filePath)
				if id == "" || id == "." || id == "/" {
					continue
				}

				if isDuplicate(existing, filePath) {
					continue
				}

				data, err := os.ReadFile(filePath)
				if err != nil {
					continue
				}

				body := strings.Trim(string(data), "\r\n")

				if len(body) == 0 {
					continue
				}

				if appCtx.Config.MaxFileSize > 0 && len(body) > appCtx.Config.MaxFileSize {
					continue
				}

				if !isFileAllowed(filePath) {
					continue
				}

				newAtt := Attachment{
					ID:   id,
					Body: body,
					Path: filePath,
					Hash: sha512sum(body),
				}
				existing = append(existing, newAtt)
			}
		}
	}

	return existing
}

// extractByTags extracts content within specified tags from the user message
func extractByTags(content string, tagsList []string) []string {
	var results []string

	for _, tag := range tagsList {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		pattern := `(?is)(?:<|\\u003c)` + regexp.QuoteMeta(tag) +
			`\b(?:\s+[^>]*?)?(?:>|\\u003e)(.*?)(?:<|\\u003c)(?:/|\\u002f)` +
			regexp.QuoteMeta(tag) + `(?:>|\\u003e)`

		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)

		for _, m := range matches {
			if len(m) > 1 {
				results = append(results, strings.TrimSpace(m[1]))
			}
		}
	}

	return results
}

// processMessages parses the JSON data and extracts required elements
func processMessages(req map[string]any) (cleanUserContent string, attachments []Attachment, err error) {

	msgsRaw, ok := req["messages"]
	if !ok {
		err = fmt.Errorf("messages field not found")
		return
	}
	msgs, ok := msgsRaw.([]any)
	if !ok || len(msgs) == 0 {
		err = fmt.Errorf("messages field invalid type or empty")
		return
	}
	lastMsg, ok := msgs[len(msgs)-1].(map[string]any)
	if !ok {
		err = fmt.Errorf("last message invalid format")
		return
	}

	if role, ok := lastMsg["role"].(string); ok && role == "user" {
		if content, ok := lastMsg["content"].(string); ok {
			if appCtx.Config.VerboseDiskLogs {
				appCtx.AccessLogger.Printf("User message content: %s", content)
			}
			cleanUserContentParts := extractByTags(content, appCtx.Config.UserMessageTags)
			cleanUserContent = strings.Join(cleanUserContentParts, " ")
			attachments = parseAttachments(content, appCtx.Config.UserMessageAskAttachmentTags)
			attachments = readAttachments(attachments, content, appCtx.Config.UserMessageAgentAttachmentTags)
			appCtx.AccessLogger.Printf("Extracted %d attachments from user message", len(attachments))
		}
	}

	if len(strings.TrimSpace(cleanUserContent)) == 0 {
		err = fmt.Errorf("no user message found to extract content")
		return
	}

	return cleanUserContent, attachments, nil
}
