// consts.go
package main

// Packet types enumeration
const (
	OtherPacket = iota
	DirectPacket
	StreamPacket
	FinishStreamPacket
)

var appConsts struct {
	MessagesWrapperSize                 int
	AvailableMessageTags                []string
	AvailableMessageAskAttachmentTags   []string
	AvailableMessageAgentAttachmentTags []string
	AvailableSearchSources              []string
	Base64FileTag                       string
	Base64FilesTag                      string
	UserMessageLeftWrapper              string
	UserMessageRightWrapper             string
	AssistantMessageLeftWrapper         string
	AssistantMessageRightWrapper        string
	AttachmentLeftWrapper               string
	AttachmentRightWrapper              string
}

func initConsts() {

	appConsts.MessagesWrapperSize =
		calculateTokens(`"messages":[`) + calculateTokens(`],`)
	appConsts.AvailableMessageTags = []string{
		"userRequest",
		"prompt",
	}
	appConsts.AvailableMessageAskAttachmentTags = []string{
		"attachment",
	}
	appConsts.AvailableMessageAgentAttachmentTags = []string{
		"editorContext",
	}
	appConsts.AvailableSearchSources = []string{
		"rag-user",
		"rag-assistant",
		"rag-file",
	}
	appConsts.Base64FileTag = "YXR0YWNobWVudA=="

	appConsts.Base64FilesTag = "YXR0YWNobWVudHM="

	appConsts.UserMessageLeftWrapper = "{\"content\":\""
	appConsts.UserMessageRightWrapper = "\",\"role\":\"rag-user\"},"
	appConsts.AssistantMessageLeftWrapper = "{\"content\":\""
	appConsts.AssistantMessageRightWrapper = "\",\"role\":\"rag-assistant\"},"
	appConsts.AttachmentLeftWrapper = "{\"content\":\""
	appConsts.AttachmentRightWrapper = "\",\"role\":\"rag-file\"},"
}
