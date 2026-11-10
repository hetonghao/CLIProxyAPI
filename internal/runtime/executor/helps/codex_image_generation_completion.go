package helps

import (
	"strconv"
	"strings"

	"github.com/MoeForge/moego/ojson"
)

// NormalizeCodexImageGenerationCompletion repairs terminal image items that
// contain a result but still carry the upstream's non-terminal status.
func NormalizeCodexImageGenerationCompletion(eventData []byte) []byte {
	switch ojson.GetBytes(eventData, "type").String() {
	case "response.output_item.done":
		return completeCodexImageGenerationItem(eventData, "item", ojson.GetBytes(eventData, "item"))
	case "response.completed", "response.done":
		for i, item := range ojson.GetBytes(eventData, "response.output").Array() {
			eventData = completeCodexImageGenerationItem(eventData, "response.output."+strconv.Itoa(i), item)
		}
	}
	return eventData
}

func completeCodexImageGenerationItem(eventData []byte, path string, item ojson.Result) []byte {
	result := item.Get("result")
	if item.Get("type").String() != "image_generation_call" ||
		item.Get("status").String() != "generating" ||
		result.Type != ojson.String || strings.TrimSpace(result.String()) == "" {
		return eventData
	}
	return SetStringIfDifferent(eventData, path+".status", "completed")
}
