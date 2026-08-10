package helps

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// NormalizeCodexImageGenerationCompletion repairs terminal image items that
// contain a result but still carry the upstream's non-terminal status.
func NormalizeCodexImageGenerationCompletion(eventData []byte) []byte {
	switch gjson.GetBytes(eventData, "type").String() {
	case "response.output_item.done":
		return completeCodexImageGenerationItem(eventData, "item", gjson.GetBytes(eventData, "item"))
	case "response.completed", "response.done":
		for i, item := range gjson.GetBytes(eventData, "response.output").Array() {
			eventData = completeCodexImageGenerationItem(eventData, "response.output."+strconv.Itoa(i), item)
		}
	}
	return eventData
}

func completeCodexImageGenerationItem(eventData []byte, path string, item gjson.Result) []byte {
	result := item.Get("result")
	if item.Get("type").String() != "image_generation_call" ||
		item.Get("status").String() != "generating" ||
		result.Type != gjson.String || strings.TrimSpace(result.String()) == "" {
		return eventData
	}
	return SetStringIfDifferent(eventData, path+".status", "completed")
}
