package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Attachment represents a file attachment
type Attachment struct {
	ID   string `json:"id"`
	Body string `json:"body"` // сохраняем все переводы строк как в оригинале
}

// parseAttachments scans the provided content for any of the tags listed in `tags` (comma-separated).
// For each found tag it:
//   - looks for a line "// filepath: /path/to/file" inside the tag body and extracts the file path,
//   - derives ID from the basename of that path (if no basename -> skip),
//   - removes only the first occurrence of the filepath line from the body,
//   - removes lines like "User's active file..." (common header lines),
//   - trims outer empty lines and returns Attachment{ID, Body}.
//
// If the body contains "User's active selection" (case-insensitive) the block is skipped.
func parseAttachments(content string, tagList []string) []Attachment {
	var out []Attachment

	// regex to find a filepath line like: // filepath: /home/piqnyx/...
	fpLineRe := regexp.MustCompile(`(?im)^[ \t]*//[ \t]*filepath:[ \t]*(.+)$`)

	// regex to remove lines like "User's active file:" or
	// "User's active file for additional context:" (case-insensitive)
	userFileRemoveRe := regexp.MustCompile(`(?im)^[ \t]*user(?:'s)?[ \t]+active[ \t]+file(?:[ \t]+for[ \t]+additional[ \t]+context)?:[ \t]*$`)

	// if this phrase appears anywhere in the body, skip the whole block
	skipSelectionRe := regexp.MustCompile(`(?i)users?\s*'?s?\s*active\s*selection`)

	for _, rawTag := range tagList {
		tag := strings.TrimSpace(rawTag)
		if tag == "" {
			continue
		}

		// pattern matches opening tag (with attributes) and captures its attributes and inner content.
		// supports literal '<' and escaped forms like \u003c / \\u003c
		pattern := `(?is)(?:<|\\u003c|\\\\u003c)` + regexp.QuoteMeta(tag) +
			`\b([^>]*?)(?:>|\\u003e|\\\\u003e)(.*?)` +
			`(?:<|\\u003c|\\\\u003c)(?:/|\\u002f|\\\\u002f)` + regexp.QuoteMeta(tag) + `(?:>|\\u003e|\\\\u003e)`

		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)

		for _, m := range matches {
			bodyRaw := ""
			if len(m) > 2 {
				bodyRaw = m[2]
			}

			// If the body contains "User's active selection" — skip this block entirely.
			if skipSelectionRe.MatchString(bodyRaw) {
				continue
			}

			// Find the first filepath line in the body (if any)
			filePath := ""
			if fpMatch := fpLineRe.FindStringSubmatch(bodyRaw); len(fpMatch) > 1 {
				filePath = strings.TrimSpace(fpMatch[1])
			}

			// If no filepath found — skip this block
			if filePath == "" {
				continue
			}

			// Derive ID from basename of the filepath
			id := ""
			base := path.Base(filePath)
			if base != "" && base != "." && base != "/" {
				id = base
			}
			if id == "" {
				continue
			}

			// Remove only the first occurrence of the filepath line from the body
			bodyAfter := bodyRaw
			if loc := fpLineRe.FindStringIndex(bodyRaw); len(loc) == 2 {
				bodyAfter = bodyRaw[:loc[0]] + bodyRaw[loc[1]:]
			}

			// Remove any "User's active file..." header lines anywhere in the body
			bodyAfter = userFileRemoveRe.ReplaceAllString(bodyAfter, "")

			// Trim outer empty lines but preserve internal newlines
			bodyAfter = strings.Trim(bodyAfter, "\r\n")

			out = append(out, Attachment{
				ID:   id,
				Body: bodyAfter,
			})
		}
	}

	return out
}

// extractByTags extracts content within specified tags from the user message
func extractByTags(content string, tagsList []string) []string {
	var results []string

	for _, tag := range tagsList {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}

		// Паттерн:
		// (?is)                       — i: case-insensitive, s: dot matches newline
		// (?:<|\\u003c)               — открывающая скобка или её unicode-форма
		// tag\b                       — имя тега с границей слова
		// (?:\s+[^>]*?)?              — необязательные атрибуты (нежадно)
		// (?:>|\\u003e)               — закрывающая скобка открывающего тега или её unicode-форма
		// (.*?)                       — захватываемое содержимое (лениво, включает переводы строк)
		// (?:<|\\u003c)(?:/|\\u002f)tag(?:>|\\u003e) — закрывающий тег (поддержка unicode-форм)
		pattern := `(?is)(?:<|\\u003c)` + regexp.QuoteMeta(tag) +
			`\b(?:\s+[^>]*?)?(?:>|\\u003e)(.*?)(?:<|\\u003c)(?:/|\\u002f)` +
			regexp.QuoteMeta(tag) + `(?:>|\\u003e)`

		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)

		for _, m := range matches {
			// m[0] = полный матч, m[1] = содержимое между тегами
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

	// Find messages
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
			attachments = parseAttachments(content, appCtx.Config.UserMessageAttachmentTags)
		}
	}

	if len(strings.TrimSpace(cleanUserContent)) == 0 {
		err = fmt.Errorf("no user message found to extract content")
		return
	}

	return cleanUserContent, attachments, nil
}
