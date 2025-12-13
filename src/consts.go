// добавлю сюда текст
package main

var appConsts struct {
	MessagesWrapperSize            int64
	AvailableMessageTags           []string
	AvailableMessageAttachmentTags []string
	AvailableSearchSources         []string
	Base64FileTag                  string
	Base64FilesTag                 string
	UserMessageLeftWrapper         string
	UserMessageRightWrapper        string
	AssistantMessageLeftWrapper    string
	AssistantMessageRightWrapper   string
}

func initConsts() {

	appConsts.MessagesWrapperSize =
		calculateTokensWithReserve(`"messages":[`) + calculateTokensWithReserve(`],`)
	appConsts.AvailableMessageTags = []string{
		"userRequest",
		"prompt",
	}
	appConsts.AvailableMessageAttachmentTags = []string{
		"attachment",
	}
	appConsts.AvailableSearchSources = []string{
		"user",
		"assistant",
		"file",
	}
	appConsts.Base64FileTag = "YXR0YWNobWVudA=="

	appConsts.Base64FilesTag = "YXR0YWNobWVudHM="

	appConsts.UserMessageLeftWrapper = "{\"content\":\""
	appConsts.UserMessageRightWrapper = "\",\"role\":\"user\"},"
	appConsts.AssistantMessageLeftWrapper = "{\"content\":\""
	appConsts.AssistantMessageRightWrapper = "\",\"role\":\"assistant\"},"
}
